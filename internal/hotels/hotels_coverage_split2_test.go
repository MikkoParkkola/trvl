package hotels

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestExtractReviewsFromText_WithReviews(t *testing.T) {
	page := "<html>\n<script>\n" +
		`{"reviewRating":{"ratingValue":"4.5"},"reviewBody":"Great hotel with amazing views","author":{"name":"John Smith"},"datePublished":"2026-01-15"}` +
		"\n</script>\n</html>"
	reviews := extractReviewsFromText(page)
	if len(reviews) < 1 {
		t.Fatalf("expected at least 1 review, got %d", len(reviews))
	}
	if reviews[0].Rating != 4.5 {
		t.Errorf("rating = %v, want 4.5", reviews[0].Rating)
	}
}

func TestExtractReviewsFromText_NoReviews(t *testing.T) {
	if reviews := extractReviewsFromText("<html>no reviews here</html>"); len(reviews) != 0 {
		t.Errorf("expected 0, got %d", len(reviews))
	}
}

func TestExtractReviewsFromText_InvalidJSON(t *testing.T) {
	if reviews := extractReviewsFromText(`prefix "reviewRating" but {invalid json: here} end`); len(reviews) != 0 {
		t.Errorf("expected 0, got %d", len(reviews))
	}
}

func TestExtractReviewsFromText_NoBraceBeforeKeyword(t *testing.T) {
	page := strings.Repeat("x", 3000) + `"reviewRating":{"ratingValue":"3.0"}`
	_ = extractReviewsFromText(page) // must not panic
}

// ---------------------------------------------------------------------------
// extractReviewFromJSON branches
// ---------------------------------------------------------------------------

func TestExtractReviewFromJSON_StringAuthor(t *testing.T) {
	obj := map[string]any{
		"reviewBody": "Nice place", "author": "Direct Author",
		"reviewRating": map[string]any{"ratingValue": 4.0}, "datePublished": "2026-03-01",
	}
	r := extractReviewFromJSON(obj)
	if r.Author != "Direct Author" || r.Date != "2026-03-01" {
		t.Errorf("Author=%q Date=%q", r.Author, r.Date)
	}
}

func TestExtractReviewFromJSON_RatingAsString(t *testing.T) {
	obj := map[string]any{"reviewBody": "Okay", "reviewRating": map[string]any{"ratingValue": "2.5"}}
	if r := extractReviewFromJSON(obj); r.Rating != 2.5 {
		t.Errorf("Rating = %v", r.Rating)
	}
}

// ---------------------------------------------------------------------------
// findReviewEntries
// ---------------------------------------------------------------------------

func TestFindReviewEntries_MapTraversal(t *testing.T) {
	data := map[string]any{"reviews": []any{
		[]any{"Nice place to stay with beautiful scenery", 4.0, "John", "2 weeks ago"},
		[]any{"Excellent service and friendly staff here", 5.0, "Jane", "1 month ago"},
	}}
	if reviews := findReviewEntries(data, 0); len(reviews) < 1 {
		t.Errorf("expected reviews, got %d", len(reviews))
	}
}

func TestFindReviewEntries_DepthLimit(t *testing.T) {
	if reviews := findReviewEntries([]any{[]any{"Long review text here with detail", 4.0}}, 13); reviews != nil {
		t.Error("expected nil at depth limit")
	}
}

// ---------------------------------------------------------------------------
// findHotelNameInData branches
// ---------------------------------------------------------------------------

func TestFindHotelNameInData_FoundAtIndex1(t *testing.T) {
	if name := findHotelNameInData([]any{nil, "Hilton Garden Inn"}, 0); name != "Hilton Garden Inn" {
		t.Errorf("got %q", name)
	}
}

func TestFindHotelNameInData_DepthLimit(t *testing.T) {
	if name := findHotelNameInData([]any{nil, "Hotel Name"}, 7); name != "" {
		t.Errorf("expected empty, got %q", name)
	}
}

func TestFindHotelNameInData_MapTraversal(t *testing.T) {
	if name := findHotelNameInData(map[string]any{"info": []any{nil, "Map Hotel"}}, 0); name != "Map Hotel" {
		t.Errorf("got %q", name)
	}
}

func TestFindHotelNameInData_SkipsTooShort(t *testing.T) {
	if name := findHotelNameInData([]any{nil, "ab"}, 0); name != "" {
		t.Errorf("got %q", name)
	}
}

// ---------------------------------------------------------------------------
// findSummaryData
// ---------------------------------------------------------------------------

