package flights

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestMapKiwiItineraryRoundTrip unmarshals a real captured Kiwi round-trip
// response (testdata/kiwi_roundtrip.json — HEL<->BCN, returnDate 2026-08-01)
// and asserts mapKiwiItinerary now emits BOTH halves: outbound HEL->AMS->BCN
// tagged "outbound" and the previously-dropped return BCN->PMI->HEL tagged
// "inbound", as a single native round-trip fare. This is the regression that
// the live probe surfaced: the return journey was discarded entirely.
func TestMapKiwiItineraryRoundTrip(t *testing.T) {
	raw, err := os.ReadFile("testdata/kiwi_roundtrip.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var itineraries []kiwiItinerary
	if err := json.Unmarshal(raw, &itineraries); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(itineraries) != 1 {
		t.Fatalf("itineraries = %d, want 1", len(itineraries))
	}
	if itineraries[0].Inbound == nil {
		t.Fatal("expected a parsed return journey, got nil")
	}

	flight := mapKiwiItinerary(itineraries[0], "EUR")

	if got := len(flight.Legs); got != 4 {
		t.Fatalf("Legs = %d, want 4 (2 outbound + 2 inbound)", got)
	}

	// Outbound: HEL -> AMS -> BCN, both tagged "outbound".
	if c := flight.Legs[0].DepartureAirport.Code; c != "HEL" {
		t.Errorf("leg[0] departure = %q, want HEL", c)
	}
	if c := flight.Legs[0].ArrivalAirport.Code; c != "AMS" {
		t.Errorf("leg[0] arrival = %q, want AMS", c)
	}
	if c := flight.Legs[1].DepartureAirport.Code; c != "AMS" {
		t.Errorf("leg[1] departure = %q, want AMS", c)
	}
	if c := flight.Legs[1].ArrivalAirport.Code; c != "BCN" {
		t.Errorf("leg[1] arrival = %q, want BCN", c)
	}
	for i := 0; i < 2; i++ {
		if d := flight.Legs[i].Direction; d != "outbound" {
			t.Errorf("leg[%d] direction = %q, want outbound", i, d)
		}
	}

	// Inbound (the dropped half): BCN -> PMI -> HEL, both tagged "inbound".
	if c := flight.Legs[2].DepartureAirport.Code; c != "BCN" {
		t.Errorf("leg[2] departure = %q, want BCN", c)
	}
	if c := flight.Legs[2].ArrivalAirport.Code; c != "PMI" {
		t.Errorf("leg[2] arrival = %q, want PMI", c)
	}
	if c := flight.Legs[3].DepartureAirport.Code; c != "PMI" {
		t.Errorf("leg[3] departure = %q, want PMI", c)
	}
	if c := flight.Legs[3].ArrivalAirport.Code; c != "HEL" {
		t.Errorf("leg[3] arrival = %q, want HEL", c)
	}
	for i := 2; i < 4; i++ {
		if d := flight.Legs[i].Direction; d != "inbound" {
			t.Errorf("leg[%d] direction = %q, want inbound", i, d)
		}
	}

	if flight.FareType != models.FareRoundTrip {
		t.Errorf("FareType = %q, want %q", flight.FareType, models.FareRoundTrip)
	}
	if flight.Price != 323 {
		t.Errorf("Price = %v, want 323 (the full round-trip total)", flight.Price)
	}
	// Outbound stops only — the inbound airports must not inflate the count.
	if flight.Stops != 1 {
		t.Errorf("Stops = %d, want 1 (outbound legs-1, return excluded)", flight.Stops)
	}
	// totalDurationSeconds (47700) / 60 = 795 minutes covers both halves.
	if flight.Duration != 795 {
		t.Errorf("Duration = %d, want 795 (totalDurationSeconds/60)", flight.Duration)
	}
}

// TestMapKiwiItineraryOneWayNoDirectionTags is the regression guard for the
// one-way path: an itinerary with no Return must stay byte-unchanged — no
// Direction tags and no round-trip FareType (empty, as a one-way result).
func TestMapKiwiItineraryOneWayNoDirectionTags(t *testing.T) {
	itinerary := kiwiItinerary{
		Price:                102,
		TotalDurationSeconds: 21300,
		BookingURL:           "https://on.kiwi.com/oneway",
		Outbound: &kiwiLeg{
			From:          "HEL",
			To:            "BCN",
			DepartureTime: "2026-07-25T13:55:00",
			ArrivalTime:   "2026-07-25T18:50:00",
			Segments: []kiwiSegment{
				{
					From:          "HEL",
					To:            "BCN",
					FromCity:      "Helsinki",
					ToCity:        "Barcelona",
					DepartureTime: "2026-07-25T13:55:00",
					ArrivalTime:   "2026-07-25T18:50:00",
				},
			},
		},
	}

	flight := mapKiwiItinerary(itinerary, "EUR")

	if len(flight.Legs) != 1 {
		t.Fatalf("Legs = %d, want 1", len(flight.Legs))
	}
	if flight.Legs[0].Direction != "" {
		t.Errorf("one-way leg Direction = %q, want empty", flight.Legs[0].Direction)
	}
	if flight.FareType != "" {
		t.Errorf("one-way FareType = %q, want empty", flight.FareType)
	}
}
