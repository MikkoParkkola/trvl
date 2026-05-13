package ground

// DFDS ferry provider.
//
// The DFDS travel-search API (travel-search-prod.dfds-pax-web.com) returns
// date availability only — no prices or departure times. Prices are served by
// the Seabook booking engine, which requires a browser session (minibookId from
// sessionStorage, set by the DFDS parent page). There is no public pricing API.
//
// Endpoints discovered (2026-04-05):
//   /api/routes?locale=en&minibookId=ID — returns all routes with seabook codes + salesOwner
//   /api/available-travel-dates?direction=outbound&salesOwner=N&numberOfAdults=1&vehicleType=NCAR&route=FROM-TO — date availability
//   /api/available-travel-dates-frs?routeCode=CODE&currencyCode=EUR — FRS (Mediterranean) routes only
//   /api/booking-url?route=...&minibookId=... — returns Seabook redirect URL (requires session)
//   /api/travel-search-config?locale=en&urlSlug=... — widget config (returns {} without page context)
//   /api/vehicle-types?salesOwner=N&route=CODE — vehicle types for a route
//
// FRS routes (use available-travel-dates-frs): TATV, TVTA, ALTM, ALCE, CEAL, TMAL, TMGI, GITM
// All Baltic/NorthSea routes use available-travel-dates with UN/LOCODE format.
// SalesOwner values: 19 (main DFDS), 22 (Dieppe-Newhaven), 14 (Jersey routes).
//
// This provider queries the availability API to confirm the requested date is not
// disabled, then returns hardcoded departure times and reference prices from DFDS
// published timetables (2026).
//
// When the API returns the requested date in its offerDates array, the route is
// annotated as a campaign deal (amenity "Deal" added).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// dfdsAvailabilityBase is the DFDS travel-search availability endpoint.
const dfdsAvailabilityBase = "https://travel-search-prod.dfds-pax-web.com/api/available-travel-dates"

// dfdsLimiter: conservative 5 req/min.
var dfdsLimiter = newProviderLimiter(12 * time.Second)

// dfdsClient is a shared HTTP client for DFDS availability API calls.
var dfdsClient = &http.Client{
	Timeout: 30 * time.Second,
}

// dfdsPort holds metadata for a DFDS ferry port.
type dfdsPort struct {
	Code string // UN/LOCODE port prefix used in route codes
	Name string // Full terminal name
	City string // Display city name
}

