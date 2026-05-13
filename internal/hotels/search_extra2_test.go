package hotels

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestSearchHotels_MissingDates(t *testing.T) {
	_, err := SearchHotels(context.Background(), "Helsinki", HotelSearchOptions{})
	if err == nil {
		t.Error("expected error for missing dates")
	}
}

func TestSearchHotels_BadCheckInDate(t *testing.T) {
	_, err := SearchHotels(context.Background(), "Helsinki", HotelSearchOptions{
		CheckIn:  "not-a-date",
		CheckOut: "2026-06-18",
	})
	if err == nil {
		t.Error("expected error for bad check-in date")
	}
}

func TestSearchHotels_BadCheckOutDate(t *testing.T) {
	_, err := SearchHotels(context.Background(), "Helsinki", HotelSearchOptions{
		CheckIn:  "2026-06-15",
		CheckOut: "invalid",
	})
	if err == nil {
		t.Error("expected error for bad check-out date")
	}
}

// --- GetHotelPrices validation ---

func TestGetHotelPrices_EmptyHotelID(t *testing.T) {
	_, err := GetHotelPrices(context.Background(), "", "2026-06-15", "2026-06-18", "USD")
	if err == nil {
		t.Error("expected error for empty hotel ID")
	}
}

func TestGetHotelPrices_EmptyDates(t *testing.T) {
	_, err := GetHotelPrices(context.Background(), "/g/123", "", "2026-06-18", "USD")
	if err == nil {
		t.Error("expected error for empty check-in")
	}

	_, err = GetHotelPrices(context.Background(), "/g/123", "2026-06-15", "", "USD")
	if err == nil {
		t.Error("expected error for empty check-out")
	}
}

func TestGetHotelPrices_BadDate(t *testing.T) {
	_, err := GetHotelPrices(context.Background(), "/g/123", "bad", "2026-06-18", "USD")
	if err == nil {
		t.Error("expected error for bad check-in date")
	}

	_, err = GetHotelPrices(context.Background(), "/g/123", "2026-06-15", "bad", "USD")
	if err == nil {
		t.Error("expected error for bad check-out date")
	}
}

func TestGetHotelPrices_DefaultCurrency(t *testing.T) {
	// Can't easily test the full flow without a real server,
	// but verify it doesn't panic with empty currency.
	// The function will fail at the HTTP request level, which is fine.
	_, err := GetHotelPrices(context.Background(), "/g/123", "2026-06-15", "2026-06-18", "")
	// Will fail because it tries to hit google.com — that's expected.
	if err == nil {
		t.Log("Unexpectedly succeeded (maybe network is available)")
	}
}

// --- parseHotelsFromPage ---

func TestParseHotelsFromPage_NoCallbacks(t *testing.T) {
	_, err := parseHotelsFromPage("<html><body>empty</body></html>", "USD")
	if err == nil {
		t.Error("expected error for page with no callbacks")
	}
}

func TestParseHotelsFromPage_CallbackNoHotels(t *testing.T) {
	page := `AF_initDataCallback({key: 'ds:0', data:[1,2,3]});`
	_, err := parseHotelsFromPage(page, "USD")
	if err == nil {
		t.Error("expected error for page with no hotel data")
	}
}

// --- ParseHotelSearchResponse ---

func TestParseHotelSearchResponse_EmptyEntries(t *testing.T) {
	_, err := ParseHotelSearchResponse([]any{}, "USD")
	if err == nil {
		t.Error("expected error for empty entries")
	}
}

func TestParseHotelSearchResponse_InvalidJSON(t *testing.T) {
	entries := []any{
		[]any{
			[]any{"wrb.fr", "AtySUc", "not valid json", nil},
		},
	}

	_, err := ParseHotelSearchResponse(entries, "USD")
	if err == nil {
		t.Error("expected error for invalid JSON in payload")
	}
}

// --- ParseHotelPriceResponse ---

func TestParseHotelPriceResponse_EmptyEntries(t *testing.T) {
	_, err := ParseHotelPriceResponse([]any{})
	if err == nil {
		t.Error("expected error for empty entries")
	}
}

func TestParseHotelPriceResponse_NoPrices(t *testing.T) {
	// Valid batch response but no price-like entries.
	inner := `[null, "no prices here"]`
	entries := []any{
		[]any{
			[]any{"wrb.fr", "yY52ce", inner, nil},
		},
	}

	_, err := ParseHotelPriceResponse(entries)
	if err == nil {
		t.Error("expected error for response with no prices")
	}
}

