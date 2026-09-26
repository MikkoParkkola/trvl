package trip

// MIK-3063: confirm DiscoverResult carries the RequestMatch score wired
// from the match package. The match package itself is exhaustively
// tested in internal/match/request_test.go — this file only checks the
// integration plumbing.

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestRankDiscoverTrials_PopulatesRequestMatch(t *testing.T) {
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)

	matchReq := buildDiscoverMatchRequest(
		DiscoverOptions{Origin: "AMS", MinNights: 2, MaxNights: 4},
		from, until,
		"EUR",
	)

	trials := []discoverTrial{
		{
			window: candidateWindow{
				start:  time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
				end:    time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC),
				nights: 3,
			},
			dest: models.ExploreDestination{
				AirportCode: "BCN",
				CityName:    "Barcelona",
				Price:       100,
			},
		},
	}
	hotelResults := map[discoverTrialKey]*discoverHotelInfo{
		{airport: "BCN", nights: 3}: {price: 75, total: 225, name: "Hotel BCN", rating: 4.2},
	}

	results := rankDiscoverTrials(trials, hotelResults, 500, "EUR", 5, nil, matchReq)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}

	// Mid-window 3-night trip with the requested origin and currency
	// hits every axis perfectly → score 100.
	if got := results[0].RequestMatch; got != 100 {
		t.Errorf("RequestMatch = %d, want 100 for mid-window perfect-fit trip", got)
	}
	if results[0].RequestMatchReason != "" {
		t.Errorf("RequestMatchReason = %q, want empty (no penalty)", results[0].RequestMatchReason)
	}
}

func TestRankDiscoverTrials_RequestMatchPenalisesEdgeNights(t *testing.T) {
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)

	// MinNights=2, MaxNights=6 → ideal=4. A 2-night trip is 2 off.
	matchReq := buildDiscoverMatchRequest(
		DiscoverOptions{Origin: "AMS", MinNights: 2, MaxNights: 6},
		from, until,
		"EUR",
	)

	trials := []discoverTrial{
		{
			window: candidateWindow{
				start:  time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
				end:    time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC),
				nights: 2,
			},
			dest: models.ExploreDestination{
				AirportCode: "BCN",
				CityName:    "Barcelona",
				Price:       100,
			},
		},
	}
	hotelResults := map[discoverTrialKey]*discoverHotelInfo{
		{airport: "BCN", nights: 2}: {price: 75, total: 150, name: "Hotel BCN", rating: 4.2},
	}

	results := rankDiscoverTrials(trials, hotelResults, 500, "EUR", 5, nil, matchReq)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	// 2 nights vs ideal 4 → 2 * 5 = 10 point penalty → 90.
	if got := results[0].RequestMatch; got != 90 {
		t.Errorf("RequestMatch = %d, want 90 (2-night drift)", got)
	}
	if results[0].RequestMatchReason == "" {
		t.Error("RequestMatchReason should be non-empty when score < 100")
	}
}

func TestBuildDiscoverMatchRequest_ZeroFlexNotNegative(t *testing.T) {
	// Edge: from == until → halfWindow = 0, no negative.
	t0 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	mr := buildDiscoverMatchRequest(DiscoverOptions{Origin: "HEL", MinNights: 0, MaxNights: 0}, t0, t0, "EUR")
	if mr.DateWindowDays < 0 {
		t.Errorf("DateWindowDays = %d, want >= 0", mr.DateWindowDays)
	}
	if mr.Nights < 1 {
		t.Errorf("Nights = %d, want >= 1 (clamped)", mr.Nights)
	}
}