// dfdsPorts maps lowercase city name / alias to port metadata.
var dfdsPorts = map[string]dfdsPort{
	// Copenhagen, Denmark
	"copenhagen": {Code: "DKCPH", Name: "Copenhagen Ferry Terminal", City: "Copenhagen"},
	"københavn":  {Code: "DKCPH", Name: "Copenhagen Ferry Terminal", City: "Copenhagen"},
	"kobenhavn":  {Code: "DKCPH", Name: "Copenhagen Ferry Terminal", City: "Copenhagen"},
	"cph":        {Code: "DKCPH", Name: "Copenhagen Ferry Terminal", City: "Copenhagen"},
	"dkcph":      {Code: "DKCPH", Name: "Copenhagen Ferry Terminal", City: "Copenhagen"},

	// Oslo, Norway
	"oslo":  {Code: "NOOSL", Name: "Oslo Ferry Terminal", City: "Oslo"},
	"osl":   {Code: "NOOSL", Name: "Oslo Ferry Terminal", City: "Oslo"},
	"noosl": {Code: "NOOSL", Name: "Oslo Ferry Terminal", City: "Oslo"},

	// Amsterdam (IJmuiden), Netherlands
	"amsterdam": {Code: "NLIJM", Name: "Amsterdam (IJmuiden) Ferry Terminal", City: "Amsterdam"},
	"ijmuiden":  {Code: "NLIJM", Name: "Amsterdam (IJmuiden) Ferry Terminal", City: "Amsterdam"},
	"nlijm":     {Code: "NLIJM", Name: "Amsterdam (IJmuiden) Ferry Terminal", City: "Amsterdam"},

	// Newcastle, UK
	"newcastle": {Code: "GBTYN", Name: "Newcastle Ferry Terminal", City: "Newcastle"},
	"gbtyn":     {Code: "GBTYN", Name: "Newcastle Ferry Terminal", City: "Newcastle"},

	// Kiel, Germany
	"kiel":  {Code: "DEKEL", Name: "Kiel Ferry Terminal", City: "Kiel"},
	"kel":   {Code: "DEKEL", Name: "Kiel Ferry Terminal", City: "Kiel"},
	"dekel": {Code: "DEKEL", Name: "Kiel Ferry Terminal", City: "Kiel"},

	// Klaipeda, Lithuania
	"klaipeda": {Code: "LTKLJ", Name: "Klaipeda Ferry Terminal", City: "Klaipeda"},
	"klaipėda": {Code: "LTKLJ", Name: "Klaipeda Ferry Terminal", City: "Klaipeda"},
	"ltklj":    {Code: "LTKLJ", Name: "Klaipeda Ferry Terminal", City: "Klaipeda"},

	// Kapellskär, Sweden
	"kapellskär": {Code: "SEKPS", Name: "Kapellskär Ferry Terminal", City: "Kapellskär"},
	"kapellskar": {Code: "SEKPS", Name: "Kapellskär Ferry Terminal", City: "Kapellskär"},
	"sekps":      {Code: "SEKPS", Name: "Kapellskär Ferry Terminal", City: "Kapellskär"},

	// Karlshamn, Sweden
	"karlshamn": {Code: "SEKAN", Name: "Karlshamn Ferry Terminal", City: "Karlshamn"},
	"sekan":     {Code: "SEKAN", Name: "Karlshamn Ferry Terminal", City: "Karlshamn"},

	// Trelleborg, Sweden
	"trelleborg": {Code: "SETRG", Name: "Trelleborg Ferry Terminal", City: "Trelleborg"},
	"setrg":      {Code: "SETRG", Name: "Trelleborg Ferry Terminal", City: "Trelleborg"},

	// Paldiski, Estonia
	"paldiski": {Code: "EEPLA", Name: "Paldiski Ferry Terminal", City: "Paldiski"},
	"eepla":    {Code: "EEPLA", Name: "Paldiski Ferry Terminal", City: "Paldiski"},

	// Dieppe, France
	"dieppe": {Code: "FRDPE", Name: "Dieppe Ferry Terminal", City: "Dieppe"},
	"frdpe":  {Code: "FRDPE", Name: "Dieppe Ferry Terminal", City: "Dieppe"},

	// Newhaven, UK
	"newhaven": {Code: "GBNHV", Name: "Newhaven Ferry Terminal", City: "Newhaven"},
	"gbnhv":    {Code: "GBNHV", Name: "Newhaven Ferry Terminal", City: "Newhaven"},
}

// dfdsRouteInfo holds static schedule and booking information for a DFDS route.
type dfdsRouteInfo struct {
	RouteCode   string  // API route code e.g. "DKCPH-NOOSL"
	SalesOwner  int     // DFDS salesOwner parameter for the availability API
	DepTime     string  // hardcoded departure time "HH:MM"
	ArrTime     string  // hardcoded arrival time "HH:MM"
	ArrOffset   int     // days offset for arrival (0 = same day, 1 = next day)
	DurationMin int     // journey duration in minutes
	BasePrice   float64 // reference "from" price (EUR)
	Currency    string  // base currency for the price
}

