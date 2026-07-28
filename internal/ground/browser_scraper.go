package ground

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/MikkoParkkola/trvl/internal/consent"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
	"github.com/chromedp/cdproto/network"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// browserScraperTimeout is the maximum time allowed for a single browser scrape.
const browserScraperTimeout = 45 * time.Second

var errBrowserScraperUnsupportedProvider = errors.New("browser scraper: unsupported provider")

var (
	browserScraperNavigateText  = chromedpNavigateText
	browserScraperSNCFResponses = chromedpSNCFResponses
	browserScraperCaptureHeader = chromedpCaptureHeader
)

var (
	browserPriceRE = regexp.MustCompile(`[£€$]\s*\d+(?:[\.,]\d+)?`)
	browserTimeRE  = regexp.MustCompile(`\b\d{2}:\d{2}\b`)
)

const browserStealthScript = `
Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
Object.defineProperty(navigator, 'plugins', {get: () => [1, 2, 3, 4, 5]});
Object.defineProperty(navigator, 'languages', {get: () => ['en-GB', 'en']});
window.chrome = window.chrome || {runtime: {}};
`

// BrowserScrapeRoutes fetches browser-assisted ground routes using the repo's
// existing Go CDP stack. It intentionally does not spawn an external language
// runtime; callers already treat non-nil errors as provider-unavailable.
//
// It runs by default and returns providers.ErrTier2Disabled when the user has
// declined the headless browser. Its doc comment used to say "opt-in", which was
// stale: nothing gated it, and both callers (trainline.go, sncf.go) invoke it
// unconditionally after the challenge path fails.
func BrowserScrapeRoutes(ctx context.Context, provider, from, to, date, currency string) ([]models.GroundRoute, error) {
	if providers.Tier2Declined() {
		return nil, providers.ErrTier2Disabled
	}
	if consent.CookiesDeclined() {
		return nil, providers.ErrTier2CookiesDeclined
	}

	provider = strings.ToLower(strings.TrimSpace(provider))
	if currency == "" {
		currency = "EUR"
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, browserScraperTimeout)
	defer cancel()

	switch provider {
	case "trainline":
		return browserScrapeTrainline(timeoutCtx, from, to, date, currency)
	case "sncf":
		return browserScrapeSNCF(timeoutCtx, from, to, date, currency)
	default:
		return nil, fmt.Errorf("%w: %s", errBrowserScraperUnsupportedProvider, provider)
	}
}

func browserScrapeTrainline(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	fromID, ok := LookupTrainlineStation(from)
	if !ok {
		return nil, fmt.Errorf("browser scraper trainline: no station ID for %q", from)
	}
	toID, ok := LookupTrainlineStation(to)
	if !ok {
		return nil, fmt.Errorf("browser scraper trainline: no station ID for %q", to)
	}

	targetURL := buildTrainlineBrowserResultsURL(fromID, toID, date)
	bodyText, err := browserScraperNavigateText(ctx, targetURL, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("browser scraper trainline: %w", err)
	}

	routes := parseTrainlineBrowserText(bodyText, from, to, date, currency)
	if len(routes) == 0 {
		return nil, errors.New("browser scraper trainline: no routes parsed")
	}
	return routes, nil
}

func browserScrapeSNCF(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	fromStation, ok := LookupSNCFStation(from)
	if !ok {
		return nil, fmt.Errorf("browser scraper sncf: no station code for %q", from)
	}
	toStation, ok := LookupSNCFStation(to)
	if !ok {
		return nil, fmt.Errorf("browser scraper sncf: no station code for %q", to)
	}

	bookingURL := buildSNCFBookingURL(fromStation.Code, toStation.Code, date)
	responses, bffKey, err := browserScraperSNCFResponses(ctx, bookingURL, fromStation, toStation, date)
	if err != nil {
		return nil, fmt.Errorf("browser scraper sncf: %w", err)
	}

	for _, data := range responses {
		routes := parseSNCFBFFResponse(data, bookingURL, date, currency)
		if len(routes) == 0 {
			continue
		}
		for i := range routes {
			routes[i].Departure.City = fromStation.City
			routes[i].Departure.Station = fromStation.Name
			routes[i].Arrival.City = toStation.City
			routes[i].Arrival.Station = toStation.Name
		}
		return routes, nil
	}

	keyState := "missing"
	if bffKey != "" {
		keyState = "present"
	}
	return nil, fmt.Errorf("browser scraper sncf: no routes parsed (x-bff-key %s)", keyState)
}