// --- extractBatchPayload edge cases ---

func TestExtractBatchPayload_DirectEntries(t *testing.T) {
	// Entries where the batch array is directly at the entry level.
	entries := []any{
		[]any{"wrb.fr", "TestRPC", `[1,2,3]`, nil},
	}

	payload, err := extractBatchPayload(entries, "TestRPC")
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	arr, ok := payload.([]any)
	if !ok {
		t.Fatalf("payload not array, got %T", payload)
	}
	if len(arr) != 3 {
		t.Errorf("expected 3 elements, got %d", len(arr))
	}
}

// --- findHotelEntries edge cases ---

func TestFindHotelEntries_DeepNesting(t *testing.T) {
	hotel := make([]any, 12)
	hotel[0] = nil
	hotel[1] = "Deep Hotel"
	hotel[2] = []any{[]any{60.168, 24.941}}

	// Wrap hotel in many layers.
	var data any = hotel
	for range 5 {
		data = []any{data}
	}

	found := findHotelEntries(data, 0)
	if len(found) != 1 {
		t.Errorf("expected 1 hotel in deep nesting, got %d", len(found))
	}
}

func TestFindHotelEntries_MaxDepth(t *testing.T) {
	// Create nesting deeper than max depth (10).
	hotel := make([]any, 12)
	hotel[0] = nil
	hotel[1] = "Too Deep Hotel"
	hotel[2] = []any{[]any{60.168, 24.941}}

	var data any = hotel
	for range 12 {
		data = []any{data}
	}

	found := findHotelEntries(data, 0)
	if len(found) != 0 {
		t.Errorf("expected 0 hotels beyond max depth, got %d", len(found))
	}
}

func TestFindHotelEntries_MapValue(t *testing.T) {
	hotel := make([]any, 12)
	hotel[0] = nil
	hotel[1] = "Map Hotel"
	hotel[2] = []any{[]any{60.168, 24.941}}

	data := map[string]any{
		"hotels": []any{hotel},
	}

	found := findHotelEntries(data, 0)
	if len(found) != 1 {
		t.Errorf("expected 1 hotel in map, got %d", len(found))
	}
}

// --- parseDateArray ---

func TestParseDateArray_EdgeCases(t *testing.T) {
	// Valid edge dates.
	got, err := parseDateArray("2000-01-01")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got != [3]int{2000, 1, 1} {
		t.Errorf("got %v", got)
	}

	got, err = parseDateArray("2099-12-31")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got != [3]int{2099, 12, 31} {
		t.Errorf("got %v", got)
	}
}

func TestParseDateArray_InvalidDate(t *testing.T) {
	_, err := parseDateArray("not-a-date")
	if err == nil {
		t.Error("expected error for invalid date")
	}
}

// --- buildHotelBookingURL ---

func TestBuildHotelBookingURL_Basic(t *testing.T) {
	url := buildHotelBookingURL("Helsinki", "2026-06-15", "2026-06-18")
	if url == "" {
		t.Fatal("expected non-empty URL")
	}
	if !strings.Contains(url, "google.com/travel/hotels") {
		t.Errorf("URL missing google.com/travel/hotels: %s", url)
	}
	if !strings.Contains(url, "Helsinki") {
		t.Errorf("URL missing location Helsinki: %s", url)
	}
	if !strings.Contains(url, "2026-06-15") {
		t.Errorf("URL missing check-in date: %s", url)
	}
	if !strings.Contains(url, "2026-06-18") {
		t.Errorf("URL missing check-out date: %s", url)
	}
}

func TestBuildHotelBookingURL_Format(t *testing.T) {
	url := buildHotelBookingURL("Tokyo", "2026-07-01", "2026-07-05")
	if !strings.Contains(url, "dates=2026-07-01,2026-07-05") {
		t.Errorf("URL date format incorrect: %s", url)
	}
}

func TestBuildHotelBookingURL_SpecialChars(t *testing.T) {
	url := buildHotelBookingURL("New York City", "2026-12-25", "2026-12-28")
	// URL should contain escaped location.
	if !strings.Contains(url, "New") {
		t.Errorf("URL missing location parts: %s", url)
	}
	// Path-escaped and query-escaped should both be present.
	if !strings.Contains(url, "hotels/") {
		t.Errorf("URL missing hotels path: %s", url)
	}
}

