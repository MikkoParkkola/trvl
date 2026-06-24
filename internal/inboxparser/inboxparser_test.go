package inboxparser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/trips"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

func TestParseConfirmation_KLM(t *testing.T) {
	// GIVEN a KLM flight confirmation fixture.
	raw := loadFixture(t, "klm.eml")

	// WHEN parsing.
	rec, legs, ok := ParseConfirmation(raw)

	// THEN it is recognised with provider, reference, and a flight leg.
	if !ok {
		t.Fatalf("KLM fixture not recognised")
	}
	if rec.Provider != "KLM" {
		t.Fatalf("rec.Provider = %q, want KLM", rec.Provider)
	}
	if rec.Reference != "ABC123" {
		t.Fatalf("rec.Reference = %q, want ABC123", rec.Reference)
	}
	if len(legs) == 0 {
		t.Fatalf("expected at least one leg")
	}
	leg := legs[0]
	if leg.From != "Amsterdam" || leg.To != "Krakow" {
		t.Fatalf("leg from/to = %q/%q, want Amsterdam/Krakow", leg.From, leg.To)
	}
	if leg.StartTime != "2026-06-16T07:30" {
		t.Fatalf("leg.StartTime = %q, want 2026-06-16T07:30", leg.StartTime)
	}
	if leg.Type != "flight" {
		t.Fatalf("leg.Type = %q, want flight", leg.Type)
	}
}

func TestParseConfirmation_KLMFromFlightLineFallback(t *testing.T) {
	// GIVEN a KLM mail with no explicit From/To lines, only a Flight: line.
	raw := []byte("From: noreply@klm.com\r\n" +
		"Subject: KLM booking\r\n" +
		"\r\n" +
		"Booking reference: ZZ9\r\n" +
		"Flight: KL999 Helsinki to Amsterdam\r\n" +
		"Departure: 2026-07-01T06:00\r\n")

	// WHEN parsing.
	rec, legs, ok := ParseConfirmation(raw)

	// THEN cities are recovered from the flight line.
	if !ok {
		t.Fatalf("expected recognition via flight-line fallback")
	}
	if legs[0].From != "Helsinki" || legs[0].To != "Amsterdam" {
		t.Fatalf("fallback from/to = %q/%q", legs[0].From, legs[0].To)
	}
	if rec.Reference != "ZZ9" {
		t.Fatalf("rec.Reference = %q, want ZZ9", rec.Reference)
	}
}

func TestParseConfirmation_Booking(t *testing.T) {
	// GIVEN a Booking.com hotel confirmation fixture.
	raw := loadFixture(t, "booking.eml")

	// WHEN parsing.
	rec, legs, ok := ParseConfirmation(raw)

	// THEN it is recognised as a Booking.com hotel stay.
	if !ok {
		t.Fatalf("Booking fixture not recognised")
	}
	if rec.Provider != "Booking.com" {
		t.Fatalf("rec.Provider = %q, want Booking.com", rec.Provider)
	}
	if rec.Reference != "1234567890" {
		t.Fatalf("rec.Reference = %q, want 1234567890", rec.Reference)
	}
	if len(legs) == 0 || legs[0].Type != "hotel" {
		t.Fatalf("expected a hotel leg, got %+v", legs)
	}
	if legs[0].To != "Krakow" || legs[0].StartTime != "2026-06-16" {
		t.Fatalf("leg = %+v, want To=Krakow StartTime=2026-06-16", legs[0])
	}
	if legs[0].EndTime != "2026-06-19" {
		t.Fatalf("leg.EndTime = %q, want 2026-06-19", legs[0].EndTime)
	}
}

func TestParseConfirmation_Airbnb(t *testing.T) {
	// GIVEN an Airbnb confirmation fixture.
	raw := loadFixture(t, "airbnb.eml")

	// WHEN parsing.
	rec, legs, ok := ParseConfirmation(raw)

	// THEN it is recognised as an Airbnb hotel-type stay.
	if !ok {
		t.Fatalf("Airbnb fixture not recognised")
	}
	if rec.Provider != "Airbnb" {
		t.Fatalf("rec.Provider = %q, want Airbnb", rec.Provider)
	}
	if rec.Reference != "HMABCDEF" {
		t.Fatalf("rec.Reference = %q, want HMABCDEF", rec.Reference)
	}
	if len(legs) == 0 || legs[0].StartTime != "2026-06-19" {
		t.Fatalf("leg = %+v, want StartTime=2026-06-19", legs)
	}
}

