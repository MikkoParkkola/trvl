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
	DepartureLocationID int                            `json:"departureLocationId"`
	ArrivalLocationID   int                            `json:"arrivalLocationId"`
	DepartureTime       string                         `json:"departureTime"`
	Adults              int                            `json:"adults"`
	Children            int                            `json:"children"`
	Criteria            trenitaliaCriteria             `json:"criteria"`
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
	Origin        string              `json:"origin"`
	Destination   string              `json:"destination"`
	DepartureTime string              `json:"departureTime"`
	ArrivalTime   string              `json:"arrivalTime"`
	Duration      string              `json:"duration"` // e.g. "3h 10min"
	Status        string              `json:"status"`
	Trains        []trenitaliaTrain   `json:"trains"`
	Price         trenitaliaPrice     `json:"price"`
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
	for _, c := range italianCities {
		if strings.Contains(lower, c) || strings.Contains(c, lower) {
			return true
		}
	}
	return false
}

// resolveTrenitaliaStation queries the locations search endpoint and returns
// the best matching station ID for the given city name.
// Preference order: non-multistation exact-name match, then non-multistation
// first result, then any first result.
func resolveTrenitaliaStation(ctx context.Context, city string) (int, string, error) {
	apiURL := fmt.Sprintf("%s/Channels.Website.BFF.WEB/website/locations/search?name=%s&limit=5",
		trenitaliaHost, url.QueryEscape(city))

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

	// Prefer non-multistation results (a real station vs. a "all stations" node).
	for _, r := range results {
		if !r.Multistation {
			return r.ID, r.Name, nil
		}
	}
	// Fall back to first result.
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
	if currency == "" {
		currency = "EUR"
	}

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
