package hotels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestParseDateArray validates date string to [year, month, day] conversion.
func TestParseDateArray(t *testing.T) {
	tests := []struct {
		input   string
		want    [3]int
		wantErr bool
	}{
		{"2026-06-15", [3]int{2026, 6, 15}, false},
		{"2026-01-01", [3]int{2026, 1, 1}, false},
		{"2026-12-31", [3]int{2026, 12, 31}, false},
		{"bad-date", [3]int{}, true},
		{"", [3]int{}, true},
		{"2026/06/15", [3]int{}, true},
	}

	for _, tt := range tests {
		got, err := parseDateArray(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseDateArray(%q) expected error, got %v", tt.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDateArray(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseDateArray(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// TestParseHotelSearchResponse tests parsing of mock hotel search data.
func TestParseHotelSearchResponse(t *testing.T) {
	// Simulated batchexecute response with organic hotel entries.
	// Structure: data[0][0][0][1][N][1]{"397419284"}[0] = hotel entry
	hotel1 := make([]any, 27)
	hotel1[0] = nil
	hotel1[1] = "Hotel Kamp"
	hotel1[2] = []any{[]any{60.168, 24.941}}
	hotel1[3] = []any{"5-star hotel", 5.0}
	hotel1[6] = []any{nil, []any{[]any{189.0, 0.0}, nil, nil, "USD"}}
	hotel1[7] = []any{[]any{4.6, 1523.0}}
	hotel1[9] = "/g/11b6d4_v_4"

	hotel2 := make([]any, 27)
	hotel2[0] = nil
	hotel2[1] = "Scandic Grand Central Helsinki"
	hotel2[2] = []any{[]any{60.170, 24.943}}
	hotel2[3] = []any{"4-star hotel", 4.0}
	hotel2[6] = []any{nil, []any{[]any{129.0, 0.0}, nil, nil, "USD"}}
	hotel2[7] = []any{[]any{4.3, 892.0}}
	hotel2[9] = "/g/11c6rk8_qb"

	hotelData := []any{
		[]any{hotel1},
		[]any{hotel2},
	}

	inner, _ := json.Marshal(hotelData)
	entries := []any{
		[]any{
			[]any{"wrb.fr", "AtySUc", string(inner), nil, nil, nil, "generic"},
		},
	}

	hotels, err := ParseHotelSearchResponse(entries, "USD")
	if err != nil {
		t.Fatalf("ParseHotelSearchResponse error: %v", err)
	}

	if len(hotels) < 2 {
		t.Fatalf("expected at least 2 hotels, got %d", len(hotels))
	}

	// Verify first hotel fields.
	h := hotels[0]
	if h.Name != "Hotel Kamp" {
		t.Errorf("hotel[0].Name = %q, want %q", h.Name, "Hotel Kamp")
	}
	if h.HotelID != "/g/11b6d4_v_4" {
		t.Errorf("hotel[0].HotelID = %q, want %q", h.HotelID, "/g/11b6d4_v_4")
	}
	if h.Rating != 9.2 {
		t.Errorf("hotel[0].Rating = %v, want 9.2 (4.6 * 2, normalized to 0-10)", h.Rating)
	}
}

// TestParseHotelPriceResponse tests parsing of mock provider price data.
func TestParseHotelPriceResponse(t *testing.T) {
	priceData := []any{
		[]any{
			"Booking.com",
			189.0,
			"USD",
			"https://www.booking.com/...",
		},
		[]any{
			"Hotels.com",
			195.0,
			"USD",
			"https://www.hotels.com/...",
		},
		[]any{
			"Expedia",
			192.0,
			"USD",
			"https://www.expedia.com/...",
		},
	}

	inner, _ := json.Marshal(priceData)
	entries := []any{
		[]any{
			[]any{"wrb.fr", "yY52ce", string(inner), nil, nil, nil, "generic"},
		},
	}

	prices, err := ParseHotelPriceResponse(entries)
	if err != nil {
		t.Fatalf("ParseHotelPriceResponse error: %v", err)
	}

	if len(prices) < 3 {
		t.Fatalf("expected at least 3 providers, got %d", len(prices))
	}

	if prices[0].Provider != "Booking.com" {
		t.Errorf("prices[0].Provider = %q, want %q", prices[0].Provider, "Booking.com")
	}
	if prices[0].Price != 189.0 {
		t.Errorf("prices[0].Price = %v, want 189.0", prices[0].Price)
	}
	if prices[0].Currency != "USD" {
		t.Errorf("prices[0].Currency = %q, want %q", prices[0].Currency, "USD")
	}
}

// TestSortHotels verifies sorting by price and rating.
func TestSortHotels(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Expensive", Price: 300},
		{Name: "Cheap", Price: 100},
		{Name: "Mid", Price: 200},
		{Name: "No Price", Price: 0},
	}

	sortHotels(hotels, "cheapest", 0, 0)
	if hotels[0].Name != "Cheap" {
		t.Errorf("cheapest sort: first hotel = %q, want %q", hotels[0].Name, "Cheap")
	}
	if hotels[1].Name != "Mid" {
		t.Errorf("cheapest sort: second hotel = %q, want %q", hotels[1].Name, "Mid")
	}
	// Hotels with price=0 should be at the end.
	if hotels[3].Name != "No Price" {
		t.Errorf("cheapest sort: last hotel = %q, want %q", hotels[3].Name, "No Price")
	}

	// Rating sort.
	hotels2 := []models.HotelResult{
		{Name: "Low", Rating: 3.5},
		{Name: "High", Rating: 4.8},
		{Name: "Mid", Rating: 4.2},
	}
	sortHotels(hotels2, "rating", 0, 0)
	if hotels2[0].Name != "High" {
		t.Errorf("rating sort: first hotel = %q, want %q", hotels2[0].Name, "High")
	}
}

// TestFilterByStars verifies star rating filtering.
func TestFilterByStars(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Two Star", Stars: 2},
		{Name: "Four Star", Stars: 4},
		{Name: "Five Star", Stars: 5},
		{Name: "Three Star", Stars: 3},
	}

	filtered := filterByStars(hotels, 4)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 hotels with >= 4 stars, got %d", len(filtered))
	}
	for _, h := range filtered {
		if h.Stars < 4 {
			t.Errorf("hotel %q has %d stars, expected >= 4", h.Name, h.Stars)
		}
	}
}

