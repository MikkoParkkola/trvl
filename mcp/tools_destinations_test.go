package mcp

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/trip"
)

// plan_trip builds its own JSON text copy instead of using
// buildAnnotatedContentBlocks, so it needs its own compactness check. Not
// parallel: planTripFunc is package state.
func TestHandlePlanTrip_TextCopyIsCompactJSON(t *testing.T) {
	flight := trip.PlanFlight{Price: 129, Currency: "EUR", Airline: "Finnair", Flight: "AY1661", Duration: 230, Departure: "2026-12-01T07:30", Arrival: "2026-12-01T10:20", Route: "HEL-BCN"}
	cases := []struct {
		name      string
		result    *trip.PlanResult
		textBlock int
	}{
		{"success", &trip.PlanResult{
			Success: true, Origin: "HEL", Destination: "BCN", DepartDate: "2026-12-01", ReturnDate: "2026-12-05", Nights: 4, Guests: 1,
			OutboundFlights: []trip.PlanFlight{flight},
			Hotels:          []trip.PlanHotel{{Name: "Hotel Test", Rating: 8.6, Reviews: 1200, PerNight: 95, Total: 380, Currency: "EUR"}},
		}, 0},
		{"partial", &trip.PlanResult{
			Success: false, Origin: "HEL", Destination: "BCN", DepartDate: "2026-12-01", Guests: 1,
			OutboundFlights: []trip.PlanFlight{flight},
			Error:           "hotel search failed",
		}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := planTripFunc
			planTripFunc = func(context.Context, trip.PlanInput) (*trip.PlanResult, error) { return tc.result, nil }
			t.Cleanup(func() { planTripFunc = orig })

			content, _, err := handlePlanTrip(context.Background(), map[string]any{
				"origin": "HEL", "destination": "BCN", "depart_date": "2026-12-01", "return_date": "2026-12-05",
			}, nil, nil, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(content) <= tc.textBlock {
				t.Fatalf("content has %d blocks, want JSON at index %d", len(content), tc.textBlock)
			}
			assertCompactJSONOf(t, content[tc.textBlock].Text, tc.result)
		})
	}
}
