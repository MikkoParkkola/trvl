package flights

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func leg(dt string) models.FlightLeg { return models.FlightLeg{DepartureTime: dt} }

// #473: with a non-zero tolerance, a departure just outside the window is kept
// but flagged as a soft near-miss; one far outside is hard-dropped.
func TestFilterFlightsByTimeWindow_SoftAndHard(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 100, Legs: []models.FlightLeg{leg("2026-06-15T08:00")}}, // inside [06:00,22:00]
		{Price: 200, Legs: []models.FlightLeg{leg("2026-06-15T05:45")}}, // 15 min before -> soft (tol 30)
		{Price: 300, Legs: []models.FlightLeg{leg("2026-06-15T22:20")}}, // 20 min after  -> soft (tol 30)
		{Price: 400, Legs: []models.FlightLeg{leg("2026-06-15T04:00")}}, // 120 min before -> hard drop
	}
	kept, soft, hard := FilterFlightsByTimeWindow(flts, "06:00", "22:00", 30)

	if len(kept) != 3 {
		t.Fatalf("kept: expected 3, got %d (%+v)", len(kept), kept)
	}
	if kept[0].Price != 100 || kept[1].Price != 200 || kept[2].Price != 300 {
		t.Errorf("kept order/prices wrong: got [%.0f %.0f %.0f]", kept[0].Price, kept[1].Price, kept[2].Price)
	}
	if len(soft) != 2 || soft[0].Price != 200 || soft[1].Price != 300 {
		t.Errorf("soft: expected [200 300], got %+v", soft)
	}
	if len(hard) != 1 || hard[0].Price != 400 {
		t.Errorf("hard: expected [400], got %+v", hard)
	}
}

// #473: tolerance boundary is inclusive — a departure exactly hardFloor minutes
// outside is a soft near-miss, one minute further is hard-dropped.
func TestFilterFlightsByTimeWindow_ToleranceBoundary(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 1, Legs: []models.FlightLeg{leg("2026-06-15T05:30")}}, // exactly 30 before -> soft
		{Price: 2, Legs: []models.FlightLeg{leg("2026-06-15T05:29")}}, // 31 before -> hard
	}
	kept, soft, hard := FilterFlightsByTimeWindow(flts, "06:00", "22:00", 30)
	if len(kept) != 1 || kept[0].Price != 1 {
		t.Errorf("kept: expected [1], got %+v", kept)
	}
	if len(soft) != 1 || soft[0].Price != 1 {
		t.Errorf("soft: expected [1], got %+v", soft)
	}
	if len(hard) != 1 || hard[0].Price != 2 {
		t.Errorf("hard: expected [2], got %+v", hard)
	}
}

// #473: zero tolerance reproduces the legacy hard cutoff, and the
// FilterFlightsByTimePreference wrapper returns exactly the kept set.
func TestFilterFlightsByTimeWindow_ZeroToleranceIsLegacy(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 100, Legs: []models.FlightLeg{leg("2026-06-15T05:59")}}, // 1 min before -> dropped
		{Price: 200, Legs: []models.FlightLeg{leg("2026-06-15T06:00")}}, // edge -> inside
		{Price: 300, Legs: []models.FlightLeg{leg("2026-06-15T14:00")}}, // inside
	}
	kept, soft, hard := FilterFlightsByTimeWindow(flts, "06:00", "22:00", 0)
	if len(kept) != 2 || kept[0].Price != 200 || kept[1].Price != 300 {
		t.Errorf("kept: expected [200 300], got %+v", kept)
	}
	if len(soft) != 0 {
		t.Errorf("soft: expected none at zero tolerance, got %+v", soft)
	}
	if len(hard) != 1 || hard[0].Price != 100 {
		t.Errorf("hard: expected [100], got %+v", hard)
	}

	// Wrapper parity: same input through the legacy entry point.
	wrap := FilterFlightsByTimePreference(flts, "06:00", "22:00")
	if len(wrap) != len(kept) {
		t.Fatalf("wrapper returned %d, kept was %d", len(wrap), len(kept))
	}
	for i := range wrap {
		if wrap[i].Price != kept[i].Price {
			t.Errorf("wrapper[%d]=%.0f, kept[%d]=%.0f", i, wrap[i].Price, i, kept[i].Price)
		}
	}
}

// #473: the return (inbound) leg is subject to the same tolerance — an early
// inbound within tolerance is soft, beyond it is hard-dropped.
func TestFilterFlightsByTimeWindow_ReturnLeg(t *testing.T) {
	flts := []models.FlightResult{
		{
			Price: 500,
			Legs: []models.FlightLeg{
				{Direction: "outbound", DepartureTime: "2026-07-01T11:00"},
				{Direction: "inbound", DepartureTime: "2026-07-08T09:40"}, // 20 min before 10:00 -> soft
			},
		},
		{
			Price: 600,
			Legs: []models.FlightLeg{
				{Direction: "outbound", DepartureTime: "2026-07-01T11:00"},
				{Direction: "inbound", DepartureTime: "2026-07-08T07:00"}, // 180 min before -> hard
			},
		},
	}
	kept, soft, hard := FilterFlightsByTimeWindow(flts, "10:00", "", 30)
	if len(kept) != 1 || kept[0].Price != 500 {
		t.Errorf("kept: expected [500], got %+v", kept)
	}
	if len(soft) != 1 || soft[0].Price != 500 {
		t.Errorf("soft: expected [500], got %+v", soft)
	}
	if len(hard) != 1 || hard[0].Price != 600 {
		t.Errorf("hard: expected [600], got %+v", hard)
	}
}
