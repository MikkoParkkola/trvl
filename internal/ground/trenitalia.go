package ground

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// trenitaliaHost is the base host for the Trenitalia BFF API.
// Overridable in tests via httptest.Server.
var trenitaliaHost = "https://www.lefrecce.it"

// trenitaliaLimiter: conservative 5 req/min to avoid hammering the BFF.
var trenitaliaLimiter = newProviderLimiter(12 * time.Second)

// trenitaliaClient is a shared HTTP client for Trenitalia API calls.
var trenitaliaClient = &http.Client{
	Timeout: 30 * time.Second,
}

// trenitaliaLocationResult is one entry from the station resolver endpoint.
type trenitaliaLocationResult struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	Timezone     string `json:"timezone"`
	Multistation bool   `json:"multistation"`
	CentroidID   int    `json:"centroidId"`
}

// trenitaliaSolutionsRequest is the POST body for fare search.
type trenitaliaSolutionsRequest struct {
	DepartureLocationID int                             `json:"departureLocationId"`
	ArrivalLocationID   int                             `json:"arrivalLocationId"`
	DepartureTime       string                          `json:"departureTime"`
	Adults              int                             `json:"adults"`
	Children            int                             `json:"children"`
	Criteria            trenitaliaCriteria              `json:"criteria"`
	AdvancedSearch      trenitaliaAdvancedSearchRequest `json:"advancedSearchRequest"`
}

type trenitaliaCriteria struct {
	FrecceOnly   bool   `json:"frecceOnly"`
	RegionalOnly bool   `json:"regionalOnly"`
	NoChanges    bool   `json:"noChanges"`
	Order        string `json:"order"`
	Limit        int    `json:"limit"`
	Offset       int    `json:"offset"`
}

type trenitaliaAdvancedSearchRequest struct {
	BestFare bool `json:"bestFare"`
}

// trenitaliaSolutionsResponse is the top-level response from the solutions endpoint.
type trenitaliaSolutionsResponse struct {
	SearchID  string                      `json:"searchId"`
	Solutions []trenitaliaSolutionWrapper `json:"solutions"`
}

type trenitaliaSolutionWrapper struct {
	Solution trenitaliaSolution `json:"solution"`
}

type trenitaliaSolution struct {
	Origin        string            `json:"origin"`
	Destination   string            `json:"destination"`
	DepartureTime string            `json:"departureTime"`
	ArrivalTime   string            `json:"arrivalTime"`
	Duration      string            `json:"duration"` // e.g. "3h 10min"
	Status        string            `json:"status"`
	Trains        []trenitaliaTrain `json:"trains"`
	Price         trenitaliaPrice   `json:"price"`
}

type trenitaliaTrain struct {
	TrainCategory string `json:"trainCategory"`
	Acronym       string `json:"acronym"`
	Name          string `json:"name"`
}

type trenitaliaPrice struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
}

// italianCities is the static set of cities served by Trenitalia high-speed and
// regional rail. Case-insensitive substring matching is used at call time.
var italianCities = []string{
	"rome", "roma",
	"milan", "milano",
	"florence", "firenze",
	"venice", "venezia",
	"naples", "napoli",
	"turin", "torino",
	"bologna",
	"genoa", "genova",
	"verona",
	"bari",
	"catania",
	"palermo",
	"messina",
	"reggio calabria",
	"salerno",
	"trieste",
	"padova", "padua",
	"brescia",
	"bergamo",
	"trento",
	"ferrara",
	"pisa",
	"siena",
	"perugia",
	"ancona",
	"rimini",
	"pescara",
	"l'aquila",
	"campobasso",
	"potenza",
	"cosenza",
	"lecce",
	"taranto",
	"foggia",
	"brindisi",
}

// HasTrenitaliaRoute returns true if both city names appear to be Italian
// cities served by Trenitalia (case-insensitive substring match).
func HasTrenitaliaRoute(from, to string) bool {
	return isItalianCity(from) && isItalianCity(to)
}

func isItalianCity(city string) bool {
	lower := strings.ToLower(strings.TrimSpace(city))
	// Guard empty/too-short input: an empty string is a substring of every
	// catalog entry, so without this HasTrenitaliaRoute("", "Rome") would match
	// and fire an avoidable live resolver call for missing-param searches.
	if len(lower) < 3 {
		return false
	}
	for _, c := range italianCities {
		if strings.Contains(lower, c) || strings.Contains(c, lower) {
			return true
		}
	}
	return false
}

