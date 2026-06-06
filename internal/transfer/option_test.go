package transfer

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func sampleRoutes() []models.GroundRoute {
	return []models.GroundRoute{
		{ // airport express, cheap, direct
			Provider: "Aerobus", Type: "bus", Price: 5.90, Currency: "EUR",
			Duration: 35, Transfers: 0,
			Departure: models.GroundStop{City: "Barcelona", Station: "BCN T1"},
			Arrival:   models.GroundStop{City: "Barcelona", Station: "Pl. Catalunya"},
			Legs: []models.GroundLeg{{Type: "bus", Provider: "Aerobus A1",
				Departure: models.GroundStop{Station: "BCN T1"},
				Arrival:   models.GroundStop{Station: "Pl. Catalunya"}, Duration: 35}},
		},
		{ // metro, cheapest, 2 changes, slow
			Provider: "metro", Type: "train", Price: 5.15, Currency: "EUR",
			Duration: 48, Transfers: 2,
			Departure: models.GroundStop{City: "Barcelona", Station: "Aeroport T1"},
			Arrival:   models.GroundStop{City: "Barcelona", Station: "Eixample"},
		},
		{ // taxi, fast, expensive
			Provider: "taxi", Type: "taxi", Price: 35, Currency: "EUR",
			Duration: 25, Transfers: 0,
			Departure: models.GroundStop{City: "Barcelona", Station: "BCN T1"},
			Arrival:   models.GroundStop{City: "Barcelona", Station: "Hotel Eixample"},
		},
	}
}

func TestBuildOptions_Success(t *testing.T) {
	cmp := BuildOptions(sampleRoutes(), "BCN", "BCN airport", "Hotel Eixample")

	if len(cmp.Options) != 5 { // 3 routes + 2 ride-hail deep-links (Uber, Bolt)
		t.Fatalf("want 5 options (3 routes + 2 ride-hail), got %d", len(cmp.Options))
	}
	if cmp.Cheapest != "metro" { // metro provider, EUR 5.15
		t.Errorf("cheapest mode = %q, want metro (EUR 5.15)", cmp.Cheapest)
	}
	if cmp.Fastest != "taxi" { // 25 min
		t.Errorf("fastest mode = %q, want taxi (25 min)", cmp.Fastest)
	}
	if cmp.LuggageBest != "taxi" {
		t.Errorf("luggage-friendly mode = %q, want taxi", cmp.LuggageBest)
	}
	if cmp.BestValue == "" {
		t.Errorf("best_value should be set")
	}
}

func TestBuildOptions_TaxiPriceFlaggedEstimate(t *testing.T) {
	cmp := BuildOptions(sampleRoutes(), "BCN", "a", "b")
	for _, o := range cmp.Options {
		if o.Mode == "taxi" && !o.PriceIsEstimate {
			t.Errorf("taxi price must be flagged as estimate")
		}
		if o.Mode == "metro" && o.PriceIsEstimate {
			t.Errorf("transit price must not be flagged as estimate")
		}
	}
}

func TestBuildOptions_ProsConsAreSignalBased(t *testing.T) {
	cmp := BuildOptions(sampleRoutes(), "BCN", "a", "b")
	var metro, taxi *models.TransferOption
	for i := range cmp.Options {
		switch cmp.Options[i].Mode {
		case "metro":
			metro = &cmp.Options[i]
		case "taxi":
			taxi = &cmp.Options[i]
		}
	}
	if metro == nil || taxi == nil {
		t.Fatal("expected metro and taxi options")
	}
	if !contains(metro.Pros, "cheapest option") {
		t.Errorf("metro pros should include 'cheapest option', got %v", metro.Pros)
	}
	if !containsSubstr(metro.Cons, "changes") {
		t.Errorf("metro (2 transfers) cons should mention changes, got %v", metro.Cons)
	}
	if !contains(taxi.Pros, "door-to-door, no luggage hassle") {
		t.Errorf("taxi pros should include door-to-door, got %v", taxi.Pros)
	}
	if !containsSubstr(taxi.Cons, "cheapest") { // 35 / 5.15 > 2x
		t.Errorf("taxi cons should flag price multiple, got %v", taxi.Cons)
	}
}

func TestBuildOptions_Empty(t *testing.T) {
	cmp := BuildOptions(nil, "BCN", "a", "b")
	if len(cmp.Options) != 0 || cmp.Cheapest != "" {
		t.Errorf("empty input must yield empty comparison, got %+v", cmp)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func containsSubstr(xs []string, sub string) bool {
	for _, x := range xs {
		if len(x) >= len(sub) && indexOf(x, sub) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
