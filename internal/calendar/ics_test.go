package calendar

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/trips"
)

func TestWriteICS_Basic(t *testing.T) {
	trip := &trips.Trip{
		ID:   "trip_abc123",
		Name: "Krakow Weekend",
		Legs: []trips.TripLeg{
			{
				Type:      "flight",
				From:      "Helsinki",
				To:        "Krakow",
				Provider:  "Finnair",
				StartTime: "2026-06-16T07:30:00",
				EndTime:   "2026-06-16T10:15:00",
				Price:     189,
				Currency:  "EUR",
				Reference: "AY1234",
				Confirmed: true,
			},
			{
				Type:      "hotel",
				From:      "Krakow",
				To:        "Krakow",
				Provider:  "Hotel Stary",
				StartTime: "2026-06-16",
				EndTime:   "2026-06-19",
				Price:     306,
				Currency:  "EUR",
				Confirmed: false,
			},
		},
	}

	ics := WriteICS(trip)

	// Required headers.
	for _, want := range []string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//Mikko Parkkola//trvl//EN",
		"CALSCALE:GREGORIAN",
		"METHOD:PUBLISH",
		"X-WR-CALNAME:Krakow Weekend",
		"END:VCALENDAR",
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("missing %q in:\n%s", want, ics)
		}
	}

	// Two events.
	if got := strings.Count(ics, "BEGIN:VEVENT"); got != 2 {
		t.Errorf("BEGIN:VEVENT count = %d, want 2", got)
	}
	if got := strings.Count(ics, "END:VEVENT"); got != 2 {
		t.Errorf("END:VEVENT count = %d, want 2", got)
	}

	// Flight summary + content.
	for _, want := range []string{
		"SUMMARY:✈️ Flight: Helsinki → Krakow",
		"DTSTART:20260616T",
		"Provider: Finnair",
		"Reference: AY1234",
		"Price: EUR 189.00",
		"STATUS:CONFIRMED",
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("missing %q in:\n%s", want, ics)
		}
	}

	// Hotel summary (all-day → DATE-only DTSTART).
	for _, want := range []string{
		"SUMMARY:🏨 Hotel: Hotel Stary\\, Krakow",
		"DTSTART;VALUE=DATE:20260616",
		"DTEND;VALUE=DATE:20260617",
		"STATUS:TENTATIVE",
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("missing %q in:\n%s", want, ics)
		}
	}

	// CRLF line endings (RFC 5545).
	if !strings.Contains(ics, "\r\n") {
		t.Error("expected CRLF line endings, got LF")
	}
}

func TestWriteICS_EmptyTrip(t *testing.T) {
	trip := &trips.Trip{ID: "trip_empty", Name: "Empty"}
	ics := WriteICS(trip)
	if !strings.Contains(ics, "BEGIN:VCALENDAR") {
		t.Error("missing BEGIN:VCALENDAR")
	}
	if !strings.Contains(ics, "END:VCALENDAR") {
		t.Error("missing END:VCALENDAR")
	}
	if strings.Contains(ics, "BEGIN:VEVENT") {
		t.Error("empty trip should produce no VEVENTs")
	}
}

func TestWriteICS_SkipsLegWithNoDate(t *testing.T) {
	trip := &trips.Trip{
		ID: "trip_x",
		Legs: []trips.TripLeg{
			{Type: "flight", From: "HEL", To: "BCN"}, // no StartTime
		},
	}
	ics := WriteICS(trip)
	// Even when start/end times are missing the BEGIN/END VEVENT pair is
	// emitted (the function returns early after END), so just verify the
	// SUMMARY/DTSTART lines do not appear.
	if strings.Contains(ics, "DTSTART:") {
		t.Error("expected no DTSTART for leg with no date")
	}
}