func TestBuildHotelBookingURL_DifferentLocations(t *testing.T) {
	tests := []struct {
		location, checkIn, checkOut string
	}{
		{"Barcelona", "2026-08-01", "2026-08-05"},
		{"London", "2026-09-10", "2026-09-15"},
		{"Singapore", "2027-01-01", "2027-01-03"},
	}
	for _, tt := range tests {
		url := buildHotelBookingURL(tt.location, tt.checkIn, tt.checkOut)
		if !strings.Contains(url, tt.location) {
			t.Errorf("URL for %s missing location: %s", tt.location, url)
		}
		if !strings.Contains(url, tt.checkIn) {
			t.Errorf("URL for %s missing check-in: %s", tt.location, url)
		}
		if !strings.Contains(url, tt.checkOut) {
			t.Errorf("URL for %s missing check-out: %s", tt.location, url)
		}
	}
}

// --- SearchHotels defaults ---

func TestSearchHotels_DefaultGuests(t *testing.T) {
	// Verify defaults by calling SearchHotels with 0 guests.
	// It will fail at the HTTP layer, but we can confirm defaults don't panic.
	_, err := SearchHotels(context.Background(), "Helsinki", HotelSearchOptions{
		CheckIn:  "2026-06-15",
		CheckOut: "2026-06-18",
		Guests:   0, // should default to 2
	})
	// Will fail because it tries to contact google.com — expected.
	if err == nil {
		t.Log("Unexpectedly succeeded")
	}
}

func TestSearchHotels_DefaultCurrency(t *testing.T) {
	_, err := SearchHotels(context.Background(), "Helsinki", HotelSearchOptions{
		CheckIn:  "2026-06-15",
		CheckOut: "2026-06-18",
		Currency: "", // should default to "USD"
	})
	if err == nil {
		t.Log("Unexpectedly succeeded")
	}
}

// --- Haversine ---

func TestHaversine_SamePoint(t *testing.T) {
	d := Haversine(60.17, 24.94, 60.17, 24.94)
	if d != 0 {
		t.Errorf("same point distance = %v, want 0", d)
	}
}

func TestHaversine_HelsinkiToTallinn(t *testing.T) {
	// Helsinki (60.17, 24.94) to Tallinn (59.44, 24.75) ~80 km
	d := Haversine(60.17, 24.94, 59.44, 24.75)
	if d < 70 || d > 90 {
		t.Errorf("Helsinki-Tallinn distance = %.1f km, expected ~80 km", d)
	}
}

func TestHaversine_HelsinkiToTokyo(t *testing.T) {
	// Helsinki (60.17, 24.94) to Tokyo (35.68, 139.69) ~7800 km
	d := Haversine(60.17, 24.94, 35.68, 139.69)
	if d < 7500 || d > 8200 {
		t.Errorf("Helsinki-Tokyo distance = %.0f km, expected ~7800 km", d)
	}
}

func TestHaversine_Antipodal(t *testing.T) {
	// North pole to south pole ~20000 km
	d := Haversine(90, 0, -90, 0)
	if d < 19900 || d > 20100 {
		t.Errorf("pole-to-pole distance = %.0f km, expected ~20015 km", d)
	}
}

// --- filterHotels ---

func TestFilterHotels_NoFilters(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "A", Price: 100, Rating: 3.5, Stars: 3},
		{Name: "B", Price: 200, Rating: 4.5, Stars: 4},
	}
	result := filterHotels(hotels, HotelSearchOptions{})
	if len(result) != 2 {
		t.Errorf("expected 2, got %d", len(result))
	}
}

func TestFilterHotels_MinPrice(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Cheap", Price: 50},
		{Name: "Mid", Price: 150},
		{Name: "Pricey", Price: 300},
		{Name: "No Price", Price: 0}, // price=0 should NOT be filtered out
	}
	result := filterHotels(hotels, HotelSearchOptions{MinPrice: 100})
	if len(result) != 3 {
		t.Errorf("expected 3 (Mid + Pricey + No Price), got %d", len(result))
	}
	for _, h := range result {
		if h.Name == "Cheap" {
			t.Error("Cheap should be filtered out by MinPrice=100")
		}
	}
}

func TestFilterHotels_MaxPrice(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Cheap", Price: 50},
		{Name: "Mid", Price: 150},
		{Name: "Pricey", Price: 300},
		{Name: "No Price", Price: 0},
	}
	result := filterHotels(hotels, HotelSearchOptions{MaxPrice: 200})
	if len(result) != 3 {
		t.Errorf("expected 3 (Cheap + Mid + No Price), got %d", len(result))
	}
	for _, h := range result {
		if h.Name == "Pricey" {
			t.Error("Pricey should be filtered out by MaxPrice=200")
		}
	}
}

