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
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/cookies"
	"github.com/MikkoParkkola/trvl/internal/models"
	trvlnab "github.com/MikkoParkkola/trvl/internal/nab"
)

const trainlineSearchURL = "https://www.thetrainline.com/api/journey-search/"

// trainlineLimiter: 5 req/min to be respectful
var trainlineLimiter = newProviderLimiter(12 * time.Second)

// trainlineClient uses Chrome TLS fingerprint to bypass Datadome bot detection.
var trainlineClient = batchexec.ChromeHTTPClient()

var (
	trainlineDo             = func(req *http.Request) (*http.Response, error) { return trainlineClient.Do(req) }
	trainlineFetchViaNab    = fetchTrainlineViaNab
	trainlineBrowserCookies = cookies.BrowserCookies
)

type trainlineHeader struct {
	name  string
	value string
}

// trainlineStations maps city names to Trainline station IDs.
// Station IDs from: https://github.com/trainline-eu/stations
var trainlineStations = map[string]string{
	"london":     "8267",
	"paris":      "4916",
	"amsterdam":  "8657",
	"brussels":   "5893",
	"berlin":     "7527",
	"munich":     "7480",
	"frankfurt":  "7604",
	"hamburg":    "7626",
	"cologne":    "21178",
	"vienna":     "22644",
	"zurich":     "6401",
	"milan":      "8490",
	"rome":       "8544",
	"barcelona":  "6617",
	"madrid":     "6663",
	"prague":     "17587",
	"warsaw":     "10491",
	"budapest":   "18819",
	"copenhagen": "17515",
	"stockholm":  "38711",
	"rotterdam":  "23616",
	"lille":      "4652",
	"lyon":       "4718",
	"marseille":  "4790",
	"nice":       "4836",
	"strasbourg": "153",
	"toulouse":   "5306",
	"venice":     "8574",
	"florence":   "8434",
	"salzburg":   "6994",
	"innsbruck":  "10461",
	"geneva":     "5335",
	"basel":      "5877",
	"antwerp":    "5929",
}

// trainlineURN converts a raw station ID to the Trainline URN format.
func trainlineURN(id string) string {
	return "urn:trainline:generic:loc:" + id
}

