package ground

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
)

// Rome2Rio is a multimodal route-DISCOVERY provider. It does NOT return
// bookable fares — it surfaces the set of ways to travel between two places
// (train / bus / ferry / fly / drive and multi-leg combinations of those),
// with an indicative price RANGE and the total duration for each option. The
// actual bookable prices for each leg come from trvl's per-leg providers
// (FlixBus, Deutsche Bahn, SNCF, ferries, flights, ...). Rome2Rio's strength
// is discovering the combinations a single-mode provider would never surface
// (e.g. "ferry to a hub, then fly").
//
// Access: the documented JSON API (/api/1.x/json/Search) is key-gated (HTTP
// 401). Instead this reads the PUBLIC server-rendered route page
// https://www.rome2rio.com/s/{origin}/{destination}, which returns the full
// multimodal discovery as HTML with no API key — the same public-SSR pattern
// trvl already uses for other providers. Each route option is a single anchor
// to /map/{O}/{D}?route=<RouteName>; the anchor's text carries the title,
// per-leg description, total duration and price range.

const rome2rioBaseURL = "https://www.rome2rio.com"

// rome2rioThinRenderRetries bounds how many times SearchRome2Rio re-fetches when
// the SSR page comes back without parseable route options. Rome2Rio's SSR is
// observed to occasionally return a partial render; the route data is present on
// a subsequent fetch.
const rome2rioThinRenderRetries = 2

// rome2rioMarker is the SSR sentinel that proves the discovery body rendered.
// Its absence (after retries) means a bot wall / partial render, which we report
// as a typed error rather than a silently-empty success.
const rome2rioMarker = "ways to get from"

var (
	// matches "2h 28m", "23h 28m", "4h 2m"
	r2rHourMin = regexp.MustCompile(`(\d+)\s*h\s*(\d+)\s*m`)
	// matches a bare "9h" or "45 min"
	r2rHoursOnly = regexp.MustCompile(`(\d+)\s*h(?:\b|[^0-9m])`)
	r2rMinsOnly  = regexp.MustCompile(`(\d+)\s*min`)
	// matches a price range "€140–250", "€30 - €65", "€28–55" (en-dash or hyphen).
	r2rPriceRange = regexp.MustCompile(`([€$£])\s*([\d,]+)\s*[–\-]\s*([€$£])?\s*([\d,]+)`)
	// matches a single price "€140".
	r2rPriceOne = regexp.MustCompile(`([€$£])\s*([\d,]+)`)
	// the route deep-link, e.g. /map/London/Paris?route=Train-to-...-fly-to-...
	r2rRouteHref = regexp.MustCompile(`^/map/[^?]+\?route=(.+)$`)
)

// SearchRome2Rio fetches and parses the Rome2Rio SSR discovery page for a
// from->to pair. It retries on a thin/partial render and returns a typed error
// (never a silently-empty success) when the page is bot-walled or unparseable.
//
// Rome2Rio sits behind Cloudflare, which 403s a plain HTTP client (even with a
// browser UA). When allowBrowser is true (the --allow-browser-fallbacks gate),
// the fetch is routed through a Chrome-impersonating TLS client (browser JA3)
// carrying the user's live browser cookies (incl cf_clearance, read via kooky) —
// the same cookie+fingerprint pairing the user's real browser presents, which is
// what Cloudflare accepts. Without the gate, the plain path is used and a
// Cloudflare wall surfaces as an honest typed error.
func SearchRome2Rio(ctx context.Context, from, to string, allowBrowser bool) ([]models.GroundRoute, error) {
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return nil, fmt.Errorf("rome2rio: from and to are required")
	}

	var lastErr error
	for attempt := 0; attempt <= rome2rioThinRenderRetries; attempt++ {
		body, err := fetchRome2Rio(ctx, from, to, allowBrowser)
		if err != nil {
			lastErr = err
			continue
		}
		if !strings.Contains(body, rome2rioMarker) {
			lastErr = fmt.Errorf("rome2rio: thin/partial render (no route data); attempt %d", attempt+1)
			continue
		}
		routes, perr := parseRome2Rio(body, from, to)
		if perr != nil {
			lastErr = perr
			continue
		}
		if len(routes) == 0 {
			lastErr = fmt.Errorf("rome2rio: page rendered but no route options parsed")
			continue
		}
		return routes, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("rome2rio: no routes")
	}
	return nil, lastErr
}

