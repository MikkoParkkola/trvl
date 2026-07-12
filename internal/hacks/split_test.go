package hacks

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func splitMakeResult(price float64, currency string) *models.FlightSearchResult {
	return &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{{Price: price, Currency: currency}},
	}
}

func splitMakeMulti(fls []models.FlightResult) *models.FlightSearchResult {
	return &models.FlightSearchResult{
		Success: true,
		Flights: fls,
	}
}

func withSplitMockSearch(t *testing.T, fn func(context.Context, string, string, string, flights.SearchOptions) (*models.FlightSearchResult, error)) {
	t.Helper()
	original := splitSearchFunc
	splitSearchFunc = fn
	t.Cleanup(func() { splitSearchFunc = original })
}

func TestDetectSplit_currencyMismatch_returnsNil(t *testing.T) {
	// RED core: rt EUR 200, owOut USD 85, owRet USD 85 → detectSplit returns nil
	// (would have lied "Saves EUR 30" before the fix).
	withSplitMockSearch(t, func(_ context.Context, origin, dest, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return splitMakeResult(200, "EUR"), nil
		}
		if origin == "HEL" && dest == "BCN" {
			return splitMakeResult(85, "USD"), nil
		}
		return splitMakeResult(85, "USD"), nil
	})

	hacks := detectSplit(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-07-01",
		ReturnDate:  "2026-07-08",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks for currency mismatch, got %d", len(hacks))
	}
}

func TestDetectSplit_allEmptyCurrency_returnsNil(t *testing.T) {
	withSplitMockSearch(t, func(_ context.Context, origin, dest, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return splitMakeResult(200, ""), nil
		}
		if origin == "HEL" && dest == "BCN" {
			return splitMakeResult(85, ""), nil
		}
		return splitMakeResult(85, ""), nil
	})

	hacks := detectSplit(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-07-01",
		ReturnDate:  "2026-07-08",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks when all currencies empty, got %d", len(hacks))
	}
}

func TestDetectSplit_positiveControl_emitsWithVerifiedCurrency(t *testing.T) {
	withSplitMockSearch(t, func(_ context.Context, origin, dest, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return splitMakeResult(300, "EUR"), nil
		}
		if origin == "HEL" && dest == "BCN" {
			return splitMakeResult(100, "EUR"), nil
		}
		return splitMakeResult(100, "EUR"), nil
	})

	hacks := detectSplit(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-07-01",
		ReturnDate:  "2026-07-08",
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack, got %d", len(hacks))
	}
	h := hacks[0]
	if h.Currency != "EUR" {
		t.Errorf("expected EUR, got %q", h.Currency)
	}
	if h.Savings != 100 {
		t.Errorf("expected savings 100, got %.0f", h.Savings)
	}
	if !strings.Contains(h.Description, "EUR") {
		t.Errorf("description should contain EUR, got: %s", h.Description)
	}
}

func TestDetectSplit_cheapestFlightCurrencyMismatch_returnsNil(t *testing.T) {
	// Result whose Flights[0] is EUR but the cheapest flight is USD must be
	// read as USD (proves minFlightPriceWithCurrency uses the min flight's
	// currency, not [0]). This drives a currency mismatch -> nil.
	withSplitMockSearch(t, func(_ context.Context, origin, dest, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return splitMakeMulti([]models.FlightResult{
				{Price: 250, Currency: "EUR"},
				{Price: 200, Currency: "USD"},
			}), nil
		}
		if origin == "HEL" && dest == "BCN" && date == "2026-07-01" {
			return splitMakeResult(100, "EUR"), nil
		}
		return splitMakeResult(100, "EUR"), nil
	})

	hacks := detectSplit(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-07-01",
		ReturnDate:  "2026-07-08",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks for cheapest-flight currency mismatch, got %d", len(hacks))
	}
}

func TestDetectSplit_belowThreshold_returnsNil(t *testing.T) {
	withSplitMockSearch(t, func(_ context.Context, origin, dest, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return splitMakeResult(300, "EUR"), nil
		}
		if origin == "HEL" && dest == "BCN" {
			return splitMakeResult(145, "EUR"), nil
		}
		return splitMakeResult(145, "EUR"), nil
	})

	hacks := detectSplit(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-07-01",
		ReturnDate:  "2026-07-08",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks for savings below threshold, got %d", len(hacks))
	}
}
