package hacks

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// ---------------------------------------------------------------------------
// Shared currency-honesty test seams for ferry_positioning, multimodal_positioning,
// multimodal_open_jaw_ground, and multimodal_skip_flight — the four detectors
// that route every leg through convertCurrency/cheapestFlightPriceInTarget/
// groundLegPriceInTarget (see currency.go). No t.Parallel: the currency seam is
// a shared package-level var, restored sequentially via t.Cleanup.
// ---------------------------------------------------------------------------

// withConvertCurrencyFn swaps the package-level currency conversion seam for
// the duration of a test and restores the original on cleanup.
func withConvertCurrencyFn(t *testing.T, fn func(ctx context.Context, amount float64, from, to string) (float64, string)) {
	t.Helper()

	swapCurrencyConverter(t, fn)

}

// eurToUSDConvert converts EUR->USD at a fixed 1.1 rate and refuses everything
// else, deterministically (no live rate-table lookup, no network).
func eurToUSDConvert(_ context.Context, amount float64, from, to string) (float64, string) {
	if from == "EUR" && to == "USD" {
		return amount * 1.1, "USD"
	}
	return 0, ""
}

// eurToUSDConvertExceptSmall behaves like eurToUSDConvert but refuses to
// convert any EUR amount below 50 — used to force exactly one leg (a static
// ground/ferry estimate) to fail conversion while flight-sized amounts still
// succeed, isolating the "inconvertible leg drops the candidate" path.
func eurToUSDConvertExceptSmall(_ context.Context, amount float64, from, to string) (float64, string) {
	if from == "EUR" && to == "USD" && amount >= 50 {
		return amount * 1.1, "USD"
	}
	return 0, ""
}

func chSearchResult(price float64, currency string) *models.FlightSearchResult {
	return &models.FlightSearchResult{Success: true, Flights: []models.FlightResult{{Price: price, Currency: currency}}}
}

func chFailedGroundSearch(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
	return &models.GroundSearchResult{Success: false}, nil
}

// ---------------------------------------------------------------------------
// ferry_positioning
// ---------------------------------------------------------------------------

// TestDetectFerryPositioning_nonEURTarget_allLegsConvert_correctTotal covers
// case (a): a USD target where every leg (direct flight, TLL ferry static
// estimate, TLL->destination flight) converts cleanly. Only the HEL->TLL route
// is made to produce a flight leg; ARN/RIX flight legs are forced to fail so
// they drop out, isolating one deterministic candidate to check arithmetic on.
func TestDetectFerryPositioning_nonEURTarget_allLegsConvert_correctTotal(t *testing.T) {
	withConvertCurrencyFn(t, eurToUSDConvert)

	hacks := detectFerryPositioning(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
		Currency:    "USD",
		SearchOverride: func(_ context.Context, origin, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
			switch origin {
			case "HEL":
				return chSearchResult(300, "EUR"), nil // direct: 300*1.1=330 USD
			case "TLL":
				return chSearchResult(100, "EUR"), nil // positioning leg: 100*1.1=110 USD
			default:
				return &models.FlightSearchResult{Success: false}, nil // ARN, RIX dropped
			}
		},
		GroundSearchOverride: chFailedGroundSearch, // forces static-EUR-estimate fallback
	})

	if len(hacks) != 1 {
		t.Fatalf("expected exactly 1 hack (TLL route only), got %d: %+v", len(hacks), hacks)
	}
	h := hacks[0]
	if h.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", h.Currency)
	}
	// TLL ferry static estimate is 19 EUR -> 20.9 USD; flight leg 110 USD;
	// total 130.9; direct 330 USD; savings 199.1 -> rounds to 199.
	wantSavings := 199.0
	if h.Savings != wantSavings {
		t.Errorf("Savings = %v, want %v", h.Savings, wantSavings)
	}
}

// TestDetectFerryPositioning_inconvertibleFerryLeg_dropsCandidate covers case
// (b): the ferry static-EUR estimate (19 EUR, below the 50 floor) cannot
// convert to target while the flight legs (>=100 EUR) can — the candidate
// must be dropped rather than mixing currencies, leaving 0 hacks.
func TestDetectFerryPositioning_inconvertibleFerryLeg_dropsCandidate(t *testing.T) {
	withConvertCurrencyFn(t, eurToUSDConvertExceptSmall)

	hacks := detectFerryPositioning(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
		Currency:    "USD",
		SearchOverride: func(_ context.Context, origin, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
			switch origin {
			case "HEL":
				return chSearchResult(300, "EUR"), nil
			case "TLL":
				return chSearchResult(100, "EUR"), nil
			default:
				return &models.FlightSearchResult{Success: false}, nil
			}
		},
		GroundSearchOverride: chFailedGroundSearch,
	})

	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks when ferry leg cannot convert to target, got %d: %+v", len(hacks), hacks)
	}
}

