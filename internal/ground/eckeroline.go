package ground

// Eckerö Line ferry provider (Helsinki–Tallinn).
//
// Eckerö Line's Magento booking site exposes a JSON API for departures.
// The schedule below is sourced from eckeroline.fi/aikataulu and is valid
// 1 Jan 2026 – 31 Dec 2027. Ship: M/S Finlandia.
//
// Live prices are fetched from the Magento AJAX API (form_key + getdepartures POST).
// Falls back to published "from" prices when the API is unavailable.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/logredact"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// eckerolinePorts maps city aliases to Eckerö Line port info.
var eckerolinePorts = map[string]struct {
	Code string
	Name string
	City string
}{
	"helsinki": {Code: "HEL", Name: "Länsisatama Terminal 2", City: "Helsinki"},
	"hel":      {Code: "HEL", Name: "Länsisatama Terminal 2", City: "Helsinki"},
	"tallinn":  {Code: "TLL", Name: "Old Port A-Terminal", City: "Tallinn"},
	"tll":      {Code: "TLL", Name: "Old Port A-Terminal", City: "Tallinn"},
	"tln":      {Code: "TLL", Name: "Old Port A-Terminal", City: "Tallinn"},
}

type eckerolineScheduleEntry struct {
	DepTime   string // "HH:MM"
	ArrTime   string // "HH:MM"
	ArrOffset int    // 0=same day, 1=next day
	Duration  int    // minutes
	Days      string // "daily", "mon-sat", "sun-fri"
}

// eckerolineSchedules: M/S Finlandia timetable (2026-2027).
// Source: eckeroline.fi/aikataulu
var eckerolineSchedules = map[string][]eckerolineScheduleEntry{
	"TLL-HEL": {
		{DepTime: "06:00", ArrTime: "08:15", ArrOffset: 0, Duration: 135, Days: "mon-sat"},
		{DepTime: "12:00", ArrTime: "14:15", ArrOffset: 0, Duration: 135, Days: "daily"},
		{DepTime: "18:30", ArrTime: "21:00", ArrOffset: 0, Duration: 150, Days: "daily"},
	},
	"HEL-TLL": {
		{DepTime: "09:00", ArrTime: "11:15", ArrOffset: 0, Duration: 135, Days: "daily"},
		{DepTime: "15:15", ArrTime: "17:30", ArrOffset: 0, Duration: 135, Days: "daily"},
		{DepTime: "21:40", ArrTime: "00:10", ArrOffset: 1, Duration: 150, Days: "sun-fri"},
	},
}

// eckerolineBasePrice is the published "from" price (EUR, foot passenger).
const eckerolineBasePrice = 19.0

// eckerolineLimiter: conservative 5 req/min (one request per 12 seconds).
var eckerolineLimiter = newProviderLimiter(12 * time.Second)

// eckerolineClient uses Chrome TLS fingerprint to interact with Eckerö Line's website.
var eckerolineClient = batchexec.ChromeHTTPClient()

// LookupEckeroLinePort resolves a city name to an Eckerö Line port.
func LookupEckeroLinePort(city string) (string, string, string, bool) {
	p, ok := eckerolinePorts[strings.ToLower(strings.TrimSpace(city))]
	if !ok {
		return "", "", "", false
	}
	return p.Code, p.Name, p.City, true
}

// HasEckeroLineRoute returns true if Eckerö Line operates between these cities.
func HasEckeroLineRoute(from, to string) bool {
	fromCode, _, _, ok1 := LookupEckeroLinePort(from)
	toCode, _, _, ok2 := LookupEckeroLinePort(to)
	if !ok1 || !ok2 {
		return false
	}
	_, ok := eckerolineSchedules[fromCode+"-"+toCode]
	return ok
}

// eckerolineDayMatch checks if a date matches the schedule's day restriction.
func eckerolineDayMatch(date string, days string) bool {
	if days == "daily" {
		return true
	}
	t, err := models.ParseDate(date)
	if err != nil {
		return true // assume yes if parse fails
	}
	wd := t.Weekday()
	switch days {
	case "mon-sat":
		return wd >= time.Monday && wd <= time.Saturday
	case "sun-fri":
		return wd == time.Sunday || (wd >= time.Monday && wd <= time.Friday)
	}
	return true
}

// eckerolineFormKeyRegex extracts the Magento CSRF form_key from HTML.
var eckerolineFormKeyRegex = regexp.MustCompile(`name="form_key"\s+(?:type="hidden"\s+)?value="([^"]+)"`)

// eckerolineDepartureResponse is the JSON from /checkout/bookingBar/getdepartures.
type eckerolineDepartureResponse struct {
	Departures []eckerolineDeparture `json:"departures"`
}