func TestFindSummaryData_RatingBreakdown(t *testing.T) {
	var s models.ReviewSummary
	findSummaryData([]any{5.0, 10.0, 20.0, 50.0, 100.0}, &s, 0)
	if s.RatingBreakdown == nil || s.RatingBreakdown["5"] != 100 {
		t.Errorf("breakdown: %+v", s.RatingBreakdown)
	}
}

func TestFindSummaryData_AvgAndCount(t *testing.T) {
	var s models.ReviewSummary
	findSummaryData([]any{4.2, 500.0}, &s, 0)
	if s.AverageRating != 4.2 || s.TotalReviews != 500 {
		t.Errorf("avg=%v total=%d", s.AverageRating, s.TotalReviews)
	}
}

func TestFindSummaryData_MapTraversal(t *testing.T) {
	var s models.ReviewSummary
	findSummaryData(map[string]any{"summary": []any{3.8, 200.0}}, &s, 0)
	if s.AverageRating != 3.8 {
		t.Errorf("avg=%v", s.AverageRating)
	}
}

// ---------------------------------------------------------------------------
// isHotelType
// ---------------------------------------------------------------------------

func TestIsHotelType_AllTypes(t *testing.T) {
	for _, typ := range []string{"Hotel", "LodgingBusiness", "Motel", "Hostel",
		"Resort", "BedAndBreakfast", "CampingPitch", "Apartment"} {
		if !isHotelType(map[string]any{"@type": typ}) {
			t.Errorf("isHotelType(%q) = false", typ)
		}
	}
}

func TestIsHotelType_NonHotel(t *testing.T) {
	for _, typ := range []string{"Organization", "Restaurant", ""} {
		if isHotelType(map[string]any{"@type": typ}) {
			t.Errorf("isHotelType(%q) = true", typ)
		}
	}
}

// ---------------------------------------------------------------------------
// Validation early-return paths
// ---------------------------------------------------------------------------

func TestCov_GetHotelReviews_EmptyID(t *testing.T) {
	_, err := GetHotelReviews(context.Background(), "", ReviewOptions{})
	if err == nil || !strings.Contains(err.Error(), "hotel ID is required") {
		t.Errorf("got: %v", err)
	}
}

