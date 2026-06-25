package trip

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestCheaperPerPersonFlights proves the summary-preference logic: a native
// single-ticket round-trip fare is used only when present AND strictly cheaper
// than the two-one-way total; otherwise the two-one-way total is returned
// byte-identically (the pre-native behaviour).
func TestCheaperPerPersonFlights(t *testing.T) {
	tests := []struct {
		name            string
		twoOneWays      float64
		nativeRoundTrip float64
		want            float64
	}{
		{name: "native cheaper wins", twoOneWays: 300, nativeRoundTrip: 250, want: 250},
		{name: "native pricier falls back", twoOneWays: 300, nativeRoundTrip: 350, want: 300},
		{name: "native equal falls back (not strictly cheaper)", twoOneWays: 300, nativeRoundTrip: 300, want: 300},
		{name: "native absent (zero) falls back", twoOneWays: 300, nativeRoundTrip: 0, want: 300},
		{name: "native negative ignored", twoOneWays: 300, nativeRoundTrip: -10, want: 300},
		{name: "both zero", twoOneWays: 0, nativeRoundTrip: 0, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cheaperPerPersonFlights(tt.twoOneWays, tt.nativeRoundTrip)
			if got != tt.want {
				t.Errorf("cheaperPerPersonFlights(%v, %v) = %v, want %v",
					tt.twoOneWays, tt.nativeRoundTrip, got, tt.want)
			}
		})
	}
}

// TestExtractTopRoundTripFares proves only native FareRoundTrip results are
// kept (composed split-ticket pairs are excluded — they are already represented
// by the split outbound/return view), sorted cheapest-first, zero-price filtered,
// and that the inbound leg is surfaced in a both-directions Route.
func TestExtractTopRoundTripFares(t *testing.T) {
	flts := []models.FlightResult{
		{
			Price: 280, Currency: "EUR", FareType: models.FareRoundTrip, Stops: 0,
			Legs: []models.FlightLeg{
				{Direction: "outbound", Airline: "Vueling", FlightNumber: "VY1001", DepartureTime: "08:00", ArrivalTime: "11:00",
					DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "BCN"}},
				{Direction: "inbound", Airline: "Vueling", FlightNumber: "VY1002", DepartureTime: "18:00", ArrivalTime: "21:00",
					DepartureAirport: models.AirportInfo{Code: "BCN"}, ArrivalAirport: models.AirportInfo{Code: "HEL"}},
			},
		},
		{
			Price: 200, Currency: "EUR", FareType: models.FareRoundTrip, Stops: 0,
			Legs: []models.FlightLeg{
				{Direction: "outbound", Airline: "Finnair", FlightNumber: "AY100", DepartureTime: "07:00", ArrivalTime: "10:00",
					DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "BCN"}},
				{Direction: "inbound", Airline: "Finnair", FlightNumber: "AY101", DepartureTime: "19:00", ArrivalTime: "22:00",
					DepartureAirport: models.AirportInfo{Code: "BCN"}, ArrivalAirport: models.AirportInfo{Code: "HEL"}},
			},
		},
		// Composed split-ticket pair — must be excluded.
		{
			Price: 150, Currency: "EUR", FareType: models.FareSplitTickets,
			Legs: []models.FlightLeg{
				{Direction: "outbound", Airline: "Ryanair", FlightNumber: "FR1", DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "BCN"}},
			},
		},
		// One-way (no FareType) — must be excluded.
		{
			Price: 90, Currency: "EUR",
			Legs: []models.FlightLeg{
				{Airline: "Wizz", FlightNumber: "W6", DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "BCN"}},
			},
		},
	}

	got := extractTopRoundTripFares(flts, 5)
	if len(got) != 2 {
		t.Fatalf("expected 2 native round-trip fares, got %d: %#v", len(got), got)
	}
	// Cheapest first: Finnair (200), then Vueling (280).
	if got[0].Price != 200 || got[0].Airline != "Finnair" {
		t.Errorf("first fare = %v %q, want 200 Finnair", got[0].Price, got[0].Airline)
	}
	// Both-directions route — inbound leg back to HEL must be present.
	if got[0].Route != "HEL -> BCN -> HEL" {
		t.Errorf("first route = %q, want HEL -> BCN -> HEL", got[0].Route)
	}
	if got[1].Price != 280 {
		t.Errorf("second fare price = %v, want 280", got[1].Price)
	}
	// Arrival is taken from the last (inbound) leg.
	if got[0].Arrival != "22:00" {
		t.Errorf("first fare arrival = %q, want 22:00 (last/inbound leg)", got[0].Arrival)
	}
}

func TestExtractTopRoundTripFares_ZeroPriceFiltered(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 0, Currency: "EUR", FareType: models.FareRoundTrip},
		{Price: 220, Currency: "EUR", FareType: models.FareRoundTrip, Legs: []models.FlightLeg{
			{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "BCN"}},
			{DepartureAirport: models.AirportInfo{Code: "BCN"}, ArrivalAirport: models.AirportInfo{Code: "HEL"}},
		}},
	}
	got := extractTopRoundTripFares(flts, 5)
	if len(got) != 1 {
		t.Fatalf("expected 1 fare (zero price filtered), got %d", len(got))
	}
	if got[0].Price != 220 {
		t.Errorf("kept fare price = %v, want 220", got[0].Price)
	}
}

func TestExtractTopRoundTripFares_NoNativeFares(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 150, Currency: "EUR", FareType: models.FareSplitTickets},
		{Price: 90, Currency: "EUR"},
	}
	got := extractTopRoundTripFares(flts, 5)
	if len(got) != 0 {
		t.Fatalf("expected 0 native fares when none are FareRoundTrip, got %d", len(got))
	}
}

func TestRoundTripRoute(t *testing.T) {
	tests := []struct {
		name string
		legs []models.FlightLeg
		want string
	}{
		{name: "empty", legs: nil, want: ""},
		{
			name: "direct round trip",
			legs: []models.FlightLeg{
				{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "BCN"}},
				{DepartureAirport: models.AirportInfo{Code: "BCN"}, ArrivalAirport: models.AirportInfo{Code: "HEL"}},
			},
			want: "HEL -> BCN -> HEL",
		},
		{
			name: "round trip with connection on outbound",
			legs: []models.FlightLeg{
				{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "AMS"}},
				{DepartureAirport: models.AirportInfo{Code: "AMS"}, ArrivalAirport: models.AirportInfo{Code: "BCN"}},
				{DepartureAirport: models.AirportInfo{Code: "BCN"}, ArrivalAirport: models.AirportInfo{Code: "HEL"}},
			},
			want: "HEL -> AMS -> BCN -> HEL",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := roundTripRoute(tt.legs)
			if got != tt.want {
				t.Errorf("roundTripRoute() = %q, want %q", got, tt.want)
			}
		})
	}
}
