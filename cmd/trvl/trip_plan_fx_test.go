package main

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trip"
)

// stubTripPlanFX converts USD to EUR at 0.5 and fails every other pair the way
// destinations.ConvertCurrency does. Not for parallel tests: it swaps a var.
func stubTripPlanFX(t *testing.T) {
	t.Helper()
	prev := tripPlanConvert
	t.Cleanup(func() { tripPlanConvert = prev })
	tripPlanConvert = func(_ context.Context, amount float64, from, to string) (float64, string) {
		if from == "" {
			return amount, to // what destinations.ConvertCurrency does
		}
		if from == "USD" && to == "EUR" {
			return amount * 0.5, to
		}
		return amount, from
	}
}

func fxPlan(hotelCurrency string) *trip.PlanResult {
	return &trip.PlanResult{
		Success: true, Origin: "HEL", Destination: "BCN", Nights: 2, Guests: 2,
		OutboundFlights: []trip.PlanFlight{{Price: 100, Currency: "USD", Airline: "Finnair"}},
		ReturnFlights:   []trip.PlanFlight{{Price: 80, Currency: "USD", Airline: "Finnair"}},
		Hotels:          []trip.PlanHotel{{Name: "Hotel", PerNight: 200, Total: 400, Currency: hotelCurrency}},
		Summary:         trip.PlanSummary{Currency: "EUR"},
	}
}

func TestPrintTripPlan_UnconvertedHotelWithholdsTotal(t *testing.T) {
	models.UseColor = false
	stubTripPlanFX(t)
	out := captureStdout(t, func() {
		if err := printTripPlan(cancelledTestContext(t), "EUR", fxPlan("GBP")); err != nil {
			t.Errorf("printTripPlan: %v", err)
		}
	})
	if !strings.Contains(out, "Total unavailable: could not convert hotel to EUR") {
		t.Errorf("output does not withhold the total:\n%s", out)
	}
	if strings.Contains(out, "= EUR") {
		t.Errorf("output still sums a GBP hotel into an EUR total:\n%s", out)
	}
}

func TestPrintTripPlan_AllConvertedPrintsTotal(t *testing.T) {
	models.UseColor = false
	stubTripPlanFX(t)
	out := captureStdout(t, func() {
		if err := printTripPlan(cancelledTestContext(t), "EUR", fxPlan("USD")); err != nil {
			t.Errorf("printTripPlan: %v", err)
		}
	})
	// Flights (50+40)x2 = 180, hotel 200, total 380.
	if !strings.Contains(out, "Flights: EUR 180 + Hotel: EUR 200") || !strings.Contains(out, "EUR 380") {
		t.Errorf("output lacks the converted total:\n%s", out)
	}
}

func TestSaveTripPlanLastSearch_IncompleteCachesNoTotal(t *testing.T) {
	withTempHome(t)
	r := fxPlan("GBP")
	r.Summary = trip.PlanSummary{Currency: "EUR", Incomplete: true, Unconverted: []string{"hotel"}}
	saveTripPlanLastSearch(r)
	ls, err := loadLastSearch()
	if err != nil {
		t.Fatalf("loadLastSearch: %v", err)
	}
	if ls.TotalPrice != 0 || ls.TotalCurrency != "" {
		t.Errorf("cached total = %v %q, want none for an incomplete plan", ls.TotalPrice, ls.TotalCurrency)
	}
	if ls.HotelPrice != 400 || ls.HotelCurrency != "GBP" {
		t.Errorf("cached hotel = %v %q, want 400 GBP", ls.HotelPrice, ls.HotelCurrency)
	}
}

func printPlan(t *testing.T, target string, r *trip.PlanResult) string {
	t.Helper()
	return captureStdout(t, func() {
		if err := printTripPlan(cancelledTestContext(t), target, r); err != nil {
			t.Errorf("printTripPlan: %v", err)
		}
	})
}

func TestPrintTripPlan_UnconvertedFlightWithholdsTotal(t *testing.T) {
	models.UseColor = false
	stubTripPlanFX(t)
	r := fxPlan("USD")
	r.ReturnFlights[0].Currency = "GBP"
	out := printPlan(t, "EUR", r)
	if !strings.Contains(out, "Total unavailable: could not convert flights to EUR") {
		t.Errorf("output does not withhold the total:\n%s", out)
	}
}

func TestPrintTripPlan_RoundTripFareStandsInForFailedLeg(t *testing.T) {
	models.UseColor = false
	stubTripPlanFX(t)
	r := fxPlan("USD")
	r.ReturnFlights[0].Currency = "GBP"
	r.RoundTripFares = []trip.PlanFlight{{Price: 300, Currency: "USD", Airline: "Finnair", Route: "HEL-BCN-HEL"}}
	out := printPlan(t, "EUR", r)
	// Round trip 150 x 2 guests, hotel 200, total 500.
	if !strings.Contains(out, "Flights: EUR 300 + Hotel: EUR 200") || !strings.Contains(out, "EUR 500") {
		t.Errorf("output lacks the round-trip total:\n%s", out)
	}
}

func TestPrintTripPlan_FailedHotelKeepsSourceTotal(t *testing.T) {
	models.UseColor = false
	stubTripPlanFX(t)
	r := fxPlan("GBP")
	r.Nights = 7
	r.Hotels[0].PerNight, r.Hotels[0].Total = 100.49, 703.43
	out := printPlan(t, "EUR", r)
	if !strings.Contains(out, formatPrice(703.43, "GBP")) || strings.Contains(out, formatPrice(700, "GBP")) {
		t.Errorf("hotel row must show the source total %s:\n%s", formatPrice(703.43, "GBP"), out)
	}
}

func TestPrintTripPlan_BlankSourceCurrencyIsNotRelabelled(t *testing.T) {
	models.UseColor = false
	stubTripPlanFX(t)
	r := fxPlan("USD")
	r.OutboundFlights[0].Currency = ""
	r.OutboundFlights[0].Price = 123
	out := printPlan(t, "EUR", r)
	if strings.Contains(out, formatPrice(123, "EUR")) {
		t.Errorf("a price with no currency was labelled EUR:\n%s", out)
	}
	if !strings.Contains(out, "Total unavailable") {
		t.Errorf("a price with no currency must not be summed:\n%s", out)
	}
}

func TestPrintTripPlan_NoTargetMixedCurrenciesWithholdsTotal(t *testing.T) {
	models.UseColor = false
	stubTripPlanFX(t)
	r := fxPlan("GBP")
	r.Summary.Currency = "USD"
	out := printPlan(t, "", r)
	if !strings.Contains(out, "Total unavailable: could not convert hotel to USD") {
		t.Errorf("mixed currencies without a target must not be summed:\n%s", out)
	}
}
