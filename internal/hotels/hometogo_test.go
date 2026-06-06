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

// loadHomeToGoFixture reads a saved HomeToGo search payload from testdata.
func loadHomeToGoFixture(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("testdata/hometogo_search_berlin.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return json.RawMessage(b)
}

// TestParseHomeToGoOffersFixture asserts the saved Berlin search payload maps
// to well-formed HotelResults: name, numeric price+currency, geo, absolute
// booking/image URLs, amenities, and a "hometogo" price source.
func TestParseHomeToGoOffersFixture(t *testing.T) {
	raw := loadHomeToGoFixture(t)
	hotels, err := parseHomeToGoOffers(raw, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hotels) == 0 {
		t.Fatal("expected at least one mapped hotel, got 0")
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
		if len(h.Sources) != 1 || h.Sources[0].Provider != "hometogo" {
			t.Errorf("hotel %d (%s): want single hometogo source, got %+v", i, h.Name, h.Sources)
		}
	}

	// First Berlin offer fixed assertions (deterministic fixture).
	first := hotels[0]
	if first.Name != "65 m² Apartment" {
		t.Errorf("first name = %q, want %q", first.Name, "65 m² Apartment")
	}
	if first.Price != 94 {
		t.Errorf("first price = %v, want 94", first.Price)
	}
	if first.Currency != "EUR" {
		t.Errorf("first currency = %q, want EUR", first.Currency)
	}
	if first.Address != "Berlin, Germany" {
		t.Errorf("first address = %q, want %q", first.Address, "Berlin, Germany")
	}
	if len(first.Amenities) == 0 {
		t.Error("first offer: expected amenities")
	}
}

// TestParseHomeToGoOffersSkipsStubs ensures lazy-loaded stubs (no price) and
// zero/garbage prices are dropped, so every result is comparable.
func TestParseHomeToGoOffersSkipsStubs(t *testing.T) {
	raw := json.RawMessage(`{"offers":[
		{"id":"a","title":"Priced","geoLocation":{"lat":1,"lon":2},"lowestPriceInfo":{"display":"120 €"},"deepLink":"/x"},
		{"id":"b","title":"Stub no price info","geoLocation":{"lat":1,"lon":2}},
		{"id":"c","title":"Empty display","lowestPriceInfo":{"display":""}},
		{"id":"d","title":"Zero price","lowestPriceInfo":{"display":"0 €"}}
	]}`)
	hotels, err := parseHomeToGoOffers(raw, "USD")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hotels) != 1 {
		t.Fatalf("want 1 mapped hotel (only the priced one), got %d", len(hotels))
	}
	if hotels[0].HotelID != "a" {
		t.Errorf("mapped wrong offer: %q", hotels[0].HotelID)
	}
}

func TestParseHomeToGoPrice(t *testing.T) {
	cases := []struct {
		display, fallback string
		wantAmt           float64
		wantCur           string
	}{
		{"94 €", "", 94, "EUR"},
		{"1,234 €", "", 1234, "EUR"},
		{"$1,099", "", 1099, "USD"},
		{"£500", "", 500, "GBP"},
		{"77", "usd", 77, "USD"},
		{"88", "", 88, "EUR"},
		{"no digits", "", 0, "EUR"},
	}
	for _, c := range cases {
		amt, cur := parseHomeToGoPrice(c.display, c.fallback)
		if amt != c.wantAmt || cur != c.wantCur {
			t.Errorf("parseHomeToGoPrice(%q,%q) = (%v,%q), want (%v,%q)",
				c.display, c.fallback, amt, cur, c.wantAmt, c.wantCur)
		}
	}
}

