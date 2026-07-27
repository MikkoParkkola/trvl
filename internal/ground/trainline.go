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
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/cookies"
	"github.com/MikkoParkkola/trvl/internal/models"
	trvlnab "github.com/MikkoParkkola/trvl/internal/nab"
	"github.com/MikkoParkkola/trvl/internal/providers"
)

const trainlineSearchURL = "https://www.thetrainline.com/api/journey-search/"

// trainlineHomeURL is the origin used to harvest the user's live browser cookies
// (incl the datadome clearance) for the Tier-1 browser-impersonation fallback.
const trainlineHomeURL = "https://www.thetrainline.com"

// trainlineHelperBudget bounds the whole external-helper attempt (a seed request
// plus the API call). Matched to sncf.go's equivalent so the two rail paths
// behave the same under a stalled network.
const trainlineHelperBudget = 35 * time.Second

// trainlineHelperMaxTime is the per-invocation cap handed to the helper itself,
// for the case where it ignores context cancellation.
const trainlineHelperMaxTime = "20"

// trainlineChromeUA must match the Chrome JA3 profile providers.NewTier1Client
// presents (Chrome 146 — the tls-client default). Datadome binds its clearance
// cookie to the (IP, UA, JA3) triple, so a mismatched UA causes the replayed
// cookie to be rejected even when it is valid. Mirrors rome2rio.go's rationale
// for the Cloudflare bypass (#213).
const trainlineChromeUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"

// trainlineChromeSecCHUA is the matching Client-Hints brand string for Chrome 146.
const trainlineChromeSecCHUA = `"Chromium";v="146", "Google Chrome";v="146", "Not(A:Brand";v="99"`

// trainlineLimiter: 5 req/min to be respectful
var trainlineLimiter = newProviderLimiter(12 * time.Second)

// trainlineClient uses Chrome TLS fingerprint to bypass Datadome bot detection.
var trainlineClient = batchexec.ChromeHTTPClient()

// trainlineMaxRetryDelay caps how long we wait on a 429 Retry-After before
// giving up; a throttled origin must not stall the whole search.
const trainlineMaxRetryDelay = 30 * time.Second

// trainlineAfter is the 429 backoff sleep seam (swapped to instant in tests).
var trainlineAfter = time.After

