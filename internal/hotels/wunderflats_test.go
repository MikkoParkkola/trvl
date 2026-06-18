package hotels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// loadWunderflatsFixture reads a saved Wunderflats SSR page from testdata.
func loadWunderflatsFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// TestExtractWunderflatsPageDataDirect asserts the data-hydrant blob with
// pageData embedded directly is parsed into well-formed HotelResults, with the
// three list-payload GOTCHAS handled: price-in-cents, GeoJSON coord swap, and
// localized title selection.
func TestExtractWunderflatsPageDataDirect(t *testing.T) {
	html := loadWunderflatsFixture(t, "wunderflats_munich.html")
	pd, err := extractWunderflatsPageData(html)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	lr := pd.ListingResults
	if lr.Total != 1144 || lr.ItemsPerPage != 30 || lr.Page != 0 {
		t.Errorf("listing meta = total %d, perPage %d, page %d; want 1144/30/0",
			lr.Total, lr.ItemsPerPage, lr.Page)
	}
	if lr.Region.Slug != "munich" {
		t.Errorf("region slug = %q, want munich", lr.Region.Slug)
	}

	hotels := parseWunderflatsListings(lr, "")
	if len(hotels) == 0 {
		t.Fatal("expected at least one mapped hotel, got 0")
	}
	first := hotels[0]

	// GOTCHA 1: price 175000 cents -> 1750.00.
	if first.Price != 1750 {
		t.Errorf("first price = %v, want 1750 (175000 cents / 100)", first.Price)
	}
	if first.Currency != "EUR" {
		t.Errorf("first currency = %q, want EUR", first.Currency)
	}
	// GOTCHA 2: coords [11.5516427, 48.1535828] (lng,lat) -> lat 48.15, lon 11.55.
	if first.Lat < 48.1 || first.Lat > 48.2 {
		t.Errorf("first lat = %v, want ~48.15 (latitude, not longitude)", first.Lat)
	}
	if first.Lon < 11.5 || first.Lon > 11.6 {
		t.Errorf("first lon = %v, want ~11.55 (longitude, not latitude)", first.Lon)
	}
	// GOTCHA 3: localized title -> prefer en.
	if !strings.Contains(first.Name, "Munich") {
		t.Errorf("first name = %q, want the English title", first.Name)
	}
	// URL constructed from _id, /x/ form.
	wantURL := "https://wunderflats.com/en/furnished-apartment/x/65cc89b38649b460f6427ec2"
	if first.BookingURL != wantURL {
		t.Errorf("first booking URL = %q, want %q", first.BookingURL, wantURL)
	}
	if !strings.HasPrefix(first.ImageURL, "https://listingimages.wunderflats.com/") {
		t.Errorf("first image URL = %q, want original CDN URL", first.ImageURL)
	}
	if first.PropertyType != "apartment" {
		t.Errorf("first property type = %q, want apartment", first.PropertyType)
	}
	if len(first.Sources) != 1 || first.Sources[0].Provider != "wunderflats" {
		t.Errorf("want single wunderflats source, got %+v", first.Sources)
	}

	for i, h := range hotels {
		if h.HotelID == "" {
			t.Errorf("hotel %d: empty id", i)
		}
		if h.Price <= 0 {
			t.Errorf("hotel %d (%s): non-positive price %v", i, h.Name, h.Price)
		}
		if h.Lat == 0 || h.Lon == 0 {
			t.Errorf("hotel %d (%s): missing geo (%v,%v)", i, h.Name, h.Lat, h.Lon)
		}
	}
}

// TestExtractWunderflatsPageDataDoubleEncoded asserts the `result` form, whose
// value is a DOUBLE-ENCODED JSON string, is decoded twice and yields the same
// pageData as the direct form.
func TestExtractWunderflatsPageDataDoubleEncoded(t *testing.T) {
	html := loadWunderflatsFixture(t, "wunderflats_munich_doubleencoded.html")
	pd, err := extractWunderflatsPageData(html)
	if err != nil {
		t.Fatalf("extract (double-encoded): %v", err)
	}
	hotels := parseWunderflatsListings(pd.ListingResults, "")
	if len(hotels) == 0 {
		t.Fatal("expected mapped hotels from double-encoded result, got 0")
	}
	if hotels[0].HotelID != "65cc89b38649b460f6427ec2" {
		t.Errorf("first id = %q, want 65cc89b38649b460f6427ec2", hotels[0].HotelID)
	}
	if hotels[0].Price != 1750 {
		t.Errorf("first price = %v, want 1750", hotels[0].Price)
	}
}

// TestDecodeNestedJSON exercises the layer-peeling unwrapper for the
// double-encoded payload variant.
func TestDecodeNestedJSON(t *testing.T) {
	// A plain object passes through unchanged.
	obj := []byte(`{"a":1}`)
	got, err := decodeNestedJSON(obj)
	if err != nil || strings.TrimSpace(string(got)) != `{"a":1}` {
		t.Fatalf("plain object: got %q err %v", got, err)
	}
	// Single-encoded: a JSON string holding JSON.
	once := []byte(`"{\"a\":1}"`)
	got, err = decodeNestedJSON(once)
	if err != nil || string(got) != `{"a":1}` {
		t.Fatalf("single-encoded: got %q err %v", got, err)
	}
	// Double-encoded: a JSON string holding a JSON string holding JSON.
	twice := []byte(`"\"{\\\"a\\\":1}\""`)
	got, err = decodeNestedJSON(twice)
	if err != nil || string(got) != `{"a":1}` {
		t.Fatalf("double-encoded: got %q err %v", got, err)
	}
}

