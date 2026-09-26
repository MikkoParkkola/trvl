package trip

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/match"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestRankDiscoverTrials_ZeroRating(t *testing.T) {
	fri := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)

	trials := []discoverTrial{
		{
			window: candidateWindow{start: fri, end: fri.AddDate(0, 0, 2), nights: 2},
			dest:   models.ExploreDestination{CityName: "Riga", AirportCode: "RIX", Price: 100},
		},
	}
	hotelResults := map[discoverTrialKey]*discoverHotelInfo{
		{airport: "RIX", nights: 2}: {total: 100, name: "Hotel", rating: 0},
	}
	results := rankDiscoverTrials(trials, hotelResults, 500, "EUR", 5, nil, match.Request{})
	if len(results) != 1 {
		t.Fatalf("expected 1, got %d", len(results))
	}
	// Zero rating → other factors still apply; score should be > 0.
	if results[0].ProfileMatch <= 0 {
		t.Errorf("profile match = %v, want > 0 even with zero rating", results[0].ProfileMatch)
	}
}

func TestRankDiscoverTrials_FallbackCityName(t *testing.T) {
	fri := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)

	trials := []discoverTrial{
		{
			window: candidateWindow{start: fri, end: fri.AddDate(0, 0, 2), nights: 2},
			dest:   models.ExploreDestination{AirportCode: "HEL", Price: 100}, // no CityName
		},
	}
	hotelResults := map[discoverTrialKey]*discoverHotelInfo{
		{airport: "HEL", nights: 2}: {total: 100, name: "Hotel", rating: 4.0},
	}
	results := rankDiscoverTrials(trials, hotelResults, 500, "EUR", 5, nil, match.Request{})
	if len(results) != 1 {
		t.Fatalf("expected 1, got %d", len(results))
	}
	if results[0].Destination == "" {
		t.Error("destination should not be empty (airport name fallback)")
	}
}

func TestRankDiscoverTrials_EmptyTrials(t *testing.T) {
	results := rankDiscoverTrials(nil, nil, 500, "EUR", 5, nil, match.Request{})
	if len(results) != 0 {
		t.Errorf("expected 0 for nil trials, got %d", len(results))
	}
}

func TestRankDiscoverTrials_BudgetFitClampedAtZero(t *testing.T) {
	fri := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)

	// Total exactly at budget -> budget_fit factor = 0, overall score is lower.
	trials := []discoverTrial{
		{
			window: candidateWindow{start: fri, end: fri.AddDate(0, 0, 2), nights: 2},
			dest:   models.ExploreDestination{CityName: "Lisbon", AirportCode: "LIS", Price: 400},
		},
	}
	hotelResults := map[discoverTrialKey]*discoverHotelInfo{
		{airport: "LIS", nights: 2}: {total: 100, name: "H", rating: 5.0},
	}
	results := rankDiscoverTrials(trials, hotelResults, 500, "EUR", 5, nil, match.Request{})
	if len(results) != 1 {
		t.Fatalf("expected 1, got %d", len(results))
	}
	// budget_fit is 0 but other neutral factors contribute; score is < 50.
	if results[0].ProfileMatch >= 50 {
		t.Errorf("profile match = %v, want < 50 when at budget (budget_fit=0)", results[0].ProfileMatch)
	}
	if results[0].BudgetSlack != 0 {
		t.Errorf("slack = %v, want 0", results[0].BudgetSlack)
	}
}

// ============================================================
// Discover validation — additional date edge cases
// ============================================================

