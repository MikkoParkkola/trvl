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
	// Only the outbound one-way differs (USD) from the EUR rt+baseline+return.
	// Isolates the owOutCur!=baseCur clause: numbers emit if that check is
	// dropped (300 rt - 200 ow = 100 saving).
	withSplitMockSearch(t, func(_ context.Context, origin, dest, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return splitMakeResult(300, "EUR"), nil
		}
		if origin == "HEL" && dest == "BCN" {
			return splitMakeResult(100, "USD"), nil
		}
		return splitMakeResult(100, "EUR"), nil
	})

	hacks := detectSplit(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-07-01",
		ReturnDate:  "2026-07-08",
		Currency:    "EUR",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks for outbound currency mismatch, got %d", len(hacks))
	}
}

func TestDetectSplit_returnCurrencyMismatch_returnsNil(t *testing.T) {
	// Only the return one-way differs (USD). Isolates owRetCur!=baseCur.
	withSplitMockSearch(t, func(_ context.Context, origin, dest, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return splitMakeResult(300, "EUR"), nil
		}
		if origin == "HEL" && dest == "BCN" {
			return splitMakeResult(100, "EUR"), nil
		}
		return splitMakeResult(100, "USD"), nil
	})

	hacks := detectSplit(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-07-01",
		ReturnDate:  "2026-07-08",
		Currency:    "EUR",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks for return currency mismatch, got %d", len(hacks))
	}
}

func TestDetectSplit_allEmptyCurrency_returnsNil(t *testing.T) {
	// Every searched fare AND the baseline currency empty -> unknown -> refuse.
	// Isolates the baseCur=="" guard: with all four empty, the equality checks
	// ("" == "") would pass, so only the empty-baseline check prevents emitting
	// a saving in an unknown currency.
	withSplitMockSearch(t, func(_ context.Context, origin, dest, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return splitMakeResult(300, ""), nil
		}
		if origin == "HEL" && dest == "BCN" {
			return splitMakeResult(100, ""), nil
		}
		return splitMakeResult(100, ""), nil
	})

	hacks := detectSplit(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-07-01",
		ReturnDate:  "2026-07-08",
		Currency:    "",
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
		Currency:    "EUR",
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
	// currency, not [0]). Numbers chosen so the buggy [0]=EUR path would clear
	// the savings threshold and emit (250 rt - 200 ow = 50) — the test only
	// passes because the correct USD read drives a currency mismatch -> nil.
	withSplitMockSearch(t, func(_ context.Context, origin, dest, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return splitMakeMulti([]models.FlightResult{
				{Price: 300, Currency: "EUR"},
				{Price: 250, Currency: "USD"},
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
		Currency:    "EUR",
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
		Currency:    "EUR",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks for savings below threshold, got %d", len(hacks))
	}
}

func TestDetectSplit_baselineCurrencyMismatch_returnsNil(t *testing.T) {
	// All three searched fares agree (EUR), but the naive baseline (in.Currency)
	// is USD. BestSaving subtracts Savings from NaivePrice, so an EUR saving
	// against a USD baseline would be a cross-currency lie -> refuse.
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
		Currency:    "USD",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks when baseline currency differs, got %d", len(hacks))
	}
}
