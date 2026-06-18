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

// loadUniplacesFixture reads the saved Uniplaces search payload from testdata.
func loadUniplacesFixture(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("testdata/uniplaces_search_lisbon.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return json.RawMessage(b)
}

// TestParseUniplacesOffersFixture asserts the saved Lisbon search payload maps
// to well-formed HotelResults: name, numeric price (converted from cents),
// currency, geo, absolute booking URL, property type, and a "uniplaces" price
// source.
func TestParseUniplacesOffersFixture(t *testing.T) {
	raw := loadUniplacesFixture(t)
	hotels, err := parseUniplacesOffers(raw, "lisbon", "")
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
		if len(h.Sources) != 1 || h.Sources[0].Provider != "uniplaces" {
			t.Errorf("hotel %d (%s): want single uniplaces source, got %+v", i, h.Name, h.Sources)
		}
	}

	// First Lisbon offer fixed assertions (deterministic fixture).
	first := hotels[0]
	if first.Name != "Cozy Double Bedroom close to Carnide Metro" {
		t.Errorf("first name = %q", first.Name)
	}
	if first.HotelID != "846117" {
		t.Errorf("first id = %q, want 846117", first.HotelID)
	}
	// 47000 cents -> 470.00.
	if first.Price != 470 {
		t.Errorf("first price = %v, want 470 (47000 cents / 100)", first.Price)
	}
	if first.Currency != "EUR" {
		t.Errorf("first currency = %q, want EUR", first.Currency)
	}
	// Coordinates are [lat, lon]: Lisbon ~ (38.75, -9.19).
	if first.Lat < 38 || first.Lat > 39 {
		t.Errorf("first lat = %v, want ~38.75 (coordinate order [lat,lon])", first.Lat)
	}
	if first.Lon > -9 || first.Lon < -10 {
		t.Errorf("first lon = %v, want ~-9.19 (coordinate order [lat,lon])", first.Lon)
	}
	if first.PropertyType != "apartment" {
		t.Errorf("first property type = %q, want apartment", first.PropertyType)
	}
	if !strings.Contains(first.BookingURL, "/accommodation/lisbon/846117") {
		t.Errorf("first booking URL = %q", first.BookingURL)
	}
}

// TestParseUniplacesOffersSkips ensures sold-out and zero-price offers are
// dropped so every result is comparable.
func TestParseUniplacesOffersSkips(t *testing.T) {
	raw := json.RawMessage(`{"pageProps":{"offers":{"data":[
		{"id":"a","attributes":{"accommodation_offer":{"title":"Priced","price":{"amount":50000,"currency_code":"EUR"}},"property":{"type":"apartment","coordinates":[38.7,-9.1]}}},
		{"id":"b","attributes":{"accommodation_offer":{"title":"Sold out","is_sold_out":true,"price":{"amount":60000,"currency_code":"EUR"}},"property":{"coordinates":[38.7,-9.1]}}},
		{"id":"c","attributes":{"accommodation_offer":{"title":"Zero price","price":{"amount":0,"currency_code":"EUR"}},"property":{"coordinates":[38.7,-9.1]}}}
	]}}}`)
	hotels, err := parseUniplacesOffers(raw, "lisbon", "USD")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hotels) != 1 {
		t.Fatalf("want 1 mapped hotel (sold-out + zero-price dropped), got %d", len(hotels))
	}
	if hotels[0].Name != "Priced" {
		t.Errorf("mapped wrong offer: %q", hotels[0].Name)
	}
	if hotels[0].Price != 500 {
		t.Errorf("price = %v, want 500", hotels[0].Price)
	}
}

// TestParseUniplacesOffersCurrencyFallback verifies the fallback currency is
// used when an offer omits a currency code.
func TestParseUniplacesOffersCurrencyFallback(t *testing.T) {
	raw := json.RawMessage(`{"pageProps":{"offers":{"data":[
		{"id":"a","attributes":{"accommodation_offer":{"title":"No currency","price":{"amount":30000}},"property":{"coordinates":[1,2]}}}
	]}}}`)
	hotels, err := parseUniplacesOffers(raw, "lisbon", "gbp")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hotels) != 1 || hotels[0].Currency != "GBP" {
		t.Fatalf("want GBP fallback currency, got %+v", hotels)
	}
}