// trenitaliaCanonical maps recognized city tokens (English aliases and the
// Italian forms) to the Italian name the lefrecce.it resolver indexes. Without
// canonicalisation, querying the resolver with an English alias mis-resolves:
// "Rome" returns "Rometta Messinese" (Sicily), "Turin" returns "Ponte Della
// Venturina" — both unrelated. Querying with the Italian name returns the real
// city centroid (Roma, Torino).
var trenitaliaCanonical = map[string]string{
	"rome": "Roma", "roma": "Roma",
	"milan": "Milano", "milano": "Milano",
	"florence": "Firenze", "firenze": "Firenze",
	"venice": "Venezia", "venezia": "Venezia",
	"naples": "Napoli", "napoli": "Napoli",
	"turin": "Torino", "torino": "Torino",
	"genoa": "Genova", "genova": "Genova",
	"padua": "Padova", "padova": "Padova",
	"bologna": "Bologna", "verona": "Verona", "bari": "Bari",
	"catania": "Catania", "palermo": "Palermo", "messina": "Messina",
	"salerno": "Salerno", "trieste": "Trieste", "brescia": "Brescia",
	"bergamo": "Bergamo", "trento": "Trento", "ferrara": "Ferrara",
	"pisa": "Pisa", "siena": "Siena", "perugia": "Perugia",
	"ancona": "Ancona", "rimini": "Rimini", "pescara": "Pescara",
	"lecce": "Lecce", "taranto": "Taranto", "foggia": "Foggia",
	"brindisi": "Brindisi",
}

// canonicalItalianCity returns the Italian resolver query term for a city name.
// It tolerates real-world inputs an agent may pass: a country qualifier
// ("Milan, Italy"), an English alias ("Rome"), or a station-style name
// ("Milano Centrale", "Milan Centrale") — all normalise to the bare Italian city
// ("Roma", "Milano") so the resolver returns the city centroid. Falls back to the
// trimmed input when nothing is recognised.
func canonicalItalianCity(city string) string {
	raw := strings.TrimSpace(city)
	// Drop a trailing country/region qualifier: "Milan, Italy" -> "Milan".
	if i := strings.IndexByte(raw, ','); i >= 0 {
		raw = strings.TrimSpace(raw[:i])
	}
	lower := strings.ToLower(raw)
	// Whole-string match (the common case: "Milan", "Roma").
	if it, ok := trenitaliaCanonical[lower]; ok {
		return it
	}
	// Token match for station-style names: "Milano Centrale" -> "Milano".
	for _, tok := range strings.Fields(lower) {
		if it, ok := trenitaliaCanonical[tok]; ok {
			return it
		}
	}
	return raw
}

// resolveTrenitaliaStation queries the locations search endpoint and returns
// the best matching station ID for the given city name.
//
// English aliases are canonicalised to Italian first (see trenitaliaCanonical),
// then among the results we prefer a non-multistation station whose name
// actually contains the queried city term — guarding against the resolver
// returning an unrelated first hit (e.g. a fuzzy match in a different region).
// Falls back to the first non-multistation result, then the first result.
func resolveTrenitaliaStation(ctx context.Context, city string) (int, string, error) {
	query := canonicalItalianCity(city)
	apiURL := fmt.Sprintf("%s/Channels.Website.BFF.WEB/website/locations/search?name=%s&limit=5",
		trenitaliaHost, url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return 0, "", fmt.Errorf("trenitalia station resolve: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "trvl/1.0 (travel agent; github.com/MikkoParkkola/trvl)")

	resp, err := trenitaliaClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("trenitalia station resolve: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, "", fmt.Errorf("trenitalia station resolve: HTTP %d: %s", resp.StatusCode, body)
	}

	var results []trenitaliaLocationResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, 512*1024)).Decode(&results); err != nil {
		return 0, "", fmt.Errorf("trenitalia station resolve decode: %w", err)
	}

	if len(results) == 0 {
		return 0, "", fmt.Errorf("trenitalia: no station found for %q", city)
	}

	queryLower := strings.ToLower(query)
	// Best: the city-level "all stations" (multistation) centroid whose name
	// matches the query. The lefrecce site uses this for a city search; it
	// returns valid fares from the city's central station and — critically —
	// avoids picking a peripheral stop. The resolver often lists an airport or
	// suburban station first (e.g. "Palermo Aeroporto", "Catania Acquicella"),
	// and POSTing those station IDs returns HTTP 400, so a first-non-multistation
	// match would silently break common city searches.
	for _, r := range results {
		if r.Multistation && strings.Contains(strings.ToLower(r.Name), queryLower) {
			return r.ID, r.Name, nil
		}
	}
	// Next: a non-multistation station whose name matches the query (cities with
	// no centroid node, e.g. a single-station town).
	for _, r := range results {
		if !r.Multistation && strings.Contains(strings.ToLower(r.Name), queryLower) {
			return r.ID, r.Name, nil
		}
	}
	// Next: any multistation centroid, then any result.
	for _, r := range results {
		if r.Multistation {
			return r.ID, r.Name, nil
		}
	}
	// Last resort: the first result.
	return results[0].ID, results[0].Name, nil
}