// rome2rioURL builds the public SSR route URL for a from->to pair.
func rome2rioURL(from, to string) string {
	// Rome2Rio uses path segments for place names; keep them human-readable but
	// percent-encode unsafe characters. PathEscape leaves spaces as %20 which
	// Rome2Rio accepts.
	return fmt.Sprintf("%s/s/%s/%s", rome2rioBaseURL, url.PathEscape(strings.TrimSpace(from)), url.PathEscape(strings.TrimSpace(to)))
}

// rome2rioChromeUA must match the Chrome JA3 profile the Tier1 client presents,
// because Cloudflare binds cf_clearance to the (IP, UA, JA3) triple — a mismatch
// causes the replayed clearance cookie to be rejected (verified: a non-matching
// UA still 403s even with a valid cf_clearance).
const rome2rioChromeUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"

func fetchRome2Rio(ctx context.Context, from, to string, allowBrowser bool) (string, error) {
	target := rome2rioURL(from, to)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	// Choose the fetch transport. Default: plain client (will be Cloudflare-walled,
	// surfaced as an honest typed error). With --allow-browser-fallbacks: a
	// Chrome-impersonating TLS client (browser JA3) carrying the user's live
	// browser cookies (cf_clearance via kooky) + a matching Chrome UA — the
	// combination Cloudflare accepts (verified end-to-end).
	doer := func(r *http.Request) (*http.Response, error) { return httpClient.Do(r) }
	if allowBrowser {
		if tier1, terr := providers.NewTier1Client(); terr == nil {
			req.Header.Set("User-Agent", rome2rioChromeUA)
			for _, ck := range providers.BrowserCookiesForURL(target) {
				req.AddCookie(ck)
			}
			doer = tier1.Do
		} else {
			req.Header.Set("User-Agent", rome2rioChromeUA)
		}
	} else {
		req.Header.Set("User-Agent", rome2rioChromeUA)
	}

	resp, err := doer(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("rome2rio: bot wall / rate limited (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("rome2rio: unexpected status HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB cap
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// parseRome2Rio extracts the multimodal route options from the SSR HTML. It is
// pure (no network) so it is exercised by offline fixture tests. Each option is
// an anchor to /map/{O}/{D}?route=<name>; the anchor's text content carries the
// title, leg descriptions, duration and price range.
func parseRome2Rio(body, from, to string) ([]models.GroundRoute, error) {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rome2rio: parse html: %w", err)
	}

	var routes []models.GroundRoute
	seen := make(map[string]bool)

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			href := attr(n, "href")
			if m := r2rRouteHref.FindStringSubmatch(href); m != nil {
				routeName := m[1]
				if !seen[routeName] {
					seen[routeName] = true
					text := normalizeWS(textOf(n))
					if route, ok := buildRome2RioRoute(routeName, text, href, from, to); ok {
						routes = append(routes, route)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return routes, nil
}

// buildRome2RioRoute turns one route anchor (its encoded name + visible text)
// into a discovery GroundRoute. Returns ok=false when the option has neither a
// duration nor a price (i.e. not a real travel option).
func buildRome2RioRoute(routeName, text, href, from, to string) (models.GroundRoute, bool) {
	modes := rome2rioModes(routeName)
	dur := rome2rioDuration(text)
	lo, hi, cur := rome2rioPrice(text)

	if dur == 0 && lo == 0 {
		return models.GroundRoute{}, false
	}

	typ := "mixed"
	if len(modes) == 1 {
		typ = modes[0]
	}
	transfers := 0
	if len(modes) > 1 {
		transfers = len(modes) - 1
	}

	legs := make([]models.GroundLeg, 0, len(modes))
	for _, m := range modes {
		legs = append(legs, models.GroundLeg{Type: m, Provider: "rome2rio"})
	}

	return models.GroundRoute{
		Provider:   "rome2rio",
		Type:       typ,
		Price:      lo, // indicative range low (discovery hint, not bookable)
		PriceMax:   hi, // indicative range high
		Currency:   cur,
		Duration:   dur,
		Transfers:  transfers,
		Legs:       legs,
		Departure:  models.GroundStop{City: from},
		Arrival:    models.GroundStop{City: to},
		BookingURL: rome2rioBaseURL + href,
	}, true
}

// rome2rioModes derives the ordered mode chain from the encoded route name.
// Examples:
//
//	"Train"                                          -> [train]
//	"Drive-Eurotunnel"                               -> [drive] (eurotunnel is a sub-step)
//	"Train-to-London-Gatwick-fly-to-Paris-..."       -> [train, fly]
//	"Ferry-to-...-fly"                               -> [ferry, fly]
func rome2rioModes(routeName string) []string {
	lower := strings.ToLower(routeName)
	lower = strings.ReplaceAll(lower, "-", " ")

	// Known transport mode keywords, in the order they may appear.
	type modeKW struct{ kw, mode string }
	kws := []modeKW{
		{"night train", "train"},
		{"train", "train"},
		{"rideshare", "rideshare"},
		{"bus", "bus"},
		{"fly", "fly"},
		{"flight", "fly"},
		{"plane", "fly"},
		{"car ferry", "ferry"},
		{"ferry", "ferry"},
		{"drive", "drive"},
		{"car train", "train"},
		{"car", "drive"},
		{"subway", "subway"},
		{"tram", "tram"},
		{"walk", "walk"},
		{"taxi", "taxi"},
	}

	// Scan the string left to right, recording modes in appearance order while
	// de-duplicating consecutive repeats.
	type hit struct {
		idx  int
		mode string
	}
	var hits []hit
	for _, k := range kws {
		from := 0
		for {
			i := strings.Index(lower[from:], k.kw)
			if i < 0 {
				break
			}
			hits = append(hits, hit{idx: from + i, mode: k.mode})
			from += i + len(k.kw)
		}
	}
	if len(hits) == 0 {
		return []string{"mixed"}
	}
	// sort by index
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].idx < hits[j-1].idx; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	var modes []string
	for _, h := range hits {
		if len(modes) == 0 || modes[len(modes)-1] != h.mode {
			modes = append(modes, h.mode)
		}
	}
	return modes
}

// rome2rioDuration parses a total duration in minutes from the option text.
func rome2rioDuration(text string) int {
	if m := r2rHourMin.FindStringSubmatch(text); m != nil {
		h, _ := strconv.Atoi(m[1])
		mn, _ := strconv.Atoi(m[2])
		return h*60 + mn
	}
	if m := r2rHoursOnly.FindStringSubmatch(text); m != nil {
		h, _ := strconv.Atoi(m[1])
		return h * 60
	}
	if m := r2rMinsOnly.FindStringSubmatch(text); m != nil {
		mn, _ := strconv.Atoi(m[1])
		return mn
	}
	return 0
}

// rome2rioPrice parses an indicative price range from the option text, returning
// (low, high, currency). A single price yields low==high. Missing -> zeros.
func rome2rioPrice(text string) (lo, hi float64, currency string) {
	if m := r2rPriceRange.FindStringSubmatch(text); m != nil {
		currency = currencyCode(m[1])
		lo = parseAmount(m[2])
		hi = parseAmount(m[4])
		if hi < lo {
			lo, hi = hi, lo
		}
		return lo, hi, currency
	}
	if m := r2rPriceOne.FindStringSubmatch(text); m != nil {
		currency = currencyCode(m[1])
		lo = parseAmount(m[2])
		hi = lo
		return lo, hi, currency
	}
	return 0, 0, ""
}

func parseAmount(s string) float64 {
	s = strings.ReplaceAll(s, ",", "")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func currencyCode(symbol string) string {
	switch symbol {
	case "€":
		return "EUR"
	case "$":
		return "USD"
	case "£":
		return "GBP"
	default:
		return ""
	}
}

// --- small HTML helpers (local to avoid a goquery dependency) ---

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var sb strings.Builder
	var rec func(*html.Node)
	rec = func(nd *html.Node) {
		if nd.Type == html.TextNode {
			sb.WriteString(nd.Data)
			sb.WriteString(" ")
		}
		for c := nd.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return sb.String()
}

func normalizeWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
