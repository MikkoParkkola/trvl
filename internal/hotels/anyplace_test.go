package hotels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// loadAnyplaceCityFixture reads the saved Anyplace _next/data listings payload.
func loadAnyplaceCityFixture(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("testdata/anyplace_city_lisbon.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return json.RawMessage(b)
}

// TestParseAnyplaceListingsFixture asserts the saved Lisbon payload maps to
// well-formed HotelResults: name, numeric price+currency, geo, absolute
// booking/image URLs, apartment property type, mid-term description, and a
// single "anyplace" price source.
func TestParseAnyplaceListingsFixture(t *testing.T) {
	raw := loadAnyplaceCityFixture(t)
	hotels, err := parseAnyplaceListings(raw, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 3 listings in fixture; the unpriced stub is dropped.
	if len(hotels) != 2 {
		t.Fatalf("want 2 mapped hotels (stub dropped), got %d", len(hotels))
	}
	for i, h := range hotels {
		if h.Name == "" {
			t.Errorf("hotel %d: empty name", i)
		}
		if h.Price <= 0 {
			t.Errorf("hotel %d (%s): non-positive price %v", i, h.Name, h.Price)
		}
		if h.Currency == "" {
			t.Errorf("hotel %d (%s): empty currency", i, h.Name)
		}
		if h.Lat == 0 || h.Lon == 0 {
			t.Errorf("hotel %d (%s): missing geo (%v,%v)", i, h.Name, h.Lat, h.Lon)
		}
		if !strings.HasPrefix(h.BookingURL, "https://") {
			t.Errorf("hotel %d (%s): booking URL not absolute: %q", i, h.Name, h.BookingURL)
		}
		if h.ImageURL != "" && !strings.HasPrefix(h.ImageURL, "https://") {
			t.Errorf("hotel %d (%s): image URL not absolute: %q", i, h.Name, h.ImageURL)
		}
		if h.PropertyType != "apartment" {
			t.Errorf("hotel %d (%s): property type = %q, want apartment", i, h.Name, h.PropertyType)
		}
		if len(h.Sources) != 1 || h.Sources[0].Provider != "anyplace" {
			t.Errorf("hotel %d (%s): want single anyplace source, got %+v", i, h.Name, h.Sources)
		}
	}

	// First listing fixed assertions (deterministic fixture).
	first := hotels[0]
	if first.HotelID != "ap-1001" {
		t.Errorf("first id = %q, want ap-1001", first.HotelID)
	}
	if first.Name != "Sunny Studio in Chiado" {
		t.Errorf("first name = %q, want %q", first.Name, "Sunny Studio in Chiado")
	}
	if first.Price != 4645 {
		t.Errorf("first price = %v, want 4645", first.Price)
	}
	if first.Currency != "USD" {
		t.Errorf("first currency = %q, want USD", first.Currency)
	}
	if first.Lat != 38.7101 || first.Lon != -9.1421 {
		t.Errorf("first geo = (%v,%v), want (38.7101,-9.1421)", first.Lat, first.Lon)
	}
	if first.ImageURL != "https://cdn.anyplace.com/listings/ap-1001/cover.jpg" {
		t.Errorf("first image = %q (want protocol-relative made absolute)", first.ImageURL)
	}
	// Mid-term fields (monthlyPrice, minimumStay, bed/bath) fold into Description.
	for _, want := range []string{"1 bed", "1 bath", "30-night min", "4170 USD/mo"} {
		if !strings.Contains(first.Description, want) {
			t.Errorf("first description %q missing %q", first.Description, want)
		}
	}
}

// TestParseAnyplaceListingsSkipsUnpriced ensures listings without a usable
// headline price are dropped, so every result is comparable.
func TestParseAnyplaceListingsSkipsUnpriced(t *testing.T) {
	raw := json.RawMessage(`{"pageProps":{"listings":[
		{"id":"a","title":"Priced","price":3000,"currency":"USD","lat":1,"long":2},
		{"id":"b","title":"Zero price","price":0,"currency":"USD","lat":1,"long":2},
		{"id":"c","title":"Negative price","price":-5,"currency":"USD","lat":1,"long":2}
	]}}`)
	hotels, err := parseAnyplaceListings(raw, "EUR")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hotels) != 1 {
		t.Fatalf("want 1 mapped hotel (only the priced one), got %d", len(hotels))
	}
	if hotels[0].HotelID != "a" {
		t.Errorf("mapped wrong listing: %q", hotels[0].HotelID)
	}
}

func TestAnyplaceCurrency(t *testing.T) {
	cases := []struct {
		currency, fallback, want string
	}{
		{"USD", "", "USD"},
		{"eur", "", "EUR"},
		{"", "gbp", "GBP"},
		{"", "", "USD"},
		{" usd ", "", "USD"},
	}
	for _, c := range cases {
		if got := anyplaceCurrency(c.currency, c.fallback); got != c.want {
			t.Errorf("anyplaceCurrency(%q,%q) = %q, want %q", c.currency, c.fallback, got, c.want)
		}
	}
}

func TestAnyplacePropertyType(t *testing.T) {
	cases := map[string]string{
		"furnished_apartment": "apartment",
		"apartment":           "apartment",
		"":                    "apartment",
		"LOFT":                "loft",
		"Studio":              "studio",
	}
	for in, want := range cases {
		if got := anyplacePropertyType(in); got != want {
			t.Errorf("anyplacePropertyType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnyplaceCitySlug(t *testing.T) {
	cases := map[string]string{
		"Lisbon":            "lisbon",
		"Lisbon, Portugal":  "lisbon-portugal",
		"  Mexico City  ":   "mexico-city",
		"São Paulo":         "so-paulo",
		"New York/Brooklyn": "new-york-brooklyn",
		"":                  "",
	}
	for in, want := range cases {
		if got := anyplaceCitySlug(in); got != want {
			t.Errorf("anyplaceCitySlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnyplaceAbsURL(t *testing.T) {
	orig := anyplaceBaseURL
	anyplaceBaseURL = "https://www.anyplace.com"
	defer func() { anyplaceBaseURL = orig }()
	cases := map[string]string{
		"":                            "",
		"/listing/x":                  "https://www.anyplace.com/listing/x",
		"cover.jpg":                   "https://www.anyplace.com/cover.jpg",
		"//cdn.anyplace.com/a.jpg":    "https://cdn.anyplace.com/a.jpg",
		"https://example.com/already": "https://example.com/already",
	}
	for in, want := range cases {
		if got := anyplaceAbsURL(in); got != want {
			t.Errorf("anyplaceAbsURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestResolveAnyplaceBuildID covers both the env override and the dynamic
// __NEXT_DATA__ resolve path, plus the missing-blob error.
func TestResolveAnyplaceBuildID(t *testing.T) {
	html, err := os.ReadFile("testdata/anyplace_resolve_lisbon.html")
	if err != nil {
		t.Fatalf("read resolve fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/listings/lisbon-portugal" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(html)
	}))
	defer srv.Close()

	orig := anyplaceBaseURL
	anyplaceBaseURL = srv.URL
	defer func() { anyplaceBaseURL = orig }()

	// Env override wins without any HTTP call.
	t.Setenv("ANYPLACE_BUILD_ID", "override-build-123")
	id, err := resolveAnyplaceBuildID(context.Background(), "lisbon-portugal")
	if err != nil {
		t.Fatalf("resolve (env): %v", err)
	}
	if id != "override-build-123" {
		t.Errorf("env override id = %q, want override-build-123", id)
	}

	// Dynamic resolve from __NEXT_DATA__.
	t.Setenv("ANYPLACE_BUILD_ID", "")
	id, err = resolveAnyplaceBuildID(context.Background(), "lisbon-portugal")
	if err != nil {
		t.Fatalf("resolve (dynamic): %v", err)
	}
	if id != "AbC123dEf456" {
		t.Errorf("dynamic id = %q, want AbC123dEf456", id)
	}

	// Missing landing page -> error.
	if _, err := resolveAnyplaceBuildID(context.Background(), "nowhere"); err == nil {
		t.Error("expected error for slug with no landing page")
	}
}

// TestSearchAnyplaceEndToEnd wires resolve + fetch against a mock server,
// exercising the full SearchAnyplace pipeline deterministically.
func TestSearchAnyplaceEndToEnd(t *testing.T) {
	html, err := os.ReadFile("testdata/anyplace_resolve_lisbon.html")
	if err != nil {
		t.Fatalf("read resolve fixture: %v", err)
	}
	cityJSON, err := os.ReadFile("testdata/anyplace_city_lisbon.json")
	if err != nil {
		t.Fatalf("read city fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/listings/lisbon-portugal":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write(html)
		case r.URL.Path == "/_next/data/AbC123dEf456/listings/lisbon-portugal.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(cityJSON)
		default:
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	origURL, origEnabled := anyplaceBaseURL, anyplaceEnabled
	anyplaceBaseURL = srv.URL
	anyplaceEnabled = true
	t.Setenv("ANYPLACE_BUILD_ID", "") // force dynamic resolve through the mock
	defer func() { anyplaceBaseURL, anyplaceEnabled = origURL, origEnabled }()

	hotels, err := SearchAnyplace(context.Background(), "Lisbon, Portugal", HotelSearchOptions{Currency: "USD"})
	if err != nil {
		t.Fatalf("SearchAnyplace: %v", err)
	}
	if len(hotels) != 2 {
		t.Fatalf("expected 2 results from end-to-end search, got %d", len(hotels))
	}
	if hotels[0].Sources[0].Provider != "anyplace" {
		t.Errorf("source provider = %q, want anyplace", hotels[0].Sources[0].Provider)
	}

	// Disabled -> nil, nil.
	anyplaceEnabled = false
	got, err := SearchAnyplace(context.Background(), "Lisbon", HotelSearchOptions{})
	if err != nil || got != nil {
		t.Errorf("disabled SearchAnyplace = (%v,%v), want (nil,nil)", got, err)
	}
}

// TestSearchAnyplaceLiveIntegration probes the real Anyplace endpoints. Opt-in
// only: gated behind TRVL_TEST_LIVE_INTEGRATIONS=1 and skipped by -short.
//
// Anyplace is DEPRECATED / off by default (see anyplaceEnabled, 2026-06-20):
// the site moved to client-side rendering and the former SSR / _next/data city
// routes now 308-redirect to the homepage shell. The shipped contract — parser
// correctness against fixtures and the honest-skip (disabled -> nil,nil) path —
// is covered deterministically by TestSearchAnyplaceEndToEnd. This live test is
// therefore a *resurrection probe*: it force-enables the provider and reports
// whether a stable data path has returned. A live transport/endpoint failure
// (the current, expected reality) is an honest Skip, not a test failure —
// mirroring the easyJet AKAMAI_BLOCK precedent. The test only FAILS if the live
// call succeeds yet yields zero listings, which would be a genuine parser
// regression worth surfacing.
func TestSearchAnyplaceLiveIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live integration in -short mode")
	}
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") != "1" {
		t.Skip("set TRVL_TEST_LIVE_INTEGRATIONS=1 to run the Anyplace live integration")
	}
	origEnabled := anyplaceEnabled
	anyplaceEnabled = true
	defer func() { anyplaceEnabled = origEnabled }()

	hotels, err := SearchAnyplace(context.Background(), "Lisbon", HotelSearchOptions{Currency: "USD"})
	if err != nil {
		// Expected while Anyplace remains deprecated (dead SSR/_next/data path).
		// Honest skip: signals "upstream still down", not a code defect.
		t.Skipf("anyplace live endpoint still unavailable (provider remains deprecated): %v", err)
	}
	if len(hotels) == 0 {
		t.Fatal("live call succeeded but returned zero hotels (parser regression)")
	}
	t.Logf("live Anyplace returned %d listings; first: %s @ %.0f %s — endpoint resurrected, candidate to re-enable",
		len(hotels), hotels[0].Name, hotels[0].Price, hotels[0].Currency)
}