// parseTrenitaliaDuration converts duration strings like "3h 10min", "45min",
// "2h" into a total number of minutes.
func parseTrenitaliaDuration(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	total := 0
	// Extract hours.
	if hIdx := strings.Index(s, "h"); hIdx >= 0 {
		hStr := strings.TrimSpace(s[:hIdx])
		if h, err := strconv.Atoi(hStr); err == nil {
			total += h * 60
		}
		s = strings.TrimSpace(s[hIdx+1:])
	}
	// Extract minutes.
	minStr := strings.TrimSuffix(strings.TrimSpace(s), "min")
	minStr = strings.TrimSpace(minStr)
	if minStr != "" {
		if m, err := strconv.Atoi(minStr); err == nil {
			total += m
		}
	}
	return total
}

// buildTrenitaliaBookingURL constructs a search deep-link for lefrecce.it.
func buildTrenitaliaBookingURL(fromID, toID int, date string) string {
	return fmt.Sprintf(
		"https://www.lefrecce.it/Channels.Website.WEB/website/#/it/results?departureLocationId=%d&arrivalLocationId=%d&departureTime=%sT08:00:00.000&adults=1&children=0",
		fromID, toID, date,
	)
}

// normaliseISOTime trims the offset and milliseconds from a Trenitalia
// timestamp (e.g. "2026-07-15T08:00:00.000+02:00" → "2026-07-15T08:00:00").
func normaliseISOTime(s string) string {
	// Strip milliseconds and timezone offset.
	if dot := strings.Index(s, "."); dot >= 0 {
		s = s[:dot]
	}
	return s
}

// SearchTrenitalia searches Trenitalia (lefrecce.it BFF) for high-speed and
// regional rail connections between two Italian cities.
//
// from/to are city names (e.g. "Milan", "Rome"). date is YYYY-MM-DD.
// The function resolves names to Trenitalia station IDs, POSTs the solutions
// request, and maps SALEABLE results with positive prices to GroundRoute values.
func SearchTrenitalia(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	// Trenitalia prices exclusively in EUR (the BFF returns € amounts), so the
	// currency parameter is accepted for signature parity with the other ground
	// providers but does not drive a conversion — results are always EUR.
	_ = currency

	if _, err := models.ParseDate(date); err != nil {
		return nil, fmt.Errorf("trenitalia: invalid date %q: %w", date, err)
	}

	if err := trenitaliaLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("trenitalia rate limiter: %w", err)
	}

	fromID, fromName, err := resolveTrenitaliaStation(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("trenitalia: resolve origin %q: %w", from, err)
	}
	toID, toName, err := resolveTrenitaliaStation(ctx, to)
	if err != nil {
		return nil, fmt.Errorf("trenitalia: resolve destination %q: %w", to, err)
	}

	slog.Debug("trenitalia search", "from", fromName, "to", toName, "date", date, "fromID", fromID, "toID", toID)

	payload := trenitaliaSolutionsRequest{
		DepartureLocationID: fromID,
		ArrivalLocationID:   toID,
		DepartureTime:       date + "T08:00:00.000",
		Adults:              1,
		Children:            0,
		Criteria: trenitaliaCriteria{
			FrecceOnly:   false,
			RegionalOnly: false,
			NoChanges:    false,
			Order:        "DEPARTURE_DATE",
			Limit:        10,
			Offset:       0,
		},
		AdvancedSearch: trenitaliaAdvancedSearchRequest{BestFare: false},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("trenitalia marshal: %w", err)
	}

	apiURL := trenitaliaHost + "/Channels.Website.BFF.WEB/website/ticket/solutions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "trvl/1.0 (travel agent; github.com/MikkoParkkola/trvl)")

	resp, err := trenitaliaClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trenitalia search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("trenitalia: HTTP %d: %s", resp.StatusCode, respBody)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("trenitalia read: %w", err)
	}
	slog.Debug("trenitalia raw response", "status", resp.StatusCode, "body_len", len(respBody))

	var apiResp trenitaliaSolutionsResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("trenitalia decode: %w", err)
	}

	bookingURL := buildTrenitaliaBookingURL(fromID, toID, date)
	var routes []models.GroundRoute

	for _, wrapper := range apiResp.Solutions {
		sol := wrapper.Solution

		// Skip non-bookable or zero-priced solutions.
		if sol.Status != "SALEABLE" {
			continue
		}
		if sol.Price.Amount <= 0 {
			continue
		}

		depTime := normaliseISOTime(sol.DepartureTime)
		arrTime := normaliseISOTime(sol.ArrivalTime)

		// Transfers = number of trains minus 1 (direct = 0).
		transfers := 0
		if len(sol.Trains) > 1 {
			transfers = len(sol.Trains) - 1
		}

		routes = append(routes, models.GroundRoute{
			Provider: "trenitalia",
			Type:     "train",
			Price:    sol.Price.Amount,
			Currency: "EUR",
			Duration: parseTrenitaliaDuration(sol.Duration),
			Departure: models.GroundStop{
				City:    from,
				Station: fromName,
				Time:    depTime,
			},
			Arrival: models.GroundStop{
				City:    to,
				Station: toName,
				Time:    arrTime,
			},
			Transfers:  transfers,
			BookingURL: bookingURL,
		})
	}

	slog.Debug("trenitalia results", "routes", len(routes))
	return routes, nil
}
