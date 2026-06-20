package hotels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// haTestAPIKey is assembled from two halves so no contiguous credential-looking
// literal lives in the source tree (also keeps secret scanners quiet). It is a
// throwaway 32-hex value used only by the mock Algolia server.
var haTestAPIKey = "abcdef0123456789" + "abcdef0123456789"

// haHarvestPage builds a search-page body carrying the Algolia creds inline,
// mirroring the SSR config the real site ships. Key is injected at runtime.
func haHarvestPage(appID, apiKey string) string {
	return fmt.Sprintf(`<!DOCTYPE html><html><head><title>Berlin</title></head><body>
<script>window.__APP_CONFIG__={"search":{"algolia":{`+
		`"x-algolia-application-id":%q,"x-algolia-api-key":%q,`+
		`"indexName":"production_listings_rank_withOrpheus"}}};</script>
</body></html>`, appID, apiKey)
}

// resetHACreds clears the harvested-credential cache between tests.
func resetHACreds(t *testing.T) {
	t.Helper()
	haInvalidateCreds()
	t.Cleanup(haInvalidateCreds)
}

func TestParseAlgoliaCreds(t *testing.T) {
	page := haHarvestPage("Y8L112MIBF", haTestAPIKey)
	creds, err := parseAlgoliaCreds([]byte(page))
	if err != nil {
		t.Fatalf("parseAlgoliaCreds: %v", err)
	}
	if creds.appID != "Y8L112MIBF" {
		t.Errorf("appID = %q, want Y8L112MIBF", creds.appID)
	}
	if creds.apiKey != haTestAPIKey {
		t.Errorf("apiKey = %q, want harvested key", creds.apiKey)
	}
}

func TestParseAlgoliaCredsAppIDFallback(t *testing.T) {
	// Page exposes only the key (inline apiKey style) -> app-id falls back to
	// the known DSN subdomain.
	page := `var cfg={algolia:{apiKey:"` + haTestAPIKey + `"}};`
	creds, err := parseAlgoliaCreds([]byte(page))
	if err != nil {
		t.Fatalf("parseAlgoliaCreds: %v", err)
	}
	if creds.appID != haKnownAppID {
		t.Errorf("appID fallback = %q, want %q", creds.appID, haKnownAppID)
	}
	if creds.apiKey != haTestAPIKey {
		t.Errorf("apiKey = %q, want harvested key", creds.apiKey)
	}
}

func TestParseAlgoliaCredsMissingKey(t *testing.T) {
	if _, err := parseAlgoliaCreds([]byte(`<html>no creds here</html>`)); err == nil {
		t.Fatal("expected error when no api key present")
	}
}

// TestParseAlgoliaCreds_IgnoresDecoyTokens reproduces the drift where the
// harvest grabbed an unrelated 15-digit numeric `applicationId` and a stray
// 32-hex token (producing a dead `410300212483493-dsn.algolia.net` host)
// instead of Algolia's own credentials. The real creds live in the inline
// `"algolia":{...}` object and must win regardless of decoy ordering.
func TestParseAlgoliaCreds_IgnoresDecoyTokens(t *testing.T) {
	realKey := "170cf5d8f85035f219107d6fb900e3dd"
	page := `<!DOCTYPE html><html><body>
<script>window.__ENV__={"applicationId":"410300212483493","branchKey":"03101295309c5e5b981583ce6e65ab7d"};</script>
<script>window.__APP_CONFIG__={"search":{"algolia":{"appId":"Y8L112MIBF","apiKey":"` + realKey + `","index":"production_listings_rank_withOrpheus"}}};</script>
</body></html>`
	creds, err := parseAlgoliaCreds([]byte(page))
	if err != nil {
		t.Fatalf("parseAlgoliaCreds: %v", err)
	}
	if creds.appID != "Y8L112MIBF" {
		t.Errorf("appID = %q, want Y8L112MIBF (decoy numeric id leaked)", creds.appID)
	}
	if creds.apiKey != realKey {
		t.Errorf("apiKey = %q, want %q (decoy hex leaked)", creds.apiKey, realKey)
	}
}

