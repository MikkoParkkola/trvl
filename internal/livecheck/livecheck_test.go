package livecheck

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// TestCheapest covers the shared lowest-positive-price selector that the
// flight, date-grid, and hotel checks all delegate to. The selection rules are
// load-bearing: a 0/negative price (an honest "no price found" from a provider)
// must never displace a real positive price, and the first positive price wins
// ties so the result is deterministic.
func TestCheapest(t *testing.T) {
	price := func(f models.FlightResult) float64 { return f.Price }

	cases := []struct {
		name     string
		in       []models.FlightResult
		wantPx   float64
		wantCurr string
	}{
		{
			name:     "single element",
			in:       []models.FlightResult{{Price: 100, Currency: "EUR"}},
			wantPx:   100,
			wantCurr: "EUR",
		},
		{
			name:     "lowest positive wins",
			in:       []models.FlightResult{{Price: 300, Currency: "EUR"}, {Price: 120, Currency: "USD"}, {Price: 250, Currency: "GBP"}},
			wantPx:   120,
			wantCurr: "USD",
		},
		{
			name:     "zero never displaces a positive",
			in:       []models.FlightResult{{Price: 200, Currency: "EUR"}, {Price: 0, Currency: "SEK"}},
			wantPx:   200,
			wantCurr: "EUR",
		},
		{
			name:     "leading zero replaced by later positive",
			in:       []models.FlightResult{{Price: 0, Currency: "SEK"}, {Price: 175, Currency: "EUR"}},
			wantPx:   175,
			wantCurr: "EUR",
		},
		{
			name:     "negative price ignored",
			in:       []models.FlightResult{{Price: -50, Currency: "DKK"}, {Price: 90, Currency: "EUR"}},
			wantPx:   90,
			wantCurr: "EUR",
		},
		{
			name:     "all non-positive falls back to first",
			in:       []models.FlightResult{{Price: 0, Currency: "NOK"}, {Price: -1, Currency: "DKK"}},
			wantPx:   0,
			wantCurr: "NOK",
		},
		{
			name:     "first positive wins ties",
			in:       []models.FlightResult{{Price: 100, Currency: "USD"}, {Price: 100, Currency: "GBP"}},
			wantPx:   100,
			wantCurr: "USD",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cheapest(tc.in, price)
			if got.Price != tc.wantPx || got.Currency != tc.wantCurr {
				t.Errorf("cheapest: got {%v %q}, want {%v %q}", got.Price, got.Currency, tc.wantPx, tc.wantCurr)
			}
		})
	}
}

// TestChecker_UnsupportedType is deterministic and hits no network: an
// unsupported watch type must return an error rather than a fabricated price.
func TestChecker_UnsupportedType(t *testing.T) {
	t.Parallel()
	price, _, _, err := Checker{}.CheckPrice(context.Background(), watch.Watch{Type: "bus"})
	if err == nil {
		t.Fatal("expected error for unsupported watch type")
	}
	if price != 0 {
		t.Errorf("price = %f, want 0 on error", price)
	}
}

func TestChecker_FlightSpecificDateHonorsCancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	price, currency, cheapestDate, err := Checker{}.CheckPrice(ctx, watch.Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		Currency:    "EUR",
	})
	if err == nil {
		t.Fatal("expected cancelled context error")
	}
	if price != 0 || currency != "" || cheapestDate != "" {
		t.Fatalf("price/currency/date = %.2f/%q/%q, want zero values on error", price, currency, cheapestDate)
	}
}

func TestChecker_FlightRangeWithCancelledContextReturnsNoFabricatedPrice(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	price, currency, cheapestDate, err := Checker{}.CheckPrice(ctx, watch.Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartFrom:  "2026-07-01",
		DepartTo:    "2026-07-07",
		Currency:    "EUR",
	})
	// Calendar search may normalize a cancelled/empty result to nil error; the
	// invariant here is that no price is fabricated.
	_ = err
	if price != 0 || currency != "" || cheapestDate != "" {
		t.Fatalf("price/currency/date = %.2f/%q/%q, want zero values", price, currency, cheapestDate)
	}
}

func TestChecker_HotelHonorsCancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	price, currency, cheapestDate, err := Checker{}.CheckPrice(ctx, watch.Watch{
		Type:        "hotel",
		Destination: "Helsinki",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-03",
		Currency:    "EUR",
	})
	if err == nil {
		t.Fatal("expected cancelled context error")
	}
	if price != 0 || currency != "" || cheapestDate != "" {
		t.Fatalf("price/currency/date = %.2f/%q/%q, want zero values on error", price, currency, cheapestDate)
	}
}