func buildTrainlineBrowserResultsURL(fromID, toID, date string) string {
	return "https://www.thetrainline.com/book/results?" +
		"journeySearchType=single" +
		"&origin=urn%3Atrainline%3Ageneric%3Aloc%3A" + fromID +
		"&destination=urn%3Atrainline%3Ageneric%3Aloc%3A" + toID +
		"&outwardDate=" + date + "T08%3A00%3A00" +
		"&outwardDateType=departAfter" +
		"&passengers%5B%5D=1996-01-01" +
		"&lang=en&transportModes%5B%5D=mixed"
}

func parseTrainlineBrowserText(text, from, to, date, currency string) []models.GroundRoute {
	if strings.Contains(text, "No tickets") {
		return nil
	}

	minPrice := 0.0
	minCurrency := strings.ToUpper(currency)
	for _, raw := range browserPriceRE.FindAllString(text, 15) {
		price, cur, ok := parseBrowserPrice(raw, minCurrency)
		if !ok {
			continue
		}
		if minPrice == 0 || price < minPrice {
			minPrice = price
			minCurrency = cur
		}
	}
	if minPrice <= 0 {
		return nil
	}

	times := browserTimeRE.FindAllString(text, 20)
	bookingURL := buildTrainlineBrowserBookingURL(from, to, date)
	if len(times) < 2 {
		return []models.GroundRoute{{
			Provider:   "trainline",
			Type:       "train",
			Price:      minPrice,
			Currency:   minCurrency,
			Departure:  models.GroundStop{City: from, Time: date},
			Arrival:    models.GroundStop{City: to, Time: date},
			BookingURL: bookingURL,
		}}
	}

	limit := min(len(times), 10)
	routes := make([]models.GroundRoute, 0, limit/2)
	for i := 0; i+1 < limit; i += 2 {
		routes = append(routes, models.GroundRoute{
			Provider:   "trainline",
			Type:       "train",
			Price:      minPrice,
			Currency:   minCurrency,
			Departure:  models.GroundStop{City: from, Time: date + "T" + times[i] + ":00"},
			Arrival:    models.GroundStop{City: to, Time: date + "T" + times[i+1] + ":00"},
			BookingURL: bookingURL,
		})
	}
	return routes
}

func parseBrowserPrice(raw, fallbackCurrency string) (float64, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, "", false
	}

	currency := strings.ToUpper(fallbackCurrency)
	symbol := []rune(raw)[0]
	switch symbol {
	case '£':
		currency = "GBP"
	case '€':
		currency = "EUR"
	case '$':
		currency = "USD"
	}

	amount := strings.TrimSpace(string([]rune(raw)[1:]))
	amount = strings.ReplaceAll(amount, ",", ".")
	price, err := strconv.ParseFloat(amount, 64)
	if err != nil || price <= 0 {
		return 0, "", false
	}
	return price, currency, true
}

func buildTrainlineBrowserBookingURL(from, to, date string) string {
	return "https://www.thetrainline.com/book/trains/" +
		trainlineSlug(from) + "/" +
		trainlineSlug(to) + "/" +
		date
}