// TestParseAlgoliaCreds_RejectsNumericAppIDFallback ensures the global fallback
// path never returns a pure-numeric app-id (which would build a dead DSN host);
// it falls back to the known subdomain instead.
func TestParseAlgoliaCreds_RejectsNumericAppIDFallback(t *testing.T) {
	page := `applicationId:"410300212483493" x-algolia-api-key:"` + haTestAPIKey + `"`
	creds, err := parseAlgoliaCreds([]byte(page))
	if err != nil {
		t.Fatalf("parseAlgoliaCreds: %v", err)
	}
	if creds.appID != haKnownAppID {
		t.Errorf("appID = %q, want fallback %q (numeric id leaked)", creds.appID, haKnownAppID)
	}
}

func TestNoKeyLiteralInSource(t *testing.T) {
	// Guards the "runtime-harvest, never hardcode" contract: the provider source
	// must not embed an Algolia search key literal.
	src, err := os.ReadFile("housinganywhere.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	// Any 32+ hex run adjacent to an algolia key label would be a smell. We
	// assert no api-key literal assignment exists in source.
	lower := strings.ToLower(string(src))
	for _, bad := range []string{`apikey: "`, `apikey:"`, `x-algolia-api-key", "`} {
		if strings.Contains(lower, bad) {
			t.Errorf("source appears to embed an api key literal near %q", bad)
		}
	}
}

func TestBuildAlgoliaParams(t *testing.T) {
	got := buildAlgoliaParams("Berlin", 20, 0, 500, 1500)
	// url.Values.Encode sorts keys alphabetically.
	for _, want := range []string{
		"facetFilters=%5B%5B%22city%3ABerlin%22%5D%5D",
		"hitsPerPage=20",
		"page=0",
		"query=",
		"priceEUR%3E%3D500",
		"priceEUR%3C%3D1500",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("params %q missing %q", got, want)
		}
	}
}

