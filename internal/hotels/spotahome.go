package hotels

// Spotahome mid-term furnished-apartment provider.
//
// Spotahome (https://www.spotahome.com) lists furnished apartments, studios and
// rooms for medium-term stays (typically 1+ month) — the relocation / nomad /
// extended-stay segment that nightly-rate hotel providers (Google Hotels,
// Trivago, Booking) miss entirely.
//
// Unauthenticated feasibility (recon-verified live 2026-06-18, Tier-A):
//   - No API key, headless browser, residential proxy nor CAPTCHA bypass is
//     required. A plain Go HTTP round-trip with a desktop Chrome User-Agent
//     suffices; no cookie is needed. We route through the providers.Tier1Client
//     (Chrome TLS/HTTP-2 impersonation) for fingerprint resilience.
//   - The search page exposes a React-Router "single-fetch" data endpoint:
//       GET /s/{city}/for-rent:apartments.data  ->  200 text/x-script
//     whose body is a turbo-stream flat array (see decoder below). Pagination is
//     ?page=N on the same .data URL.
//
// TURBO-STREAM FORMAT (verified against the captured Lisbon fixture, 48 cards):
// the body is a single flat JSON array of deduplicated values. Structure is
// reconstructed by integer index references:
//   - object: {"_<keyIdx>": <valIdx>, ...} — both key and value are indices into
//     the flat array; the key index resolves to a string.
//   - reference array: [<idx>, <idx>, ...] — each element is an index.
//   - null-prototype object: ["N", {<inline object>}] — a tagged 2-element array
//     whose first element is the string "N" and whose second element is an inline
//     object (its values are still indices).
//   - negative indices (e.g. -5) are turbo-stream sentinels (undefined/±Inf) and
//     decode to nil.
//
// Card -> models.HotelResult (verified field names):
//   id               -> HotelID (+ listing URL)
//   displayPrice     -> Price        (EUR DECIMAL STRING, e.g. "1685", NOT cents)
//   currencyIsoCode  -> Currency     (e.g. "EUR")
//   location.coord   -> [lon, lat]   SWAPPED to Lat/Lon (Lisbon = [-9.14, 38.74])
//   location.street  -> Address
//   area             -> sqm          (folded into Description; no HotelResult field)
//   numberOfBedrooms -> bed count    (folded into Description)
//   type             -> PropertyType ("apartment", ...)
//   mainPhotoUrl     -> ImageURL
//   url              -> https://www.spotahome.com/{city}/for-rent:apartments/{id}

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
	"golang.org/x/time/rate"
)

// spotahomeEnabled controls whether SearchSpotahome makes live HTTP requests.
// Disabled in the test suite (see testmain_test.go) so deterministic tests never
// fire real network calls; individual tests flip it on with a mock server.
var spotahomeEnabled = true

// spotahomeBaseURL is the root of the Spotahome site. Overridable in tests so an
// httptest.Server can stand in for the live host.
var spotahomeBaseURL = "https://www.spotahome.com"

// spotahomeUserAgent is a desktop Chrome UA. Spotahome serves the .data endpoint
// to ordinary desktop clients; no special headers are required.
const spotahomeUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// spotahomeLimiter enforces a conservative request rate (one request every
// 500ms) to keep traffic indistinguishable from human browsing.
var spotahomeLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 2)

// spotahomeClient is the Fetcher used for requests. Nil means "lazily build a
// providers.Tier1Client"; tests inject a plain *http.Client (which satisfies
// providers.Fetcher) pointed at an httptest.Server.
var spotahomeClient providers.Fetcher
var spotahomeClientOnce sync.Once

// spotahomeFetcher returns the Fetcher to use, lazily constructing the Chrome-
// impersonating Tier-1 client on first use. A test-injected spotahomeClient
// takes precedence.
func spotahomeFetcher() providers.Fetcher {
	if spotahomeClient != nil {
		return spotahomeClient
	}
	spotahomeClientOnce.Do(func() {
		if c, err := providers.NewTier1Client(); err == nil {
			spotahomeClient = c
		}
	})
	return spotahomeClient
}

// SearchSpotahome searches Spotahome for mid-term furnished apartments in a city.
// It is non-fatal by contract: callers treat any error as "zero results".
// Returns nil, nil when disabled (test mode).
func SearchSpotahome(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
	if !spotahomeEnabled {
		return nil, nil
	}
	if strings.TrimSpace(location) == "" {
		return nil, fmt.Errorf("spotahome: location is required")
	}
	city := spotahomeCitySlug(location)
	if city == "" {
		return nil, fmt.Errorf("spotahome: could not derive city slug from %q", location)
	}

	slog.Debug("spotahome search", "location", location, "city", city)
	raw, err := spotahomeGet(ctx, spotahomeDataURL(city, 0))
	if err != nil {
		return nil, fmt.Errorf("spotahome fetch: %w", err)
	}

	hotels, err := parseSpotahomeData(raw, city, strings.TrimSpace(opts.Currency))
	if err != nil {
		return nil, fmt.Errorf("spotahome parse: %w", err)
	}
	slog.Debug("spotahome results", "location", location, "count", len(hotels))
	return hotels, nil
}

