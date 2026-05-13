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
// Returns 0 when the API is unavailable (caller falls back to published schedule).
func tryEckeroLineLive(ctx context.Context, fromCode, toCode, date string) ([]eckerolineDeparture, error) {
	if err := eckerolineLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("eckeroline rate limiter: %w", err)
	}

	// Step 1: GET homepage to obtain form_key + Magento session cookie.
	homeReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.eckeroline.fi/", nil)
	if err != nil {
		slog.Debug("eckeroline homepage request build failed", "err", err)
		return nil, nil
	}
	homeReq.Header.Set("Accept", "text/html")
	homeReq.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36")

	homeResp, err := eckerolineClient.Do(homeReq)
	if err != nil {
		slog.Debug("eckeroline homepage fetch failed", "err", err)
		return nil, nil
	}
	defer func() { _ = homeResp.Body.Close() }()

	if homeResp.StatusCode != http.StatusOK {
		slog.Debug("eckeroline homepage non-200", "status", homeResp.StatusCode)
		return nil, nil
	}

	homeBody, err := io.ReadAll(io.LimitReader(homeResp.Body, 512*1024))
	if err != nil {
		slog.Debug("eckeroline homepage body read failed", "err", err)
		return nil, nil
	}

	// Extract form_key from HTML.
	formKeyMatch := eckerolineFormKeyRegex.FindSubmatch(homeBody)
	if len(formKeyMatch) < 2 {
		slog.Debug("eckeroline form_key not found in homepage")
		return nil, nil
	}
	formKey := string(formKeyMatch[1])

	// Collect session cookies from the homepage response.
	var cookieParts []string
	for _, c := range homeResp.Cookies() {
		cookieParts = append(cookieParts, c.Name+"="+c.Value)
	}
	sessionCookies := strings.Join(cookieParts, "; ")
	if sessionCookies == "" {
		slog.Debug("eckeroline no session cookies from homepage")
		return nil, nil
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
		slog.Debug("eckeroline getdepartures request build failed", "err", err)
		return nil, nil
	}
	depReq.Header.Set("Content-Type", "application/json")
	depReq.Header.Set("Accept", "application/json")
	depReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	depReq.Header.Set("Cookie", sessionCookies)
	depReq.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36")

	depResp, err := eckerolineClient.Do(depReq)
	if err != nil {
		slog.Debug("eckeroline getdepartures failed", "err", err)
		return nil, nil
	}
	defer func() { _ = depResp.Body.Close() }()

	if depResp.StatusCode != http.StatusOK {
		slog.Debug("eckeroline getdepartures non-200", "status", depResp.StatusCode)
		return nil, nil
	}

	body, err := io.ReadAll(io.LimitReader(depResp.Body, 64*1024))
	if err != nil {
		slog.Debug("eckeroline getdepartures body read failed", "err", err)
		return nil, nil
	}

	// Try parsing as array directly or as object with departures field.
	var departures []eckerolineDeparture
	if err := json.Unmarshal(body, &departures); err != nil {
		var wrapped eckerolineDepartureResponse
		if err2 := json.Unmarshal(body, &wrapped); err2 != nil {
			slog.Debug("eckeroline getdepartures parse failed", "err", err, "body", string(body[:min(len(body), 200)]))
			return nil, nil
		}
		departures = wrapped.Departures
	}

	slog.Debug("eckeroline live departures", "count", len(departures))
	return departures, nil
}

// SearchEckeroLine returns Eckerö Line ferry departures for the given route and date.
// It first tries the Magento AJAX API for live departures with prices. Falls back to
// published timetable with reference prices when the API is unavailable.
func SearchEckeroLine(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	fromCode, fromName, fromCity, ok := LookupEckeroLinePort(from)
	if !ok {
		return nil, fmt.Errorf("eckeroline: no port for %q", from)
	}
	toCode, toName, toCity, ok := LookupEckeroLinePort(to)
	if !ok {
		return nil, fmt.Errorf("eckeroline: no port for %q", to)
	}
	if currency == "" {
		currency = "EUR"
	}
	if _, err := models.ParseDate(date); err != nil {
		return nil, fmt.Errorf("eckeroline: invalid date %q: %w", date, err)
	}

	key := fromCode + "-" + toCode
	if _, ok := eckerolineSchedules[key]; !ok {
		slog.Debug("eckeroline no schedule for route", "key", key)
		return nil, nil
	}

	slog.Debug("eckeroline search", "from", fromCity, "to", toCity, "date", date)
	bookingURL := fmt.Sprintf("https://www.eckeroline.fi/booking?from=%s&to=%s&date=%s&adults=1", fromCode, toCode, date)

	// Try live departures from Magento AJAX API.
	liveDeps, liveErr := tryEckeroLineLive(ctx, fromCode, toCode, date)
	if liveErr != nil {
		slog.Debug("eckeroline live API error", "err", liveErr)
	}
	if len(liveDeps) > 0 {
		var routes []models.GroundRoute
		for _, dep := range liveDeps {
			if dep.Time == "" {
				continue
			}
			depTime := date + "T" + dep.Time + ":00"
			// Estimate arrival: ~2h15m crossing.
			depT, _ := time.Parse("2006-01-02T15:04:05", depTime)
			arrT := depT.Add(135 * time.Minute)
			arrTime := arrT.Format("2006-01-02T15:04:05")

			ship := dep.Ship
			if ship == "" {
				ship = "M/S Finlandia"
			}
			price := dep.Price
			if price <= 0 {
				price = eckerolineBasePrice
			}

			routes = append(routes, models.GroundRoute{
				Provider:   "eckeroline",
				Type:       "ferry",
				Price:      price,
				Currency:   currency,
				Duration:   135,
				Departure:  models.GroundStop{City: fromCity, Station: fromName + " (" + ship + ")", Time: depTime},
				Arrival:    models.GroundStop{City: toCity, Station: toName, Time: arrTime},
				Transfers:  0,
				Amenities:  []string{"Live"},
				BookingURL: bookingURL,
			})
		}
		if len(routes) > 0 {
			slog.Debug("eckeroline live results", "routes", len(routes))
			return routes, nil
		}
	}

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
			Currency:   currency,
			Duration:   e.Duration,
			Departure:  models.GroundStop{City: fromCity, Station: fromName + " (M/S Finlandia)", Time: depTime},
			Arrival:    models.GroundStop{City: toCity, Station: toName, Time: arrTime},
			Transfers:  0,
			Amenities:  []string{"Reference"},
			BookingURL: bookingURL,
		})
	}

	slog.Debug("eckeroline reference results", "routes", len(routes))
	return routes, nil
}