var (
	trainlineDo             = func(req *http.Request) (*http.Response, error) { return trainlineClient.Do(req) }
	trainlineFetchViaNab    = fetchTrainlineViaNab
	trainlineBrowserCookies = cookies.BrowserCookiesContext
	// trainlineViaCurlFn shells out to the system curl binary for the curl-assisted
	// fallback. Overridable in tests so the 403 escalation chain runs offline.
	trainlineViaCurlFn = trainlineViaCurl

	// trainlineNewTier1 builds a Chrome-impersonating TLS client (browser JA3).
	// Overridable in tests so the Tier-1 fallback can be pointed at httptest.
	trainlineNewTier1 = func() (providers.Fetcher, error) { return providers.NewTier1Client() }
	// trainlineTier1Cookies harvests the user's live browser cookies for a URL
	// (datadome clearance via kooky). Overridable in tests.
	trainlineTier1Cookies = providers.BrowserCookiesForURL

	// trainlineResolveChallenge escalates an anti-bot challenge HEADLESS-first
	// (no visible window, no focus steal) via providers.ResolveChallenge. It runs
	// above the visible-window fallback: only if it reports ChallengeNeedsHuman
	// do we open a real window. Overridable in tests so the orchestration is
	// exercised offline without spawning a browser.
	trainlineResolveChallenge = func(ctx context.Context, targetURL string) (*providers.ChallengeResult, error) {
		return providers.ResolveChallenge(ctx, targetURL)
	}
	// trainlineOpenBrowser opens a VISIBLE browser window so a human can solve an
	// interactive captcha. Only invoked on ChallengeNeedsHuman. Overridable in tests.
	trainlineOpenBrowser = cookies.OpenBrowserForAuth
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

// trainlineDoWithRetry issues the request and, on a single HTTP 429, honours the
// Retry-After header (capped at trainlineMaxRetryDelay) and replays the request
// exactly once. makeReq rebuilds a fresh request per attempt so the POST body is
// re-read cleanly. A 403 bot wall is never retried here — SearchTrainline's own
// fallback ladder handles it; any non-429 response is returned to the caller as-is.
func trainlineDoWithRetry(ctx context.Context, makeReq func() (*http.Request, error)) (*http.Response, error) {
	req, err := makeReq()
	if err != nil {
		return nil, err
	}
	resp, err := trainlineDo(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		return resp, nil
	}

	delay := providers.RetryAfterOrDefault(resp.Header.Get("Retry-After"), time.Now())
	if delay > trainlineMaxRetryDelay {
		delay = trainlineMaxRetryDelay
	}
	// Drain + close the 429 body before replaying so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-trainlineAfter(delay):
	}

	req2, err := makeReq()
	if err != nil {
		return nil, err
	}
	return trainlineDo(req2)
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

	resp, err := trainlineDoWithRetry(ctx, func() (*http.Request, error) {
		return newTrainlineRequest("")
	})
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

		// Tier 1 (highest confidence): a Chrome-impersonating TLS client (browser
		// JA3) carrying the user's LIVE browser cookies (incl the datadome
		// clearance, harvested via kooky) + a matching Chrome UA. Datadome binds
		// its clearance to the (IP, UA, JA3) triple, so presenting all three is
		// what gets accepted — the same bypass proven for Rome2Rio's Cloudflare
		// wall (#213). Only attempted when live cookies are available; without the
		// clearance cookie the JA3 alone won't pass, so we skip to cheaper tiers.
		if cks := trainlineTier1Cookies(trainlineHomeURL); len(cks) > 0 {
			slog.Debug("retrying trainline via Tier1 (JA3 + live datadome cookie)", "cookies", len(cks))
			if t1Routes, t1Err := trainlineViaTier1(ctx, body, cks, from, to, date, currency); t1Err == nil && len(t1Routes) > 0 {
				return t1Routes, nil
			} else if t1Err != nil {
				slog.Debug("trainline tier1 fallback failed", "err", t1Err)
			}
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
		cookieHeader := trainlineBrowserCookies(ctx, "thetrainline.com")
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

		if cRoutes, cErr := trainlineViaCurlFn(ctx, fromID, toID, date, currency); cErr == nil && len(cRoutes) > 0 {
			populateTrainlineCities(cRoutes, from, to)
			return cRoutes, nil
		} else if cErr != nil {
			slog.Debug("trainline curl fallback failed", "err", cErr)
		}

		// Headless-first challenge escalation (MIK-6218): drive the user's
		// installed browser HEADLESS (no window, no focus steal) to let the
		// anti-bot challenge resolve. If it clears silently, retry with the
		// harvested cookies — NO window. Only an interactive captcha that a
		// headless browser cannot solve falls through to a VISIBLE window.
		if res, rErr := trainlineResolveChallenge(ctx, trainlineHomeURL); rErr != nil {
			slog.Debug("trainline headless challenge resolve failed", "err", rErr)
		} else if res != nil && res.Status == providers.ChallengeCleared {
			if ch := cookieSliceToHeader(res.Cookies); ch != "" {
				slog.Debug("trainline challenge cleared headlessly — retrying with harvested cookies")
				if reqC, errC := newTrainlineRequest(ch); errC == nil {
					if respC, errC := trainlineDo(reqC); errC == nil {
						defer func() { _ = respC.Body.Close() }()
						if respC.StatusCode == http.StatusOK {
							return readAndParseTrainlineResponse(respC.Body, from, to, date, currency)
						}
						slog.Debug("trainline headless-cleared retry still blocked", "status", respC.StatusCode)
					}
				}
			}
		} else if res != nil && res.Status == providers.ChallengeNeedsHuman {
			slog.Warn("trainline requires human verification — opening browser", "vendor", res.Marker)
			_, _ = fmt.Fprintf(os.Stderr, "⚠️  Trainline requires verification. Opening browser — please solve the challenge, then retry.\n")
			_ = trainlineOpenBrowser(trainlineHomeURL)
		}

		// Last resort: opt-in browser scraper via Go CDP.
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

// trainlineViaTier1 POSTs the journey-search request through a Chrome-impersonating
// TLS client (browser JA3) carrying the user's live browser cookies and a matching
// Chrome UA + Client-Hints. This is the highest-confidence Datadome bypass; it
// mirrors the Rome2Rio Cloudflare path (#213). Returns a typed error (never a
// silently-empty success) when the wall persists.
func trainlineViaTier1(ctx context.Context, body []byte, cks []*http.Cookie, from, to, date, currency string) ([]models.GroundRoute, error) {
	tier1, err := trainlineNewTier1()
	if err != nil {
		return nil, fmt.Errorf("trainline tier1 client: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, trainlineSearchURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	applyTrainlineHeaders(req, "")
	// Override UA + Client-Hints to match the Chrome 146 JA3 the Tier1 client
	// presents (the default headers carry a Chrome 133 UA, which would mismatch).
	req.Header.Set("User-Agent", trainlineChromeUA)
	req.Header.Set("sec-ch-ua", trainlineChromeSecCHUA)
	for _, ck := range cks {
		req.AddCookie(ck)
	}

	resp, err := tier1.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trainline tier1: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("trainline tier1: bot wall (HTTP %d): %s", resp.StatusCode, b)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("trainline tier1: HTTP %d: %s", resp.StatusCode, b)
	}

	routes, perr := readAndParseTrainlineResponse(resp.Body, from, to, date, currency)
	if perr != nil {
		return nil, perr
	}
	populateTrainlineCities(routes, from, to)
	return routes, nil
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

// cookieSliceToHeader renders a slice of cookies into a single Cookie request
// header value ("name=value; name2=value2"). Empty-named/valued cookies are
// skipped. Returns "" when no usable cookies are present. Used to replay the
// cookies harvested by a HEADLESS challenge resolution (MIK-6218) on a silent
// retry — no visible window required.
func cookieSliceToHeader(cks []*http.Cookie) string {
	var b strings.Builder
	for _, c := range cks {
		if c == nil || c.Name == "" || c.Value == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString(c.Name)
		b.WriteString("=")
		b.WriteString(c.Value)
	}
	return b.String()
}
