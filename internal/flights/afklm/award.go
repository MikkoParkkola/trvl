// Package afklm provides a Flying Blue award flight scanner.
//
// # IMPORTANT: Unverified endpoint and response types
//
// The internal KLM endpoint used by this scanner
// (https://www.klm.com/api/flights/award) and all klmAward* request/response
// types in award_types.go were HYPOTHESIZED based on the public AFKL Offers API
// schema and common SPA patterns. They were NOT verified against live klm.com
// traffic in this implementation session. The endpoint URL, request shape,
// and response JSON fields may all be wrong.
//
// The test fixture (testdata/award_prg_ams.json) is SYNTHETIC — fabricated to
// match the hypothesized types. It does not represent data returned by the real
// KLM API.
//
// To verify and fix: log into klm.com, open DevTools → Network, initiate a
// "Book with miles" search, and inspect the actual XHR request/response. Update
// klmAwardBaseURL, klmAwardSearchRequest, and klmAwardResponse (in award_types.go)
// to match the real traffic. Replace the synthetic fixture with a real response
// sample (stripped of PII).
//
// # Discovery findings (2026-04-22)
//
// The public AFKL Offers API (api.airfranceklm.com/opendata/offers/v3/available-offers)
// accepts bookingFlow "REWARD" in its schema but returns HTTP 400 "Reward not allowed"
// for public API keys. This is an entitlement wall — the enum value is recognised
// but requires a commercial partnership key, not a free developer key.
//
// Alternative probe results:
//   - bookingFlow AWARD, MILES, MILEAGE, POINTS → 400 "Expected one of: [REWARD, CORPORATE, LEISURE, STAFF]"
//   - /opendata/offers/v3/available-miles-offers → 404 (path does not exist)
//   - /opendata/offers/v3/available-award-offers → 404 (path does not exist)
//   - AFKL-TRAVEL-Host AF instead of KL → same 400 "Reward not allowed"
//   - loyaltyCards added to passenger → same 400 "Reward not allowed"
//
// The klm.com SPA ("Book with miles") uses a different internal endpoint
// authenticated via session cookies (Flying Blue OAuth bearer token), not the
// public API key. The endpoint base URL below is hypothesized — see the
// IMPORTANT disclaimer above.
//
// For CI / offline usage, set TRVL_TEST_AWARD_FIXTURE=1 to load responses from
// internal/flights/afklm/testdata/award_*.json instead of making live calls.
package afklm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// klmAwardBaseURL is the hypothesized KLM internal API used by the "Book with
	// miles" SPA. THIS URL WAS NOT VERIFIED AGAINST LIVE TRAFFIC. It requires
	// Flying Blue session cookies (OAuth bearer token) if correct.
	klmAwardBaseURL = "https://www.klm.com/api/flights/award"

	// klmAwardUserAgent mimics the KLM SPA to avoid bot detection.
	klmAwardUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

	// AwardMilesCeiling is the maximum miles budget for a European award flight.
	AwardMilesCeiling = 15_000

	// AwardMilesIdeal is the ideal miles target for a European award flight.
	AwardMilesIdeal = 10_000
)

// ErrNoAwardCookies is returned when no KLM session cookies are configured.
var ErrNoAwardCookies = fmt.Errorf("afklm: award search requires KLM session cookies (Flying Blue login). Set AFKL_KLM_COOKIES env var or pass cookies directly")

// AwardScanner searches for Flying Blue award flight availability across a date range.
// It uses the KLM internal web API (not the public Offers API) authenticated via
// session cookies from a Flying Blue logged-in browser session.
type AwardScanner struct {
	httpClient *http.Client
	cookies    string // raw Cookie header value
	baseURL    string // injectable for tests
	mu         sync.Mutex
	lastCall   time.Time
}

// AwardScannerOptions configures an AwardScanner.
type AwardScannerOptions struct {
	// Cookies is the raw Cookie header value for KLM/Flying Blue session.
	// Required unless AFKL_KLM_COOKIES env var is set.
	Cookies string
	// HTTPClient overrides the default HTTP client (useful for tests).
	HTTPClient *http.Client
	// BaseURL overrides the KLM API base URL (useful for tests).
	BaseURL string
}

