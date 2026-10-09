package trip

import (
	"context"
	"reflect"
	"testing"
)

// planFixture is a two-guest round trip priced in USD; fakeFX converts USD to
// EUR at 0.5 and fails every other pair. Nights is 0 and the destination is not
// in the transfer/tax tables, so meals, transfers and taxes stay out unless a
// test opts in.
func planFixture() (*PlanResult, PlanInput) {
	result := &PlanResult{
		OutboundFlights: []PlanFlight{{Price: 100, ComparablePrice: 120, Currency: "USD"}},
		ReturnFlights:   []PlanFlight{{Price: 80, Currency: "USD"}},
		Hotels:          []PlanHotel{{Total: 400, Currency: "USD"}},
	}
	return result, PlanInput{Destination: "Nowhereville", Guests: 2, Currency: "EUR"}
}

func TestBuildPlanSummary_AllConvertedSumsInTarget(t *testing.T) {
	result, input := planFixture()
	s := buildPlanSummary(context.Background(), result, input, 0, fakeFX)
	if s.Incomplete || len(s.Unconverted) != 0 {
		t.Fatalf("incomplete=%v unconverted=%v, want a complete summary", s.Incomplete, s.Unconverted)
	}
	// Per person all-in 60+40, headline 50+40; two guests; hotel 200.
	if s.FlightsTotal != 180 || s.BaggageTotal != 20 || s.HotelTotal != 200 || s.GrandTotal != 400 || s.PerPerson != 200 || s.Currency != "EUR" {
		t.Fatalf("summary = %+v, want flights 180, baggage 20, hotel 200, grand 400, per person 200 EUR", s)
	}
}

func TestBuildPlanSummary_UnconvertedHotelWithholdsTotals(t *testing.T) {
	result, input := planFixture()
	result.Hotels[0].Currency = "GBP"
	input.Budget = 1
	s := buildPlanSummary(context.Background(), result, input, 0, fakeFX)
	if !s.Incomplete || !reflect.DeepEqual(s.Unconverted, []string{"hotel"}) {
		t.Fatalf("incomplete=%v unconverted=%v, want incomplete with [hotel]", s.Incomplete, s.Unconverted)
	}
	if s.GrandTotal != 0 || s.PerPerson != 0 || s.PerDay != 0 || s.HotelTotal != 0 {
		t.Fatalf("summary = %+v, want grand/per-person/per-day/hotel withheld as 0", s)
	}
	if s.FlightsTotal != 180 {
		t.Errorf("flights total = %v, want the converted 180 kept", s.FlightsTotal)
	}
	if s.OverBudget || s.BudgetMessage != "" {
		t.Errorf("an incomplete summary must not give a budget verdict: %+v", s)
	}
}

func TestBuildPlanSummary_BlankHotelCurrencyIsUnconverted(t *testing.T) {
	result, input := planFixture()
	result.Hotels[0].Currency = ""
	s := buildPlanSummary(context.Background(), result, input, 0, fakeFX)
	if !s.Incomplete || !reflect.DeepEqual(s.Unconverted, []string{"hotel"}) || s.GrandTotal != 0 {
		t.Fatalf("summary = %+v, want incomplete with [hotel] and no grand total", s)
	}
}

func TestBuildPlanSummary_RoundTripFareCoversAFailedLeg(t *testing.T) {
	result, input := planFixture()
	result.ReturnFlights[0].Currency = "GBP"
	result.RoundTripFares = []PlanFlight{{Price: 300, Currency: "USD"}}
	s := buildPlanSummary(context.Background(), result, input, 0, fakeFX)
	if s.Incomplete {
		t.Fatalf("unconverted=%v, want the converted round-trip fare to stand in", s.Unconverted)
	}
	// Round trip 150 per person, two guests, hotel 200.
	if s.FlightsTotal != 300 || s.GrandTotal != 500 {
		t.Fatalf("summary = %+v, want flights 300 and grand 500", s)
	}
}

func TestBuildPlanSummary_NoConvertibleFlights(t *testing.T) {
	result, input := planFixture()
	result.OutboundFlights[0].Currency = "GBP"
	s := buildPlanSummary(context.Background(), result, input, 0, fakeFX)
	if !s.Incomplete || !reflect.DeepEqual(s.Unconverted, []string{"flights"}) || s.FlightsTotal != 0 || s.GrandTotal != 0 {
		t.Fatalf("summary = %+v, want incomplete with [flights] and no flight or grand total", s)
	}
}

func TestBuildPlanSummary_UnconvertedTransfers(t *testing.T) {
	result, input := planFixture()
	for i := range result.OutboundFlights {
		result.OutboundFlights[i].Currency = "GBP"
	}
	result.ReturnFlights[0].Currency = "GBP"
	result.Hotels[0].Currency = "GBP"
	input.Currency, input.Destination = "GBP", "Paris"
	// Flights and hotel are already in GBP; the EUR transfer table is not.
	s := buildPlanSummary(context.Background(), result, input, 0, fakeFX)
	if !s.Incomplete || !reflect.DeepEqual(s.Unconverted, []string{"transfers"}) || s.TransfersEstimated || s.GrandTotal != 0 {
		t.Fatalf("summary = %+v, want incomplete with [transfers] and no grand total", s)
	}
}

func TestBuildPlanSummary_NoRequestedCurrencyUsesFirstFlight(t *testing.T) {
	result, input := planFixture()
	input.Currency = ""
	s := buildPlanSummary(context.Background(), result, input, 0, fakeFX)
	// Everything is already USD, so nothing converts and the total is in USD.
	if s.Incomplete || s.Currency != "USD" || s.GrandTotal != 800 {
		t.Fatalf("summary = %+v, want a complete USD summary of 800", s)
	}
}