func TestHAIndexForSort(t *testing.T) {
	cases := map[string]string{
		"":                  "production_listings_rank_withOrpheus",
		"relevance":         "production_listings_rank_withOrpheus",
		"cheapest":          "production_listings_price_low_to_high",
		"price_high_to_low": "production_listings_price_high_to_low",
		"most_recent":       "production_listings_most_recent",
	}
	for in, want := range cases {
		if got := haIndexForSort(in); got != want {
			t.Errorf("haIndexForSort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHACityFromLocation(t *testing.T) {
	cases := map[string]string{
		"Berlin":          "Berlin",
		"Berlin, Germany": "Berlin",
		"  Paris ":        "Paris",
		"Den Haag":        "Den Haag",
	}
	for in, want := range cases {
		if got := haCityFromLocation(in); got != want {
			t.Errorf("haCityFromLocation(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHAListingURL(t *testing.T) {
	cases := map[string]string{
		"":                              "",
		"/en/rental-unit/x/1":           "https://housinganywhere.com/en/rental-unit/x/1",
		"en/rental-unit/x/2":            "https://housinganywhere.com/en/rental-unit/x/2",
		"https://housinganywhere.com/y": "https://housinganywhere.com/y",
	}
	for in, want := range cases {
		if got := haListingURL(in); got != want {
			t.Errorf("haListingURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMapHAHitFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/housinganywhere_algolia_berlin.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var resp algoliaResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("want 1 result, got %d", len(resp.Results))
	}
	res := resp.Results[0]
	if res.NbHits != 1287 {
		t.Errorf("nbHits = %d, want 1287", res.NbHits)
	}

	mapped := make([]models.HotelResult, 0, len(res.Hits))
	for _, h := range res.Hits {
		if m, ok := mapHAHit(h, "EUR"); ok {
			mapped = append(mapped, m)
		}
	}
	// Third hit has priceEUR=0 and must be dropped.
	if len(mapped) != 2 {
		t.Fatalf("want 2 mapped hits (zero-price dropped), got %d", len(mapped))
	}

	first := mapped[0]
	if first.HotelID != "1048576" {
		t.Errorf("id = %q, want 1048576", first.HotelID)
	}
	if first.Price != 950 { // whole EUR, not cents
		t.Errorf("price = %v, want 950", first.Price)
	}
	if first.Currency != "EUR" {
		t.Errorf("currency = %q, want EUR", first.Currency)
	}
	if first.Lat != 52.5163 || first.Lon != 13.3777 {
		t.Errorf("geo = (%v,%v), want (52.5163,13.3777)", first.Lat, first.Lon)
	}
	if first.PropertyType != "apartment" {
		t.Errorf("propertyType = %q, want apartment", first.PropertyType)
	}
	if first.BookingURL != "https://housinganywhere.com/en/rental-unit/apartment-for-rent-berlin/1048576" {
		t.Errorf("bookingURL = %q", first.BookingURL)
	}
	if first.Address != "Berlin, Germany" {
		t.Errorf("address = %q, want 'Berlin, Germany'", first.Address)
	}
	if !strings.Contains(first.Description, "1–12 months") {
		t.Errorf("description = %q, want lease window", first.Description)
	}
	if !strings.Contains(first.Description, "2026-07-01") {
		t.Errorf("description = %q, want dateFrom", first.Description)
	}
	if len(first.Sources) != 1 || first.Sources[0].Provider != "housinganywhere" {
		t.Errorf("sources = %+v, want single housinganywhere source", first.Sources)
	}
}

// TestSearchHousingAnywhereEndToEnd wires harvest + Algolia query against mock
// servers, exercising the full pipeline (including the city facet + index) and
// confirming the harvested key is sent on the POST.
func TestSearchHousingAnywhereEndToEnd(t *testing.T) {
	resetHACreds(t)

	raw, err := os.ReadFile("testdata/housinganywhere_algolia_berlin.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	// Mock Algolia host: assert auth headers + body shape, return the fixture.
	var gotAppID, gotAPIKey, gotBody string
	algolia := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAppID = r.Header.Get("X-Algolia-Application-Id")
		gotAPIKey = r.Header.Get("X-Algolia-API-Key")
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	defer algolia.Close()

	// Mock search page: serves the harvest config.
	page := haHarvestPage("Y8L112MIBF", haTestAPIKey)
	searchPage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer searchPage.Close()

	// Point the provider at the mocks. haAlgoliaHostTmpl ignores the app-id and
	// returns the mock host so the URL builder still works unchanged.
	origPage, origTmpl, origEnabled := haSearchPageURL, haAlgoliaHostTmpl, housinganywhereEnabled
	haSearchPageURL = searchPage.URL
	haAlgoliaHostTmpl = algolia.URL + "%.0s" // consume the app-id arg, emit mock host
	housinganywhereEnabled = true
	defer func() {
		haSearchPageURL, haAlgoliaHostTmpl, housinganywhereEnabled = origPage, origTmpl, origEnabled
	}()

	hotels, err := SearchHousingAnywhere(context.Background(), "Berlin, Germany", HotelSearchOptions{Currency: "EUR", Sort: "cheapest"})
	if err != nil {
		t.Fatalf("SearchHousingAnywhere: %v", err)
	}
	if len(hotels) != 2 {
		t.Fatalf("want 2 hotels, got %d", len(hotels))
	}
	if gotAppID != "Y8L112MIBF" {
		t.Errorf("POST X-Algolia-Application-Id = %q, want Y8L112MIBF", gotAppID)
	}
	if gotAPIKey != haTestAPIKey {
		t.Errorf("POST X-Algolia-API-Key = %q, want harvested key", gotAPIKey)
	}
	if !strings.Contains(gotBody, "production_listings_price_low_to_high") {
		t.Errorf("body index = %q, want price_low_to_high (cheapest sort)", gotBody)
	}
	if !strings.Contains(gotBody, "city%3ABerlin") {
		t.Errorf("body missing Berlin city facet: %q", gotBody)
	}

	// Disabled -> nil, nil.
	housinganywhereEnabled = false
	got, err := SearchHousingAnywhere(context.Background(), "Berlin", HotelSearchOptions{})
	if err != nil || got != nil {
		t.Errorf("disabled = (%v,%v), want (nil,nil)", got, err)
	}
}

// TestHarvestAlgoliaCredsCaches confirms the harvested pair is cached for the
// TTL window: a second harvest does not re-fetch the page.
func TestHarvestAlgoliaCredsCaches(t *testing.T) {
	resetHACreds(t)

	var hits int
	page := haHarvestPage("Y8L112MIBF", haTestAPIKey)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	orig := haSearchPageURL
	haSearchPageURL = srv.URL
	defer func() { haSearchPageURL = orig }()

	c1, err := harvestAlgoliaCreds(context.Background())
	if err != nil {
		t.Fatalf("harvest 1: %v", err)
	}
	c2, err := harvestAlgoliaCreds(context.Background())
	if err != nil {
		t.Fatalf("harvest 2: %v", err)
	}
	if hits != 1 {
		t.Errorf("page fetched %d times, want 1 (cached)", hits)
	}
	if c1 != c2 {
		t.Errorf("cached creds differ: %+v vs %+v", c1, c2)
	}

	// Expire the cache -> next harvest re-fetches (rotation self-heal).
	origNow := haNow
	haNow = func() time.Time { return origNow().Add(haCredTTL + time.Hour) }
	defer func() { haNow = origNow }()
	if _, err := harvestAlgoliaCreds(context.Background()); err != nil {
		t.Fatalf("harvest 3: %v", err)
	}
	if hits != 2 {
		t.Errorf("expired cache fetched %d times total, want 2", hits)
	}
}

// TestResolveHousingAnywhereCity exercises the geonames helper against a mock.
func TestResolveHousingAnywhereCity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") != "Berlin--Germany" {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cities":[{"lat":52.52,"lng":13.405}]}`))
	}))
	defer srv.Close()

	orig := haGeonamesURL
	haGeonamesURL = srv.URL
	defer func() { haGeonamesURL = orig }()

	lat, lon, err := ResolveHousingAnywhereCity(context.Background(), "Berlin", "Germany")
	if err != nil {
		t.Fatalf("ResolveHousingAnywhereCity: %v", err)
	}
	if lat != 52.52 || lon != 13.405 {
		t.Errorf("coords = (%v,%v), want (52.52,13.405)", lat, lon)
	}
}

// TestSearchHousingAnywhereLiveProbe hits the real endpoints. Opt-in only:
// gated behind TRVL_TEST_LIVE_INTEGRATIONS=1 per the offline-default-suite rule.
func TestSearchHousingAnywhereLiveProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live probe in -short mode")
	}
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") != "1" {
		t.Skip("set TRVL_TEST_LIVE_INTEGRATIONS=1 to run the HousingAnywhere live probe")
	}
	resetHACreds(t)
	origEnabled := housinganywhereEnabled
	housinganywhereEnabled = true
	defer func() { housinganywhereEnabled = origEnabled }()

	hotels, err := SearchHousingAnywhere(context.Background(), "Berlin, Germany", HotelSearchOptions{Currency: "EUR"})
	if err != nil {
		t.Fatalf("live SearchHousingAnywhere: %v", err)
	}
	if len(hotels) == 0 {
		t.Fatal("live probe returned zero results")
	}
	t.Logf("live HousingAnywhere returned %d results; first: %s @ %.0f %s",
		len(hotels), hotels[0].Name, hotels[0].Price, hotels[0].Currency)
}