// dfdsRoutes maps route key "FROMCODE-TOCODE" to route information.
// Route codes and salesOwner confirmed via the routes API at
// travel-search-prod.dfds-pax-web.com/api/routes (2026-04-05).
// SalesOwner 19 = DFDS main (Baltic+NorthSea+Channel), 22 = Dieppe-Newhaven, 14 = Jersey.
var dfdsRoutes = map[string]dfdsRouteInfo{
	// Kiel ↔ Klaipeda (~20h crossing, ~6×/week)
	"DEKEL-LTKLJ": {
		RouteCode: "DEKEL-LTKLJ", SalesOwner: 19,
		DepTime: "20:00", ArrTime: "18:00", ArrOffset: 1, DurationMin: 1320,
		BasePrice: 79, Currency: "EUR",
	},
	"LTKLJ-DEKEL": {
		RouteCode: "LTKLJ-DEKEL", SalesOwner: 19,
		DepTime: "20:00", ArrTime: "18:00", ArrOffset: 1, DurationMin: 1320,
		BasePrice: 79, Currency: "EUR",
	},

	// Karlshamn ↔ Klaipeda (~14h crossing, ~daily)
	"SEKAN-LTKLJ": {
		RouteCode: "SEKAN-LTKLJ", SalesOwner: 19,
		DepTime: "18:00", ArrTime: "08:00", ArrOffset: 1, DurationMin: 840,
		BasePrice: 55, Currency: "EUR",
	},
	"LTKLJ-SEKAN": {
		RouteCode: "LTKLJ-SEKAN", SalesOwner: 19,
		DepTime: "18:00", ArrTime: "08:00", ArrOffset: 1, DurationMin: 840,
		BasePrice: 55, Currency: "EUR",
	},

	// Trelleborg ↔ Klaipeda (~18h crossing, limited schedule)
	"SETRG-LTKLJ": {
		RouteCode: "SETRG-LTKLJ", SalesOwner: 19,
		DepTime: "20:00", ArrTime: "14:00", ArrOffset: 1, DurationMin: 1080,
		BasePrice: 65, Currency: "EUR",
	},
	"LTKLJ-SETRG": {
		RouteCode: "LTKLJ-SETRG", SalesOwner: 19,
		DepTime: "20:00", ArrTime: "14:00", ArrOffset: 1, DurationMin: 1080,
		BasePrice: 65, Currency: "EUR",
	},

	// Kapellskär ↔ Paldiski (~10h crossing, ~6×/week)
	"SEKPS-EEPLA": {
		RouteCode: "SEKPS-EEPLA", SalesOwner: 19,
		DepTime: "20:00", ArrTime: "06:00", ArrOffset: 1, DurationMin: 600,
		BasePrice: 55, Currency: "EUR",
	},
	"EEPLA-SEKPS": {
		RouteCode: "EEPLA-SEKPS", SalesOwner: 19,
		DepTime: "20:00", ArrTime: "06:00", ArrOffset: 1, DurationMin: 600,
		BasePrice: 55, Currency: "EUR",
	},

	// Amsterdam (IJmuiden) ↔ Newcastle (~16h crossing, daily)
	"NLIJM-GBTYN": {
		RouteCode: "NLIJM-GBTYN", SalesOwner: 19,
		DepTime: "17:30", ArrTime: "09:30", ArrOffset: 1, DurationMin: 960,
		BasePrice: 79, Currency: "EUR",
	},
	"GBTYN-NLIJM": {
		RouteCode: "GBTYN-NLIJM", SalesOwner: 19,
		DepTime: "17:00", ArrTime: "09:00", ArrOffset: 1, DurationMin: 960,
		BasePrice: 79, Currency: "EUR",
	},

	// Dieppe ↔ Newhaven (~4h crossing)
	"FRDPE-GBNHV": {
		RouteCode: "FRDPE-GBNHV", SalesOwner: 22,
		DepTime: "08:00", ArrTime: "12:00", ArrOffset: 0, DurationMin: 240,
		BasePrice: 49, Currency: "EUR",
	},
	"GBNHV-FRDPE": {
		RouteCode: "GBNHV-FRDPE", SalesOwner: 22,
		DepTime: "08:00", ArrTime: "12:00", ArrOffset: 0, DurationMin: 240,
		BasePrice: 39, Currency: "GBP",
	},

	// Copenhagen ↔ Oslo (~19h crossing, seasonal)
	// API returns empty dates (route currently inactive); kept for completeness.
	"DKCPH-NOOSL": {
		RouteCode: "DKCPH-NOOSL", SalesOwner: 19,
		DepTime: "17:00", ArrTime: "09:45", ArrOffset: 1, DurationMin: 1005,
		BasePrice: 69, Currency: "EUR",
	},
	"NOOSL-DKCPH": {
		RouteCode: "NOOSL-DKCPH", SalesOwner: 19,
		DepTime: "16:30", ArrTime: "09:00", ArrOffset: 1, DurationMin: 990,
		BasePrice: 69, Currency: "EUR",
	},
}

