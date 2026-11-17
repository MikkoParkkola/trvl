package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trip"
)

var budgetReviewArgs = map[string]any{"hotel_id": budgetAccHotelID, "limit": 200}

func stubHotelReviews(t *testing.T, n, pad int) {
	t.Helper()
	orig := getHotelReviewsFunc
	t.Cleanup(func() { getHotelReviewsFunc = orig })
	getHotelReviewsFunc = func(context.Context, string, hotels.ReviewOptions) (*models.HotelReviewResult, error) {
		reviews := make([]models.HotelReview, n)
		for i := range reviews {
			reviews[i] = models.HotelReview{Rating: 4, Author: fmt.Sprintf("Reviewer %03d", i), Date: "2027-01-01", Text: strings.Repeat("t", pad)}
		}
		return &models.HotelReviewResult{Success: true, HotelID: budgetAccHotelID, Name: "Hotel 000", Reviews: reviews, Count: n,
			Summary: models.ReviewSummary{AverageRating: 4, TotalReviews: n}}, nil
	}
}

func TestReplyBudgetHotelReviews(t *testing.T) {
	stubHotelReviews(t, 200, 1000)
	got := callGroundTool(t, "hotel_reviews", budgetReviewArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	assertUnderPromise(t, got)
	authors := rowNames(got.structured, "reviews", "author")
	if len(authors) == 0 || len(authors) >= 200 || authors[0] != "Reviewer 000" {
		t.Fatalf("reviews shown = %d (first %v), want a tail cut keeping Reviewer 000", len(authors), authors[:min(1, len(authors))])
	}
	if c := bodyInt(got.structured, "count"); c != len(authors) {
		t.Errorf("count = %d, want the %d reviews in this response", c, len(authors))
	}
	if o := bodyInt(got.structured, "budget_omitted"); o != 200-len(authors) {
		t.Errorf("budget_omitted = %d, want %d", o, 200-len(authors))
	}
	if !strings.Contains(got.userText, fmt.Sprintf("Showing %d of 200 reviews", len(authors))) {
		t.Errorf("summary should say Showing %d of 200 reviews:\n%s", len(authors), got.userText)
	}
	assertTextCopyMatches(t, got, "reviews", "author")
}

func TestReplyBudgetHotelReviewsFloor(t *testing.T) {
	stubHotelReviews(t, 10, 20_000)
	got := callGroundTool(t, "hotel_reviews", budgetReviewArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	assertUnderPromise(t, got)
	if a := rowNames(got.structured, "reviews", "author"); len(a) != 1 {
		t.Errorf("floor kept %d reviews, want 1", len(a))
	}
}

func TestReplyBudgetHotelReviewsFloorOverBudget(t *testing.T) {
	stubHotelReviews(t, 3, 30_000)
	assertBoundedFloorError(t, callGroundTool(t, "hotel_reviews", budgetReviewArgs))
}

var budgetTransferArgs = map[string]any{"airport_code": "VIE", "destination": "Vienna", "date": "2027-03-10"}

func budgetTransferURL(i int) string { return fmt.Sprintf("https://transfers.example/r%03d", i) }

func stubBudgetTransfers(t *testing.T, n, pad int) {
	t.Helper()
	orig := searchAirportTransfersFunc
	t.Cleanup(func() { searchAirportTransfersFunc = orig })
	searchAirportTransfersFunc = func(context.Context, trip.AirportTransferInput) (*trip.AirportTransferResult, error) {
		routes := make([]models.GroundRoute, n)
		for i := range routes {
			routes[i] = models.GroundRoute{Provider: "flixbus", Type: "bus", Price: float64(5 + i), Currency: "EUR", Duration: 30,
				Departure: models.GroundStop{City: "Vienna Airport"}, Arrival: models.GroundStop{City: "Vienna"},
				BookingURL: budgetTransferURL(i) + "?pad=" + strings.Repeat("u", pad)}
		}
		return &trip.AirportTransferResult{Success: true, AirportCode: "VIE", Airport: "Vienna International", AirportCity: "Vienna",
			Destination: "Vienna", Date: "2027-03-10", Count: n, ExactMatches: n, Routes: routes}, nil
	}
}

func transferKeys(body map[string]any) []string {
	urls := rowNames(body, "routes", "booking_url")
	for i, u := range urls {
		urls[i], _, _ = strings.Cut(u, "?")
	}
	return urls
}

func TestReplyBudgetAirportTransfers(t *testing.T) {
	stubBudgetTransfers(t, 60, 1500)
	got := callGroundTool(t, "search_airport_transfers", budgetTransferArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	assertUnderPromise(t, got)
	keys := transferKeys(got.structured)
	if len(keys) == 0 || len(keys) >= 60 {
		t.Fatalf("routes shown = %d, want a cut between 1 and 59", len(keys))
	}
	for i, k := range keys {
		if k != budgetTransferURL(i) {
			t.Fatalf("route %d is %s; the cut must drop from the bottom", i, k)
		}
	}
	if c := bodyInt(got.structured, "count"); c != len(keys) {
		t.Errorf("count = %d, want the %d routes in this response", c, len(keys))
	}
	if o := bodyInt(got.structured, "budget_omitted"); o != 60-len(keys) {
		t.Errorf("budget_omitted = %d, want %d", o, 60-len(keys))
	}
	if !strings.Contains(got.userText, fmt.Sprintf("Showing %d of 60 routes", len(keys))) || !strings.Contains(got.userText, "50 KB") {
		t.Errorf("summary should say Showing %d of 60 routes and the 50 KB budget:\n%s", len(keys), got.userText)
	}
}

func TestReplyBudgetAirportTransfersFloor(t *testing.T) {
	stubBudgetTransfers(t, 10, 10_000)
	got := callGroundTool(t, "search_airport_transfers", budgetTransferArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	assertUnderPromise(t, got)
	if k := transferKeys(got.structured); !slices.Equal(k, []string{budgetTransferURL(0)}) {
		t.Errorf("floor kept %d routes, want only the first", len(k))
	}
}

func TestReplyBudgetAirportTransfersFloorOverBudget(t *testing.T) {
	stubBudgetTransfers(t, 3, 30_000)
	assertBoundedFloorError(t, callGroundTool(t, "search_airport_transfers", budgetTransferArgs))
}

// TestReplyBudgetGroundFloorOverBudget: a single route too big for the budget
// is a bounded error, not an oversized success.
func TestReplyBudgetGroundFloorOverBudget(t *testing.T) {
	routes := make([]models.GroundRoute, 3)
	for i := range routes {
		routes[i] = models.GroundRoute{Provider: "flixbus", Type: "bus", Price: float64(10 + i), Currency: "EUR",
			BookingURL: budgetTransferURL(i) + "?pad=" + strings.Repeat("u", 30_000)}
	}
	stubGroundRoutes(t, routes)
	assertBoundedFloorError(t, callSearchGround(t, groundContractArgs))
}
