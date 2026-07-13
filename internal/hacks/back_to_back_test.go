package hacks

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// --- input validation (advisory path) ---

func TestDetectBackToBack_emptyInput(t *testing.T) {
	hacks := detectBackToBack(context.Background(), DetectorInput{})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for empty input, got %d", len(hacks))
	}
}

func TestDetectBackToBack_missingOrigin(t *testing.T) {
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Destination: "BCN",
		Date:        "2026-06-01",
		ReturnDate:  "2026-06-04",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks with missing origin, got %d", len(hacks))
	}
}

func TestDetectBackToBack_missingDestination(t *testing.T) {
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:     "HEL",
		Date:       "2026-06-01",
		ReturnDate: "2026-06-04",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks with missing destination, got %d", len(hacks))
	}
}

func TestDetectBackToBack_noReturnDate(t *testing.T) {
	// One-way search — back-to-back doesn't apply.
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for one-way search, got %d", len(hacks))
	}
}

func TestDetectBackToBack_noDate(t *testing.T) {
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		ReturnDate:  "2026-06-04",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks with missing depart date, got %d", len(hacks))
	}
}

func TestDetectBackToBack_invalidDates(t *testing.T) {
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "not-a-date",
		ReturnDate:  "2026-06-04",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for invalid depart date, got %d", len(hacks))
	}

	hacks = detectBackToBack(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
		ReturnDate:  "bad-date",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for invalid return date, got %d", len(hacks))
	}
}

func TestDetectBackToBack_longTrip(t *testing.T) {
	// 15-night trip — too long for back-to-back.
	depart := time.Now().AddDate(0, 0, 30)
	ret := depart.AddDate(0, 0, 15)
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        depart.Format("2006-01-02"),
		ReturnDate:  ret.Format("2006-01-02"),
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for 15-night trip, got %d", len(hacks))
	}
}

func TestDetectBackToBack_zeroNights(t *testing.T) {
	// Same-day return — too short.
	depart := time.Now().AddDate(0, 0, 30)
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        depart.Format("2006-01-02"),
		ReturnDate:  depart.Format("2006-01-02"),
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for same-day return, got %d", len(hacks))
	}
}

// --- advisory fallback (when live search fails) ---

// mockSearchFail always returns an error, forcing the advisory path.
func mockSearchFail(_ context.Context, _, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
	return nil, fmt.Errorf("mock search failure")
}

func TestDetectBackToBack_advisoryFallback(t *testing.T) {
	depart := time.Now().AddDate(0, 0, 30)
	ret := depart.AddDate(0, 0, 3)
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           depart.Format("2006-01-02"),
		ReturnDate:     ret.Format("2006-01-02"),
		SearchOverride: mockSearchFail,
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 advisory hack on search failure, got %d", len(hacks))
	}
	h := hacks[0]
	if h.Type != "back_to_back" {
		t.Errorf("expected type back_to_back, got %q", h.Type)
	}
	if h.Savings != 0 {
		t.Errorf("advisory hack should have 0 savings, got %.0f", h.Savings)
	}
	if !strings.Contains(h.Description, "HEL") || !strings.Contains(h.Description, "BCN") {
		t.Errorf("advisory description should mention route, got: %s", h.Description)
	}
	if len(h.Steps) == 0 {
		t.Error("expected non-empty steps")
	}
	if len(h.Risks) == 0 {
		t.Error("expected non-empty risks")
	}
}

func TestDetectBackToBack_advisoryCurrencyDefault(t *testing.T) {
	depart := time.Now().AddDate(0, 0, 30)
	ret := depart.AddDate(0, 0, 3)
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           depart.Format("2006-01-02"),
		ReturnDate:     ret.Format("2006-01-02"),
		SearchOverride: mockSearchFail,
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack, got %d", len(hacks))
	}
	if hacks[0].Currency != "EUR" {
		t.Errorf("expected EUR default, got %q", hacks[0].Currency)
	}
}