// spotahomeDataURL builds the single-fetch .data URL for a city. page 0 omits
// the ?page query (page 1 equivalent); page>=1 appends ?page=N.
func spotahomeDataURL(city string, page int) string {
	u := spotahomeBaseURL + "/s/" + city + "/for-rent:apartments.data"
	if page > 0 {
		u += "?page=" + strconv.Itoa(page)
	}
	return u
}

// spotahomeCitySlug reduces a free-text location to the single-token city slug
// Spotahome uses (lowercase, first segment before any comma). e.g.
// "Lisbon, Portugal" -> "lisbon", "New York" -> "new-york".
func spotahomeCitySlug(location string) string {
	s := strings.ToLower(strings.TrimSpace(location))
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	var b strings.Builder
	prevHyphen := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		case r == ' ' || r == '-' || r == '/' || r == '.':
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// spotahomeGet performs a rate-limited GET with the desktop UA and returns the
// response body. Non-2xx responses are errors.
func spotahomeGet(ctx context.Context, url string) ([]byte, error) {
	if err := spotahomeLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	f := spotahomeFetcher()
	if f == nil {
		return nil, fmt.Errorf("no fetcher available")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", spotahomeUserAgent)
	req.Header.Set("Accept", "text/x-script, application/json, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := f.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
	}
	if err := validateDestinationResponseURL(req.URL, effectiveResponseURL(resp)); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20)) // 16 MiB cap
}

// parseSpotahomeData decodes a turbo-stream .data body and maps every listing
// card to a HotelResult. Cards without a positive price are skipped so every
// returned result is comparable. fallbackCurrency is used when a card carries no
// currency; when empty it defaults to EUR.
func parseSpotahomeData(raw []byte, city, fallbackCurrency string) ([]models.HotelResult, error) {
	dec, err := newSpotahomeDecoder(raw)
	if err != nil {
		return nil, err
	}
	dec.resolve(0) // hydrate the whole reachable tree into the memo

	cards := dec.cards()
	results := make([]models.HotelResult, 0, len(cards))
	for _, c := range cards {
		price := parseDecimal(spotahomeString(c["displayPrice"]))
		if price <= 0 {
			continue
		}
		id := spotahomeString(c["id"])
		if id == "" {
			continue
		}
		currency := strings.ToUpper(strings.TrimSpace(spotahomeString(c["currencyIsoCode"])))
		if currency == "" {
			if fallbackCurrency != "" {
				currency = strings.ToUpper(fallbackCurrency)
			} else {
				currency = "EUR"
			}
		}

		h := models.HotelResult{
			Name:         spotahomeName(c),
			HotelID:      id,
			Price:        price,
			Currency:     currency,
			ImageURL:     spotahomeString(c["mainPhotoUrl"]),
			BookingURL:   spotahomeListingURL(city, id),
			PropertyType: spotahomePropertyType(spotahomeString(c["type"])),
			Description:  spotahomeDescription(c),
		}
		if loc, ok := c["location"].(map[string]interface{}); ok {
			h.Address = strings.TrimSpace(spotahomeString(loc["street"]))
			// coord is [lon, lat] — SWAP to Lat/Lon.
			if coord, ok := loc["coord"].([]interface{}); ok && len(coord) == 2 {
				h.Lon = spotahomeFloat(coord[0])
				h.Lat = spotahomeFloat(coord[1])
			}
		}
		if h.Name == "" {
			h.Name = "Spotahome apartment"
		}
		h.Sources = []models.PriceSource{{
			Provider:   "spotahome",
			Price:      price,
			Currency:   currency,
			BookingURL: h.BookingURL,
		}}
		results = append(results, h)
	}
	// Deterministic order: ascending numeric id.
	sort.Slice(results, func(i, j int) bool {
		ai, _ := strconv.Atoi(results[i].HotelID)
		aj, _ := strconv.Atoi(results[j].HotelID)
		if ai != aj {
			return ai < aj
		}
		return results[i].HotelID < results[j].HotelID
	})
	return results, nil
}

// spotahomeName extracts a human title from a card. The card has no single
// "title" field in the fixture, so fall back to the street address.
func spotahomeName(c map[string]interface{}) string {
	for _, k := range []string{"title", "name", "homeName"} {
		if v := strings.TrimSpace(spotahomeString(c[k])); v != "" {
			return v
		}
	}
	if loc, ok := c["location"].(map[string]interface{}); ok {
		if s := strings.TrimSpace(spotahomeString(loc["street"])); s != "" {
			return s
		}
	}
	return ""
}

// spotahomeDescription folds the mid-term-specific fields (bedrooms, area) that
// have no dedicated HotelResult field into a compact, user-visible summary.
func spotahomeDescription(c map[string]interface{}) string {
	parts := make([]string, 0, 3)
	parts = append(parts, "Furnished apartment")
	if beds := int(spotahomeFloat(c["numberOfBedrooms"])); beds > 0 {
		parts = append(parts, fmt.Sprintf("%d bed", beds))
	}
	if area := spotahomeFloat(c["area"]); area > 0 {
		parts = append(parts, fmt.Sprintf("%.0f m²", area))
	}
	return strings.Join(parts, " · ")
}

