package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

var budgetFlightArgs = map[string]any{"origin": "HEL", "destination": "VIE", "departure_date": "2027-03-10"}

func budgetFlightURL(i int) string { return fmt.Sprintf("https://flights.example/f%03d", i) }

// budgetFlightFixture returns n one-stop flights sorted cheapest first; the
// last (priciest) one is the only nonstop, so a naive tail cut would drop the
// "Nonstop options from" flight the summary names.
func budgetFlightFixture(n, pad int) []models.FlightResult {
	rows := make([]models.FlightResult, n)
	for i := range rows {
		rows[i] = models.FlightResult{
			Price:      float64(100 + i),
			Currency:   "EUR",
			Stops:      1,
			Provider:   "google_flights",
			BookingURL: budgetFlightURL(i) + "?pad=" + strings.Repeat("p", pad),
			Legs: []models.FlightLeg{{
				DepartureAirport: models.AirportInfo{Code: "HEL"},
				ArrivalAirport:   models.AirportInfo{Code: "VIE"},
				Airline:          "Test Air",
				AirlineCode:      "ZZ",
			}},
		}
	}
	rows[n-1].Stops = 0
	return rows
}

func stubFlightSearch(t *testing.T, rows []models.FlightResult) {
	t.Helper()
	orig := dispatchFlightSearchFunc
	t.Cleanup(func() { dispatchFlightSearchFunc = orig })
	dispatchFlightSearchFunc = func(context.Context, map[string]any, string, string, string, flights.SearchOptions) (*models.FlightSearchResult, error) {
		cp := slices.Clone(rows)
		return &models.FlightSearchResult{Success: true, Count: len(cp), TripType: "one_way", Flights: cp}, nil
	}
}

// flightKeys strips the padding so rows compare by identity.
func flightKeys(body map[string]any) []string {
	urls := rowNames(body, "flights", "booking_url")
	for i, u := range urls {
		urls[i], _, _ = strings.Cut(u, "?")
	}
	return urls
}

func TestReplyBudgetSearchFlights(t *testing.T) {
	stubFlightSearch(t, budgetFlightFixture(200, 1000))
	for _, via := range []string{"search_flights", "travel"} {
		t.Run(via, func(t *testing.T) {
			var got groundCall
			if via == "travel" {
				got = callGroundTool(t, "travel", map[string]any{"intent": "search_flights", "params": budgetFlightArgs})
			} else {
				got = callGroundTool(t, "search_flights", budgetFlightArgs)
			}
			if got.isError {
				t.Fatalf("isError: %s", got.errText)
			}
			assertUnderPromise(t, got)
			body := replyBody(t, got)
			keys := flightKeys(body)
			if len(keys) == 0 || len(keys) >= 200 {
				t.Fatalf("flights shown = %d, want a cut between 1 and 199", len(keys))
			}
			if c := bodyInt(body, "count"); c != len(keys) {
				t.Errorf("count = %d, want the %d rows in this response", c, len(keys))
			}
			if o := bodyInt(body, "budget_omitted"); o != 200-len(keys) {
				t.Errorf("budget_omitted = %d, want %d", o, 200-len(keys))
			}
			for _, want := range []string{budgetFlightURL(0), budgetFlightURL(199)} {
				if !slices.Contains(keys, want) {
					t.Errorf("%s (named by the summary) was cut", want)
				}
			}
			if !strings.Contains(got.userText, "Found 200 flights") ||
				!strings.Contains(got.userText, fmt.Sprintf("Showing %d of 200 flights", len(keys))) ||
				!strings.Contains(got.userText, "50 KB") {
				t.Errorf("summary should describe all 200 and say Showing %d of 200 flights and the 50 KB budget:\n%s", len(keys), got.userText)
			}
			assertTextCopyMatches(t, got)
		})
	}
}

func TestReplyBudgetFlightsUnderBudgetUnchanged(t *testing.T) {
	stubFlightSearch(t, budgetFlightFixture(5, 100))
	got := callGroundTool(t, "search_flights", budgetFlightArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	if _, ok := got.structured["budget_omitted"]; ok {
		t.Errorf("budget_omitted present on an under-budget reply")
	}
	if c := bodyInt(got.structured, "count"); c != 5 {
		t.Errorf("count = %d, want 5", c)
	}
	if strings.Contains(got.userText, "Showing") || strings.Contains(got.userText, "50 KB") {
		t.Errorf("under-budget summary changed:\n%s", got.userText)
	}
	want := []string{budgetFlightURL(0), budgetFlightURL(1), budgetFlightURL(2), budgetFlightURL(3), budgetFlightURL(4)}
	if keys := flightKeys(got.structured); !slices.Equal(keys, want) {
		t.Errorf("under-budget flights = %v, want all five in search order", keys)
	}
	assertTextCopyMatches(t, got)
}

func TestReplyBudgetFlightsFloor(t *testing.T) {
	stubFlightSearch(t, budgetFlightFixture(200, 9_000))
	got := callGroundTool(t, "search_flights", budgetFlightArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	assertUnderPromise(t, got)
	if keys := flightKeys(got.structured); strings.Join(keys, ",") != budgetFlightURL(0)+","+budgetFlightURL(199) {
		t.Errorf("floor kept %d rows, want only the cheapest and the cheapest nonstop", len(keys))
	}
}

func TestReplyBudgetFlightsFloorOverBudget(t *testing.T) {
	stubFlightSearch(t, budgetFlightFixture(10, 30_000))
	assertBoundedFloorError(t, callGroundTool(t, "search_flights", budgetFlightArgs))
}