func TestDetectBackToBack_advisoryCustomCurrency(t *testing.T) {
	depart := time.Now().AddDate(0, 0, 30)
	ret := depart.AddDate(0, 0, 3)
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           depart.Format("2006-01-02"),
		ReturnDate:     ret.Format("2006-01-02"),
		Currency:       "GBP",
		SearchOverride: mockSearchFail,
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack, got %d", len(hacks))
	}
	if hacks[0].Currency != "GBP" {
		t.Errorf("expected GBP currency, got %q", hacks[0].Currency)
	}
}

// --- live price comparison tests ---

// makeFlightResult builds a FlightSearchResult with one flight at the given price.
func makeFlightResult(price float64, currency, airline string) *models.FlightSearchResult {
	return &models.FlightSearchResult{
		Success: true,
		Count:   1,
		Flights: []models.FlightResult{
			{
				Price:    price,
				Currency: currency,
				Legs: []models.FlightLeg{
					{Airline: airline, AirlineCode: "XX"},
				},
			},
		},
	}
}

// mockSearchWithPrices returns a mock search function that returns different
// prices based on whether ReturnDate is set (round-trip vs one-way).
func mockSearchWithPrices(owOutPrice, owRetPrice, rtOriginPrice, rtDestPrice float64) flights.SearchFunc {
	return func(_ context.Context, origin, dest, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			// Round-trip search. Distinguish by direction.
			// RT from origin: origin is the user's origin
			// RT from dest: origin is the user's destination
			// We detect by checking which direction this is.
			// In the implementation:
			//   RT from origin: SearchFlightsWithClient(ctx, client, origin, dest, departDate, ...)
			//   RT from dest:   SearchFlightsWithClient(ctx, client, dest, origin, returnDate, ...)
			// So if origin param matches user origin -> rtOriginPrice, else rtDestPrice.
			// To keep tests simple, we use date to distinguish:
			// departDate -> rtOriginPrice, returnDate -> rtDestPrice
			if opts.ReturnDate != "" {
				// Check if this looks like RT from origin or RT from dest
				// RT from origin uses departDate, RT from dest uses returnDate
				// We distinguish by looking at the origin parameter
				if origin == "HEL" {
					return makeFlightResult(rtOriginPrice, "EUR", "Finnair"), nil
				}
				return makeFlightResult(rtDestPrice, "EUR", "Vueling"), nil
			}
		}
		// One-way search. Distinguish by direction.
		if origin == "HEL" {
			return makeFlightResult(owOutPrice, "EUR", "Finnair"), nil
		}
		return makeFlightResult(owRetPrice, "EUR", "Vueling"), nil
	}
}

func TestDetectBackToBack_livePrices_savings(t *testing.T) {
	// OW out: 200, OW ret: 180, total = 380
	// RT origin: 130, RT dest: 120, total = 250
	// Savings: 380 - 250 = 130
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           "2026-06-01",
		ReturnDate:     "2026-06-04",
		SearchOverride: mockSearchWithPrices(200, 180, 130, 120),
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack with savings, got %d", len(hacks))
	}
	h := hacks[0]
	if h.Type != "back_to_back" {
		t.Errorf("expected type back_to_back, got %q", h.Type)
	}
	if h.Savings != 130 {
		t.Errorf("expected savings 130, got %.0f", h.Savings)
	}
	if h.Currency != "EUR" {
		t.Errorf("expected EUR currency, got %q", h.Currency)
	}
	// Description should mention concrete prices
	if !strings.Contains(h.Description, "130") || !strings.Contains(h.Description, "120") {
		t.Errorf("description should contain RT prices, got: %s", h.Description)
	}
	if !strings.Contains(h.Description, "200") || !strings.Contains(h.Description, "180") {
		t.Errorf("description should contain OW prices, got: %s", h.Description)
	}
	if len(h.Steps) < 2 {
		t.Errorf("expected at least 2 steps for live hack, got %d", len(h.Steps))
	}
	if len(h.Citations) != 2 {
		t.Errorf("expected 2 citations, got %d", len(h.Citations))
	}
}

func TestDetectBackToBack_livePrices_noSavings(t *testing.T) {
	// OW total = 200 + 180 = 380
	// RT total = 200 + 200 = 400
	// No savings — RT is more expensive.
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           "2026-06-01",
		ReturnDate:     "2026-06-04",
		SearchOverride: mockSearchWithPrices(200, 180, 200, 200),
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks when RT is more expensive, got %d", len(hacks))
	}
}