func TestGetHotelPrices_EmptyID(t *testing.T) {
	if _, err := GetHotelPrices(context.Background(), "", "2026-07-01", "2026-07-05", "USD"); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetHotelPrices_NoDates(t *testing.T) {
	if _, err := GetHotelPrices(context.Background(), "test-id", "", "", "USD"); err == nil {
		t.Fatal("expected error")
	}
}

func TestFetchHotelAmenities_EmptyID(t *testing.T) {
	if _, err := FetchHotelAmenities(context.Background(), ""); err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// parseReviewsFromPage edge cases
// ---------------------------------------------------------------------------

func TestCov_ParseReviewsFromPage_NoCallbacks(t *testing.T) {
	if _, err := parseReviewsFromPage("<html>nothing</html>", "test-id", ReviewOptions{Limit: 10, Sort: "newest"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseReviewsFromPage_WithReviewText(t *testing.T) {
	page := "AF_initDataCallback({key: 'ds:0', data:[\"no review data here\"]});\n" +
		"<script>" +
		`{"reviewRating":{"ratingValue":"4.0"},"reviewBody":"Excellent location and very clean rooms","author":{"name":"Test Author"}}` +
		"</script>"
	result, err := parseReviewsFromPage(page, "test-id", ReviewOptions{Limit: 10, Sort: "newest"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(result.Reviews) < 1 {
		t.Errorf("expected at least 1 review, got %d", len(result.Reviews))
	}
}

func TestParseReviewsFromPage_ComputesSummary(t *testing.T) {
	page := "AF_initDataCallback({key: 'ds:0', data:[\"empty\"]});\n" +
		`<script>{"reviewRating":{"ratingValue":"5.0"},"reviewBody":"Perfect stay with amazing breakfast and pool","author":{"name":"A"}}</script>` + "\n" +
		`<script>{"reviewRating":{"ratingValue":"3.0"},"reviewBody":"Average experience nothing special but ok","author":{"name":"B"}}</script>`
	result, err := parseReviewsFromPage(page, "test-id", ReviewOptions{Limit: 10, Sort: "newest"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.Summary.TotalReviews == 0 {
		t.Error("expected computed summary")
	}
}

func TestParseReviewsFromRawEntries_Empty(t *testing.T) {
	if _, err := parseReviewsFromRawEntries([]any{}, "test-id", ReviewOptions{Limit: 10, Sort: "newest"}); err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// findRoomData map traversal
// ---------------------------------------------------------------------------

func TestFindRoomData_MapTraversal(t *testing.T) {
	data := map[string]any{"rooms": []any{
		[]any{"Deluxe Double Room", 199.0},
		[]any{"Standard Twin", 129.0},
	}}
	if rooms := findRoomData(data, "EUR", 0); len(rooms) < 2 {
		t.Errorf("expected 2+, got %d", len(rooms))
	}
}

// ---------------------------------------------------------------------------
// extractSizeM2 / extractMaxGuests
// ---------------------------------------------------------------------------

func TestExtractSizeM2_Variants(t *testing.T) {
	for _, tt := range []struct {
		text string
		want float64
	}{
		{"Room is 35 m\u00b2", 35},
		{"28m2 room", 28},
		{"40 sqm space", 40},
		{"50 sq m floor", 50},
		{"no size info", 0},
	} {
		if got := extractSizeM2(tt.text); got != tt.want {
			t.Errorf("extractSizeM2(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestExtractMaxGuests_Variants(t *testing.T) {
	for _, tt := range []struct {
		text string
		want int
	}{
		{"max 4 guests", 4},
		{"sleeps 6 adults", 6},
		{"for 2 people", 2},
		{"accommodates 3 persons", 3},
		{"up to 5 guests", 5},
		{"no guest info", 0},
		{"maximum 25 guests", 0},
	} {
		if got := extractMaxGuests(tt.text); got != tt.want {
			t.Errorf("extractMaxGuests(%q) = %d, want %d", tt.text, got, tt.want)
		}
	}
}

func TestExtractRoomAmenities_Empty(t *testing.T) {
	if result := extractRoomAmenities(""); result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// DefaultProvider
// ---------------------------------------------------------------------------

func TestDefaultProvider_SearchHotels_CancelledCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &DefaultProvider{}
	// Cancelled context may or may not error; key is delegation works and no panic.
	_, _ = p.SearchHotels(ctx, "Paris", models.HotelSearchOptions{
		CheckIn: "2026-07-01", CheckOut: "2026-07-05", Guests: 2, Currency: "USD",
	})
}

// ---------------------------------------------------------------------------
// sortReviews
// ---------------------------------------------------------------------------

func TestCov_SortReviews_Highest(t *testing.T) {
	reviews := []models.HotelReview{{Rating: 3.0}, {Rating: 5.0}, {Rating: 1.0}}
	sortReviews(reviews, "highest")
	if reviews[0].Rating != 5.0 {
		t.Errorf("first = %v", reviews[0].Rating)
	}
}

func TestCov_SortReviews_Lowest(t *testing.T) {
	reviews := []models.HotelReview{{Rating: 3.0}, {Rating: 5.0}, {Rating: 1.0}}
	sortReviews(reviews, "lowest")
	if reviews[0].Rating != 1.0 {
		t.Errorf("first = %v", reviews[0].Rating)
	}
}

// ---------------------------------------------------------------------------
// SearchHotelsByName / SearchHotelByName validation
// ---------------------------------------------------------------------------

func TestSearchHotelsByName_EmptyName(t *testing.T) {
	if _, err := SearchHotelsByName(context.Background(), "", "Paris", "2026-07-01", "2026-07-05", "EUR"); err == nil {
		t.Fatal("expected error")
	}
}

func TestSearchHotelsByName_NoDates(t *testing.T) {
	if _, err := SearchHotelsByName(context.Background(), "Hotel", "Paris", "", "", "EUR"); err == nil {
		t.Fatal("expected error")
	}
}

func TestSearchHotelByName_EmptyQuery(t *testing.T) {
	if _, err := SearchHotelByName(context.Background(), "", "2026-07-01", "2026-07-05", "EUR"); err == nil {
		t.Fatal("expected error")
	}
}

func TestSearchHotelByName_NoDates(t *testing.T) {
	if _, err := SearchHotelByName(context.Background(), "Hotel", "", "", "EUR"); err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// isDateString
// ---------------------------------------------------------------------------

func TestIsDateString_AdditionalCases(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  bool
	}{
		{"2 weeks ago", true},
		{"3 months ago", true},
		{"a month ago", true},
		{"last year", true},
		{"January 2026", true},
		{"Mar 15", true},
		{"2026-01-15", true},
		{"1 day ago", true},
		{"1 hour ago", true},
		{"decent hotel room", false},
		{"not a date at all", false},
		{"", false},
		{strings.Repeat("x", 51), false},
		{"a week ago", true},
	} {
		if got := isDateString(tt.input); got != tt.want {
			t.Errorf("isDateString(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// buildNameQuery
// ---------------------------------------------------------------------------

func TestBuildNameQuery_LocationAlreadyInName(t *testing.T) {
	if got := buildNameQuery("Hotel Ritz Paris", "Paris"); got != "Hotel Ritz Paris" {
		t.Errorf("got %q", got)
	}
}

func TestBuildNameQuery_AppendLocation(t *testing.T) {
	if got := buildNameQuery("Hotel Ritz", "Paris"); got != "Hotel Ritz, Paris" {
		t.Errorf("got %q", got)
	}
}

func TestBuildNameQuery_EmptyLocation(t *testing.T) {
	if got := buildNameQuery("Hotel Ritz", ""); got != "Hotel Ritz" {
		t.Errorf("got %q", got)
	}
}

// ---------------------------------------------------------------------------
// filterByNameMatch / normalizeWords
// ---------------------------------------------------------------------------

func TestFilterByNameMatch_EmptySearchName(t *testing.T) {
	hotels := []models.HotelResult{{Name: "Hotel A"}}
	if result := filterByNameMatch(hotels, "a"); len(result) != 1 {
		t.Errorf("expected 1, got %d", len(result))
	}
}

func TestNormalizeWords_StripsPunctuation(t *testing.T) {
	for _, w := range normalizeWords("Hotel's (Best) room!") {
		if strings.ContainsAny(w, "'()!") {
			t.Errorf("word %q has punctuation", w)
		}
	}
}

// ---------------------------------------------------------------------------
// propertyTypeCode
// ---------------------------------------------------------------------------

func TestPropertyTypeCode_AllTypes(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"hotel", "2"}, {"apartment", "3"}, {"hostel", "4"}, {"resort", "5"},
		{"bnb", "7"}, {"bed_and_breakfast", "7"}, {"bed and breakfast", "7"},
		{"villa", "8"}, {"", ""}, {"unrecognized", ""}, {" Hotel ", "2"},
	} {
		if got := propertyTypeCode(tt.input); got != tt.want {
			t.Errorf("propertyTypeCode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// filterHotels
// ---------------------------------------------------------------------------

func TestFilterHotels_AllFilters(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Cheap", Price: 50, Stars: 2, Rating: 3.0, Lat: 60.17, Lon: 24.93, Amenities: []string{"WiFi"}},
		{Name: "Mid", Price: 150, Stars: 4, Rating: 8.0, Lat: 60.18, Lon: 24.94, Amenities: []string{"WiFi", "Pool"}},
		{Name: "Expensive", Price: 500, Stars: 5, Rating: 9.5, Lat: 60.19, Lon: 24.95, Amenities: []string{"WiFi", "Pool", "Gym"}},
		{Name: "Far Away", Price: 100, Stars: 3, Rating: 7.0, Lat: 61.50, Lon: 25.00, Amenities: []string{"WiFi"}},
	}
	opts := HotelSearchOptions{
		MinPrice: 100, MaxPrice: 300, Stars: 3, MinRating: 5.0,
		Amenities: []string{"WiFi"}, Brand: "Mid",
		CenterLat: 60.17, CenterLon: 24.93, MaxDistanceKm: 5,
	}
	result := filterHotels(hotels, opts)
	found := false
	for _, h := range result {
		if h.Name == "Mid" {
			found = true
		}
		if h.Name == "Far Away" || h.Name == "Cheap" || h.Name == "Expensive" {
			t.Errorf("%s should have been filtered", h.Name)
		}
	}
	if !found {
		t.Error("Mid should survive")
	}
}

func TestFilterHotels_ExternalProviderNoRating(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Airbnb Place", Price: 100, Rating: 0, Sources: []models.PriceSource{{Provider: "airbnb"}}},
		{Name: "Google Hotel", Price: 100, Rating: 0, Sources: []models.PriceSource{{Provider: "google_hotels"}}},
	}
	result := filterHotels(hotels, HotelSearchOptions{MinRating: 5.0})
	foundAirbnb := false
	for _, h := range result {
		if h.Name == "Airbnb Place" {
			foundAirbnb = true
		}
		if h.Name == "Google Hotel" {
			t.Error("Google Hotel without rating should be filtered")
		}
	}
	if !foundAirbnb {
		t.Error("Airbnb Place should survive (external provider)")
	}
}

// ---------------------------------------------------------------------------
// parseReviewsFromRawEntries — with actual review data
// ---------------------------------------------------------------------------
