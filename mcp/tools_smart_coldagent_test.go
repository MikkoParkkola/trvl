package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestColdAgentReachesBothSearchesInTwoCalls replays the MIK-7987 report: an
// agent that knows only the travel tool sends the names it guessed (date,
// origin, a city name for a flight origin). Each search must reach its
// provider, with the right route and date, within two tools/call requests,
// and the only input to the retry is what the first reply said.
func TestColdAgentReachesBothSearchesInTwoCalls(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	date := time.Now().AddDate(0, 1, 0).Format("2006-01-02")

	type flightCall struct{ origin, dest, date string }
	var flightCalls []flightCall
	origFlight := dispatchFlightSearchFunc
	t.Cleanup(func() { dispatchFlightSearchFunc = origFlight })
	dispatchFlightSearchFunc = func(_ context.Context, _ map[string]any, origin, dest, d string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
		flightCalls = append(flightCalls, flightCall{origin, dest, d})
		return &models.FlightSearchResult{Success: true}, nil
	}

	type groundCall struct{ from, to, date string }
	var groundCalls []groundCall
	origGround := searchGroundByNameFunc
	t.Cleanup(func() { searchGroundByNameFunc = origGround })
	searchGroundByNameFunc = func(_ context.Context, from, to, d string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		groundCalls = append(groundCalls, groundCall{from, to, d})
		return &models.GroundSearchResult{Success: true}, nil
	}

	s := NewServer()

	// Flights, call 1: the reported guesses.
	text, _, isErr := callTravel(t, s, map[string]any{
		"intent": "search_flights",
		"params": map[string]any{"origin": "Torino", "destination": "CDG", "date": date},
	})
	if !isErr {
		t.Fatalf("Torino is not a known city; call 1 should fail, got %q", text)
	}
	// The retry is driven only by the reply: it must name the bad value and
	// ask for an IATA code, and the agent supplies Turin's code itself.
	if !strings.Contains(text, "Torino") || !strings.Contains(text, "IATA") {
		t.Fatalf("call 1 reply does not tell the agent what to fix: %q", text)
	}
	text, _, isErr = callTravel(t, s, map[string]any{
		"intent": "search_flights",
		"params": map[string]any{"origin": "TRN", "destination": "CDG", "date": date},
	})
	if isErr {
		t.Fatalf("flight call 2 failed: %q", text)
	}
	if len(flightCalls) != 1 || flightCalls[0] != (flightCall{"TRN", "CDG", date}) {
		t.Fatalf("flight provider calls = %+v, want one TRN->CDG on %s", flightCalls, date)
	}

	// Ground, call 1: flight-style names.
	text, _, isErr = callTravel(t, s, map[string]any{
		"intent": "search_ground",
		"params": map[string]any{"origin": "Torino", "destination": "Lyon", "departure_date": date},
	})
	if isErr {
		t.Fatalf("ground call 1 failed: %q", text)
	}
	if len(groundCalls) != 1 || groundCalls[0] != (groundCall{"Torino", "Lyon", date}) {
		t.Fatalf("ground provider calls = %+v, want one Torino->Lyon on %s", groundCalls, date)
	}
}