// TestParseWunderflatsListingsSkipsBad ensures items without an id or with a
// non-positive price are dropped, and currency falls back when absent.
func TestParseWunderflatsListingsSkipsBad(t *testing.T) {
	lr := wunderflatsListingResults{
		Items: []wunderflatsListing{
			{ID: "ok", Price: 90000, Currency: "", Title: map[string]string{"en": "Ok"},
				Address: wunderflatsAddress{Location: wunderflatsGeoPoint{Coordinates: []float64{13.4, 52.5}}}},
			{ID: "", Price: 50000, Currency: "EUR"},   // no id -> skip
			{ID: "free", Price: 0, Currency: "EUR"},   // zero price -> skip
			{ID: "neg", Price: -100, Currency: "EUR"}, // negative -> skip
		},
	}
	hotels := parseWunderflatsListings(lr, "usd")
	if len(hotels) != 1 {
		t.Fatalf("want 1 mapped hotel, got %d", len(hotels))
	}
	h := hotels[0]
	if h.HotelID != "ok" {
		t.Errorf("mapped wrong item: %q", h.HotelID)
	}
	if h.Price != 900 {
		t.Errorf("price = %v, want 900 (90000 cents)", h.Price)
	}
	if h.Currency != "USD" {
		t.Errorf("currency fallback = %q, want USD", h.Currency)
	}
	// coord swap check.
	if h.Lat != 52.5 || h.Lon != 13.4 {
		t.Errorf("coords = (%v,%v), want lat 52.5 lon 13.4", h.Lat, h.Lon)
	}
}

func TestWunderflatsTitle(t *testing.T) {
	cases := []struct {
		in   map[string]string
		want string
	}{
		{map[string]string{"en": "English", "de": "Deutsch"}, "English"},
		{map[string]string{"de": "Deutsch"}, "Deutsch"},
		{map[string]string{"fr": "Francais"}, "Francais"},
		{map[string]string{"en": "  "}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := wunderflatsTitle(c.in); got != c.want {
			t.Errorf("wunderflatsTitle(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWunderflatsSlug(t *testing.T) {
	cases := map[string]string{
		"Munich":      "munich",
		"München":     "mnchen", // accents are dropped (not transliterated), matching hometogoSlug
		"Berlin, DE":  "berlin-de",
		"  Hamburg  ": "hamburg",
		"a/b.c":       "a-b-c",
		"":            "",
	}
	for in, want := range cases {
		if got := wunderflatsSlug(in); got != want {
			t.Errorf("wunderflatsSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWunderflatsListingURL(t *testing.T) {
	orig := wunderflatsBaseURL
	wunderflatsBaseURL = "https://wunderflats.com"
	defer func() { wunderflatsBaseURL = orig }()
	if got := wunderflatsListingURL("abc123"); got != "https://wunderflats.com/en/furnished-apartment/x/abc123" {
		t.Errorf("listing URL = %q", got)
	}
	if got := wunderflatsListingURL("  "); got != "" {
		t.Errorf("empty id URL = %q, want empty", got)
	}
}

// TestSearchWunderflatsEndToEnd wires the full fetch+parse pipeline against a
// mock server serving the real (trimmed) data-hydrant fixture.
func TestSearchWunderflatsEndToEnd(t *testing.T) {
	html := loadWunderflatsFixture(t, "wunderflats_munich.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/en/furnished-apartments/munich") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("page") != "0" {
			http.Error(w, "want page=0", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(html)
	}))
	defer srv.Close()

	origURL, origEnabled := wunderflatsBaseURL, wunderflatsEnabled
	wunderflatsBaseURL = srv.URL
	wunderflatsEnabled = true
	defer func() { wunderflatsBaseURL, wunderflatsEnabled = origURL, origEnabled }()

	hotels, err := SearchWunderflats(context.Background(), "Munich", HotelSearchOptions{Currency: "EUR"})
	if err != nil {
		t.Fatalf("SearchWunderflats: %v", err)
	}
	if len(hotels) == 0 {
		t.Fatal("expected results from end-to-end search")
	}
	if hotels[0].Sources[0].Provider != "wunderflats" {
		t.Errorf("source provider = %q, want wunderflats", hotels[0].Sources[0].Provider)
	}

	// Disabled -> nil, nil.
	wunderflatsEnabled = false
	got, err := SearchWunderflats(context.Background(), "Munich", HotelSearchOptions{})
	if err != nil || got != nil {
		t.Errorf("disabled SearchWunderflats = (%v,%v), want (nil,nil)", got, err)
	}
}

func TestSearchWunderflatsEmptyLocation(t *testing.T) {
	origEnabled := wunderflatsEnabled
	wunderflatsEnabled = true
	defer func() { wunderflatsEnabled = origEnabled }()
	if _, err := SearchWunderflats(context.Background(), "   ", HotelSearchOptions{}); err == nil {
		t.Error("expected error for empty location")
	}
}

// TestSearchWunderflatsLiveIntegration hits the real Wunderflats endpoint.
// Opt-in only: gated behind TRVL_TEST_LIVE_INTEGRATIONS=1 and skipped by -short.
func TestSearchWunderflatsLiveIntegration(t *testing.T) {
	testutil.RequireLiveIntegration(t)

	origEnabled := wunderflatsEnabled
	wunderflatsEnabled = true
	defer func() { wunderflatsEnabled = origEnabled }()

	hotels, err := SearchWunderflats(context.Background(), "Munich", HotelSearchOptions{Currency: "EUR"})
	if err != nil {
		t.Fatalf("live SearchWunderflats: %v", err)
	}
	if len(hotels) == 0 {
		t.Fatal("live integration returned zero hotels")
	}
	t.Logf("live Wunderflats returned %d hotels; first: %s @ %.0f %s (%.4f,%.4f)",
		len(hotels), hotels[0].Name, hotels[0].Price, hotels[0].Currency, hotels[0].Lat, hotels[0].Lon)
}