// TestGeocodeCache verifies that the geocode cache works.
func TestGeocodeCache(t *testing.T) {
	// Manually prime the cache.
	geoCache.Lock()
	geoCache.entries["TestCity"] = geoEntry{lat: 60.17, lon: 24.94}
	geoCache.Unlock()

	lat, lon, err := ResolveLocation(context.Background(), "TestCity")
	if err != nil {
		t.Fatalf("ResolveLocation from cache error: %v", err)
	}
	if lat != 60.17 || lon != 24.94 {
		t.Errorf("got (%v, %v), want (60.17, 24.94)", lat, lon)
	}

	// Clean up.
	geoCache.Lock()
	delete(geoCache.entries, "TestCity")
	geoCache.Unlock()
}

// TestNominatimLookup tests the Nominatim API integration with a mock server.
func TestNominatimLookup(t *testing.T) {
	// Create a mock Nominatim server.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "missing q parameter", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"lat":"60.1695","lon":"24.9354","display_name":"Helsinki, Finland"}]`))
	}))
	defer ts.Close()

	// We can't easily mock the global nominatimURL, so we test the parsing
	// via ParseHotelSearchResponse and ResolveLocation cache path instead.
	// The mock server test is here for documentation of the expected API format.
	t.Logf("Mock Nominatim server running at %s", ts.URL)
}