func TestFilterHotels_MinRating(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Low", Rating: 6.0},
		{Name: "Mid", Rating: 8.0},
		{Name: "High", Rating: 9.6},
		{Name: "No Rating", Rating: 0}, // SHOULD be filtered out — unrated
	}
	result := filterHotels(hotels, HotelSearchOptions{MinRating: 8.0})
	// Unrated properties are now excluded when MinRating is set. They are
	// typically private rooms, new listings, or apartment units without
	// enough guest reviews to establish quality — exactly what a serious
	// traveler does NOT want when asking for "at least 8/10".
	if len(result) != 2 {
		t.Errorf("expected 2 (Mid + High), got %d", len(result))
	}
	for _, h := range result {
		if h.Name == "Low" {
			t.Error("Low should be filtered out by MinRating=8.0")
		}
		if h.Name == "No Rating" {
			t.Error("No Rating should be filtered out by MinRating=8.0")
		}
	}
}

func TestFilterHotels_Stars(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Two", Stars: 2},
		{Name: "Four", Stars: 4},
		{Name: "Five", Stars: 5},
	}
	result := filterHotels(hotels, HotelSearchOptions{Stars: 4})
	if len(result) != 2 {
		t.Errorf("expected 2, got %d", len(result))
	}
}

func TestFilterHotels_MaxDistance(t *testing.T) {
	// Helsinki center: 60.17, 24.94
	hotels := []models.HotelResult{
		{Name: "Close", Lat: 60.17, Lon: 24.94},  // ~0 km
		{Name: "Medium", Lat: 60.20, Lon: 24.94}, // ~3.3 km
		{Name: "Far", Lat: 60.50, Lon: 24.94},    // ~36.7 km
		{Name: "No Coords", Lat: 0, Lon: 0},      // no coords, should NOT be filtered
	}
	result := filterHotels(hotels, HotelSearchOptions{
		MaxDistanceKm: 5,
		CenterLat:     60.17,
		CenterLon:     24.94,
	})
	if len(result) != 3 {
		t.Errorf("expected 3 (Close + Medium + No Coords), got %d", len(result))
	}
	for _, h := range result {
		if h.Name == "Far" {
			t.Error("Far should be filtered out by MaxDistanceKm=5")
		}
	}
}

func TestFilterHotels_MaxDistanceNoCenterCoords(t *testing.T) {
	// If center coords are 0, distance filter should not remove anything.
	hotels := []models.HotelResult{
		{Name: "A", Lat: 60.17, Lon: 24.94},
		{Name: "B", Lat: 35.68, Lon: 139.69},
	}
	result := filterHotels(hotels, HotelSearchOptions{
		MaxDistanceKm: 1,
		CenterLat:     0,
		CenterLon:     0,
	})
	if len(result) != 2 {
		t.Errorf("expected 2 (no filtering without center), got %d", len(result))
	}
}

func TestFilterHotels_Combined(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Perfect", Price: 150, Rating: 4.5, Stars: 4, Lat: 60.17, Lon: 24.94},
		{Name: "Cheap Bad", Price: 50, Rating: 2.0, Stars: 2, Lat: 60.17, Lon: 24.94},
		{Name: "Expensive", Price: 500, Rating: 4.8, Stars: 5, Lat: 60.17, Lon: 24.94},
		{Name: "Far Good", Price: 120, Rating: 4.2, Stars: 4, Lat: 60.50, Lon: 24.94},
	}
	result := filterHotels(hotels, HotelSearchOptions{
		Stars:         3,
		MinPrice:      80,
		MaxPrice:      300,
		MinRating:     4.0,
		MaxDistanceKm: 5,
		CenterLat:     60.17,
		CenterLon:     24.94,
	})
	if len(result) != 1 {
		t.Errorf("expected 1 (Perfect only), got %d", len(result))
		for _, h := range result {
			t.Logf("  %s: price=%.0f rating=%.1f stars=%d", h.Name, h.Price, h.Rating, h.Stars)
		}
	}
	if len(result) > 0 && result[0].Name != "Perfect" {
		t.Errorf("expected Perfect, got %q", result[0].Name)
	}
}

// --- sortHotels stars and distance ---

