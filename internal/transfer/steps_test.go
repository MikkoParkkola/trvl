package transfer

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/transfer/airportkb"
)

// TestAssembleSteps_GroundingContract is the anti-hallucination guard: every
// Grounded=true step must trace to real route legs or the curated airport KB.
// A step that trvl cannot ground must be flagged Grounded=false so callers
// render it "(estimated)" — never as fact.
func TestAssembleSteps_GroundingContract(t *testing.T) {
	profile, ok := airportkb.Lookup("BCN")
	if !ok {
		t.Fatal("BCN profile must exist")
	}

	route := models.GroundRoute{
		Provider: "Aerobus", Type: "bus", Duration: 35,
		Legs: []models.GroundLeg{{
			Type: "bus", Provider: "Aerobus A1",
			Departure: models.GroundStop{Station: "BCN T1"},
			Arrival:   models.GroundStop{Station: "Pl. Catalunya"}, Duration: 35,
		}},
	}
	steps := AssembleSteps(route, "airport_express", &profile)
	if len(steps) < 2 {
		t.Fatalf("expected KB snippet + leg step, got %d", len(steps))
	}
	for _, s := range steps {
		if !s.Grounded {
			t.Errorf("step from KB+legs must be grounded: %q", s.Text)
		}
		// The grounded text must originate from KB snippet or the leg carrier;
		// it must never invent a terminal absent from inputs.
		if strings.Contains(s.Text, "T2") {
			t.Errorf("step invented terminal T2 not present in inputs: %q", s.Text)
		}
	}
	// First step must be the curated KB exit snippet.
	if !strings.Contains(steps[0].Text, "Aerobus A1") {
		t.Errorf("first step should be the curated KB snippet, got %q", steps[0].Text)
	}
}

func TestAssembleSteps_TaxiWithoutKBIsEstimated(t *testing.T) {
	route := models.GroundRoute{Provider: "taxi", Type: "taxi", Duration: 25}
	steps := AssembleSteps(route, "taxi", nil) // no profile
	if len(steps) != 1 {
		t.Fatalf("want 1 generic taxi step, got %d", len(steps))
	}
	if steps[0].Grounded {
		t.Errorf("taxi step without KB caveat must be flagged Grounded=false (estimated)")
	}
}

func TestAssembleSteps_TaxiWithKBIsGrounded(t *testing.T) {
	profile, _ := airportkb.Lookup("BCN")
	route := models.GroundRoute{Provider: "taxi", Type: "taxi", Duration: 25}
	steps := AssembleSteps(route, "taxi", &profile)
	if len(steps) == 0 || !steps[0].Grounded {
		t.Fatalf("taxi step with curated KB caveat must be grounded, got %+v", steps)
	}
	if !strings.Contains(steps[0].Text, "black-and-yellow") {
		t.Errorf("expected curated BCN taxi caveat, got %q", steps[0].Text)
	}
}

func TestAssembleSteps_SingleSegmentGrounded(t *testing.T) {
	route := models.GroundRoute{
		Provider: "metro", Type: "train", Duration: 48,
		Departure: models.GroundStop{Station: "Aeroport T1"},
		Arrival:   models.GroundStop{Station: "Eixample"},
	}
	steps := AssembleSteps(route, "train", nil)
	if len(steps) != 1 || !steps[0].Grounded {
		t.Fatalf("single-segment transit must yield 1 grounded step, got %+v", steps)
	}
}