// TestExtractBatchPayload tests the batchexecute response extraction.
func TestExtractBatchPayload(t *testing.T) {
	inner := `[["Hotel A","/g/123",4.5]]`
	entries := []any{
		[]any{
			[]any{"wrb.fr", "AtySUc", inner, nil, nil, nil, "generic"},
		},
	}

	payload, err := extractBatchPayload(entries, "AtySUc")
	if err != nil {
		t.Fatalf("extractBatchPayload error: %v", err)
	}

	arr, ok := payload.([]any)
	if !ok {
		t.Fatalf("payload not array, got %T", payload)
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(arr))
	}
}

// TestExtractBatchPayload_NotFound tests error on missing rpcid.
func TestExtractBatchPayload_NotFound(t *testing.T) {
	entries := []any{
		[]any{
			[]any{"wrb.fr", "OtherRPC", `[]`, nil},
		},
	}

	_, err := extractBatchPayload(entries, "AtySUc")
	if err == nil {
		t.Error("expected error for missing rpcid, got nil")
	}
}

// TestFindHotelEntries verifies the hotel entry detection in nested structures.
func TestFindHotelEntries(t *testing.T) {
	// A valid organic hotel entry has: [0]=nil, [1]=name, [2]=[[lat,lon],...], ...
	validHotel := make([]any, 12)
	validHotel[0] = nil
	validHotel[1] = "Hotel Kamp"
	validHotel[2] = []any{[]any{60.168, 24.941}}

	found := findHotelEntries(validHotel, 0)
	if len(found) != 1 {
		t.Errorf("expected 1 hotel entry, got %d", len(found))
	}

	// Invalid: too short
	short := []any{nil, "Hotel"}
	found = findHotelEntries(short, 0)
	if len(found) != 0 {
		t.Errorf("expected 0 entries for short array, got %d", len(found))
	}

	// Invalid: no name
	noName := make([]any, 12)
	noName[0] = nil
	noName[1] = 42.0
	found = findHotelEntries(noName, 0)
	if len(found) != 0 {
		t.Errorf("expected 0 entries for no-name array, got %d", len(found))
	}
}

// TestSearchHotels_ValidationErrors tests input validation.
func TestSearchHotels_ValidationErrors(t *testing.T) {
	ctx := context.Background()

	// Missing dates.
	_, err := SearchHotels(ctx, "Helsinki", HotelSearchOptions{})
	if err == nil {
		t.Error("expected error for missing dates, got nil")
	}

	// Bad date format.
	_, err = SearchHotels(ctx, "Helsinki", HotelSearchOptions{
		CheckIn:  "bad",
		CheckOut: "2026-06-18",
	})
	if err == nil {
		t.Error("expected error for bad date, got nil")
	}
}

// TestGetHotelPrices_ValidationErrors tests price lookup validation.
func TestGetHotelPrices_ValidationErrors(t *testing.T) {
	ctx := context.Background()

	// Missing hotel ID.
	_, err := GetHotelPrices(ctx, "", "2026-06-15", "2026-06-18", "USD")
	if err == nil {
		t.Error("expected error for empty hotel ID, got nil")
	}

	// Missing dates.
	_, err = GetHotelPrices(ctx, "/g/123", "", "2026-06-18", "USD")
	if err == nil {
		t.Error("expected error for missing check-in, got nil")
	}

	// Bad date.
	_, err = GetHotelPrices(ctx, "/g/123", "not-a-date", "2026-06-18", "USD")
	if err == nil {
		t.Error("expected error for bad date, got nil")
	}
}