func TestHomeToGoSlug(t *testing.T) {
	cases := map[string]string{
		"Berlin":        "berlin",
		"New York, USA": "new-york-usa",
		"  Paris  ":     "paris",
		"São Paulo":     "so-paulo",
		"Côte d'Azur":   "cte-dazur",
		"a/b.c":         "a-b-c",
		"":              "",
	}
	for in, want := range cases {
		if got := hometogoSlug(in); got != want {
			t.Errorf("hometogoSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHomeToGoAbsURL(t *testing.T) {
	orig := hometogoBaseURL
	hometogoBaseURL = "https://www.hometogo.com"
	defer func() { hometogoBaseURL = orig }()
	cases := map[string]string{
		"":                            "",
		"/deeplink/?x=1":              "https://www.hometogo.com/deeplink/?x=1",
		"deeplink":                    "https://www.hometogo.com/deeplink",
		"//cdn.hometogo.net/a.jpg":    "https://cdn.hometogo.net/a.jpg",
		"https://example.com/already": "https://example.com/already",
	}
	for in, want := range cases {
		if got := hometogoAbsURL(in); got != want {
			t.Errorf("hometogoAbsURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveHomeToGoLocation(t *testing.T) {
	html, err := os.ReadFile("testdata/hometogo_resolve_berlin.html")
	if err != nil {
		t.Fatalf("read resolve fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/berlin/" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(html)
	}))
	defer srv.Close()

	orig := hometogoBaseURL
	hometogoBaseURL = srv.URL
	defer func() { hometogoBaseURL = orig }()

	id, err := resolveHomeToGoLocation(context.Background(), "berlin")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if id != "5460aed009800" {
		t.Errorf("resolved id = %q, want 5460aed009800", id)
	}

	// Missing location marker -> error.
	if _, err := resolveHomeToGoLocation(context.Background(), "nowhere"); err == nil {
		t.Error("expected error for slug with no landing page")
	}
}

// TestSearchHomeToGoEndToEnd wires resolve + fetch against a mock server,
// exercising the full SearchHomeToGo pipeline deterministically.
func TestSearchHomeToGoEndToEnd(t *testing.T) {
	html, err := os.ReadFile("testdata/hometogo_resolve_berlin.html")
	if err != nil {
		t.Fatalf("read resolve fixture: %v", err)
	}
	searchJSON, err := os.ReadFile("testdata/hometogo_search_berlin.json")
	if err != nil {
		t.Fatalf("read search fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/berlin/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write(html)
		case strings.HasPrefix(r.URL.Path, "/search/5460aed009800"):
			if r.URL.Query().Get("_format") != "json" {
				http.Error(w, "want _format=json", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(searchJSON)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	origURL, origEnabled := hometogoBaseURL, hometogoEnabled
	hometogoBaseURL = srv.URL
	hometogoEnabled = true
	defer func() { hometogoBaseURL, hometogoEnabled = origURL, origEnabled }()

	hotels, err := SearchHomeToGo(context.Background(), "Berlin", HotelSearchOptions{Currency: "EUR"})
	if err != nil {
		t.Fatalf("SearchHomeToGo: %v", err)
	}
	if len(hotels) == 0 {
		t.Fatal("expected results from end-to-end search")
	}
	if hotels[0].Sources[0].Provider != "hometogo" {
		t.Errorf("source provider = %q, want hometogo", hotels[0].Sources[0].Provider)
	}

	// Disabled -> nil, nil.
	hometogoEnabled = false
	got, err := SearchHomeToGo(context.Background(), "Berlin", HotelSearchOptions{})
	if err != nil || got != nil {
		t.Errorf("disabled SearchHomeToGo = (%v,%v), want (nil,nil)", got, err)
	}
}

// TestSearchHomeToGoLiveProbe hits the real HomeToGo endpoints. Opt-in only:
// gated behind TRVL_TEST_LIVE_PROBES=1 and skipped by -short.
func TestSearchHomeToGoLiveProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live probe in -short mode")
	}
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("set TRVL_TEST_LIVE_PROBES=1 to run the HomeToGo live probe")
	}
	origEnabled := hometogoEnabled
	hometogoEnabled = true
	defer func() { hometogoEnabled = origEnabled }()

	hotels, err := SearchHomeToGo(context.Background(), "Berlin", HotelSearchOptions{Currency: "EUR"})
	if err != nil {
		t.Fatalf("live SearchHomeToGo: %v", err)
	}
	if len(hotels) == 0 {
		t.Fatal("live probe returned zero hotels")
	}
	t.Logf("live HomeToGo returned %d hotels; first: %s @ %.0f %s",
		len(hotels), hotels[0].Name, hotels[0].Price, hotels[0].Currency)
}
