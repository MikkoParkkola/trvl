package imports

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/profile"
)

func TestParseReservationTextFlight(t *testing.T) {
	got, err := ParseReservationText("Booking confirmation - KLM", "Your flight has been confirmed.\nFlight: HEL -> AMS\nDeparture: 2026-07-01\nTotal: EUR 189.00\nBooking reference: ABC123", "email")
	if err != nil {
		t.Fatalf("ParseReservationText: %v", err)
	}
	if len(got.Records) != 1 || got.Records[0].Type != "flight" {
		t.Fatalf("records = %#v", got.Records)
	}
	if got.Legs[0].From != "HEL" || got.Legs[0].To != "AMS" || !got.Legs[0].Confirmed {
		t.Fatalf("leg = %#v", got.Legs[0])
	}
	if len(got.Actions) != 1 {
		t.Fatalf("actions = %#v", got.Actions)
	}
}

func TestFromProfileBookingHotelCreatesStayLeg(t *testing.T) {
	got := FromProfileBooking(profile.Booking{
		Type:       "hotel",
		TravelDate: "2026-07-01",
		To:         "Prague",
		Provider:   "Hotel Praha",
		Price:      240,
		Currency:   "EUR",
		Nights:     3,
		Reference:  "H123",
	}, "profile", "hash")
	if got.Records[0].Type != "hotel" {
		t.Fatalf("record = %#v", got.Records[0])
	}
	if got.Legs[0].From != "Prague" || got.Legs[0].To != "Hotel Praha" || got.Legs[0].EndTime != "2026-07-04" {
		t.Fatalf("hotel leg = %#v", got.Legs[0])
	}
}

func TestParseReservationTextNonBooking(t *testing.T) {
	if _, err := ParseReservationText("Newsletter", "Cheap ideas for summer", "email"); err == nil {
		t.Fatalf("expected non-booking error")
	}
}