// ---------------------------------------------------------------------------
// multimodal_positioning
// ---------------------------------------------------------------------------

// TestDetectMultiModalPositioning_nonEURTarget_allLegsConvert_correctTotal
// covers case (a) for the ground-then-fly detector.
func TestDetectMultiModalPositioning_nonEURTarget_allLegsConvert_correctTotal(t *testing.T) {
	withConvertCurrencyFn(t, eurToUSDConvert)

	hacks := detectMultiModalPositioning(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
		Currency:    "USD",
		SearchOverride: func(_ context.Context, origin, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
			switch origin {
			case "HEL":
				return chSearchResult(400, "EUR"), nil // direct: 440 USD
			case "TLL":
				return chSearchResult(100, "EUR"), nil // hub->dest: 110 USD
			default:
				return &models.FlightSearchResult{Success: false}, nil // RIX, ARN dropped
			}
		},
		GroundSearchOverride: chFailedGroundSearch, // forces static-EUR-estimate fallback
	})

	if len(hacks) != 1 {
		t.Fatalf("expected exactly 1 hack (TLL hub only), got %d: %+v", len(hacks), hacks)
	}
	h := hacks[0]
	if h.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", h.Currency)
	}
	// TLL ground static estimate is 19 EUR -> 20.9 USD; flight leg 110 USD;
	// total 130.9; direct 440 USD; savings 309.1 -> rounds to 309.
	wantSavings := 309.0
	if h.Savings != wantSavings {
		t.Errorf("Savings = %v, want %v", h.Savings, wantSavings)
	}
}

// TestDetectMultiModalPositioning_inconvertibleGroundLeg_dropsCandidate
// covers case (b): the ground static-EUR estimate (19 EUR) cannot convert
// while flight legs can, so the candidate is dropped -> 0 hacks.
func TestDetectMultiModalPositioning_inconvertibleGroundLeg_dropsCandidate(t *testing.T) {
	withConvertCurrencyFn(t, eurToUSDConvertExceptSmall)

	hacks := detectMultiModalPositioning(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
		Currency:    "USD",
		SearchOverride: func(_ context.Context, origin, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
			switch origin {
			case "HEL":
				return chSearchResult(400, "EUR"), nil
			case "TLL":
				return chSearchResult(100, "EUR"), nil
			default:
				return &models.FlightSearchResult{Success: false}, nil
			}
		},
		GroundSearchOverride: chFailedGroundSearch,
	})

	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks when ground leg cannot convert to target, got %d: %+v", len(hacks), hacks)
	}
}

// ---------------------------------------------------------------------------
// multimodal_open_jaw_ground
// ---------------------------------------------------------------------------

// TestDetectMultiModalOpenJawGround_nonEURTarget_allLegsConvert_correctTotal
// covers case (a), isolating the overnight ZAG hub (which also exercises the
// hotel-bonus conversion) while SPU is dropped via a failed flight leg.
func TestDetectMultiModalOpenJawGround_nonEURTarget_allLegsConvert_correctTotal(t *testing.T) {
	withConvertCurrencyFn(t, eurToUSDConvert)

	hacks := detectMultiModalOpenJawGround(context.Background(), DetectorInput{
		Origin:      "AMS",
		Destination: "DBV",
		Date:        "2026-06-01",
		Currency:    "USD",
		SearchOverride: func(_ context.Context, _, destination, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
			switch destination {
			case "DBV":
				return chSearchResult(500, "EUR"), nil // direct: 550 USD
			case "ZAG":
				return chSearchResult(150, "EUR"), nil // origin->hub: 165 USD
			default:
				return &models.FlightSearchResult{Success: false}, nil // SPU dropped
			}
		},
		GroundSearchOverride: chFailedGroundSearch, // forces static-EUR-estimate fallback
	})

	if len(hacks) != 1 {
		t.Fatalf("expected exactly 1 hack (ZAG hub only), got %d: %+v", len(hacks), hacks)
	}
	h := hacks[0]
	if h.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", h.Currency)
	}
	// ZAG ground static estimate 15 EUR -> 16.5 USD; flight leg 165 USD;
	// total 181.5; direct 550 USD; hotel bonus 60 EUR -> 66 USD;
	// savings = 550 - 181.5 + 66 = 434.5 -> rounds to 434 (banker's-safe: 434.5
	// rounds away from zero to 435 per math.Round; verified below).
	wantSavings := 435.0
	if h.Savings != wantSavings {
		t.Errorf("Savings = %v, want %v", h.Savings, wantSavings)
	}
}