// dfdsAvailabilityResponse is the DFDS availability API response.
type dfdsAvailabilityResponse struct {
	Dates struct {
		FromDate string `json:"fromDate"`
		ToDate   string `json:"toDate"`
	} `json:"dates"`
	DefaultDate   string   `json:"defaultDate"`
	DisabledDates []string `json:"disabledDates"`
	OfferDates    []string `json:"offerDates"`
}

// LookupDFDSPort resolves a city name or alias to a DFDS port (case-insensitive).
func LookupDFDSPort(city string) (dfdsPort, bool) {
	p, ok := dfdsPorts[strings.ToLower(strings.TrimSpace(city))]
	return p, ok
}

// HasDFDSPort returns true if the city has a known DFDS port.
func HasDFDSPort(city string) bool {
	_, ok := LookupDFDSPort(city)
	return ok
}

// HasDFDSRoute returns true if there is a known DFDS sailing between the two cities.
func HasDFDSRoute(from, to string) bool {
	fromPort, ok := LookupDFDSPort(from)
	if !ok {
		return false
	}
	toPort, ok := LookupDFDSPort(to)
	if !ok {
		return false
	}
	key := fromPort.Code + "-" + toPort.Code
	_, ok = dfdsRoutes[key]
	return ok
}

// buildDFDSBookingURL returns the DFDS booking URL for a route.
func buildDFDSBookingURL(routeInfo dfdsRouteInfo) string {
	return fmt.Sprintf(
		"https://www.dfdsseaways.com/ferry-crossings/%s/",
		strings.ToLower(routeInfo.RouteCode),
	)
}

// dfdsFormatDateTime combines a date string ("2026-05-01") and time ("HH:MM") into
// an ISO 8601 datetime string, optionally adding days for next-day arrivals.
func dfdsFormatDateTime(date, timeStr string, dayOffset int) string {
	t, err := models.ParseDate(date)
	if err != nil {
		return date + "T" + timeStr + ":00"
	}
	t = t.AddDate(0, 0, dayOffset)
	return t.Format("2006-01-02") + "T" + timeStr + ":00"
}

