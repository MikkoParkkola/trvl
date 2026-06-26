package flights

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestFilterByAirline(t *testing.T) {
	ay := models.FlightResult{Price: 100, Legs: []models.FlightLeg{{AirlineCode: "AY"}}}
	af := models.FlightResult{Price: 200, Legs: []models.FlightLeg{{AirlineCode: "AF"}}}
	// Multi-leg flight where only a connecting leg matches the requested code.
	klmConnect := models.FlightResult{Price: 300, Legs: []models.FlightLeg{
		{AirlineCode: "AY"}, {AirlineCode: "KL"},
	}}
	noLegs := models.FlightResult{Price: 400}

	tests := []struct {
		name     string
		flights  []models.FlightResult
		airlines []string
		want     []float64 // expected prices, in order
	}{
		{
			name:     "empty filter is a no-op",
			flights:  []models.FlightResult{ay, af},
			airlines: nil,
			want:     []float64{100, 200},
		},
		{
			name:     "all-blank filter is a no-op",
			flights:  []models.FlightResult{ay, af},
			airlines: []string{"", "  "},
			want:     []float64{100, 200},
		},
		{
			name:     "single code narrows to matching carrier",
			flights:  []models.FlightResult{ay, af},
			airlines: []string{"AF"},
			want:     []float64{200},
		},
		{
			name:     "match is case-insensitive and whitespace-trimmed",
			flights:  []models.FlightResult{ay, af},
			airlines: []string{" af "},
			want:     []float64{200},
		},
		{
			name:     "multiple codes keep any match",
			flights:  []models.FlightResult{ay, af, klmConnect},
			airlines: []string{"AF", "KL"},
			want:     []float64{200, 300},
		},
		{
			name:     "connecting leg satisfies the restriction",
			flights:  []models.FlightResult{af, klmConnect},
			airlines: []string{"KL"},
			want:     []float64{300},
		},
		{
			name:     "no match yields empty slice",
			flights:  []models.FlightResult{ay, af},
			airlines: []string{"LH"},
			want:     nil,
		},
		{
			name:     "flight without legs is dropped when filter active",
			flights:  []models.FlightResult{ay, noLegs},
			airlines: []string{"AY"},
			want:     []float64{100},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterByAirline(tt.flights, tt.airlines)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d flights, want %d (%v)", len(got), len(tt.want), tt.want)
			}
			for i, price := range tt.want {
				if got[i].Price != price {
					t.Errorf("flight[%d].Price = %.0f, want %.0f", i, got[i].Price, price)
				}
			}
		})
	}
}

// TestFilterByAirline_DoesNotMutateInput guarantees the helper is safe to call
// on a shared result slice without corrupting the caller's data.
func TestFilterByAirline_DoesNotMutateInput(t *testing.T) {
	in := []models.FlightResult{
		{Price: 100, Legs: []models.FlightLeg{{AirlineCode: "AY"}}},
		{Price: 200, Legs: []models.FlightLeg{{AirlineCode: "AF"}}},
	}
	_ = FilterByAirline(in, []string{"AY"})
	if len(in) != 2 || in[0].Price != 100 || in[1].Price != 200 {
		t.Fatalf("input slice was mutated: %+v", in)
	}
}