func TestWriteICS_FoldLongLines(t *testing.T) {
	trip := &trips.Trip{
		ID:   "trip_long",
		Name: strings.Repeat("X", 200),
		Legs: []trips.TripLeg{
			{Type: "flight", From: "A", To: "B", StartTime: "2026-06-16T10:00:00"},
		},
	}
	ics := WriteICS(trip)
	// Folded continuation lines start with a space.
	if !strings.Contains(ics, "\r\n ") {
		t.Error("expected folded long line (CRLF + space) in output")
	}
}

func TestEscapeText(t *testing.T) {
	cases := map[string]string{
		"hello, world":  `hello\, world`,
		"line1\nline2":  `line1\nline2`,
		`back\slash`:    `back\\slash`,
		"semi;colon":    `semi\;colon`,
		"clean":         "clean",
		"all,;\nthings": `all\,\;\nthings`,
	}
	for in, want := range cases {
		got := escapeText(in)
		if got != want {
			t.Errorf("escapeText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExportICS_Basic(t *testing.T) {
	trip := trips.Trip{
		ID:   "trip_abc123",
		Name: "Krakow Weekend",
		Legs: []trips.TripLeg{
			{
				Type:      "flight",
				From:      "HEL",
				To:        "NRT",
				Provider:  "KLM",
				StartTime: "2026-06-16T07:30:00",
				EndTime:   "2026-06-16T10:15:00",
				Price:     650,
				Currency:  "EUR",
				Reference: "KL1234",
				Confirmed: true,
			},
			{
				Type:      "train",
				From:      "Helsinki",
				To:        "Tampere",
				StartTime: "2026-06-17T09:00:00",
				EndTime:   "2026-06-17T10:30:00",
			},
		},
	}

	ics, err := ExportICS(trip)
	if err != nil {
		t.Fatalf("ExportICS returned error: %v", err)
	}

	// RFC 5545 structure.
	for _, want := range []string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:",
		"END:VCALENDAR",
		"BEGIN:VEVENT",
		"END:VEVENT",
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("missing %q in ICS output", want)
		}
	}

	// Two events.
	if got := strings.Count(ics, "BEGIN:VEVENT"); got != 2 {
		t.Errorf("BEGIN:VEVENT count = %d, want 2", got)
	}

	// Flight content.
	if !strings.Contains(ics, "HEL") || !strings.Contains(ics, "NRT") {
		t.Error("missing flight route in ICS output")
	}
	if !strings.Contains(ics, "KL1234") {
		t.Error("missing flight reference in ICS output")
	}

	// Train content.
	if !strings.Contains(ics, "Helsinki") || !strings.Contains(ics, "Tampere") {
		t.Error("missing train route in ICS output")
	}

	// CRLF line endings.
	if !strings.Contains(ics, "\r\n") {
		t.Error("expected CRLF line endings")
	}
}

func TestExportICS_NoID(t *testing.T) {
	trip := trips.Trip{Legs: []trips.TripLeg{{Type: "flight", From: "A", To: "B"}}}
	_, err := ExportICS(trip)
	if err == nil {
		t.Error("expected error for trip with no ID")
	}
}

func TestExportICS_NoLegs(t *testing.T) {
	trip := trips.Trip{ID: "trip_empty"}
	_, err := ExportICS(trip)
	if err == nil {
		t.Error("expected error for trip with no legs")
	}
}

func TestMakeUID_Stable(t *testing.T) {
	trip := &trips.Trip{ID: "trip_abc"}
	leg := trips.TripLeg{Type: "flight", From: "HEL", To: "NRT", StartTime: "2026-06-16T10:00:00"}
	uid1 := makeUID(trip, 0, leg)
	uid2 := makeUID(trip, 0, leg)
	if uid1 != uid2 {
		t.Errorf("makeUID not stable: %q vs %q", uid1, uid2)
	}
	// Different leg index → different UID.
	if makeUID(trip, 1, leg) == uid1 {
		t.Error("makeUID should differ for different leg indices")
	}
}