func TestParseConfirmation_NegativeNotRecognised(t *testing.T) {
	// GIVEN a non-travel newsletter.
	raw := loadFixture(t, "negative.eml")

	// WHEN parsing.
	rec, legs, ok := ParseConfirmation(raw)

	// THEN it is not recognised and returns zero values without panicking.
	if ok {
		t.Fatalf("negative fixture was recognised: rec=%+v legs=%+v", rec, legs)
	}
	if rec.Provider != "" || len(legs) != 0 {
		t.Fatalf("expected zero values, got rec=%+v legs=%+v", rec, legs)
	}
}

func TestParseConfirmation_MalformedDoesNotPanic(t *testing.T) {
	// GIVEN bytes that are not a valid RFC-822 message.
	raw := []byte("this is not an email at all, no headers, no blank line")

	// WHEN parsing.
	_, _, ok := ParseConfirmation(raw)

	// THEN it returns ok=false without panicking.
	if ok {
		t.Fatalf("malformed input was unexpectedly recognised")
	}
}

func TestParseConfirmation_MatchedButMissingFields(t *testing.T) {
	// GIVEN a KLM-sender mail with no usable booking fields.
	raw := []byte("From: noreply@klm.com\r\nSubject: KLM news\r\n\r\nJust a newsletter.\r\n")

	// WHEN parsing.
	_, _, ok := ParseConfirmation(raw)

	// THEN the provider matched but parsing fails cleanly (ok=false).
	if ok {
		t.Fatalf("expected ok=false for matched-but-empty KLM mail")
	}
}

func TestIngestConfirmations_PopulatesLegsAndBookings(t *testing.T) {
	// GIVEN a fresh trip and two recognised fixtures plus one negative.
	trip := trips.Trip{ID: "trip_ingest", Name: "Krakow"}
	raws := [][]byte{
		loadFixture(t, "klm.eml"),
		loadFixture(t, "booking.eml"),
		loadFixture(t, "negative.eml"),
	}

	// WHEN ingesting.
	out, summary := IngestConfirmations(trip, raws)

	// THEN both legs and bookings are populated with no manual entry.
	if len(out.Legs) == 0 {
		t.Fatalf("len(trip.Legs) = 0, want > 0")
	}
	if len(out.Bookings) == 0 {
		t.Fatalf("len(trip.Bookings) = 0, want > 0")
	}
	if summary.Parsed != 2 {
		t.Fatalf("summary.Parsed = %d, want 2", summary.Parsed)
	}
	if summary.Unrecognised != 1 {
		t.Fatalf("summary.Unrecognised = %d, want 1", summary.Unrecognised)
	}
	if summary.BookingsAdded != 2 {
		t.Fatalf("summary.BookingsAdded = %d, want 2", summary.BookingsAdded)
	}
	if out.Workspace == nil || len(out.Workspace.ImportedRecords) != 2 {
		t.Fatalf("expected 2 imported records, got %+v", out.Workspace)
	}
}

func TestIngestConfirmations_Idempotent(t *testing.T) {
	// GIVEN a trip ingested once.
	trip := trips.Trip{ID: "trip_idem", Name: "Krakow"}
	raws := [][]byte{loadFixture(t, "klm.eml"), loadFixture(t, "booking.eml")}
	once, _ := IngestConfirmations(trip, raws)

	// WHEN ingesting the same mail again.
	twice, summary := IngestConfirmations(once, raws)

	// THEN no new legs or bookings are added.
	if len(twice.Legs) != len(once.Legs) {
		t.Fatalf("legs grew on re-ingest: %d -> %d", len(once.Legs), len(twice.Legs))
	}
	if len(twice.Bookings) != len(once.Bookings) {
		t.Fatalf("bookings grew on re-ingest: %d -> %d", len(once.Bookings), len(twice.Bookings))
	}
	if summary.BookingsAdded != 0 {
		t.Fatalf("summary.BookingsAdded = %d on re-ingest, want 0", summary.BookingsAdded)
	}
}
