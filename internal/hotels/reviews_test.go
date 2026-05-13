package hotels

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// --- Validation tests ---

func TestGetHotelReviews_EmptyID(t *testing.T) {
	_, err := GetHotelReviews(context.Background(), "", ReviewOptions{})
	if err == nil {
		t.Error("expected error for empty hotel ID")
	}
}

func TestGetHotelReviews_DefaultOptions(t *testing.T) {
	// Verify defaults are applied. Can't test full flow without network,
	// but we can verify the function doesn't panic on defaults.
	_, err := GetHotelReviews(context.Background(), "fake-id", ReviewOptions{})
	// Error expected (network), but not a panic.
	if err == nil {
		t.Log("surprisingly succeeded with fake hotel ID")
	}
}

// --- Sort tests ---

func TestSortReviews_Highest(t *testing.T) {
	reviews := []models.HotelReview{
		{Rating: 3, Text: "ok"},
		{Rating: 5, Text: "great"},
		{Rating: 1, Text: "terrible"},
		{Rating: 4, Text: "good"},
	}

	sortReviews(reviews, "highest")

	if reviews[0].Rating != 5 {
		t.Errorf("first review rating = %v, want 5", reviews[0].Rating)
	}
	if reviews[len(reviews)-1].Rating != 1 {
		t.Errorf("last review rating = %v, want 1", reviews[len(reviews)-1].Rating)
	}
}

func TestSortReviews_Lowest(t *testing.T) {
	reviews := []models.HotelReview{
		{Rating: 3, Text: "ok"},
		{Rating: 5, Text: "great"},
		{Rating: 1, Text: "terrible"},
	}

	sortReviews(reviews, "lowest")

	if reviews[0].Rating != 1 {
		t.Errorf("first review rating = %v, want 1", reviews[0].Rating)
	}
	if reviews[len(reviews)-1].Rating != 5 {
		t.Errorf("last review rating = %v, want 5", reviews[len(reviews)-1].Rating)
	}
}

func TestSortReviews_Newest(t *testing.T) {
	reviews := []models.HotelReview{
		{Rating: 3, Text: "first"},
		{Rating: 5, Text: "second"},
	}

	sortReviews(reviews, "newest")

	// Newest is a no-op (data is already in order from source).
	if reviews[0].Text != "first" {
		t.Errorf("order should be preserved for newest sort")
	}
}

// --- Parser tests ---

func TestTryParseOneReview_Valid(t *testing.T) {
	entry := []any{
		nil,
		"John Smith",
		4.0,
		"This hotel was absolutely wonderful, great location and friendly staff throughout our stay.",
		"2 weeks ago",
	}

	r, ok := tryParseOneReview(entry)
	if !ok {
		t.Fatal("expected valid review")
	}
	if r.Rating != 4 {
		t.Errorf("rating = %v, want 4", r.Rating)
	}
	if r.Text == "" {
		t.Error("expected non-empty text")
	}
}

func TestTryParseOneReview_TooShort(t *testing.T) {
	entry := []any{"hello", 3.0}
	_, ok := tryParseOneReview(entry)
	if ok {
		t.Error("expected false for too-short entry")
	}
}

func TestTryParseOneReview_NoRating(t *testing.T) {
	entry := []any{
		"Author Name",
		"This is a very long review text that should be long enough to qualify as review content.",
		"Some other field",
	}
	_, ok := tryParseOneReview(entry)
	if ok {
		t.Error("expected false for entry without rating")
	}
}

func TestTryParseReviewList_Multiple(t *testing.T) {
	list := []any{
		[]any{
			nil,
			"Alice",
			5.0,
			"Absolutely fantastic hotel, would definitely recommend to anyone visiting the city.",
			"1 week ago",
		},
		[]any{
			nil,
			"Bob",
			3.0,
			"Average experience, nothing special but clean and functional for a short stay.",
			"3 weeks ago",
		},
		[]any{
			nil,
			"Carol",
			4.0,
			"Very nice hotel with great amenities and a wonderful breakfast buffet every morning.",
			"1 month ago",
		},
	}

	reviews := tryParseReviewList(list)
	if len(reviews) < 2 {
		t.Fatalf("expected at least 2 reviews, got %d", len(reviews))
	}
}