func TestDetectBackToBack_livePrices_minimalSavings(t *testing.T) {
	// OW total = 200 + 200 = 400
	// RT total = 195 + 195 = 390
	// Savings = 10, ratio = 10/400 = 2.5% — below 5% threshold
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           "2026-06-01",
		ReturnDate:     "2026-06-04",
		SearchOverride: mockSearchWithPrices(200, 200, 195, 195),
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks when savings below threshold, got %d", len(hacks))
	}
}

func TestDetectBackToBack_livePrices_partialSearchFailure(t *testing.T) {
	// One search fails -> falls back to advisory
	var callCount atomic.Int32
	override := func(_ context.Context, _, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
		n := callCount.Add(1)
		if n == 3 {
			return nil, fmt.Errorf("network error")
		}
		return makeFlightResult(200, "EUR", "Finnair"), nil
	}

	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           "2026-06-01",
		ReturnDate:     "2026-06-04",
		SearchOverride: override,
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 advisory hack on partial failure, got %d", len(hacks))
	}
	if hacks[0].Savings != 0 {
		t.Errorf("should fall back to advisory (savings=0), got %.0f", hacks[0].Savings)
	}
}

func TestDetectBackToBack_livePrices_zeroPriceResult(t *testing.T) {
	// One search returns 0 price -> falls back to advisory
	override := func(_ context.Context, origin, _, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if origin == "HEL" && opts.ReturnDate != "" {
			return makeFlightResult(0, "EUR", "Finnair"), nil
		}
		return makeFlightResult(200, "EUR", "Finnair"), nil
	}

	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           "2026-06-01",
		ReturnDate:     "2026-06-04",
		SearchOverride: override,
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 advisory hack on zero price, got %d", len(hacks))
	}
	if hacks[0].Savings != 0 {
		t.Errorf("should fall back to advisory (savings=0), got %.0f", hacks[0].Savings)
	}
}

func TestDetectBackToBack_livePrices_stepsContainDates(t *testing.T) {
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           "2026-06-01",
		ReturnDate:     "2026-06-04",
		SearchOverride: mockSearchWithPrices(300, 280, 150, 140),
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack, got %d", len(hacks))
	}
	// Steps should mention the depart and return dates.
	joined := strings.Join(hacks[0].Steps, " ")
	if !strings.Contains(joined, "2026-06-01") {
		t.Errorf("steps should contain depart date, got: %s", joined)
	}
	if !strings.Contains(joined, "2026-06-04") {
		t.Errorf("steps should contain return date, got: %s", joined)
	}
	// Should also mention the +14 day overlap dates.
	if !strings.Contains(joined, "2026-06-15") {
		t.Errorf("steps should contain overlap date (depart+14), got: %s", joined)
	}
	if !strings.Contains(joined, "2026-06-18") {
		t.Errorf("steps should contain overlap date (return+14), got: %s", joined)
	}
}

func TestDetectBackToBack_livePrices_contextCancelled(t *testing.T) {
	// Cancelled context -> search fails -> advisory
	override := func(ctx context.Context, _, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	hacks := detectBackToBack(ctx, DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           "2026-06-01",
		ReturnDate:     "2026-06-04",
		SearchOverride: override,
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 advisory hack on cancelled context, got %d", len(hacks))
	}
	if hacks[0].Savings != 0 {
		t.Errorf("should fall back to advisory, got savings %.0f", hacks[0].Savings)
	}
}

func TestDetectBackToBack_livePrices_searchCount(t *testing.T) {
	// Verify exactly 4 searches are made.
	var count atomic.Int32
	override := func(_ context.Context, _, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
		count.Add(1)
		return makeFlightResult(200, "EUR", "Finnair"), nil
	}

	detectBackToBack(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           "2026-06-01",
		ReturnDate:     "2026-06-04",
		SearchOverride: override,
	})
	if got := count.Load(); got != 4 {
		t.Errorf("expected exactly 4 search calls, got %d", got)
	}
}

