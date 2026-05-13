package ground

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/cookies"
	"github.com/MikkoParkkola/trvl/internal/models"
	trvlnab "github.com/MikkoParkkola/trvl/internal/nab"
)

const eurostarGateway = "https://site-api.eurostar.com/gateway"

// eurostarLimiter enforces Eurostar's aggressive rate limit: 3 req/min (conservative).
var eurostarLimiter = newProviderLimiter(20 * time.Second)

// eurostarClient is a dedicated HTTP client for Eurostar API calls.
// Uses Chrome TLS fingerprint via utls to bypass Datadome bot detection.
var eurostarClient = batchexec.ChromeHTTPClient()

var (
	eurostarDo             = func(req *http.Request) (*http.Response, error) { return eurostarClient.Do(req) }
	eurostarFetchViaNab    = fetchEurostarViaNab
	eurostarBrowserCookies = cookies.BrowserCookies
)

type eurostarHeader struct {
	name  string
	value string
}

// EurostarStation holds metadata for a Eurostar station.
type EurostarStation struct {
	UIC     string
	Name    string
	City    string
	Country string
}

// eurostarStations maps lowercase city name to station info.
var eurostarStations = map[string]EurostarStation{
	"london": {
		UIC: "7015400", Name: "London St Pancras", City: "London", Country: "GB",
	},
	"paris": {
		UIC: "8727100", Name: "Paris Gare du Nord", City: "Paris", Country: "FR",
	},
	"brussels": {
		UIC: "8814001", Name: "Brussels Midi", City: "Brussels", Country: "BE",
	},
	"amsterdam": {
		UIC: "8400058", Name: "Amsterdam Centraal", City: "Amsterdam", Country: "NL",
	},
	"rotterdam": {
		UIC: "8400530", Name: "Rotterdam Centraal", City: "Rotterdam", Country: "NL",
	},
	"cologne": {
		UIC: "8015458", Name: "Cologne Hbf", City: "Cologne", Country: "DE",
	},
	"lille": {
		UIC: "8722326", Name: "Lille Europe", City: "Lille", Country: "FR",
	},
	"antwerp": {
		UIC: "8821006", Name: "Antwerpen-Centraal", City: "Antwerp", Country: "BE",
	},
	"antwerpen": {
		UIC: "8821006", Name: "Antwerpen-Centraal", City: "Antwerp", Country: "BE",
	},
}

// LookupEurostarStation resolves a city name to a Eurostar station (case-insensitive).
func LookupEurostarStation(city string) (EurostarStation, bool) {
	s, ok := eurostarStations[strings.ToLower(strings.TrimSpace(city))]
	return s, ok
}

// eurostarSnapRoutes lists the city pairs where Eurostar Snap fares are
// available (up to 14 days before travel).
// Source: https://snap.eurostar.com/uk-en
var eurostarSnapRoutes = map[[2]string]bool{
	{"paris", "brussels"}:   true,
	{"paris", "amsterdam"}:  true,
	{"paris", "rotterdam"}:  true,
	{"paris", "cologne"}:    true,
	{"london", "brussels"}:  true,
	{"london", "paris"}:     true,
	{"london", "lille"}:     true,
	{"london", "amsterdam"}: true,
	{"london", "rotterdam"}: true,
}

// HasEurostarSnapRoute returns true if the city pair is a Snap route
// (checked in both directions).
func HasEurostarSnapRoute(from, to string) bool {
	a := strings.ToLower(strings.TrimSpace(from))
	b := strings.ToLower(strings.TrimSpace(to))
	return eurostarSnapRoutes[[2]string{a, b}] || eurostarSnapRoutes[[2]string{b, a}]
}