// TestUniplacesCitySlug verifies free-text -> single-token city slug reduction.
func TestUniplacesCitySlug(t *testing.T) {
	cases := map[string]string{
		"Lisbon":            "lisbon",
		"Lisbon, Portugal":  "lisbon",
		"New York":          "new-york",
		"  Berlin  ":        "berlin",
		"São Paulo, Brazil": "so-paulo",
	}
	for in, want := range cases {
		if got := uniplacesCitySlug(in); got != want {
			t.Errorf("uniplacesCitySlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSearchUniplacesMockServer exercises the full two-step flow against an
// httptest server: resolve the rotating buildId from the listing page's
// __NEXT_DATA__ blob, then fetch the _next/data JSON for that buildId.
func TestSearchUniplacesMockServer(t *testing.T) {
	listingHTML, err := os.ReadFile("testdata/uniplaces_listing_lisbon.html")
	if err != nil {
		t.Fatalf("read listing fixture: %v", err)
	}
	offersJSON, err := os.ReadFile("testdata/uniplaces_search_lisbon.json")
	if err != nil {
		t.Fatalf("read offers fixture: %v", err)
	}
	const wantBuildID = "search-06e8c5954f295939db05be8c4e59664a7fc91851"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/accommodation/lisbon":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write(listingHTML)
		case r.URL.Path == "/_next/data/"+wantBuildID+"/en/accommodation/lisbon.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(offersJSON)
		default:
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// Enable live path against the mock server.
	savedEnabled, savedBase := uniplacesEnabled, uniplacesBaseURL
	uniplacesEnabled, uniplacesBaseURL = true, srv.URL
	defer func() { uniplacesEnabled, uniplacesBaseURL = savedEnabled, savedBase }()

	hotels, err := SearchUniplaces(context.Background(), "Lisbon, Portugal", HotelSearchOptions{Currency: "EUR"})
	if err != nil {
		t.Fatalf("SearchUniplaces: %v", err)
	}
	if len(hotels) != 3 {
		t.Fatalf("want 3 hotels from fixture, got %d", len(hotels))
	}
	if hotels[0].HotelID != "846117" {
		t.Errorf("first hotel id = %q, want 846117", hotels[0].HotelID)
	}
}

// TestSearchUniplacesDisabledReturnsNil verifies the test-mode short circuit.
func TestSearchUniplacesDisabledReturnsNil(t *testing.T) {
	saved := uniplacesEnabled
	uniplacesEnabled = false
	defer func() { uniplacesEnabled = saved }()
	hotels, err := SearchUniplaces(context.Background(), "Lisbon", HotelSearchOptions{})
	if err != nil || hotels != nil {
		t.Fatalf("want nil,nil when disabled; got %v, %v", hotels, err)
	}
}

// TestSearchUniplacesLive is an opt-in live integration probe. It is skipped by
// default (offline-default-suite rule) and only runs when
// TRVL_TEST_LIVE_INTEGRATIONS is set. It exercises the real rotating-buildId
// resolution and _next/data fetch against the live site.
func TestSearchUniplacesLive(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") == "" {
		t.Skip("set TRVL_TEST_LIVE_INTEGRATIONS=1 to run live Uniplaces probe")
	}
	saved := uniplacesEnabled
	uniplacesEnabled = true
	defer func() { uniplacesEnabled = saved }()

	hotels, err := SearchUniplaces(context.Background(), "Lisbon", HotelSearchOptions{Currency: "EUR"})
	if err != nil {
		t.Fatalf("live SearchUniplaces: %v", err)
	}
	if len(hotels) == 0 {
		t.Fatal("live: expected at least one hotel")
	}
	for i, h := range hotels {
		if h.Price <= 0 || h.Currency == "" || h.Name == "" {
			t.Errorf("live hotel %d malformed: %+v", i, h)
		}
	}
}