func TestDetectBackToBack_livePrices_savingsRounding(t *testing.T) {
	// OW: 199.5 + 180.3 = 379.8
	// RT: 129.7 + 119.4 = 249.1
	// Savings: 130.7 -> rounds to 131
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           "2026-06-01",
		ReturnDate:     "2026-06-04",
		SearchOverride: mockSearchWithPrices(199.5, 180.3, 129.7, 119.4),
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack, got %d", len(hacks))
	}
	if hacks[0].Savings != 131 {
		t.Errorf("expected savings 131 (rounded), got %.0f", hacks[0].Savings)
	}
}

// --- boundary tests ---

func TestDetectBackToBack_oneNight(t *testing.T) {
	depart := time.Now().AddDate(0, 0, 30)
	ret := depart.AddDate(0, 0, 1)
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           depart.Format("2006-01-02"),
		ReturnDate:     ret.Format("2006-01-02"),
		SearchOverride: mockSearchFail,
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack for 1-night trip, got %d", len(hacks))
	}
}

func TestDetectBackToBack_twoWeekTrip(t *testing.T) {
	depart := time.Now().AddDate(0, 0, 30)
	ret := depart.AddDate(0, 0, 14)
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           depart.Format("2006-01-02"),
		ReturnDate:     ret.Format("2006-01-02"),
		SearchOverride: mockSearchFail,
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack for 14-night trip, got %d", len(hacks))
	}
}

// --- backToBackOverlapDays constant ---

func TestBackToBackOverlapDays(t *testing.T) {
	if backToBackOverlapDays != 14 {
		t.Errorf("expected backToBackOverlapDays=14, got %d", backToBackOverlapDays)
	}
}

// --- currency honesty (display-currency conversion) ---

// TestDetectBackToBack_livePrices_eurTargetPassthrough covers currency
// honesty case (a): an EUR target with EUR-denominated flights passes
// through labelled EUR.
func TestDetectBackToBack_livePrices_eurTargetPassthrough(t *testing.T) {
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           "2026-06-01",
		ReturnDate:     "2026-06-04",
		Currency:       "EUR",
		SearchOverride: mockSearchWithPrices(200, 180, 130, 120),
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack with savings, got %d", len(hacks))
	}
	if hacks[0].Currency != "EUR" {
		t.Errorf("expected Currency=EUR, got %q", hacks[0].Currency)
	}
}

// TestDetectBackToBack_livePrices_nonEURTargetFallsBackToAdvisory covers case
// (b): a non-EUR target with no usable rate table must suppress the priced
// figure rather than show an unconverted or mislabelled one. The detector's
// existing search-failure fallback (advisory, Savings=0, no concrete price)
// is the honest suppression path here. Placeholder currency codes (ZZZ/ZZY)
// are used by convention (see currency_sweep_test.go) since they appear in no
// real exchange-rate table, and the context is cancelled so no live FX fetch
// can populate the cache either.
func TestDetectBackToBack_livePrices_nonEURTargetFallsBackToAdvisory(t *testing.T) {
	override := func(_ context.Context, _, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
		return makeFlightResult(200, "ZZZ", "Finnair"), nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	hacks := detectBackToBack(ctx, DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           "2026-06-01",
		ReturnDate:     "2026-06-04",
		Currency:       "ZZY",
		SearchOverride: override,
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 advisory hack (suppressed price), got %d", len(hacks))
	}
	if hacks[0].Savings != 0 {
		t.Errorf("expected advisory fallback with 0 savings when inconvertible, got %.0f", hacks[0].Savings)
	}
	if hacks[0].Currency != "ZZY" {
		t.Errorf("expected Currency=ZZY (normalized target), got %q", hacks[0].Currency)
	}
}

// TestDetectBackToBack_livePrices_hackCurrencyMatchesTarget covers case (c):
// whenever a live-priced hack surfaces, its Currency field equals the
// normalized target currency.
func TestDetectBackToBack_livePrices_hackCurrencyMatchesTarget(t *testing.T) {
	hacks := detectBackToBack(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           "2026-06-01",
		ReturnDate:     "2026-06-04",
		Currency:       "eur", // lower-case input must normalize to EUR
		SearchOverride: mockSearchWithPrices(200, 180, 130, 120),
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack, got %d", len(hacks))
	}
	if hacks[0].Currency != "EUR" {
		t.Errorf("expected Hack.Currency == target EUR, got %q", hacks[0].Currency)
	}
}