func TestSortHotels_Stars(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Two", Stars: 2},
		{Name: "Five", Stars: 5},
		{Name: "Three", Stars: 3},
		{Name: "Four", Stars: 4},
	}
	sortHotels(hotels, "stars", 0, 0)
	if hotels[0].Name != "Five" {
		t.Errorf("first = %q, want Five", hotels[0].Name)
	}
	if hotels[1].Name != "Four" {
		t.Errorf("second = %q, want Four", hotels[1].Name)
	}
}

func TestSortHotels_Distance(t *testing.T) {
	// Helsinki center: 60.17, 24.94
	hotels := []models.HotelResult{
		{Name: "Far", Lat: 60.50, Lon: 24.94},
		{Name: "Close", Lat: 60.17, Lon: 24.94},
		{Name: "Medium", Lat: 60.20, Lon: 24.94},
	}
	sortHotels(hotels, "distance", 60.17, 24.94)
	if hotels[0].Name != "Close" {
		t.Errorf("first = %q, want Close", hotels[0].Name)
	}
	if hotels[1].Name != "Medium" {
		t.Errorf("second = %q, want Medium", hotels[1].Name)
	}
	if hotels[2].Name != "Far" {
		t.Errorf("third = %q, want Far", hotels[2].Name)
	}
}

func TestSortHotels_DistanceNoCenter(t *testing.T) {
	// With center=(0,0), distance sort should still not panic.
	hotels := []models.HotelResult{
		{Name: "A", Lat: 60.17, Lon: 24.94},
		{Name: "B", Lat: 35.68, Lon: 139.69},
	}
	sortHotels(hotels, "distance", 0, 0)
	// No crash is the success condition.
	if len(hotels) != 2 {
		t.Errorf("expected 2 hotels, got %d", len(hotels))
	}
}

// TestBuildTravelURL_ServerSideFilters verifies that server-side filter params
// are included in the URL when the corresponding options are set.
func TestBuildTravelURL_ServerSideFilters(t *testing.T) {
	opts := HotelSearchOptions{
		CheckIn:       "2026-07-01",
		CheckOut:      "2026-07-05",
		Guests:        2,
		Currency:      "EUR",
		MinPrice:      50,
		MaxPrice:      200,
		Stars:         4,
		MinRating:     8.0,
		MaxDistanceKm: 5,
	}

	u := buildTravelURL("Helsinki", opts)

	checks := map[string]string{
		"min_price": "50",
		"max_price": "200",
		"class":     "4",
		"rating":    "8",    // 8.0 on 0-10 scale, passed directly
		"lrad":      "5000", // 5 km * 1000
	}
	for param, want := range checks {
		if !strings.Contains(u, param+"="+want) {
			t.Errorf("URL missing %s=%s: %s", param, want, u)
		}
	}
}

// TestBuildTravelURL_NoFiltersWhenZero verifies that filter params are omitted
// when their values are zero (the default).
func TestBuildTravelURL_NoFiltersWhenZero(t *testing.T) {
	opts := HotelSearchOptions{
		CheckIn:  "2026-07-01",
		CheckOut: "2026-07-05",
		Guests:   2,
		Currency: "USD",
	}

	u := buildTravelURL("Paris", opts)

	absent := []string{"min_price", "max_price", "class", "rating", "lrad"}
	for _, param := range absent {
		if strings.Contains(u, param+"=") {
			t.Errorf("URL should not contain %s when zero: %s", param, u)
		}
	}
}

// TestBuildTravelURL_PartialFilters verifies that only the set filters appear.
func TestBuildTravelURL_PartialFilters(t *testing.T) {
	opts := HotelSearchOptions{
		CheckIn:  "2026-07-01",
		CheckOut: "2026-07-05",
		Guests:   2,
		Currency: "USD",
		MaxPrice: 300,
		Stars:    3,
	}

	u := buildTravelURL("London", opts)

	if !strings.Contains(u, "max_price=300") {
		t.Errorf("URL missing max_price=300: %s", u)
	}
	if !strings.Contains(u, "class=3") {
		t.Errorf("URL missing class=3: %s", u)
	}
	// These should be absent.
	if strings.Contains(u, "min_price=") {
		t.Errorf("URL should not contain min_price when zero: %s", u)
	}
	if strings.Contains(u, "rating=") {
		t.Errorf("URL should not contain rating when zero: %s", u)
	}
	if strings.Contains(u, "lrad=") {
		t.Errorf("URL should not contain lrad when zero: %s", u)
	}
}