func TestTryParseReviewList_SingleItem(t *testing.T) {
	// A single-item list should not return results (needs >= 2).
	list := []any{
		[]any{
			nil,
			"Alice",
			5.0,
			"Great hotel with excellent service and wonderful location near everything.",
		},
	}

	reviews := tryParseReviewList(list)
	if len(reviews) != 0 {
		t.Errorf("expected 0 reviews for single item, got %d", len(reviews))
	}
}

// --- Summary tests ---

func TestExtractReviewSummary_Found(t *testing.T) {
	// Nested data with a [rating, count] pair.
	data := []any{
		nil,
		[]any{
			[]any{4.3, 1523.0},
		},
	}

	s := extractReviewSummary(data)
	if s.AverageRating != 4.3 {
		t.Errorf("average rating = %v, want 4.3", s.AverageRating)
	}
	if s.TotalReviews != 1523 {
		t.Errorf("total reviews = %d, want 1523", s.TotalReviews)
	}
}

func TestExtractReviewSummary_WithBreakdown(t *testing.T) {
	data := []any{
		nil,
		[]any{4.2, 500.0},
		[]any{10.0, 20.0, 30.0, 140.0, 300.0}, // 1-star to 5-star
	}

	s := extractReviewSummary(data)
	if s.RatingBreakdown == nil {
		t.Fatal("expected rating breakdown")
	}
	if s.RatingBreakdown["5"] != 300 {
		t.Errorf("5-star count = %d, want 300", s.RatingBreakdown["5"])
	}
	if s.RatingBreakdown["1"] != 10 {
		t.Errorf("1-star count = %d, want 10", s.RatingBreakdown["1"])
	}
}

func TestExtractReviewSummary_NotFound(t *testing.T) {
	data := []any{"hello", 42.0}
	s := extractReviewSummary(data)
	if s.AverageRating != 0 {
		t.Errorf("expected zero average rating, got %v", s.AverageRating)
	}
}

// --- computeSummary ---

func TestComputeSummary(t *testing.T) {
	reviews := []models.HotelReview{
		{Rating: 5},
		{Rating: 4},
		{Rating: 3},
		{Rating: 4},
		{Rating: 5},
	}

	s := computeSummary(reviews)
	if s.TotalReviews != 5 {
		t.Errorf("total = %d, want 5", s.TotalReviews)
	}
	// Average: (5+4+3+4+5)/5 = 4.2
	if s.AverageRating != 4.2 {
		t.Errorf("average = %v, want 4.2", s.AverageRating)
	}
	if s.RatingBreakdown["5"] != 2 {
		t.Errorf("5-star count = %d, want 2", s.RatingBreakdown["5"])
	}
}

func TestComputeSummary_Empty(t *testing.T) {
	s := computeSummary(nil)
	if s.TotalReviews != 0 {
		t.Errorf("total = %d, want 0", s.TotalReviews)
	}
}

// --- Helper tests ---