func trainlineSlug(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func chromedpNavigateText(ctx context.Context, targetURL string, dwell time.Duration) (string, error) {
	taskCtx, cancel, err := newBrowserScraperContext(ctx)
	if err != nil {
		return "", err
	}
	defer cancel()

	var bodyText string
	actions := browserBaseActions()
	actions = append(actions,
		chromedp.Navigate(targetURL),
		dismissCookieBanners(),
		chromedp.Sleep(dwell),
		chromedp.Text("body", &bodyText, chromedp.ByQuery),
	)
	if err := chromedp.Run(taskCtx, actions...); err != nil {
		return "", err
	}
	return bodyText, nil
}

func chromedpCaptureHeader(ctx context.Context, targetURLs []string, headerName string, dwell time.Duration) (string, error) {
	taskCtx, cancel, err := newBrowserScraperContext(ctx)
	if err != nil {
		return "", err
	}
	defer cancel()

	var (
		mu    sync.Mutex
		value string
	)
	chromedp.ListenTarget(taskCtx, func(ev any) {
		req, ok := ev.(*network.EventRequestWillBeSent)
		if !ok {
			return
		}
		if got := networkHeaderValue(req.Request.Headers, headerName); got != "" {
			mu.Lock()
			if value == "" {
				value = got
			}
			mu.Unlock()
		}
	})

	actions := browserBaseActions()
	for _, targetURL := range targetURLs {
		if strings.TrimSpace(targetURL) == "" {
			continue
		}
		actions = append(actions,
			chromedp.Navigate(targetURL),
			chromedp.Sleep(dwell),
		)
	}
	if err := chromedp.Run(taskCtx, actions...); err != nil {
		return "", err
	}

	mu.Lock()
	defer mu.Unlock()
	return value, nil
}

func chromedpSNCFResponses(ctx context.Context, bookingURL string, fromStation, toStation SNCFStation, date string) ([]map[string]any, string, error) {
	taskCtx, cancel, err := newBrowserScraperContext(ctx)
	if err != nil {
		return nil, "", err
	}
	defer cancel()

	var (
		mu     sync.Mutex
		bffKey string
		raws   []string
	)
	chromedp.ListenTarget(taskCtx, func(ev any) {
		req, ok := ev.(*network.EventRequestWillBeSent)
		if !ok {
			return
		}
		if got := networkHeaderValue(req.Request.Headers, "x-bff-key"); got != "" {
			mu.Lock()
			if bffKey == "" {
				bffKey = got
			}
			mu.Unlock()
		}
	})

	actions := browserBaseActions()
	actions = append(actions,
		chromedp.Navigate(sncfHomeURL),
		chromedp.Sleep(3*time.Second),
		dismissCookieBanners(),
		chromedp.Navigate(bookingURL),
		chromedp.Sleep(4*time.Second),
		chromedp.ActionFunc(func(ctx context.Context) error {
			mu.Lock()
			key := bffKey
			mu.Unlock()
			for _, bffPath := range sncfBFFPaths {
				raw, err := evaluateSNCFFetch(ctx, bffPath.path, bffPath.bodyFn(fromStation.Code, toStation.Code, date), key)
				if err != nil {
					slog.Debug("browser scraper sncf fetch failed", "path", bffPath.path, "err", err)
					continue
				}
				mu.Lock()
				raws = append(raws, raw)
				mu.Unlock()
			}
			return nil
		}),
	)

	if err := chromedp.Run(taskCtx, actions...); err != nil {
		return nil, "", err
	}

	mu.Lock()
	defer mu.Unlock()
	return decodeBrowserJSONBodies(raws), bffKey, nil
}

func evaluateSNCFFetch(ctx context.Context, endpoint, body, bffKey string) (string, error) {
	headers := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}
	if bffKey != "" {
		headers["x-bff-key"] = bffKey
	}
	headersJSON, err := json.Marshal(headers)
	if err != nil {
		return "", err
	}

	js := fmt.Sprintf(`(async () => {
const response = await fetch(%s, {method: "POST", headers: %s, body: %s});
const text = await response.text();
if (response.status !== 200) {
  return JSON.stringify({_httpError: response.status, _body: text.slice(0, 100)});
}
return text;
})()`, jsString(endpoint), string(headersJSON), jsString(body))

	var raw string
	if err := chromedp.Evaluate(js, &raw).Do(ctx); err != nil {
		return "", err
	}
	return raw, nil
}