// TestDetectMultiModalOpenJawGround_inconvertibleGroundLeg_dropsCandidate
// covers case (b): the ground static-EUR estimate (15 EUR) cannot convert
// while flight and hotel-bonus amounts (>=50 EUR) can, so the candidate is
// dropped -> 0 hacks.
func TestDetectMultiModalOpenJawGround_inconvertibleGroundLeg_dropsCandidate(t *testing.T) {
	withConvertCurrencyFn(t, eurToUSDConvertExceptSmall)

	hacks := detectMultiModalOpenJawGround(context.Background(), DetectorInput{
		Origin:      "AMS",
		Destination: "DBV",
		Date:        "2026-06-01",
		Currency:    "USD",
		SearchOverride: func(_ context.Context, _, destination, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
			switch destination {
			case "DBV":
				return chSearchResult(500, "EUR"), nil
			case "ZAG":
				return chSearchResult(150, "EUR"), nil
			default:
				return &models.FlightSearchResult{Success: false}, nil
			}
		},
		GroundSearchOverride: chFailedGroundSearch,
	})

	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks when ground leg cannot convert to target, got %d: %+v", len(hacks), hacks)
	}
}

// ---------------------------------------------------------------------------
// multimodal_skip_flight
// ---------------------------------------------------------------------------

func chOvernightGroundResult(price float64, currency string) *models.GroundSearchResult {
	return &models.GroundSearchResult{
		Success: true,
		Routes: []models.GroundRoute{{
			Provider:   "flixbus",
			Type:       "bus",
			Price:      price,
			Currency:   currency,
			Departure:  models.GroundStop{City: "Helsinki", Time: "2026-06-01T22:00"},
			Arrival:    models.GroundStop{City: "Barcelona", Time: "2026-06-02T06:00"},
			BookingURL: "https://example.test/bus",
		}},
	}
}

// TestDetectMultiModalSkipFlight_nonEURTarget_allLegsConvert_correctTotal
// covers case (a): flight, overnight ground route, and hotel bonus all
// convert to a non-EUR target with correct arithmetic.
func TestDetectMultiModalSkipFlight_nonEURTarget_allLegsConvert_correctTotal(t *testing.T) {
	withConvertCurrencyFn(t, eurToUSDConvert)

	hacks := detectMultiModalSkipFlight(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
		Currency:    "USD",
		SearchOverride: func(_ context.Context, _, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
			return chSearchResult(300, "EUR"), nil // 330 USD
		},
		GroundSearchOverride: func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
			return chOvernightGroundResult(100, "EUR"), nil // 110 USD
		},
	})

	if len(hacks) != 1 {
		t.Fatalf("expected exactly 1 hack, got %d: %+v", len(hacks), hacks)
	}
	h := hacks[0]
	if h.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", h.Currency)
	}
	// flight 330 USD - ground 110 USD + hotel bonus (60 EUR -> 66 USD) = 286.
	wantSavings := 286.0
	if h.Savings != wantSavings {
		t.Errorf("Savings = %v, want %v", h.Savings, wantSavings)
	}
}

// TestDetectMultiModalSkipFlight_inconvertibleGroundLeg_dropsCandidate covers
// case (b): the only overnight ground route is priced in a currency the
// conversion seam refuses, so it must be skipped rather than mixed into the
// saving figure -- no route survives -> 0 hacks.
func TestDetectMultiModalSkipFlight_inconvertibleGroundLeg_dropsCandidate(t *testing.T) {
	withConvertCurrencyFn(t, func(_ context.Context, amount float64, from, to string) (float64, string) {
		if from == "EUR" && to == "USD" {
			return amount * 1.1, "USD" // flight + hotel bonus convert fine
		}
		return 0, "" // ground route currency ("XXX") never converts
	})

	hacks := detectMultiModalSkipFlight(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
		Currency:    "USD",
		SearchOverride: func(_ context.Context, _, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
			return chSearchResult(300, "EUR"), nil
		},
		GroundSearchOverride: func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
			return chOvernightGroundResult(100, "XXX"), nil // inconvertible leg
		},
	})

	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks when the only overnight ground route cannot convert to target, got %d: %+v", len(hacks), hacks)
	}
}
