package livecheck

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/watch"
)

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
