package trip

import (
	"context"
	"testing"
)

// fakeFX converts USD<->EUR at a fixed rate and fails every other pair the
// way destinations.ConvertCurrency does: amount and source currency unchanged.
// Plan conversion tests use it instead of the live exchange-rate API (MIK-8107).
func fakeFX(_ context.Context, amount float64, from, to string) (float64, string) {
	switch {
	case from == to:
		return amount, to
	case from == "USD" && to == "EUR":
		return amount * 0.5, to
	case from == "EUR" && to == "USD":
		return amount * 2, to
	}
	return amount, from
}

func TestConvertPlanFlights_ConvertsPriceAndComparableWithCentRounding(t *testing.T) {
	flights := []PlanFlight{{Price: 199.99, ComparablePrice: 100.01, Currency: "USD"}}
	convertPlanFlights(context.Background(), flights, "EUR", fakeFX)
	f := flights[0]
	if f.Price != 100 || f.ComparablePrice != 50.01 || f.Currency != "EUR" {
		t.Fatalf("got price=%v comparable=%v currency=%q, want 100 / 50.01 / EUR", f.Price, f.ComparablePrice, f.Currency)
	}
}

func TestConvertPlanHotels_ConvertsPerNightAndTotal(t *testing.T) {
	hotels := []PlanHotel{{PerNight: 50, Total: 200, Currency: "USD"}}
	convertPlanHotels(context.Background(), hotels, "EUR", fakeFX)
	h := hotels[0]
	if h.PerNight != 25 || h.Total != 100 || h.Currency != "EUR" {
		t.Fatalf("got perNight=%v total=%v currency=%q, want 25 / 100 / EUR", h.PerNight, h.Total, h.Currency)
	}
}

// MIK-8138: when the rate is unavailable the item keeps its source amount AND
// source currency; it is never relabelled as the target.
func TestConvertPlanHotels_FailedConversionKeepsSourceCurrency(t *testing.T) {
	hotels := []PlanHotel{{PerNight: 50, Total: 200, Currency: "GBP"}}
	convertPlanHotels(context.Background(), hotels, "EUR", fakeFX)
	h := hotels[0]
	if h.PerNight != 50 || h.Total != 200 || h.Currency != "GBP" {
		t.Fatalf("got perNight=%v total=%v currency=%q, want 50 / 200 / GBP", h.PerNight, h.Total, h.Currency)
	}
}

func TestConvertPlanFlights_FailedConversionKeepsSourceCurrency(t *testing.T) {
	flights := []PlanFlight{{Price: 120, ComparablePrice: 140, Currency: "GBP"}}
	convertPlanFlights(context.Background(), flights, "EUR", fakeFX)
	f := flights[0]
	if f.Price != 120 || f.ComparablePrice != 140 || f.Currency != "GBP" {
		t.Fatalf("got price=%v comparable=%v currency=%q, want 120 / 140 / GBP", f.Price, f.ComparablePrice, f.Currency)
	}
}

// A converter that succeeds for the first amount of an item and fails for the
// next must not leave the item half-converted under either label.
func TestConvertPlanHotels_PartialConversionLeavesItemUntouched(t *testing.T) {
	calls := 0
	firstOnly := func(_ context.Context, amount float64, from, to string) (float64, string) {
		calls++
		if calls == 1 {
			return amount * 0.5, to
		}
		return amount, from
	}
	hotels := []PlanHotel{{PerNight: 50, Total: 200, Currency: "USD"}}
	convertPlanHotels(context.Background(), hotels, "EUR", firstOnly)
	h := hotels[0]
	if h.PerNight != 50 || h.Total != 200 || h.Currency != "USD" {
		t.Fatalf("got perNight=%v total=%v currency=%q, want 50 / 200 / USD", h.PerNight, h.Total, h.Currency)
	}
}