func trainlineRequestHeaders(cookieHeader string) []trainlineHeader {
	headers := []trainlineHeader{
		{name: "Content-Type", value: "application/json"},
		{name: "Accept", value: "application/json"},
		{name: "Accept-Language", value: "en-GB,en;q=0.9"},
		{name: "Origin", value: "https://www.thetrainline.com"},
		{name: "Referer", value: "https://www.thetrainline.com/"},
		{name: "User-Agent", value: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"},
		{name: "sec-ch-ua", value: `"Chromium";v="133", "Not(A:Brand";v="99"`},
		{name: "sec-ch-ua-mobile", value: "?0"},
		{name: "sec-ch-ua-platform", value: `"macOS"`},
		{name: "sec-fetch-dest", value: "empty"},
		{name: "sec-fetch-mode", value: "cors"},
		{name: "sec-fetch-site", value: "same-origin"},
		{name: "x-version", value: "4.46.32109"},
	}
	if cookieHeader != "" {
		headers = append(headers, trainlineHeader{name: "Cookie", value: cookieHeader})
	}
	return headers
}

func applyTrainlineHeaders(req *http.Request, cookieHeader string) {
	for _, header := range trainlineRequestHeaders(cookieHeader) {
		req.Header.Set(header.name, header.value)
	}
}

// LookupTrainlineStation resolves a city name to a Trainline station ID.
func LookupTrainlineStation(city string) (string, bool) {
	id, ok := trainlineStations[strings.ToLower(strings.TrimSpace(city))]
	return id, ok
}

// HasTrainlineStation returns true if the city has a known Trainline station.
func HasTrainlineStation(city string) bool {
	_, ok := LookupTrainlineStation(city)
	return ok
}

// trainlineJourneySearchRequest is the JSON body for the journey-search API.
type trainlineJourneySearchRequest struct {
	Passengers              []trainlinePassenger  `json:"passengers"`
	IsEurope                bool                  `json:"isEurope"`
	Cards                   []any                 `json:"cards"`
	TransitDefinitions      []trainlineTransitDef `json:"transitDefinitions"`
	Type                    string                `json:"type"`
	MaximumJourneys         int                   `json:"maximumJourneys"`
	IncludeRealtime         bool                  `json:"includeRealtime"`
	TransportModes          []string              `json:"transportModes"`
	DirectSearch            bool                  `json:"directSearch"`
	Composition             []string              `json:"composition"`
	AutoApplyCorporateCodes bool                  `json:"autoApplyCorporateCodes"`
	Origin                  string                `json:"origin"`
	Destination             string                `json:"destination"`
}

type trainlinePassenger struct {
	DateOfBirth string `json:"dateOfBirth"`
	CardIDs     []any  `json:"cardIds"`
}

type trainlineTransitDef struct {
	Direction   string               `json:"direction"`
	Origin      string               `json:"origin"`
	Destination string               `json:"destination"`
	JourneyDate trainlineJourneyDate `json:"journeyDate"`
}

type trainlineJourneyDate struct {
	Type string `json:"type"`
	Time string `json:"time"`
}

// trainlineJourneySearchResponse is the top-level response from journey-search.
type trainlineJourneySearchResponse struct {
	Journeys []trainlineJourney `json:"journeys"`
	Tickets  []trainlineTicket  `json:"tickets"`
}

type trainlineJourney struct {
	ID            string         `json:"id"`
	DepartureTime string         `json:"departureTime"`
	ArrivalTime   string         `json:"arrivalTime"`
	Legs          []trainlineLeg `json:"legs"`
	TicketIDs     []string       `json:"ticketIds"`
}

type trainlineLeg struct {
	DepartureTime string `json:"departureTime"`
	ArrivalTime   string `json:"arrivalTime"`
	TransportMode string `json:"transportMode"`
	Carrier       string `json:"carrier"`
}

type trainlineTicket struct {
	ID         string           `json:"id"`
	JourneyIDs []string         `json:"journeyIds"`
	Prices     []trainlinePrice `json:"prices"`
}

type trainlinePrice struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// trainlineViaCurl calls /api/journey-search/ using the system curl binary with
// a cookie jar. macOS curl uses BoringSSL / Secure Transport which produces a
// browser-like TLS ClientHello that often passes Datadome's TLS fingerprint
// check. We first visit the homepage with curl to seed the cookie jar (so the
// datadome cookie is associated with the same TLS session), then POST to the API.
func trainlineViaCurl(ctx context.Context, fromID, toID, date, currency string) ([]models.GroundRoute, error) {
	dateTime, err := models.ParseDate(date)
	if err != nil {
		return nil, fmt.Errorf("trainlineViaCurl invalid date %q: %w", date, err)
	}
	departureISO := dateTime.Add(6 * time.Hour).Format("2006-01-02T15:04:05")

	originURN := trainlineURN(fromID)
	destURN := trainlineURN(toID)

	reqBody := trainlineJourneySearchRequest{
		Passengers:              []trainlinePassenger{{DateOfBirth: "1996-01-01", CardIDs: []any{}}},
		IsEurope:                true,
		Cards:                   []any{},
		Type:                    "single",
		MaximumJourneys:         5,
		IncludeRealtime:         true,
		TransportModes:          []string{"mixed"},
		DirectSearch:            false,
		Composition:             []string{"through", "interchangeSplit"},
		AutoApplyCorporateCodes: false,
		Origin:                  originURN,
		Destination:             destURN,
		TransitDefinitions: []trainlineTransitDef{
			{
				Direction:   "outward",
				Origin:      originURN,
				Destination: destURN,
				JourneyDate: trainlineJourneyDate{
					Type: "departAfter",
					Time: departureISO,
				},
			},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("trainlineViaCurl marshal: %w", err)
	}

	// Common browser-like headers shared between the seed and API requests.
	commonHeaders := []string{
		"-H", "Accept-Language: en-GB,en;q=0.9",
		"-H", `sec-ch-ua: "Chromium";v="133", "Not(A:Brand";v="99"`,
		"-H", "sec-ch-ua-mobile: ?0",
		"-H", `sec-ch-ua-platform: "macOS"`,
		"-H", "User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36",
	}

	// Step 1: Seed the cookie jar by visiting the homepage so Datadome sets its
	// cookie bound to this exact curl TLS session.
	cookieJarFile := fmt.Sprintf("/tmp/trainline-cookies-%d.txt", time.Now().UnixNano())
	defer func() { _ = os.Remove(cookieJarFile) }()
	seedArgs := append([]string{
		"-s", "--http2",
		"-L",                // follow redirects
		"-c", cookieJarFile, // write cookies
		"-b", cookieJarFile, // send cookies
		"-H", "Accept: text/html,application/xhtml+xml",
		"-H", "sec-fetch-dest: document",
		"-H", "sec-fetch-mode: navigate",
		"-H", "sec-fetch-site: none",
		"https://www.thetrainline.com",
		"-o", "/dev/null",
	}, commonHeaders...)

	seedCmd := exec.CommandContext(ctx, "curl", seedArgs...)
	if seedErr := seedCmd.Run(); seedErr != nil {
		slog.Debug("trainlineViaCurl: seed request failed", "err", seedErr)
		// Continue anyway — the API call may still work.
	} else {
		slog.Debug("trainlineViaCurl: homepage seed complete", "jar", cookieJarFile)
	}

	// Step 2: POST to the journey-search API using the seeded cookie jar.
	apiArgs := append([]string{
		"-s", "--http2",
		"-X", "POST",
		"-c", cookieJarFile,
		"-b", cookieJarFile,
		trainlineSearchURL,
		"-H", "Content-Type: application/json",
		"-H", "Accept: application/json",
		"-H", "sec-fetch-dest: empty",
		"-H", "sec-fetch-mode: cors",
		"-H", "sec-fetch-site: same-origin",
		"-H", "x-version: 4.46.32109",
		"-H", "Origin: https://www.thetrainline.com",
		"-H", "Referer: https://www.thetrainline.com/",
		"-d", string(bodyBytes),
	}, commonHeaders...)

	apiCmd := exec.CommandContext(ctx, "curl", apiArgs...)
	curlOut, err := apiCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("trainlineViaCurl curl: %w", err)
	}

	trimmed := strings.TrimSpace(string(curlOut))
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return nil, fmt.Errorf("trainlineViaCurl: non-JSON response (%.80s)", trimmed)
	}

	return readAndParseTrainlineResponse(strings.NewReader(trimmed), "", "", date, currency)
}