// spotahomePropertyType normalises Spotahome's type token to the HotelResult
// vocabulary. Spotahome's inventory is "apartment".
func spotahomePropertyType(pt string) string {
	pt = strings.ToLower(strings.TrimSpace(pt))
	if pt == "" {
		return "apartment"
	}
	return pt
}

// spotahomeListingURL builds the canonical listing URL for a card id.
func spotahomeListingURL(city, id string) string {
	if id == "" {
		return ""
	}
	return spotahomeBaseURL + "/" + city + "/for-rent:apartments/" + id
}

// ---- turbo-stream decoder ----

// spotahomeDecoder reconstructs a turbo-stream flat array into nested Go values.
type spotahomeDecoder struct {
	flat []json.RawMessage
	memo map[int]interface{}
}

func newSpotahomeDecoder(raw []byte) (*spotahomeDecoder, error) {
	var flat []json.RawMessage
	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, fmt.Errorf("turbo-stream: not a JSON array: %w", err)
	}
	return &spotahomeDecoder{flat: flat, memo: make(map[int]interface{}, len(flat))}, nil
}

// resolve returns the decoded value at flat index i, memoising to break cycles.
func (d *spotahomeDecoder) resolve(i int) interface{} {
	if i < 0 || i >= len(d.flat) {
		return nil
	}
	if v, ok := d.memo[i]; ok {
		return v
	}
	raw := bytesTrimSpace(d.flat[i])
	if len(raw) == 0 {
		d.memo[i] = nil
		return nil
	}
	switch raw[0] {
	case '{':
		return d.decodeObject(i, raw)
	case '[':
		return d.decodeArray(i, raw)
	default:
		var v interface{}
		_ = json.Unmarshal(raw, &v)
		d.memo[i] = v
		return v
	}
}

// decodeObject decodes a {"_<keyIdx>": <valIdx>, ...} object. If i >= 0 the
// result is memoised under i (set before filling so cycles resolve).
func (d *spotahomeDecoder) decodeObject(i int, raw json.RawMessage) interface{} {
	m := make(map[string]interface{})
	if i >= 0 {
		d.memo[i] = m
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return m
	}
	for k, v := range fields {
		keyIdx, err := strconv.Atoi(strings.TrimPrefix(k, "_"))
		if err != nil {
			continue
		}
		keyStr, _ := d.resolve(keyIdx).(string)
		if keyStr == "" {
			continue
		}
		m[keyStr] = d.decodeChild(v)
	}
	return m
}

// decodeArray decodes either a tagged value (["N", obj]) or a reference array
// ([idx, idx, ...]).
func (d *spotahomeDecoder) decodeArray(i int, raw json.RawMessage) interface{} {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		d.memo[i] = nil
		return nil
	}
	if len(elems) == 0 {
		s := []interface{}{}
		d.memo[i] = s
		return s
	}
	// Tagged value: first element is a JSON string.
	var tag string
	if json.Unmarshal(elems[0], &tag) == nil {
		if tag == "N" && len(elems) >= 2 {
			return d.decodeObject(i, elems[1]) // inline null-proto object
		}
		d.memo[i] = nil
		return nil
	}
	// Reference array: each element is an index.
	s := make([]interface{}, 0, len(elems))
	for _, e := range elems {
		s = append(s, d.decodeChild(e))
	}
	d.memo[i] = s
	return s
}

// decodeChild resolves an object value / array element, which is normally an
// integer index into the flat array.
func (d *spotahomeDecoder) decodeChild(v json.RawMessage) interface{} {
	var n int
	if err := json.Unmarshal(v, &n); err == nil {
		return d.resolve(n)
	}
	var any interface{}
	_ = json.Unmarshal(v, &any)
	return any
}

// cards returns every decoded map that looks like a listing card (carries both
// "displayPrice" and "id"). Cards live in the memo after resolve(0).
func (d *spotahomeDecoder) cards() []map[string]interface{} {
	out := make([]map[string]interface{}, 0, 48)
	for _, v := range d.memo {
		if m, ok := v.(map[string]interface{}); ok {
			if _, hasPrice := m["displayPrice"]; hasPrice {
				if _, hasID := m["id"]; hasID {
					out = append(out, m)
				}
			}
		}
	}
	return out
}

// ---- small value helpers ----

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

// spotahomeString coerces a decoded JSON value (string or number) to a string.
func spotahomeString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

// spotahomeFloat coerces a decoded JSON value to a float64 (0 on miss).
func spotahomeFloat(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		return parseDecimal(t)
	default:
		return 0
	}
}

// parseDecimal parses a decimal string (possibly with thousands separators or a
// trailing currency symbol) into a float64; returns 0 on failure.
func parseDecimal(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == '.' {
			b.WriteRune(r)
		} else if r == ',' {
			// treat comma as thousands separator -> drop
			continue
		}
	}
	f, err := strconv.ParseFloat(b.String(), 64)
	if err != nil {
		return 0
	}
	return f
}
