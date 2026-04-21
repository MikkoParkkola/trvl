package flights

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestFilterFlightsByTimePreference_NoBounds(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 100, Legs: []models.FlightLeg{{DepartureTime: "2026-06-15T05:30"}}},
		{Price: 200, Legs: []models.FlightLeg{{DepartureTime: "2026-06-15T14:00"}}},
	}
	got := FilterFlightsByTimePreference(flts, "", "")
	if len(got) != 2 {
		t.Errorf("no bounds: expected 2 flights, got %d", len(got))
	}
}

func TestFilterFlightsByTimePreference_EarliestOnly(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 100, Legs: []models.FlightLeg{{DepartureTime: "2026-06-15T05:30"}}},
		{Price: 200, Legs: []models.FlightLeg{{DepartureTime: "2026-06-15T06:00"}}},
		{Price: 300, Legs: []models.FlightLeg{{DepartureTime: "2026-06-15T14:00"}}},
	}
	got := FilterFlightsByTimePreference(flts, "06:00", "")
	if len(got) != 2 {
		t.Fatalf("earliest 06:00: expected 2 flights, got %d", len(got))
	}
	if got[0].Price != 200 {
		t.Errorf("first flight should be 200, got %.0f", got[0].Price)
	}
}

func TestFilterFlightsByTimePreference_LatestOnly(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 100, Legs: []models.FlightLeg{{DepartureTime: "2026-06-15T14:00"}}},
		{Price: 200, Legs: []models.FlightLeg{{DepartureTime: "2026-06-15T23:30"}}},
	}
	got := FilterFlightsByTimePreference(flts, "", "23:00")
	if len(got) != 1 {
		t.Fatalf("latest 23:00: expected 1 flight, got %d", len(got))
	}
	if got[0].Price != 100 {
		t.Errorf("kept flight should be 100, got %.0f", got[0].Price)
	}
}

func TestFilterFlightsByTimePreference_BothBounds(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 50, Legs: []models.FlightLeg{{DepartureTime: "2026-06-15T04:00"}}},
		{Price: 100, Legs: []models.FlightLeg{{DepartureTime: "2026-06-15T08:30"}}},
		{Price: 200, Legs: []models.FlightLeg{{DepartureTime: "2026-06-15T18:00"}}},
		{Price: 300, Legs: []models.FlightLeg{{DepartureTime: "2026-06-15T23:59"}}},
	}
	got := FilterFlightsByTimePreference(flts, "06:00", "22:00")
	if len(got) != 2 {
		t.Fatalf("06:00-22:00: expected 2 flights, got %d", len(got))
	}
	if got[0].Price != 100 || got[1].Price != 200 {
		t.Errorf("expected prices [100, 200], got [%.0f, %.0f]", got[0].Price, got[1].Price)
	}
}

func TestFilterFlightsByTimePreference_NoLegs_Kept(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 100}, // no legs — keep it
	}
	got := FilterFlightsByTimePreference(flts, "06:00", "23:00")
	if len(got) != 1 {
		t.Errorf("flights with no legs should be kept, got %d", len(got))
	}
}

func TestFilterFlightsByTimePreference_BareHHMM(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 100, Legs: []models.FlightLeg{{DepartureTime: "05:30"}}},
		{Price: 200, Legs: []models.FlightLeg{{DepartureTime: "12:00"}}},
	}
	got := FilterFlightsByTimePreference(flts, "06:00", "")
	if len(got) != 1 || got[0].Price != 200 {
		t.Errorf("bare HH:MM parsing: expected [200], got %v", got)
	}
}

func TestFilterFlightsByTimePreference_SpaceSeparated(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 100, Legs: []models.FlightLeg{{DepartureTime: "2026-06-15 05:30"}}},
		{Price: 200, Legs: []models.FlightLeg{{DepartureTime: "2026-06-15 12:00"}}},
	}
	got := FilterFlightsByTimePreference(flts, "06:00", "")
	if len(got) != 1 || got[0].Price != 200 {
		t.Errorf("space-separated: expected [200], got %v", got)
	}
}

func TestFilterFlightsByBudget_NoBudget(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 500},
		{Price: 1000},
	}
	got := FilterFlightsByBudget(flts, 0)
	if len(got) != 2 {
		t.Errorf("no budget: expected 2 flights, got %d", len(got))
	}
}

func TestFilterFlightsByBudget_WithBudget(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 100},
		{Price: 300},
		{Price: 500},
	}
	got := FilterFlightsByBudget(flts, 300)
	if len(got) != 2 {
		t.Fatalf("budget 300: expected 2 flights, got %d", len(got))
	}
	if got[0].Price != 100 || got[1].Price != 300 {
		t.Errorf("expected [100, 300], got [%.0f, %.0f]", got[0].Price, got[1].Price)
	}
}

func TestFilterFlightsByBudget_ZeroPrice_Kept(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 0},   // unknown price — keep
		{Price: 500}, // over budget
	}
	got := FilterFlightsByBudget(flts, 300)
	if len(got) != 1 {
		t.Fatalf("expected 1 flight (0-price kept), got %d", len(got))
	}
	if got[0].Price != 0 {
		t.Errorf("expected 0-price flight kept, got %.0f", got[0].Price)
	}
}

func TestFirstPricedResult_SkipsZeroPrice(t *testing.T) {
	flts := []models.FlightResult{{Price: 0}, {Price: 150}, {Price: 200}}
	got := FirstPricedResult(flts)
	if len(got) != 1 {
		t.Fatalf("expected 1 flight, got %d", len(got))
	}
	if got[0].Price != 150 {
		t.Errorf("expected price 150, got %.0f", got[0].Price)
	}
}

func TestFirstPricedResult_AllZero(t *testing.T) {
	flts := []models.FlightResult{{Price: 0}, {Price: 0}}
	got := FirstPricedResult(flts)
	if got != nil {
		t.Errorf("all zero prices: expected nil, got %v", got)
	}
}

func TestFirstPricedResult_AllPriced(t *testing.T) {
	flts := []models.FlightResult{{Price: 100}, {Price: 200}}
	got := FirstPricedResult(flts)
	if len(got) != 1 || got[0].Price != 100 {
		t.Errorf("all priced: expected [{Price:100}], got %v", got)
	}
}

func TestExtractDepartureHHMM(t *testing.T) {
	tests := []struct {
		name string
		dt   string
		want string
	}{
		{"ISO datetime", "2026-06-15T10:30", "10:30"},
		{"bare HH:MM", "14:00", "14:00"},
		{"space separated", "2026-06-15 08:45", "08:45"},
		{"empty", "", ""},
		{"no colon", "1430", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := models.FlightResult{}
			if tt.dt != "" {
				f.Legs = []models.FlightLeg{{DepartureTime: tt.dt}}
			}
			got := extractDepartureHHMM(f)
			if got != tt.want {
				t.Errorf("extractDepartureHHMM(%q) = %q, want %q", tt.dt, got, tt.want)
			}
		})
	}
}