func decodeBrowserJSONBodies(raws []string) []map[string]any {
	responses := make([]map[string]any, 0, len(raws))
	for _, raw := range raws {
		raw = strings.TrimSpace(raw)
		if raw == "" || (!strings.HasPrefix(raw, "{") && !strings.HasPrefix(raw, "[")) {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			continue
		}
		if data["_httpError"] != nil {
			continue
		}
		responses = append(responses, data)
	}
	return responses
}

func newBrowserScraperContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	// An explicit decline is absolute and is checked HERE, on the function that
	// builds the allocator, for the same reason internal/providers checks it on
	// runCDPCollect rather than on its entrypoints: this is the third place in
	// the repo that can start a browser, and gating only the two in
	// internal/providers left this one spawning Chrome after the user said no
	// (#521). A caller that reaches past BrowserScrapeRoutes still cannot.
	if providers.Tier2Declined() {
		return nil, nil, providers.ErrTier2Disabled
	}

	// And the cookie decline stops it too, for the reason stated at the paired
	// check in internal/providers/tier2_chromedp.go: two variables, one question,
	// and the narrower one must not be a bypass. An adversarial review of this
	// branch found that gating here on Tier2Declined ALONE let a user who set
	// only TRVL_NO_BROWSER_COOKIES still get Chrome launched — and the SNCF
	// caller captures an x-bff-key from that session and returns it, so the
	// bypass leaked a credential, not merely a page.
	if consent.CookiesDeclined() {
		return nil, nil, providers.ErrTier2CookiesDeclined
	}

	// And the cookie decline stops it too, for the reason stated at the paired
	// check in internal/providers/tier2_chromedp.go: two variables, one question,
	// and the narrower one must not be a bypass. An adversarial review of this
	// branch found that gating here on Tier2Declined ALONE let a user who set
	// only TRVL_NO_BROWSER_COOKIES still get Chrome launched — and the SNCF
	// caller captures an x-bff-key from that session and returns it, so the
	// bypass leaked a credential, not merely a page.

	allocOpts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	allocOpts = append(allocOpts,
		chromedp.UserAgent(trainlineChromeUA),
		chromedp.Headless,
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("no-sandbox", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	taskCtx, cancelTask := chromedp.NewContext(allocCtx)
	return taskCtx, func() {
		cancelTask()
		cancelAlloc()
	}, nil
}

func browserBaseActions() []chromedp.Action {
	return []chromedp.Action{
		network.Enable(),
		network.SetExtraHTTPHeaders(network.Headers{
			"Accept-Language":    "en-GB,en;q=0.9",
			"sec-ch-ua":          trainlineChromeSecCHUA,
			"sec-ch-ua-mobile":   "?0",
			"sec-ch-ua-platform": `"macOS"`,
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := cdppage.AddScriptToEvaluateOnNewDocument(browserStealthScript).Do(ctx)
			return err
		}),
		chromedp.EmulateViewport(1280, 800),
	}
}

func dismissCookieBanners() chromedp.Action {
	const script = `(function () {
const selectors = [
  '#onetrust-accept-btn-handler',
  'button[aria-label="Accept all"]',
  'button:has-text("Accept all")'
];
for (const selector of selectors) {
  try {
    const button = document.querySelector(selector);
    if (button) { button.click(); return true; }
  } catch (_) {}
}
return false;
})()`
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var clicked bool
		_ = chromedp.Evaluate(script, &clicked).Do(ctx)
		return nil
	})
}

func networkHeaderValue(headers network.Headers, name string) string {
	for k, v := range headers {
		if !strings.EqualFold(k, name) {
			continue
		}
		switch typed := v.(type) {
		case string:
			return typed
		case []string:
			return strings.Join(typed, ",")
		default:
			return fmt.Sprint(typed)
		}
	}
	return ""
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
