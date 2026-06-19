package hotels

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

// loadAgodaSearchFixture reads the saved citySearch priced payload (Berlin) and
// unmarshals it into the response type parseAgodaSearch consumes.
func loadAgodaSearchFixture(t *testing.T) *agodaSearchResponse {
	t.Helper()
	b, err := os.ReadFile("testdata/agoda_search.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var sr agodaSearchResponse
	if err := json.Unmarshal(b, &sr); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return &sr
}

// TestParseAgodaSearchFixture asserts the saved Berlin payload maps to
// well-formed HotelResults: name, numeric price+currency, geo, absolute
// booking/image URLs, hotel property type, guest rating, and a single "agoda"
// price source carrying the crossed-out price.
func TestParseAgodaSearchFixture(t *testing.T) {
	resp := loadAgodaSearchFixture(t)
	hotels := parseAgodaSearch(resp, HotelSearchOptions{})
	if len(hotels) != 3 {
		t.Fatalf("want 3 mapped hotels, got %d", len(hotels))
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
		if len(h.Sources) != 1 || h.Sources[0].Provider != "agoda" {
			t.Errorf("hotel %d (%s): want single agoda source, got %+v", i, h.Name, h.Sources)
		}
	}

	// First property fixed assertions (deterministic fixture).
	first := hotels[0]
	if first.HotelID != "86662172" {
		t.Errorf("first id = %q, want 86662172", first.HotelID)
	}
	if first.Name != "The Hoxton, Berlin" {
		t.Errorf("first name = %q, want %q", first.Name, "The Hoxton, Berlin")
	}
	if first.Price != 79.71 {
		t.Errorf("first price = %v, want 79.71", first.Price)
	}
	if first.Currency != "EUR" {
		t.Errorf("first currency = %q, want EUR", first.Currency)
	}
	if first.Stars != 4 {
		t.Errorf("first stars = %d, want 4", first.Stars)
	}
	if first.Rating != 8.5 || first.ReviewCount != 73 {
		t.Errorf("first review = (%v, %d), want (8.5, 73)", first.Rating, first.ReviewCount)
	}
	if first.PropertyType != "hotel" {
		t.Errorf("first property type = %q, want hotel", first.PropertyType)
	}
	if first.Address != "Berlin, Germany" {
		t.Errorf("first address = %q, want %q", first.Address, "Berlin, Germany")
	}
	if first.Lat != 52.500835 || first.Lon != 13.329097 {
		t.Errorf("first geo = (%v,%v), want (52.500835,13.329097)", first.Lat, first.Lon)
	}
	if first.BookingURL != "https://www.agoda.com/the-hoxton-charlottenburg/hotel/berlin-de.html" {
		t.Errorf("first booking URL = %q", first.BookingURL)
	}
	if !strings.HasPrefix(first.ImageURL, "https://pix8.agoda.net/") {
		t.Errorf("first image URL = %q, want pix8.agoda.net absolute", first.ImageURL)
	}
	// Crossed-out price preserved on the source as MaxPrice.
	if first.Sources[0].MaxPrice != 98.54 {
		t.Errorf("first source MaxPrice = %v, want 98.54", first.Sources[0].MaxPrice)
	}
}

// TestParseAgodaSearchFilters covers the client-side MaxPrice and MinRating
// filters and the skip-unnamed guard.
func TestParseAgodaSearchFilters(t *testing.T) {
	resp := loadAgodaSearchFixture(t)

	// MaxPrice below the second/third property drops them (Hoxton 79.71 stays).
	capped := parseAgodaSearch(resp, HotelSearchOptions{MaxPrice: 80})
	if len(capped) != 1 || capped[0].HotelID != "86662172" {
		t.Fatalf("MaxPrice=80 -> want only Hoxton, got %d", len(capped))
	}

	// MinRating filters by guest score (0-10). All three fixtures score >= 8.0.
	rated := parseAgodaSearch(resp, HotelSearchOptions{MinRating: 9.9})
	if len(rated) != 0 {
		t.Errorf("MinRating=9.9 -> want 0, got %d", len(rated))
	}

	// Unnamed property is skipped.
	synthetic := &agodaSearchResponse{}
	synthetic.Data.CitySearch.Properties = make([]agodaProperty, 2)
	synthetic.Data.CitySearch.Properties[0].PropertyID = 1 // no displayName
	synthetic.Data.CitySearch.Properties[1].PropertyID = 2
	synthetic.Data.CitySearch.Properties[1].Content.InformationSummary.DisplayName = "Named"
	got := parseAgodaSearch(synthetic, HotelSearchOptions{})
	if len(got) != 1 || got[0].Name != "Named" {
		t.Errorf("unnamed skip -> want 1 (Named), got %d", len(got))
	}
}

// TestAgodaGateMeta asserts x-gate-meta is base64 of "ms|uuid|/graphql/".
func TestAgodaGateMeta(t *testing.T) {
	meta := agodaGateMeta()
	raw, err := base64.StdEncoding.DecodeString(meta)
	if err != nil {
		t.Fatalf("gate meta not base64: %v", err)
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		t.Fatalf("gate meta parts = %d, want 3 (%q)", len(parts), string(raw))
	}
	if _, err := strconv.ParseInt(parts[0], 10, 64); err != nil {
		t.Errorf("gate meta epoch not numeric: %q", parts[0])
	}
	if len(strings.Split(parts[1], "-")) != 5 {
		t.Errorf("gate meta userId not a UUID: %q", parts[1])
	}
	if parts[2] != "/graphql/" {
		t.Errorf("gate meta path = %q, want /graphql/", parts[2])
	}
}

func TestAgodaUUIDFormat(t *testing.T) {
	u := agodaUUID()
	segs := strings.Split(u, "-")
	wantLens := []int{8, 4, 4, 4, 12}
	if len(segs) != 5 {
		t.Fatalf("uuid %q has %d segments, want 5", u, len(segs))
	}
	for i, s := range segs {
		if len(s) != wantLens[i] {
			t.Errorf("uuid segment %d = %q len %d, want %d", i, s, len(s), wantLens[i])
		}
	}
	if u == agodaUUID() {
		t.Error("agodaUUID returned identical values on consecutive calls")
	}
}

func TestAgodaPropertyType(t *testing.T) {
	cases := map[string]string{
		"Hotel":              "hotel",
		"":                   "hotel",
		"Hostel":             "hostel",
		"Apartment":          "apartment",
		"Serviced Apartment": "apartment",
		"Guest House":        "guest house",
	}
	for in, want := range cases {
		if got := agodaPropertyType(in); got != want {
			t.Errorf("agodaPropertyType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAgodaAbsoluteURL(t *testing.T) {
	orig := agodaBaseURL
	agodaBaseURL = "https://www.agoda.com"
	defer func() { agodaBaseURL = orig }()
	cases := map[string]string{
		"":                          "",
		"//pix8.agoda.net/a.jpg":    "https://pix8.agoda.net/a.jpg",
		"/img/x.jpg":                "https://www.agoda.com/img/x.jpg",
		"https://example.com/y.jpg": "https://example.com/y.jpg",
	}
	for in, want := range cases {
		if got := agodaAbsoluteURL(in); got != want {
			t.Errorf("agodaAbsoluteURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAgodaBookingURL(t *testing.T) {
	orig := agodaBaseURL
	agodaBaseURL = "https://www.agoda.com"
	defer func() { agodaBaseURL = orig }()
	cases := map[string]string{
		"":                             "",
		"/hotel/x.html":                "https://www.agoda.com/hotel/x.html",
		"hotel/x.html":                 "https://www.agoda.com/hotel/x.html",
		"https://www.agoda.com/y.html": "https://www.agoda.com/y.html",
	}
	for in, want := range cases {
		if got := agodaBookingURL(in); got != want {
			t.Errorf("agodaBookingURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAgodaNights(t *testing.T) {
	cases := []struct {
		in, out string
		want    int
	}{
		{"2026-07-15", "2026-07-17", 2},
		{"2026-07-15", "2026-07-16", 1},
		{"2026-07-15", "2026-07-15", 1}, // floor 1
		{"bad", "dates", 1},
		{"2026-07-15", "2026-07-25", 10},
	}
	for _, c := range cases {
		if got := agodaNights(c.in, c.out); got != c.want {
			t.Errorf("agodaNights(%q,%q) = %d, want %d", c.in, c.out, got, c.want)
		}
	}
}

// TestBuildAgodaSearchBody asserts the embedded template is patched with the
// live cityId, dates, occupancy and currency.
func TestBuildAgodaSearchBody(t *testing.T) {
	body, err := buildAgodaSearchBody(9999, HotelSearchOptions{
		CheckIn:      "2026-09-01",
		CheckOut:     "2026-09-04",
		Guests:       3,
		Rooms:        2,
		ChildrenAges: []int{5, 8},
		Currency:     "gbp",
	})
	if err != nil {
		t.Fatalf("build body: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("re-decode body: %v", err)
	}
	if doc["operationName"] != "citySearch" {
		t.Errorf("operationName = %v, want citySearch", doc["operationName"])
	}
	vars := doc["variables"].(map[string]any)
	csr := vars["CitySearchRequest"].(map[string]any)
	if int(csr["cityId"].(float64)) != 9999 {
		t.Errorf("cityId = %v, want 9999", csr["cityId"])
	}
	sc := csr["searchRequest"].(map[string]any)["searchCriteria"].(map[string]any)
	if sc["localCheckInDate"] != "2026-09-01" {
		t.Errorf("localCheckInDate = %v, want 2026-09-01", sc["localCheckInDate"])
	}
	if int(sc["los"].(float64)) != 3 {
		t.Errorf("los = %v, want 3", sc["los"])
	}
	if int(sc["adults"].(float64)) != 3 {
		t.Errorf("adults = %v, want 3", sc["adults"])
	}
	if int(sc["rooms"].(float64)) != 2 {
		t.Errorf("rooms = %v, want 2", sc["rooms"])
	}
	if int(sc["children"].(float64)) != 2 {
		t.Errorf("children = %v, want 2", sc["children"])
	}
	if sc["currency"] != "GBP" {
		t.Errorf("currency = %v, want GBP", sc["currency"])
	}
}

// TestSearchAgodaEndToEnd wires the autocomplete + GraphQL search against a mock
// server, exercising the full SearchAgoda pipeline deterministically.
func TestSearchAgodaEndToEnd(t *testing.T) {
	searchJSON, err := os.ReadFile("testdata/agoda_search.json")
	if err != nil {
		t.Fatalf("read search fixture: %v", err)
	}
	suggestJSON, err := os.ReadFile("testdata/agoda_suggest.json")
	if err != nil {
		t.Fatalf("read suggest fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/cronos/search/GetUnifiedSuggestResult"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(suggestJSON)
		case r.URL.Path == "/graphql/search":
			if r.Header.Get("x-gate-meta") == "" {
				http.Error(w, "missing gate meta", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(searchJSON)
		default:
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	origURL, origEnabled := agodaBaseURL, agodaEnabled
	agodaBaseURL = srv.URL
	agodaEnabled = true
	t.Setenv("AGODA_CITY_ID", "") // force autocomplete resolve through the mock
	defer func() { agodaBaseURL, agodaEnabled = origURL, origEnabled }()

	hotels, err := SearchAgoda(context.Background(), "Berlin", HotelSearchOptions{Currency: "EUR"})
	if err != nil {
		t.Fatalf("SearchAgoda: %v", err)
	}
	if len(hotels) != 3 {
		t.Fatalf("want 3 hotels, got %d", len(hotels))
	}
	if hotels[0].Name != "The Hoxton, Berlin" {
		t.Errorf("first hotel = %q", hotels[0].Name)
	}
}

func TestResolveAgodaCityIDEnvOverride(t *testing.T) {
	t.Setenv("AGODA_CITY_ID", "12345")
	id, err := resolveAgodaCityID(context.Background(), "anywhere")
	if err != nil {
		t.Fatalf("resolve (env): %v", err)
	}
	if id != 12345 {
		t.Errorf("env override id = %d, want 12345", id)
	}
}

// TestSearchAgodaLive hits the real Agoda endpoints. Opt-in only.
func TestSearchAgodaLive(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") != "1" {
		t.Skip("set TRVL_TEST_LIVE_INTEGRATIONS=1 to run live Agoda integration test")
	}
	origEnabled := agodaEnabled
	agodaEnabled = true
	defer func() { agodaEnabled = origEnabled }()

	hotels, err := SearchAgoda(context.Background(), "Berlin", HotelSearchOptions{
		CheckIn:  "2026-09-01",
		CheckOut: "2026-09-03",
		Currency: "EUR",
		Guests:   2,
		Rooms:    1,
	})
	if err != nil {
		t.Fatalf("live SearchAgoda: %v", err)
	}
	if len(hotels) == 0 {
		t.Fatal("live search returned zero hotels")
	}
	t.Logf("live Agoda returned %d hotels; first: %s @ %.2f %s",
		len(hotels), hotels[0].Name, hotels[0].Price, hotels[0].Currency)
}