func TestIsDateString(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"2 weeks ago", true},
		{"3 months ago", true},
		{"March 2026", true},
		{"2026-03-15", true},
		{"January", true},
		{"Hello world", false},
		{"Booking.com", false},
		{"", false},
	}

	for _, tt := range tests {
		got := isDateString(tt.input)
		if got != tt.want {
			t.Errorf("isDateString(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsURL(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"https://example.com", true},
		{"http://example.com", true},
		{"/path/to/something", true},
		{"not a url", false},
		{"Booking.com", false},
	}

	for _, tt := range tests {
		got := isURL(tt.input)
		if got != tt.want {
			t.Errorf("isURL(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- extractReviewFromJSON ---

func TestExtractReviewFromJSON(t *testing.T) {
	obj := map[string]any{
		"reviewBody": "Excellent hotel with great service.",
		"author": map[string]any{
			"name": "Jane Doe",
		},
		"reviewRating": map[string]any{
			"ratingValue": "5",
		},
		"datePublished": "2026-03-15",
	}

	r := extractReviewFromJSON(obj)
	if r.Text != "Excellent hotel with great service." {
		t.Errorf("text = %q", r.Text)
	}
	if r.Author != "Jane Doe" {
		t.Errorf("author = %q", r.Author)
	}
	if r.Rating != 5 {
		t.Errorf("rating = %v", r.Rating)
	}
	if r.Date != "2026-03-15" {
		t.Errorf("date = %q", r.Date)
	}
}

func TestExtractReviewFromJSON_NumericRating(t *testing.T) {
	obj := map[string]any{
		"reviewBody": "Good place.",
		"reviewRating": map[string]any{
			"ratingValue": 4.0,
		},
	}

	r := extractReviewFromJSON(obj)
	if r.Rating != 4 {
		t.Errorf("rating = %v, want 4", r.Rating)
	}
}

func TestExtractReviewFromJSON_AuthorString(t *testing.T) {
	obj := map[string]any{
		"reviewBody": "Nice stay overall.",
		"author":     "Bob Smith",
		"reviewRating": map[string]any{
			"ratingValue": "3",
		},
	}

	r := extractReviewFromJSON(obj)
	if r.Author != "Bob Smith" {
		t.Errorf("author = %q, want %q", r.Author, "Bob Smith")
	}
}

// --- parseReviewsFromBatchResponse ---

func TestParseReviewsFromBatchResponse_ValidEntries(t *testing.T) {
	// Build mock batchexecute response with ocp93e payload containing reviews.
	review1 := []any{
		nil,
		"Alice",
		5.0,
		"Outstanding hotel with incredible views and impeccable service throughout.",
		"1 week ago",
	}
	review2 := []any{
		nil,
		"Bob",
		3.0,
		"Decent hotel but a bit overpriced for what you get, average amenities.",
		"2 weeks ago",
	}

	reviewList := []any{review1, review2}
	// Wrap with summary data.
	payload := []any{
		nil,
		"Test Hotel",
		[]any{
			[]any{4.1, 892.0},
		},
		reviewList,
	}

	inner, _ := json.Marshal(payload)
	entries := []any{
		[]any{
			[]any{"wrb.fr", "ocp93e", string(inner), nil, nil, nil, "generic"},
		},
	}

	result, err := parseReviewsFromBatchResponse(entries, "/g/test123", ReviewOptions{Limit: 10, Sort: "newest"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(result.Reviews) < 2 {
		t.Fatalf("expected at least 2 reviews, got %d", len(result.Reviews))
	}
	if result.HotelID != "/g/test123" {
		t.Errorf("hotel_id = %q", result.HotelID)
	}
}

func TestParseReviewsFromBatchResponse_NoPayload(t *testing.T) {
	entries := []any{
		[]any{
			[]any{"wrb.fr", "other_rpc", `[]`, nil},
		},
	}

	_, err := parseReviewsFromBatchResponse(entries, "/g/test", ReviewOptions{Limit: 10, Sort: "newest"})
	if err == nil {
		t.Error("expected error for missing ocp93e payload")
	}
}

// --- parseReviewsFromPage ---

func TestParseReviewsFromPage_EmptyPage(t *testing.T) {
	_, err := parseReviewsFromPage("", "/g/test", ReviewOptions{Limit: 10})
	if err == nil {
		t.Error("expected error for empty page")
	}
}

func TestParseReviewsFromPage_NoCallbacks(t *testing.T) {
	_, err := parseReviewsFromPage("<html><body>No data here</body></html>", "/g/test", ReviewOptions{Limit: 10})
	if err == nil {
		t.Error("expected error for page without callbacks")
	}
}

func TestParseReviewsFromPage_LongCallbackPreamble(t *testing.T) {
	review1 := []any{
		nil,
		"Alice",
		5.0,
		"Outstanding hotel with incredible views and impeccable service throughout.",
		"1 week ago",
	}
	review2 := []any{
		nil,
		"Bob",
		3.0,
		"Decent hotel but a bit overpriced for what you get, average amenities.",
		"2 weeks ago",
	}
	dataJSON, _ := json.Marshal([]any{review1, review2})

	page := `AF_initDataCallback({key: 'ds:0', ` + longCallbackPreamble() + `data:` + string(dataJSON) + `});`

	result, err := parseReviewsFromPage(page, "/g/test", ReviewOptions{Limit: 10, Sort: "newest"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.Count != 2 {
		t.Fatalf("expected 2 reviews, got %d", result.Count)
	}
	if result.Reviews[0].Author != "Alice" {
		t.Errorf("first review author = %q", result.Reviews[0].Author)
	}
}

// --- findReviewEntries ---

func TestFindReviewEntries_DirectReviewList(t *testing.T) {
	// Test findReviewEntries with the exact same structure
	// that extractBatchPayload would return.
	review1 := []any{
		nil,
		"Alice",
		5.0,
		"Outstanding hotel with incredible views and impeccable service throughout.",
		"1 week ago",
	}
	review2 := []any{
		nil,
		"Bob",
		3.0,
		"Decent hotel but a bit overpriced for what you get, average amenities.",
		"2 weeks ago",
	}

	// This is the exact payload from extractBatchPayload.
	payload := []any{
		nil,
		"Test Hotel",
		[]any{[]any{4.1, 892.0}},
		[]any{review1, review2},
	}

	// JSON round-trip to simulate what extractBatchPayload does.
	data, _ := json.Marshal(payload)
	var parsed any
	_ = json.Unmarshal(data, &parsed)

	reviews := findReviewEntries(parsed, 0)
	t.Logf("findReviewEntries returned %d reviews", len(reviews))
	for i, r := range reviews {
		t.Logf("  [%d] rating=%.0f author=%q text=%.40s...", i, r.Rating, r.Author, r.Text)
	}
	if len(reviews) < 2 {
		t.Errorf("expected at least 2 reviews, got %d", len(reviews))
	}
}

func TestFindReviewEntries_NestedReviews(t *testing.T) {
	reviews := []any{
		[]any{
			nil,
			"Author A",
			4.0,
			"Really enjoyed our stay at this wonderful hotel with great amenities and service.",
			"3 days ago",
		},
		[]any{
			nil,
			"Author B",
			5.0,
			"Perfect in every way, the staff went above and beyond to make our trip memorable.",
			"1 week ago",
		},
	}

	// Wrap deeply.
	data := []any{nil, []any{nil, reviews}}

	found := findReviewEntries(data, 0)
	if len(found) < 2 {
		t.Errorf("expected at least 2 reviews, got %d", len(found))
	}
}

func TestFindReviewEntries_MaxDepth(t *testing.T) {
	// Build deeply nested structure beyond max depth.
	var data any = []any{nil, "Author", 4.0, "Some review text that is long enough for the parser"}
	for range 15 {
		data = []any{data}
	}

	found := findReviewEntries(data, 0)
	// Should not find anything due to depth limit.
	if len(found) > 0 {
		t.Errorf("expected 0 reviews at max depth, got %d", len(found))
	}
}

// --- extractHotelName ---

func TestExtractHotelName(t *testing.T) {
	data := []any{nil, "Hotel Kamp Helsinki"}
	name := extractHotelName(data)
	if name != "Hotel Kamp Helsinki" {
		t.Errorf("name = %q, want %q", name, "Hotel Kamp Helsinki")
	}
}

func TestExtractHotelName_NotFound(t *testing.T) {
	data := []any{42.0, 43.0}
	name := extractHotelName(data)
	if name != "" {
		t.Errorf("expected empty name, got %q", name)
	}
}