// NewAwardScanner creates a new AwardScanner.
// Cookies are resolved from opts.Cookies, then AFKL_KLM_COOKIES env var.
// Returns ErrNoAwardCookies if no cookies can be found.
func NewAwardScanner(opts AwardScannerOptions) (*AwardScanner, error) {
	cookies := opts.Cookies
	if cookies == "" {
		cookies = os.Getenv("AFKL_KLM_COOKIES")
	}
	if cookies == "" {
		return nil, ErrNoAwardCookies
	}

	base := opts.BaseURL
	if base == "" {
		base = klmAwardBaseURL
	}

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	return &AwardScanner{
		httpClient: client,
		cookies:    cookies,
		baseURL:    base,
	}, nil
}

// ScanDateRange searches award availability for a route across the given date
// range. Calls are made sequentially at 1 QPS. Partial errors are collected
// in AwardScanResult.Errors.
//
// dates is a slice of ISO calendar date strings ("2026-06-01", "2026-06-02", ...).
// Use DateRange to generate a month's worth of dates.
func (s *AwardScanner) ScanDateRange(ctx context.Context, origin, destination string, dates []string) (*AwardScanResult, error) {
	result := &AwardScanResult{
		Origin:      origin,
		Destination: destination,
		Errors:      make(map[string]string),
	}

	for _, date := range dates {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		// Rate-limit: 1 call/sec.
		s.mu.Lock()
		elapsed := time.Since(s.lastCall)
		if elapsed < time.Second {
			time.Sleep(time.Second - elapsed)
		}
		s.lastCall = time.Now()
		s.mu.Unlock()

		offers, err := s.Search(ctx, origin, destination, date)
		if err != nil {
			result.Errors[date] = err.Error()
			continue
		}
		result.Offers = append(result.Offers, offers...)
	}
	return result, nil
}

