package scoring_test

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/scoring"
)

// #473: the time-hard-floor factor tiers a departure into inside / soft
// near-miss / beyond-floor, and stays neutral when it carries no signal.
func TestScoreTimeHardFloorCompliance_Tiers(t *testing.T) {
	mk := func(hardFloor int, depart string) float64 {
		p := preferences.Default()
		p.FlightTimeEarliest = "06:00"
		p.FlightTimeLatest = "22:00"
		p.FlightTimeHardFloor = hardFloor
		in := scoring.DiscoverInput{AirportCode: "BCN", CityName: "Barcelona", DepartTime: depart}
		_, breakdown := scoring.ComputeProfileMatch(p, in)
		v, ok := breakdown[scoring.FactorTimeHardFloorCompliance]
		if !ok {
			t.Fatalf("breakdown missing %s", scoring.FactorTimeHardFloorCompliance)
		}
		return v
	}

	if got := mk(30, "08:00"); got != 1.0 {
		t.Errorf("inside window: got %.2f, want 1.0", got)
	}
	if got := mk(30, "05:45"); got != 0.3 { // 15 min before earliest, within 30
		t.Errorf("soft near-miss: got %.2f, want 0.3", got)
	}
	if got := mk(30, "22:20"); got != 0.3 { // 20 min after latest, within 30
		t.Errorf("soft near-miss (late): got %.2f, want 0.3", got)
	}
	if got := mk(30, "04:00"); got != 0.0 { // 120 min before, beyond 30
		t.Errorf("beyond floor: got %.2f, want 0.0", got)
	}
	if got := mk(0, "04:00"); got != 0.5 { // no floor configured -> neutral
		t.Errorf("no hard floor: got %.2f, want 0.5", got)
	}
	if got := mk(30, ""); got != 0.5 { // unknown departure -> neutral
		t.Errorf("unknown departure: got %.2f, want 0.5", got)
	}
}

// #473: the factor is wired into DefaultWeights so it contributes to the score.
func TestDefaultWeights_IncludesHardFloor(t *testing.T) {
	w := scoring.DefaultWeights()
	if _, ok := w[scoring.FactorTimeHardFloorCompliance]; !ok {
		t.Errorf("DefaultWeights missing %s", scoring.FactorTimeHardFloorCompliance)
	}
}