func eurostarRequestHeaders(cookieHeader string) []eurostarHeader {
	headers := []eurostarHeader{
		{name: "Content-Type", value: "application/json"},
		{name: "Accept", value: "*/*"},
		{name: "Accept-Language", value: "en-GB,en;q=0.9"},
		{name: "Origin", value: "https://www.eurostar.com"},
		{name: "Referer", value: "https://www.eurostar.com/"},
		{name: "User-Agent", value: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"},
		{name: "x-platform", value: "web"},
		{name: "x-market-code", value: "uk"},
	}
	if cookieHeader != "" {
		headers = append(headers, eurostarHeader{name: "Cookie", value: cookieHeader})
	}
	return headers
}

func applyEurostarHeaders(req *http.Request, cookieHeader string) {
	for _, header := range eurostarRequestHeaders(cookieHeader) {
		req.Header.Set(header.name, header.value)
	}
}

// HasEurostarRoute returns true if both cities have Eurostar stations.
func HasEurostarRoute(from, to string) bool {
	_, fromOK := LookupEurostarStation(from)
	_, toOK := LookupEurostarStation(to)
	return fromOK && toOK
}

// eurostarTimetableEntry holds a single train from the timetableServices response.
type eurostarTimetableEntry struct {
	TrainNumber   string
	DepartureTime string // ISO datetime from origin
	ArrivalTime   string // ISO datetime at destination
}

// eurostarTimetableResponse is the GraphQL response for timetableServices.
type eurostarTimetableResponse struct {
	Data struct {
		TimetableServices []struct {
			Model struct {
				TrainNumber                string `json:"trainNumber"`
				ScheduledDepartureDateTime string `json:"scheduledDepartureDateTime"`
			} `json:"model"`
			Origin struct {
				Model struct {
					ScheduledDepartureDateTime string `json:"scheduledDepartureDateTime"`
				} `json:"model"`
			} `json:"origin"`
			Destination struct {
				Model struct {
					ScheduledArrivalDateTime string `json:"scheduledArrivalDateTime"`
				} `json:"model"`
			} `json:"destination"`
		} `json:"timetableServices"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// eurostarBuildTimetableBody builds the timetableServices GraphQL request body.
func eurostarBuildTimetableBody(originUIC, destUIC, date string) ([]byte, error) {
	// The API expects a full ISO datetime; use midnight UTC for the requested date.
	dateTime := date + "T00:00:00.000Z"
	variables := map[string]interface{}{
		"date":           dateTime,
		"originUic":      originUIC,
		"destinationUic": destUIC,
	}
	body := eurostarGQLBody{
		OperationName: "timetableServices",
		Variables:     variables,
		Query:         `query timetableServices($date: Date!, $trainNumber: String, $originUic: String, $destinationUic: String) { timetableServices(date: $date trainNumber: $trainNumber originUic: $originUic destinationUic: $destinationUic) { model { trainNumber scheduledDepartureDateTime __typename } origin { model { scheduledDepartureDateTime __typename } __typename } destination { model { scheduledArrivalDateTime __typename } __typename } __typename } }`,
	}
	return json.Marshal(body)
}

// searchEurostarTimetable fetches the timetable for a specific date and city pair.
// Returns a slice of timetable entries ordered by departure time.
// A non-fatal API error (e.g. no trains on date) returns an empty slice, not an error.
func searchEurostarTimetable(ctx context.Context, fromStation, toStation EurostarStation, date string) ([]eurostarTimetableEntry, error) {
	body, err := eurostarBuildTimetableBody(fromStation.UIC, toStation.UIC, date)
	if err != nil {
		return nil, fmt.Errorf("eurostar timetable marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, eurostarGateway, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("eurostar timetable request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-GB,en;q=0.9")
	req.Header.Set("Origin", "https://www.eurostar.com")
	req.Header.Set("Referer", "https://www.eurostar.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("x-platform", "web")
	req.Header.Set("x-market-code", "uk")

	slog.Debug("eurostar timetable", "from", fromStation.City, "to", toStation.City, "date", date)

	resp, err := eurostarClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("eurostar timetable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		slog.Debug("eurostar timetable non-200", "status", resp.StatusCode, "body", string(body))
		// Non-fatal: cheapest fares are still usable without timetable data.
		return nil, nil
	}

	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, fmt.Errorf("eurostar timetable read: %w", err)
	}
	slog.Debug("eurostar timetable response", "body_len", len(rawBody))

	var ttResp eurostarTimetableResponse
	if err := json.Unmarshal(rawBody, &ttResp); err != nil {
		slog.Debug("eurostar timetable decode error", "err", err)
		return nil, nil // non-fatal
	}
	if len(ttResp.Errors) > 0 {
		slog.Debug("eurostar timetable graphql error", "msg", ttResp.Errors[0].Message)
		return nil, nil // non-fatal
	}

	var entries []eurostarTimetableEntry
	for _, svc := range ttResp.Data.TimetableServices {
		dep := svc.Origin.Model.ScheduledDepartureDateTime
		if dep == "" {
			dep = svc.Model.ScheduledDepartureDateTime
		}
		arr := svc.Destination.Model.ScheduledArrivalDateTime
		entries = append(entries, eurostarTimetableEntry{
			TrainNumber:   svc.Model.TrainNumber,
			DepartureTime: dep,
			ArrivalTime:   arr,
		})
	}
	return entries, nil
}

// eurostarGQLBody is the full GraphQL request body sent to the Eurostar gateway.
type eurostarGQLBody struct {
	OperationName string                 `json:"operationName"`
	Variables     map[string]interface{} `json:"variables"`
	Query         string                 `json:"query"`
}

// eurostarBuildBody builds the cheapestFaresSearch GraphQL request body.
// If snapOnly is true, uses productFamiliesSearch: ["SNAP"] to filter for
// Eurostar Snap last-minute deals (released ~1 week before travel).
// Regular fares use ["PUB_STANDARD", "RED_PUB_STANDARD"].
func eurostarBuildBody(originUIC, destUIC, startDate, endDate, currency string, snapOnly bool) ([]byte, error) {
	productFamilies := []string{"PUB_STANDARD", "RED_PUB_STANDARD"}
	if snapOnly {
		productFamilies = []string{"SNAP"}
	}
	variables := map[string]interface{}{
		"cheapestFaresLists": []map[string]string{{
			"origin":      originUIC,
			"destination": destUIC,
			"startDate":   startDate,
			"endDate":     endDate,
			"journeyType": "RETURN",
			"direction":   "OUTBOUND",
		}},
		"currency":              strings.ToUpper(currency),
		"numberOfPassenger":     1,
		"productFamiliesSearch": productFamilies,
	}
	body := eurostarGQLBody{
		OperationName: "cheapestFaresSearch",
		Variables:     variables,
		Query:         `query cheapestFaresSearch($numberOfPassenger: Int, $productFamiliesSearch: [String!], $currency: Currency!, $cheapestFaresLists: [CheapestFaresList!]!) { cheapestFaresSearch(numberOfPassenger: $numberOfPassenger, productFamiliesSearch: $productFamiliesSearch, currency: $currency, cheapestFaresLists: $cheapestFaresLists) { cheapestFares { date price __typename } __typename } }`,
	}
	return json.Marshal(body)
}

// eurostarGQLResponse is the expected GraphQL response structure.
type eurostarGQLResponse struct {
	Data struct {
		CheapestFaresSearch []struct {
			CheapestFares []struct {
				Date  string  `json:"date"`
				Price float64 `json:"price"`
			} `json:"cheapestFares"`
		} `json:"cheapestFaresSearch"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// SearchEurostar searches Eurostar for cheapest fares between two cities.
// from/to are city names (e.g. "London", "Paris"). startDate and endDate are YYYY-MM-DD.
// If snapOnly is true, filters for Eurostar Snap last-minute deals only.
func SearchEurostar(ctx context.Context, from, to, startDate, endDate, currency string, snapOnly bool) ([]models.GroundRoute, error) {
	fromStation, ok := LookupEurostarStation(from)
	if !ok {
		return nil, fmt.Errorf("no Eurostar station for %q", from)
	}
	toStation, ok := LookupEurostarStation(to)
	if !ok {
		return nil, fmt.Errorf("no Eurostar station for %q", to)
	}

	if currency == "" {
		currency = "GBP"
	}

	body, err := eurostarBuildBody(fromStation.UIC, toStation.UIC, startDate, endDate, currency, snapOnly)
	if err != nil {
		return nil, fmt.Errorf("eurostar marshal query: %w", err)
	}

	// Wait for rate limiter.
	if err := eurostarLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("eurostar rate limiter: %w", err)
	}

	// newEurostarRequest builds a POST request with standard Eurostar headers.
	// cookieHeader is optional; pass "" to omit.
	newEurostarRequest := func(cookieHeader string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, eurostarGateway, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		applyEurostarHeaders(req, cookieHeader)
		return req, nil
	}

	slog.Debug("eurostar search", "from", fromStation.City, "to", toStation.City,
		"start", startDate, "end", endDate, "snap", snapOnly)

	req, err := newEurostarRequest("")
	if err != nil {
		return nil, err
	}

	resp, err := eurostarDo(req)
	if err != nil {
		return nil, fmt.Errorf("eurostar search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusForbidden {
		firstBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		_ = firstBody // consumed for logging; body closed by defer

		// Attempt retry with browser cookies.
		cookieHeader := eurostarBrowserCookies("eurostar.com")
		if cookieHeader != "" {
			slog.Debug("retrying eurostar with browser cookies")
			req2, err2 := newEurostarRequest(cookieHeader)
			if err2 != nil {
				return nil, fmt.Errorf("eurostar retry build: %w", err2)
			}
			resp2, err2 := eurostarDo(req2)
			if err2 != nil {
				return nil, fmt.Errorf("eurostar retry: %w", err2)
			}
			defer func() { _ = resp2.Body.Close() }()
			if resp2.StatusCode == http.StatusOK {
				body2, err3 := io.ReadAll(io.LimitReader(resp2.Body, 1024*1024))
				if err3 != nil {
					return nil, fmt.Errorf("eurostar read (cookie retry): %w", err3)
				}
				return parseEurostarSearchResponse(ctx, body2, fromStation, toStation, startDate, currency, snapOnly)
			}
			// Cookie retry did not yield 200; log and fall through to 403 error.
			retryBody, _ := io.ReadAll(io.LimitReader(resp2.Body, 512))
			slog.Debug("eurostar cookie retry non-200", "status", resp2.StatusCode, "body", string(retryBody))
		}

		if nRoutes, nErr := eurostarFetchViaNab(ctx, body, fromStation, toStation, startDate, currency, snapOnly); nErr == nil && len(nRoutes) > 0 {
			return nRoutes, nil
		} else if nErr != nil && !errors.Is(nErr, trvlnab.ErrNotAvailable) {
			slog.Debug("eurostar nab fallback failed", "err", nErr)
		}

		isCaptcha, captchaURL := cookies.IsCaptchaResponse(http.StatusForbidden, firstBody)
		if isCaptcha {
			slog.Warn("eurostar requires browser verification", "captcha_url", captchaURL)
		}
		return nil, fmt.Errorf("eurostar search: HTTP 403: %s", firstBody)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("eurostar search: HTTP %d: %s", resp.StatusCode, respBody)
	}

	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, fmt.Errorf("eurostar read body: %w", err)
	}
	return parseEurostarSearchResponse(ctx, rawBody, fromStation, toStation, startDate, currency, snapOnly)
}

func parseEurostarSearchResponse(
	ctx context.Context,
	rawBody []byte,
	fromStation, toStation EurostarStation,
	startDate, currency string,
	snapOnly bool,
) ([]models.GroundRoute, error) {
	preview := rawBody
	if len(preview) > 500 {
		preview = preview[:500]
	}
	slog.Debug("eurostar response", "body_len", len(rawBody), "body_preview", string(preview))

	var gqlResp eurostarGQLResponse
	if err := json.Unmarshal(rawBody, &gqlResp); err != nil {
		return nil, fmt.Errorf("eurostar decode: %w", err)
	}
	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("eurostar graphql: %s", gqlResp.Errors[0].Message)
	}

	timetable, _ := searchEurostarTimetable(ctx, fromStation, toStation, startDate)
	return buildEurostarRoutes(gqlResp, fromStation, toStation, currency, snapOnly, timetable)
}

func fetchEurostarViaNab(
	ctx context.Context,
	requestBody []byte,
	fromStation, toStation EurostarStation,
	startDate, currency string,
	snapOnly bool,
) ([]models.GroundRoute, error) {
	client, err := trvlnab.New()
	if err != nil {
		return nil, err
	}

	var headers []string
	for _, header := range eurostarRequestHeaders("") {
		headers = append(headers, fmt.Sprintf("%s: %s", header.name, header.value))
	}

	body, err := client.Fetch(ctx, eurostarGateway, trvlnab.FetchOptions{
		Method:  "POST",
		Body:    string(requestBody),
		Headers: headers,
	})
	if err != nil {
		return nil, err
	}
	return parseEurostarSearchResponse(ctx, body, fromStation, toStation, startDate, currency, snapOnly)
}

// eurostarRouteDuration returns the typical journey duration in minutes for a
// Eurostar city pair. Durations are approximate scheduled times.
func eurostarRouteDuration(fromCity, toCity string) int {
	key := strings.ToLower(fromCity) + "-" + strings.ToLower(toCity)
	switch key {
	case "london-paris", "paris-london":
		return 135 // 2h 15m
	case "london-brussels", "brussels-london":
		return 120 // 2h 00m
	case "london-amsterdam", "amsterdam-london":
		return 195 // 3h 15m
	case "london-rotterdam", "rotterdam-london":
		return 180 // 3h 00m
	case "london-cologne", "cologne-london":
		return 240 // 4h 00m
	default:
		return 135 // default to London–Paris
	}
}

// buildEurostarRoutes converts a parsed GraphQL response into GroundRoute values.
// When timetable data is available (non-nil, non-empty), each cheapest-fare date
// entry is expanded into one route per train on that date using actual departure
// and arrival times. When no timetable data is provided, falls back to showing the
// date as "Jan 02" (daily cheapest price display).
func buildEurostarRoutes(gqlResp eurostarGQLResponse, fromStation, toStation EurostarStation, currency string, snapOnly bool, timetable []eurostarTimetableEntry) ([]models.GroundRoute, error) {
	defaultDuration := eurostarRouteDuration(fromStation.City, toStation.City)
	provider := "eurostar"
	if snapOnly {
		provider = "eurostar snap"
	}

	var routes []models.GroundRoute
	for _, search := range gqlResp.Data.CheapestFaresSearch {
		for _, fare := range search.CheapestFares {
			if fare.Price <= 0 {
				continue
			}

			// If we have timetable trains for this fare's date, emit one route per train.
			var fareTrains []eurostarTimetableEntry
			for _, tt := range timetable {
				if len(tt.DepartureTime) >= 10 && tt.DepartureTime[:10] == fare.Date {
					fareTrains = append(fareTrains, tt)
				}
			}

			if len(fareTrains) > 0 {
				for _, tt := range fareTrains {
					dur := defaultDuration
					if tt.DepartureTime != "" && tt.ArrivalTime != "" {
						if computed := computeDurationMinutes(tt.DepartureTime, tt.ArrivalTime); computed > 0 {
							dur = computed
						}
					}
					routes = append(routes, models.GroundRoute{
						Provider: provider,
						Type:     "train",
						Price:    fare.Price,
						Currency: strings.ToUpper(currency),
						Duration: dur,
						Departure: models.GroundStop{
							City:    fromStation.City,
							Station: fromStation.Name,
							Time:    tt.DepartureTime,
						},
						Arrival: models.GroundStop{
							City:    toStation.City,
							Station: toStation.Name,
							Time:    tt.ArrivalTime,
						},
						BookingURL: buildEurostarBookingURL(fromStation.UIC, toStation.UIC, fare.Date),
					})
				}
			} else {
				// No timetable data — show date as "Jan 02" (daily cheapest fallback).
				displayDate := fare.Date
				if t, err := models.ParseDate(fare.Date); err == nil {
					displayDate = t.Format("Jan 02")
				}
				routes = append(routes, models.GroundRoute{
					Provider: provider,
					Type:     "train",
					Price:    fare.Price,
					Currency: strings.ToUpper(currency),
					Duration: defaultDuration,
					Departure: models.GroundStop{
						City:    fromStation.City,
						Station: fromStation.Name,
						Time:    displayDate,
					},
					Arrival: models.GroundStop{
						City:    toStation.City,
						Station: toStation.Name,
						Time:    displayDate,
					},
					BookingURL: buildEurostarBookingURL(fromStation.UIC, toStation.UIC, fare.Date),
				})
			}
		}
	}
	return routes, nil
}

func buildEurostarBookingURL(originUIC, destUIC, date string) string {
	return fmt.Sprintf("https://www.eurostar.com/en/train-tickets?origin=%s&destination=%s&outbound=%s",
		url.QueryEscape(originUIC), url.QueryEscape(destUIC), url.QueryEscape(date))
}