// Search fetches award flight offers for a single date.
// Returns one AwardOffer per flight product in the response.
func (s *AwardScanner) Search(ctx context.Context, origin, destination, date string) ([]AwardOffer, error) {
	// In fixture mode, load from testdata.
	if os.Getenv("TRVL_TEST_AWARD_FIXTURE") == "1" {
		return loadAwardFixture(origin, destination, date)
	}

	reqBody := klmAwardSearchRequest{
		BookingFlow: "REWARD",
		Passengers:  []klmAwardPassenger{{ID: 1, Type: "ADT"}},
		Connections: []klmAwardLeg{
			{
				DepartureDate: date,
				Origin:        klmPlace{Type: "AIRPORT", Code: strings.ToUpper(origin)},
				Destination:   klmPlace{Type: "AIRPORT", Code: strings.ToUpper(destination)},
			},
		},
		Currency:     "EUR",
		CommerCabins: []string{"ECONOMY"},
	}

	rawBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("afklm award: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL, bytes.NewReader(rawBody))
	if err != nil {
		return nil, fmt.Errorf("afklm award: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", klmAwardUserAgent)
	req.Header.Set("Cookie", s.cookies)
	req.Header.Set("Origin", "https://www.klm.com")
	req.Header.Set("Referer", "https://www.klm.com/search/fr/FR/book-with-miles")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("afklm award: http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("afklm award: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		snippet := string(respBody)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return nil, fmt.Errorf("afklm award: API error %d: %s", resp.StatusCode, snippet)
	}

	var awardResp klmAwardResponse
	if err := json.Unmarshal(respBody, &awardResp); err != nil {
		return nil, fmt.Errorf("afklm award: parse response: %w", err)
	}

	return mapAwardResponse(&awardResp, origin, destination, date), nil
}

// mapAwardResponse converts a klmAwardResponse into []AwardOffer.
func mapAwardResponse(resp *klmAwardResponse, origin, destination, date string) []AwardOffer {
	// Index top-level bound connections by id.
	lookup := make(map[int]klmAwardBoundConn)
	if len(resp.Connections) > 0 {
		for _, bc := range resp.Connections[0] {
			lookup[bc.ID] = bc
		}
	}

	var offers []AwardOffer
	for _, rec := range resp.Recommendations {
		for _, fp := range rec.FlightProducts {
			if len(fp.Connections) == 0 {
				continue
			}

			pc := fp.Connections[0]
			bc, ok := lookup[pc.ConnectionID]
			if !ok {
				continue
			}

			miles := pc.MilesPrice.Miles
			if miles == 0 {
				miles = fp.MilesPrice.Miles
			}
			taxEUR := pc.MilesPrice.TaxEUR
			if taxEUR == 0 {
				taxEUR = fp.MilesPrice.TaxEUR
			}
			cabin := pc.Cabin
			if cabin == "" {
				cabin = "ECONOMY"
			}

			stops := 0
			if len(bc.Segments) > 1 {
				stops = len(bc.Segments) - 1
			}

			departTime := ""
			arriveTime := ""
			flightNum := ""
			if len(bc.Segments) > 0 {
				first := bc.Segments[0]
				last := bc.Segments[len(bc.Segments)-1]
				departTime = timeFromDateTime(first.DepartureDateTime)
				arriveTime = timeFromDateTime(last.ArrivalDateTime)
				flightNum = first.MarketingFlight.Carrier.Code + first.MarketingFlight.Number
			}

			offers = append(offers, AwardOffer{
				Date:          date,
				FlightNumber:  flightNum,
				Origin:        origin,
				Destination:   destination,
				DepartureTime: departTime,
				ArrivalTime:   arriveTime,
				Miles:         miles,
				TaxEUR:        taxEUR,
				Cabin:         cabin,
				Stops:         stops,
				Available:     miles > 0,
			})
		}
	}
	return offers
}

// timeFromDateTime extracts "HH:MM" from "2006-01-02T15:04:05".
func timeFromDateTime(dt string) string {
	if len(dt) >= 16 {
		return dt[11:16]
	}
	return dt
}

// DateRange generates a slice of ISO date strings from start to end (inclusive).
// start and end are ISO dates "2006-01-02". Returns nil on parse error.
func DateRange(start, end string) []string {
	const layout = "2006-01-02"
	s, err := time.Parse(layout, start)
	if err != nil {
		return nil
	}
	e, err := time.Parse(layout, end)
	if err != nil {
		return nil
	}
	var dates []string
	for d := s; !d.After(e); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d.Format(layout))
	}
	return dates
}

// MonthDateRange generates all dates in the given year-month (e.g. "2026-06").
func MonthDateRange(yearMonth string) []string {
	const layout = "2006-01"
	t, err := time.Parse(layout, yearMonth)
	if err != nil {
		return nil
	}
	// Last day of month: first day of next month minus one day.
	start := t.Format("2006-01-02")
	endT := t.AddDate(0, 1, 0).AddDate(0, 0, -1)
	end := endT.Format("2006-01-02")
	return DateRange(start, end)
}

// loadAwardFixture loads award offers from a testdata JSON file.
// Used when TRVL_TEST_AWARD_FIXTURE=1. The fixture file is looked up as:
//
//	internal/flights/afklm/testdata/award_<origin>_<dest>.json
func loadAwardFixture(origin, destination, date string) ([]AwardOffer, error) {
	filename := fmt.Sprintf("testdata/award_%s_%s.json", strings.ToLower(origin), strings.ToLower(destination))

	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("afklm award fixture: %w (set TRVL_TEST_AWARD_FIXTURE=0 to skip)", err)
	}

	var resp klmAwardResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("afklm award fixture: parse: %w", err)
	}

	all := mapAwardResponse(&resp, origin, destination, date)

	// Override date to match the requested date (fixture has a fixed representative date).
	for i := range all {
		all[i].Date = date
	}
	return all, nil
}