// SearchTrainline searches thetrainline.com for train connections between two cities.
// By default it uses the public journey-search API only. Browser/curl-assisted
// fallbacks are opt-in via allowBrowserFallbacks.
func SearchTrainline(ctx context.Context, from, to, date, currency string, allowBrowserFallbacks bool) ([]models.GroundRoute, error) {
	fromID, ok := LookupTrainlineStation(from)
	if !ok {
		return nil, fmt.Errorf("no Trainline station for %q", from)
	}
	toID, ok := LookupTrainlineStation(to)
	if !ok {
		return nil, fmt.Errorf("no Trainline station for %q", to)
	}

	dateTime, err := models.ParseDate(date)
	if err != nil {
		return nil, fmt.Errorf("invalid date %q: %w", date, err)
	}
	departureISO := dateTime.Add(6 * time.Hour).Format("2006-01-02T15:04:05")

	originURN := trainlineURN(fromID)
	destURN := trainlineURN(toID)

	reqBody := trainlineJourneySearchRequest{
		Passengers:              []trainlinePassenger{{DateOfBirth: "1996-01-01", CardIDs: []any{}}},
		IsEurope:                true,
		Cards:                   []any{},
		Type:                    "single",
		MaximumJourneys:         5,
		IncludeRealtime:         true,
		TransportModes:          []string{"mixed"},
		DirectSearch:            false,
		Composition:             []string{"through", "interchangeSplit"},
		AutoApplyCorporateCodes: false,
		Origin:                  originURN,
		Destination:             destURN,
		TransitDefinitions: []trainlineTransitDef{
			{
				Direction:   "outward",
				Origin:      originURN,
				Destination: destURN,
				JourneyDate: trainlineJourneyDate{
					Type: "departAfter",
					Time: departureISO,
				},
			},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("trainline marshal: %w", err)
	}

	if err := trainlineLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("trainline rate limiter: %w", err)
	}

	newTrainlineRequest := func(cookieHeader string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, trainlineSearchURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		applyTrainlineHeaders(req, cookieHeader)
		return req, nil
	}

	slog.Debug("trainline search", "from", from, "to", to, "date", date)

	req, err := newTrainlineRequest("")
	if err != nil {
		return nil, err
	}

	resp, err := trainlineDo(req)
	if err != nil {
		return nil, fmt.Errorf("trainline search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusForbidden {
		firstBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		_ = resp.Body.Close()
		if !allowBrowserFallbacks {
			return nil, fmt.Errorf("trainline: HTTP 403: %s", firstBody)
		}

		// Try 1: extract the datadome cookie that Datadome sets on the 403 response
		// and immediately retry. Datadome uses this to verify cookie support —
		// presenting their own seeded cookie on the next request is a positive signal.
		if ddCookie := extractDatadomeCookie(resp.Cookies()); ddCookie != "" {
			slog.Debug("retrying trainline with datadome seed cookie")
			req2, err2 := newTrainlineRequest(ddCookie)
			if err2 != nil {
				return nil, fmt.Errorf("trainline retry build: %w", err2)
			}
			resp2, err2 := trainlineDo(req2)
			if err2 != nil {
				return nil, fmt.Errorf("trainline retry: %w", err2)
			}
			defer func() { _ = resp2.Body.Close() }()
			if resp2.StatusCode == http.StatusOK {
				return readAndParseTrainlineResponse(resp2.Body, from, to, date, currency)
			}
			body2, _ := io.ReadAll(io.LimitReader(resp2.Body, 2048))
			slog.Debug("datadome seed cookie retry still blocked", "status", resp2.StatusCode, "body", string(body2))
		}

		// Try 2: use a real browser session cookie extracted from Brave/Chrome.
		// Requires the user to have visited thetrainline.com in their browser.
		cookieHeader := trainlineBrowserCookies("thetrainline.com")
		if cookieHeader != "" {
			slog.Debug("retrying trainline with browser cookies")
			req3, err3 := newTrainlineRequest(cookieHeader)
			if err3 != nil {
				return nil, fmt.Errorf("trainline retry build: %w", err3)
			}
			resp3, err3 := trainlineDo(req3)
			if err3 != nil {
				return nil, fmt.Errorf("trainline retry: %w", err3)
			}
			defer func() { _ = resp3.Body.Close() }()
			if resp3.StatusCode == http.StatusOK {
				return readAndParseTrainlineResponse(resp3.Body, from, to, date, currency)
			}
		}

		if nRoutes, nErr := trainlineFetchViaNab(ctx, body, from, to, date, currency); nErr == nil && len(nRoutes) > 0 {
			return nRoutes, nil
		} else if nErr != nil && !errors.Is(nErr, trvlnab.ErrNotAvailable) {
			slog.Debug("trainline nab fallback failed", "err", nErr)
		}

		if cRoutes, cErr := trainlineViaCurl(ctx, fromID, toID, date, currency); cErr == nil && len(cRoutes) > 0 {
			populateTrainlineCities(cRoutes, from, to)
			return cRoutes, nil
		} else if cErr != nil {
			slog.Debug("trainline curl fallback failed", "err", cErr)
		}

		isCaptcha, captchaURL := cookies.IsCaptchaResponse(http.StatusForbidden, firstBody)
		if isCaptcha {
			slog.Warn("trainline requires browser verification — opening browser", "captcha_url", captchaURL)
			_, _ = fmt.Fprintf(os.Stderr, "⚠️  Trainline requires verification. Opening browser — please solve the challenge, then retry.\n")
			_ = cookies.OpenBrowserForAuth("https://www.thetrainline.com/")
		}

		// Last resort: browser scraper via Playwright.
		slog.Debug("trainline 403 — trying browser scraper fallback")
		if bRoutes, bErr := BrowserScrapeRoutes(ctx, "trainline", from, to, date, currency); bErr == nil && len(bRoutes) > 0 {
			return bRoutes, nil
		} else if bErr != nil {
			slog.Debug("trainline browser scraper failed", "err", bErr)
		}

		return nil, fmt.Errorf("trainline: HTTP 403: %s", firstBody)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("trainline: HTTP %d: %s", resp.StatusCode, respBody)
	}

	return readAndParseTrainlineResponse(resp.Body, from, to, date, currency)
}

func fetchTrainlineViaNab(
	ctx context.Context,
	requestBody []byte,
	from, to, date, currency string,
) ([]models.GroundRoute, error) {
	client, err := trvlnab.New()
	if err != nil {
		return nil, err
	}

	var headers []string
	for _, header := range trainlineRequestHeaders("") {
		headers = append(headers, fmt.Sprintf("%s: %s", header.name, header.value))
	}

	body, err := client.Fetch(ctx, trainlineSearchURL, trvlnab.FetchOptions{
		Method:  "POST",
		Body:    string(requestBody),
		Headers: headers,
	})
	if err != nil {
		return nil, err
	}
	return readAndParseTrainlineResponse(bytes.NewReader(body), from, to, date, currency)
}

func populateTrainlineCities(routes []models.GroundRoute, from, to string) {
	for i := range routes {
		if routes[i].Departure.City == "" {
			routes[i].Departure.City = from
		}
		if routes[i].Arrival.City == "" {
			routes[i].Arrival.City = to
		}
	}
}

func readAndParseTrainlineResponse(r io.Reader, from, to, date, currency string) ([]models.GroundRoute, error) {
	respBody, err := io.ReadAll(io.LimitReader(r, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("trainline read: %w", err)
	}
	slog.Debug("trainline raw response", "body", string(respBody[:min(len(respBody), 1024)]))

	var tlResp trainlineJourneySearchResponse
	if err := json.Unmarshal(respBody, &tlResp); err != nil {
		return nil, fmt.Errorf("trainline decode: %w", err)
	}
	return parseTrainlineResults(tlResp, from, to, date, currency)
}

func parseTrainlineResults(resp trainlineJourneySearchResponse, from, to, date, currency string) ([]models.GroundRoute, error) {
	// Build journey->cheapest price map from tickets.
	journeyPrice := make(map[string]float64)
	journeyCurrency := make(map[string]string)
	for _, ticket := range resp.Tickets {
		if len(ticket.Prices) == 0 {
			continue
		}
		price := ticket.Prices[0].Amount
		cur := strings.ToUpper(ticket.Prices[0].Currency)
		for _, jid := range ticket.JourneyIDs {
			if existing, ok := journeyPrice[jid]; !ok || price < existing {
				journeyPrice[jid] = price
				journeyCurrency[jid] = cur
			}
		}
	}

	var routes []models.GroundRoute
	for _, j := range resp.Journeys {
		price := journeyPrice[j.ID]
		cur := journeyCurrency[j.ID]
		if cur == "" {
			cur = "EUR"
		}

		routeType := "train"
		for _, leg := range j.Legs {
			mode := strings.ToLower(leg.TransportMode)
			if strings.Contains(mode, "bus") || strings.Contains(mode, "coach") {
				if routeType == "train" {
					routeType = "mixed"
				} else {
					routeType = "bus"
				}
			}
		}

		duration := computeLegDuration(j.DepartureTime, j.ArrivalTime)
		transfers := len(j.Legs) - 1
		if transfers < 0 {
			transfers = 0
		}

		route := models.GroundRoute{
			Provider:  "trainline",
			Type:      routeType,
			Price:     price,
			Currency:  cur,
			Duration:  duration,
			Transfers: transfers,
			Departure: models.GroundStop{
				City: from,
				Time: j.DepartureTime,
			},
			Arrival: models.GroundStop{
				City: to,
				Time: j.ArrivalTime,
			},
			BookingURL: fmt.Sprintf("https://www.thetrainline.com/book/trains/%s/%s/%s",
				strings.ReplaceAll(strings.ToLower(from), " ", "-"),
				strings.ReplaceAll(strings.ToLower(to), " ", "-"),
				date),
		}
		routes = append(routes, route)
	}

	slog.Debug("trainline results", "count", len(routes))
	return routes, nil
}

// extractDatadomeCookie extracts the "datadome" cookie value from a set of
// response cookies and returns it as a Cookie header value.
// Datadome sets this cookie on 403 responses; presenting it on the next
// request proves cookie support and may allow subsequent requests through.
func extractDatadomeCookie(cookies []*http.Cookie) string {
	for _, c := range cookies {
		if c.Name == "datadome" && c.Value != "" {
			return "datadome=" + c.Value
		}
	}
	return ""
}