// TestParseOrganicHotel verifies single organic hotel entry parsing.
func TestParseOrganicHotel(t *testing.T) {
	// Organic hotel entry format (27 elements):
	// [0]=nil, [1]=name, [2]=[[lat,lon],...], [3]=["X-star",X], ...
	// [6]=price_block, [7]=[[rating, review_count]], [9]=place_id
	entry := make([]any, 27)
	entry[0] = nil
	entry[1] = "Grand Hotel"
	entry[2] = []any{
		[]any{51.5074, -0.1278}, // [lat, lon]
	}
	entry[3] = []any{"3-star hotel", 3.0}
	entry[6] = []any{nil, []any{[]any{150.0, 0.0}, nil, nil, "EUR"}}
	entry[7] = []any{[]any{4.2, 500.0}}
	entry[9] = "0x123:0x456"

	h := parseOrganicHotel(entry, "EUR")

	if h.Name != "Grand Hotel" {
		t.Errorf("Name = %q, want %q", h.Name, "Grand Hotel")
	}
	if h.Rating != 8.4 {
		t.Errorf("Rating = %v, want 8.4 (4.2 * 2, normalized to 0-10)", h.Rating)
	}
	if h.Stars != 3 {
		t.Errorf("Stars = %d, want 3", h.Stars)
	}
	if h.Lat == 0 || h.Lon == 0 {
		t.Error("expected non-zero coordinates")
	}
	if h.Price != 150.0 {
		t.Errorf("Price = %v, want 150.0", h.Price)
	}
	if h.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", h.Currency)
	}
	if h.HotelID != "0x123:0x456" {
		t.Errorf("HotelID = %q, want %q", h.HotelID, "0x123:0x456")
	}
}

// TestParseOneProvider verifies single provider entry parsing.
func TestParseOneProvider(t *testing.T) {
	entry := []any{"Booking.com", 189.0, "USD", "https://booking.com/..."}

	p := parseOneProvider(entry)

	if p.Provider != "Booking.com" {
		t.Errorf("Provider = %q, want %q", p.Provider, "Booking.com")
	}
	if p.Price != 189.0 {
		t.Errorf("Price = %v, want 189.0", p.Price)
	}
	if p.Currency != "USD" {
		t.Errorf("Currency = %q, want %q", p.Currency, "USD")
	}
}

// TestNormalizeHotelCity verifies that English city names are mapped to the
// local-language names that Google Hotels uses for geocoding.
func TestNormalizeHotelCity(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Cities that need mapping (English → local)
		{"Prague", "Praha"},
		{"prague", "Praha"},     // case-insensitive
		{"  Prague  ", "Praha"}, // trimmed
		{"Munich", "München"},
		{"Vienna", "Wien"},
		{"Cologne", "Köln"},
		{"Copenhagen", "København"},
		{"Warsaw", "Warszawa"},
		{"Bucharest", "București"},
		{"Gothenburg", "Göteborg"},
		{"Nuremberg", "Nürnberg"},

		// Cities that should pass through unchanged (Google handles them fine)
		{"Paris", "Paris"},
		{"London", "London"},
		{"Rome", "Rome"},
		{"Helsinki", "Helsinki"},
		{"Barcelona", "Barcelona"},
		{"New York", "New York"},
		{"", ""},
	}

	for _, tt := range tests {
		got := normalizeHotelCity(tt.input)
		if got != tt.want {
			t.Errorf("normalizeHotelCity(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestHotelCityAliasesComplete verifies all aliases in the map have non-empty values.
func TestHotelCityAliasesComplete(t *testing.T) {
	for eng, local := range hotelCityAliases {
		if eng == "" {
			t.Error("hotelCityAliases has empty key")
		}
		if local == "" {
			t.Errorf("hotelCityAliases[%q] has empty value", eng)
		}
		// Key should be lowercase (map lookup does ToLower)
		if eng != strings.ToLower(eng) {
			t.Errorf("hotelCityAliases key %q should be lowercase", eng)
		}
	}
}

// TestNormalizeHotelCityIdempotent verifies that normalizing an already-local name
// doesn't break it (i.e., "Praha" stays "Praha", not double-mapped).
func TestNormalizeHotelCityIdempotent(t *testing.T) {
	for _, local := range hotelCityAliases {
		got := normalizeHotelCity(local)
		if got != local {
			t.Errorf("normalizeHotelCity(%q) = %q, expected passthrough for already-local name", local, got)
		}
	}
}