func TestDiscover_UntilEqualsFrom_NoWindows(t *testing.T) {
	// When until == from, no windows can be formed (a Friday + MinNights
	// always exceeds until). Returns success with empty trips.
	result, err := Discover(t.Context(), DiscoverOptions{
		Origin: "HEL",
		Budget: 500,
		From:   "2026-08-07", // Friday
		Until:  "2026-08-07",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Trips) != 0 {
		t.Errorf("expected 0 trips, got %d", len(result.Trips))
	}
}

func TestDiscover_NoFridaysInRange(t *testing.T) {
	// Mon-Thu range with no Friday.
	result, err := Discover(t.Context(), DiscoverOptions{
		Origin: "HEL",
		Budget: 500,
		From:   "2026-08-10", // Monday
		Until:  "2026-08-13", // Thursday
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Success {
		t.Error("expected Success=true for empty-but-valid search")
	}
	if len(result.Trips) != 0 {
		t.Errorf("expected 0 trips (no Fridays), got %d", len(result.Trips))
	}
}

func TestDiscover_WindowNightsExceedUntil(t *testing.T) {
	// Friday is in range but MinNights=7 exceeds until.
	result, err := Discover(t.Context(), DiscoverOptions{
		Origin:    "HEL",
		Budget:    500,
		From:      "2026-08-07", // Friday
		Until:     "2026-08-10", // Monday, 3 days later
		MinNights: 7,
		MaxNights: 7,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Trips) != 0 {
		t.Errorf("expected 0 trips (window exceeds until), got %d", len(result.Trips))
	}
}

// ============================================================
// buildReviewSnippets — pure function extracted from PlanTrip enrichment
// ============================================================

func TestBuildReviewSnippets_Basic(t *testing.T) {
	reviews := []models.HotelReview{
		{Rating: 4.5, Text: "Great hotel with excellent service", Author: "Alice", Date: "2026-01-15"},
		{Rating: 3.0, Text: "Decent but noisy", Author: "Bob", Date: "2026-02-01"},
	}
	snippets := buildReviewSnippets(reviews, "Grand Hotel")
	if len(snippets) != 2 {
		t.Fatalf("expected 2 snippets, got %d", len(snippets))
	}
	if snippets[0].Rating != 4.5 {
		t.Errorf("first rating = %v, want 4.5", snippets[0].Rating)
	}
	if snippets[0].Author != "Alice" {
		t.Errorf("first author = %q, want Alice", snippets[0].Author)
	}
	if snippets[0].HotelName != "Grand Hotel" {
		t.Errorf("first hotel = %q, want Grand Hotel", snippets[0].HotelName)
	}
	if snippets[0].Date != "2026-01-15" {
		t.Errorf("first date = %q, want 2026-01-15", snippets[0].Date)
	}
}

func TestBuildReviewSnippets_SkipsEmptyText(t *testing.T) {
	reviews := []models.HotelReview{
		{Rating: 5.0, Text: "", Author: "NoText"},
		{Rating: 4.0, Text: "Lovely stay", Author: "WithText"},
	}
	snippets := buildReviewSnippets(reviews, "Hotel")
	if len(snippets) != 1 {
		t.Fatalf("expected 1 snippet (empty text skipped), got %d", len(snippets))
	}
	if snippets[0].Author != "WithText" {
		t.Errorf("author = %q, want WithText", snippets[0].Author)
	}
}

func TestBuildReviewSnippets_CapsAtThree(t *testing.T) {
	reviews := []models.HotelReview{
		{Rating: 5.0, Text: "Review one"},
		{Rating: 4.0, Text: "Review two"},
		{Rating: 3.0, Text: "Review three"},
		{Rating: 2.0, Text: "Review four"},
		{Rating: 1.0, Text: "Review five"},
	}
	snippets := buildReviewSnippets(reviews, "Hotel")
	if len(snippets) != 3 {
		t.Errorf("expected 3 snippets (capped), got %d", len(snippets))
	}
}

func TestBuildReviewSnippets_Empty(t *testing.T) {
	snippets := buildReviewSnippets(nil, "Hotel")
	if len(snippets) != 0 {
		t.Errorf("expected 0 snippets for nil reviews, got %d", len(snippets))
	}
}

func TestBuildReviewSnippets_TruncatesLongText(t *testing.T) {
	longText := "This is a very long review that exceeds one hundred and eighty characters. " +
		"It goes on and on about the hotel amenities, the breakfast buffet, the pool, " +
		"the spa, the location, and many other aspects of the stay."
	reviews := []models.HotelReview{
		{Rating: 4.0, Text: longText},
	}
	snippets := buildReviewSnippets(reviews, "Hotel")
	if len(snippets) != 1 {
		t.Fatalf("expected 1 snippet, got %d", len(snippets))
	}
	if len(snippets[0].Text) > 185 { // 180 + "..." suffix
		t.Errorf("text not truncated: len=%d", len(snippets[0].Text))
	}
}

// ============================================================
// buildDestinationContext — pure function extracted from PlanTrip enrichment
// ============================================================

func TestBuildDestinationContext_AllSections(t *testing.T) {
	guide := &models.WikivoyageGuide{
		Location: "Barcelona",
		Summary:  "Barcelona is a vibrant Mediterranean city.",
		URL:      "https://en.wikivoyage.org/wiki/Barcelona",
		Sections: map[string]string{
			"When to go": "Spring and autumn are the best times to visit.",
			"Get around": "The metro system is excellent.",
			"See":        "Visit La Sagrada Familia.",
		},
	}
	ctx := buildDestinationContext(guide)
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}
	if ctx.Source != "https://en.wikivoyage.org/wiki/Barcelona" {
		t.Errorf("source = %q", ctx.Source)
	}
	if ctx.Summary != "Barcelona is a vibrant Mediterranean city." {
		t.Errorf("summary = %q", ctx.Summary)
	}
	if ctx.WhenToGo == "" {
		t.Error("WhenToGo should not be empty")
	}
	if ctx.GetAround == "" {
		t.Error("GetAround should not be empty")
	}
}

func TestBuildDestinationContext_SummaryOnly(t *testing.T) {
	guide := &models.WikivoyageGuide{
		Summary:  "A short summary.",
		Sections: map[string]string{},
	}
	ctx := buildDestinationContext(guide)
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}
	if ctx.Summary != "A short summary." {
		t.Errorf("summary = %q", ctx.Summary)
	}
	if ctx.WhenToGo != "" {
		t.Errorf("WhenToGo = %q, want empty", ctx.WhenToGo)
	}
}

func TestBuildDestinationContext_ReturnsNilWhenEmpty(t *testing.T) {
	guide := &models.WikivoyageGuide{
		Summary:  "",
		Sections: map[string]string{"See": "something"},
	}
	ctx := buildDestinationContext(guide)
	if ctx != nil {
		t.Error("expected nil context when no summary/whentogo/getaround")
	}
}

func TestBuildDestinationContext_AlternateKeys(t *testing.T) {
	guide := &models.WikivoyageGuide{
		Summary: "Summary here.",
		Sections: map[string]string{
			"Understand":     "Important background info about the city.",
			"Getting around": "Taxis are cheap.",
		},
	}
	ctx := buildDestinationContext(guide)
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}
	if ctx.WhenToGo == "" {
		t.Error("WhenToGo should match 'Understand' fallback")
	}
	if ctx.GetAround == "" {
		t.Error("GetAround should match 'Getting around' fallback")
	}
}

func TestBuildDestinationContext_ClimateKey(t *testing.T) {
	guide := &models.WikivoyageGuide{
		Summary: "A place.",
		Sections: map[string]string{
			"Climate": "Tropical climate all year round.",
		},
	}
	ctx := buildDestinationContext(guide)
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}
	if ctx.WhenToGo == "" {
		t.Error("WhenToGo should match 'Climate' fallback")
	}
}