type eckerolineDeparture struct {
	Time  string  `json:"time"`  // "09:00"
	Price float64 `json:"price"` // 19.00
	Ship  string  `json:"ship"`  // "M/s Finlandia"
}

// tryEckeroLineLive calls Eckerö Line's Magento AJAX API for live departure prices.
// Flow: GET homepage → extract form_key + session cookie → POST getdepartures.
// Every failure is an error: a refusal (429, 403, 5xx) wraps ErrRateLimited and
// an answer of the wrong shape wraps ErrParseFailed. Only a valid departure
// list, possibly empty, returns a nil error.
func tryEckeroLineLive(ctx context.Context, fromCode, toCode, date string) ([]eckerolineDeparture, error) {
	if err := eckerolineLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("eckeroline rate limiter: %w", err)
	}

	// Step 1: GET homepage to obtain form_key + Magento session cookie.
	homeReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.eckeroline.fi/", nil)
	if err != nil {
		return nil, fmt.Errorf("eckeroline homepage request: %w", err)
	}
	homeReq.Header.Set("Accept", "text/html")
	homeReq.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36")

	homeResp, err := eckerolineClient.Do(homeReq)
	if err != nil {
		return nil, fmt.Errorf("eckeroline homepage: %w", err)
	}
	defer func() { _ = homeResp.Body.Close() }()

	if homeResp.StatusCode != http.StatusOK {
		return nil, httpStatusError("eckeroline", "homepage", homeResp.StatusCode)
	}

	homeBody, err := io.ReadAll(io.LimitReader(homeResp.Body, 512*1024))
	if err != nil {
		return nil, fmt.Errorf("eckeroline homepage read: %w", err)
	}

	formKeyMatch := eckerolineFormKeyRegex.FindSubmatch(homeBody)
	if len(formKeyMatch) < 2 {
		return nil, fmt.Errorf("eckeroline homepage: no form_key: %w", models.ErrParseFailed)
	}
	formKey := string(formKeyMatch[1])

	var cookieParts []string
	for _, c := range homeResp.Cookies() {
		cookieParts = append(cookieParts, c.Name+"="+c.Value)
	}
	sessionCookies := strings.Join(cookieParts, "; ")
	if sessionCookies == "" {
		return nil, fmt.Errorf("eckeroline homepage: no session cookie: %w", models.ErrParseFailed)
	}

	// Step 2: POST /checkout/bookingBar/getdepartures with form_key + session.
	direction := "outward"
	if fromCode == "TLL" {
		direction = "homeward"
	}

	payload := fmt.Sprintf(
		`{"form_key":"%s","date":"%s","district":"%s","earliest":"","latest":"","timeType":"","productCode":"ELI_STD","vehicle1":"","vehicle2":"","withCar":false,"adults":1,"children":0,"infants":0}`,
		formKey, date, direction,
	)

	depReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://www.eckeroline.fi/checkout/bookingBar/getdepartures", strings.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("eckeroline getdepartures request: %w", err)
	}
	depReq.Header.Set("Content-Type", "application/json")
	depReq.Header.Set("Accept", "application/json")
	depReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	depReq.Header.Set("Cookie", sessionCookies)
	depReq.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36")

	depResp, err := eckerolineClient.Do(depReq)
	if err != nil {
		return nil, fmt.Errorf("eckeroline getdepartures: %w", err)
	}
	defer func() { _ = depResp.Body.Close() }()

	if depResp.StatusCode != http.StatusOK {
		return nil, httpStatusError("eckeroline", "getdepartures", depResp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(depResp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("eckeroline getdepartures read: %w", err)
	}

	departures, err := parseEckeroDepartures(body)
	if err != nil {
		slog.Debug("eckeroline getdepartures parse failed", "body", string(body[:min(len(body), 200)]))
		return nil, err
	}
	slog.Debug("eckeroline live departures", "count", len(departures))
	return departures, nil
}

// parseEckeroDepartures accepts a bare departure list or {"departures": [...]}.
// Any other shape (an error object, a maintenance page) is a parse failure,
// never an empty answer.
func parseEckeroDepartures(body []byte) ([]eckerolineDeparture, error) {
	var list []eckerolineDeparture
	if err := json.Unmarshal(body, &list); err == nil {
		return list, nil
	}
	var wrapped map[string]json.RawMessage
	if err := json.Unmarshal(body, &wrapped); err != nil || wrapped["departures"] == nil {
		return nil, fmt.Errorf("eckeroline getdepartures: unexpected answer: %w", models.ErrParseFailed)
	}
	if err := json.Unmarshal(wrapped["departures"], &list); err != nil {
		return nil, fmt.Errorf("eckeroline getdepartures: departures: %w", models.ErrParseFailed)
	}
	return list, nil
}

// SearchEckeroLine returns Eckerö Line ferry departures for the given route and date.
// It first tries the Magento AJAX API for live departures with prices. Falls back to
// published timetable with reference prices when the API is unavailable.
func SearchEckeroLine(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	fromCode, fromName, fromCity, ok := LookupEckeroLinePort(from)
	if !ok {
		return nil, fmt.Errorf("eckeroline: no port for %q: %w", from, models.ErrNotApplicable)
	}
	toCode, toName, toCity, ok := LookupEckeroLinePort(to)
	if !ok {
		return nil, fmt.Errorf("eckeroline: no port for %q: %w", to, models.ErrNotApplicable)
	}
	if _, err := models.ParseDate(date); err != nil {
		return nil, fmt.Errorf("eckeroline: invalid date %q: %w", date, err)
	}

	key := fromCode + "-" + toCode
	if _, ok := eckerolineSchedules[key]; !ok {
		return nil, fmt.Errorf("eckeroline: no sailing %s: %w", key, models.ErrNotApplicable)
	}

	slog.Debug("eckeroline search", "from", fromCity, "to", toCity, "date", date)
	bookingURL := fmt.Sprintf("https://www.eckeroline.fi/booking?from=%s&to=%s&date=%s&adults=1", fromCode, toCode, date)

	// Try live departures from Magento AJAX API. A valid live answer is
	// authoritative: an empty list means no sailing that date, a priced
	// departure is a live fare, and a departure without a price has no fare
	// (never the reference price).
	liveDeps, liveErr := tryEckeroLineLive(ctx, fromCode, toCode, date)
	if liveErr == nil {
		var routes []models.GroundRoute
		for _, dep := range liveDeps {
			if dep.Time == "" {
				continue
			}
			depTime := date + "T" + dep.Time + ":00"
			// Estimate arrival: ~2h15m crossing.
			depT, _ := time.Parse("2006-01-02T15:04:05", depTime)
			arrTime := depT.Add(135 * time.Minute).Format("2006-01-02T15:04:05")

			ship := dep.Ship
			if ship == "" {
				ship = "M/S Finlandia"
			}
			route := models.GroundRoute{
				Provider:   "eckeroline",
				Type:       "ferry",
				Currency:   "EUR", // Eckerö fares are euros
				Duration:   135,
				Departure:  models.GroundStop{City: fromCity, Station: fromName + " (" + ship + ")", Time: depTime},
				Arrival:    models.GroundStop{City: toCity, Station: toName, Time: arrTime},
				Transfers:  0,
				BookingURL: bookingURL,
			}
			if dep.Price > 0 {
				route.Price = dep.Price
				route.PriceProvenance = models.PriceLive
				route.Amenities = []string{"Live"}
			}
			routes = append(routes, route)
		}
		slog.Debug("eckeroline live results", "routes", len(routes))
		return routes, nil
	}
	slog.Debug("eckeroline live API error", "err", logredact.Err(liveErr))

	// Fallback: published timetable with reference prices.
	entries := eckerolineSchedules[key]
	var routes []models.GroundRoute
	for _, e := range entries {
		if !eckerolineDayMatch(date, e.Days) {
			continue
		}

		t, _ := models.ParseDate(date)
		arrDate := t.AddDate(0, 0, e.ArrOffset)
		depTime := date + "T" + e.DepTime + ":00"
		arrTime := arrDate.Format("2006-01-02") + "T" + e.ArrTime + ":00"

		routes = append(routes, models.GroundRoute{
			Provider:   "eckeroline",
			Type:       "ferry",
			Price:      eckerolineBasePrice,
			Currency:   "EUR", // Eckerö fares and the reference price are euros
			Duration:   e.Duration,
			Departure:  models.GroundStop{City: fromCity, Station: fromName + " (M/S Finlandia)", Time: depTime},
			Arrival:    models.GroundStop{City: toCity, Station: toName, Time: arrTime},
			Transfers:  0,
			Amenities:  []string{"Reference"},
			BookingURL: bookingURL,
		})
	}

	slog.Debug("eckeroline reference results", "routes", len(routes))
	if liveErr != nil {
		// The live fetch really failed: the timetable still answers, as
		// reference (estimated) fares, and the caller learns why.
		return routes, &models.ProviderNotice{
			Reason:  models.ClassifyProviderReason(liveErr.Error()),
			FixHint: "live prices failed; fares shown are the published reference price",
			Err:     liveErr,
		}
	}
	return routes, nil
}