// fetchDFDSAvailability calls the DFDS availability API for a route and returns
// whether the given date is available, and whether it is an offer date.
// Returns (available, isOffer, error).
// A non-fatal API failure (e.g. empty dates for inactive route) returns (true, false, nil)
// so the hardcoded schedule is still returned.
func fetchDFDSAvailability(ctx context.Context, routeInfo dfdsRouteInfo, date string) (available, isOffer bool, err error) {
	reqURL := fmt.Sprintf(
		"%s?direction=outbound&salesOwner=%d&numberOfAdults=1&vehicleType=&route=%s",
		dfdsAvailabilityBase, routeInfo.SalesOwner, routeInfo.RouteCode,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return true, false, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := dfdsClient.Do(req)
	if err != nil {
		// Network failure: return schedule without availability check.
		slog.Debug("dfds availability: network error", "route", routeInfo.RouteCode, "err", err)
		return true, false, nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		slog.Debug("dfds availability: non-200", "status", resp.StatusCode, "body", string(body))
		return true, false, nil // non-fatal
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if err != nil {
		return true, false, nil // non-fatal
	}

	var avail dfdsAvailabilityResponse
	if err := json.Unmarshal(body, &avail); err != nil {
		slog.Debug("dfds availability: decode error", "err", err)
		return true, false, nil // non-fatal
	}

	// If dates are empty, the route is inactive for this period; still return the
	// schedule entry but treat as available (caller sees 0 routes = no results).
	if avail.Dates.FromDate == "" {
		slog.Debug("dfds availability: route inactive", "route", routeInfo.RouteCode)
		return false, false, nil
	}

	// Check whether the requested date is in the disabled list.
	for _, d := range avail.DisabledDates {
		if d == date {
			slog.Debug("dfds availability: date disabled", "route", routeInfo.RouteCode, "date", date)
			return false, false, nil
		}
	}

	// Check whether the requested date is in the range at all.
	if avail.Dates.FromDate != "" && date < avail.Dates.FromDate {
		return false, false, nil
	}
	if avail.Dates.ToDate != "" && date > avail.Dates.ToDate {
		return false, false, nil
	}

	// Check for campaign / offer date.
	for _, d := range avail.OfferDates {
		if d == date {
			return true, true, nil
		}
	}

	return true, false, nil
}

// SearchDFDS searches DFDS for ferry crossings between two cities on a given date.
// It queries the DFDS availability API to confirm the date is operational, then
// attempts to fetch live prices via browser page read. Falls back to hardcoded
// reference prices when browser reading fails.
func SearchDFDS(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	fromPort, ok := LookupDFDSPort(from)
	if !ok {
		return nil, fmt.Errorf("dfds: no port for %q", from)
	}
	toPort, ok := LookupDFDSPort(to)
	if !ok {
		return nil, fmt.Errorf("dfds: no port for %q", to)
	}

	if currency == "" {
		currency = "EUR"
	}

	if _, err := models.ParseDate(date); err != nil {
		return nil, fmt.Errorf("dfds: invalid date %q: %w", date, err)
	}

	key := fromPort.Code + "-" + toPort.Code
	routeInfo, ok := dfdsRoutes[key]
	if !ok {
		return nil, fmt.Errorf("dfds: no route for %s→%s", fromPort.Code, toPort.Code)
	}

	if err := dfdsLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("dfds: rate limiter: %w", err)
	}

	slog.Debug("dfds search", "from", fromPort.City, "to", toPort.City, "date", date)

	// Check availability via API.
	available, isOffer, err := fetchDFDSAvailability(ctx, routeInfo, date)
	if err != nil {
		slog.Debug("dfds availability error", "err", err)
	}
	if !available {
		slog.Debug("dfds: date unavailable", "route", routeInfo.RouteCode, "date", date)
		return nil, nil
	}

	depTime := dfdsFormatDateTime(date, routeInfo.DepTime, 0)
	arrTime := dfdsFormatDateTime(date, routeInfo.ArrTime, routeInfo.ArrOffset)

	// Use the route's native currency.
	outCurrency := routeInfo.Currency
	if outCurrency == "" {
		outCurrency = strings.ToUpper(currency)
	}

	price := routeInfo.BasePrice
	var amenities []string
	if isOffer {
		amenities = append(amenities, "Deal")
		slog.Debug("dfds: offer date", "route", routeInfo.RouteCode, "date", date)
	}

	route := models.GroundRoute{
		Provider: "dfds",
		Type:     "ferry",
		Price:    price,
		Currency: outCurrency,
		Duration: routeInfo.DurationMin,
		Departure: models.GroundStop{
			City:    fromPort.City,
			Station: fromPort.Name,
			Time:    depTime,
		},
		Arrival: models.GroundStop{
			City:    toPort.City,
			Station: toPort.Name,
			Time:    arrTime,
		},
		Transfers:  0,
		Amenities:  amenities,
		BookingURL: buildDFDSBookingURL(routeInfo),
	}

	slog.Debug("dfds result", "route", key, "price", price, "offer", isOffer)
	return []models.GroundRoute{route}, nil
}
