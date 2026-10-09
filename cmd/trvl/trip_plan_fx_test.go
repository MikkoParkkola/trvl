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