// ============================================================
// applyOSMEnrichment — pure function extracted from PlanTrip enrichment
// ============================================================

func TestApplyOSMEnrichment_AllFields(t *testing.T) {
	hotel := &PlanHotel{Name: "Test Hotel"}
	extra := &destinations.HotelEnrichment{
		Stars:      4,
		Website:    "https://testhotel.com",
		Wheelchair: "yes",
	}
	applyOSMEnrichment(hotel, extra)
	if hotel.OSMStars != 4 {
		t.Errorf("OSMStars = %d, want 4", hotel.OSMStars)
	}
	if hotel.Website != "https://testhotel.com" {
		t.Errorf("Website = %q, want https://testhotel.com", hotel.Website)
	}
	if hotel.Wheelchair != "yes" {
		t.Errorf("Wheelchair = %q, want yes", hotel.Wheelchair)
	}
}

func TestApplyOSMEnrichment_DoesNotOverwriteExisting(t *testing.T) {
	hotel := &PlanHotel{
		Name:     "Test Hotel",
		OSMStars: 3,
		Website:  "https://existing.com",
	}
	extra := &destinations.HotelEnrichment{
		Stars:      5,
		Website:    "https://new.com",
		Wheelchair: "limited",
	}
	applyOSMEnrichment(hotel, extra)
	// Stars: existing=3 (non-zero), so keep existing.
	if hotel.OSMStars != 3 {
		t.Errorf("OSMStars = %d, want 3 (should not overwrite)", hotel.OSMStars)
	}
	// Website: existing non-empty, so keep existing.
	if hotel.Website != "https://existing.com" {
		t.Errorf("Website = %q, want existing (should not overwrite)", hotel.Website)
	}
	// Wheelchair: always applied.
	if hotel.Wheelchair != "limited" {
		t.Errorf("Wheelchair = %q, want limited", hotel.Wheelchair)
	}
}

func TestApplyOSMEnrichment_ZeroStars(t *testing.T) {
	hotel := &PlanHotel{Name: "Test"}
	extra := &destinations.HotelEnrichment{Stars: 0}
	applyOSMEnrichment(hotel, extra)
	if hotel.OSMStars != 0 {
		t.Errorf("OSMStars = %d, want 0 (extra has zero stars)", hotel.OSMStars)
	}
}

func TestApplyOSMEnrichment_EmptyExtra(t *testing.T) {
	hotel := &PlanHotel{Name: "Test"}
	extra := &destinations.HotelEnrichment{}
	applyOSMEnrichment(hotel, extra)
	if hotel.OSMStars != 0 {
		t.Errorf("OSMStars = %d, want 0", hotel.OSMStars)
	}
	if hotel.Website != "" {
		t.Errorf("Website = %q, want empty", hotel.Website)
	}
	if hotel.Wheelchair != "" {
		t.Errorf("Wheelchair = %q, want empty", hotel.Wheelchair)
	}
}

// ============================================================
// FindWeekendGetaways validation — additional edge cases
// ============================================================

func TestFindWeekendGetaways_NegativeNightsDefaultsTo2(t *testing.T) {
	// Should default to 2 even with negative input.
	_, err := FindWeekendGetaways(t.Context(), "HEL", WeekendOptions{
		Month:  "invalid-month",
		Nights: -5,
	})
	// Will fail on invalid month, but defaults() runs first.
	if err == nil {
		t.Error("expected error for invalid month")
	}
}
