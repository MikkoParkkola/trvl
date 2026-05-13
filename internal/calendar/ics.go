// Package calendar exports trvl trips as iCalendar (RFC 5545) .ics files
// so users can drop trips into Apple Calendar, Google Calendar, Outlook,
// and any other calendar that consumes the standard format.
//
// The exporter is intentionally pure: it takes a trips.Trip and returns
// the .ics text. No I/O. The CLI layer is responsible for writing to
// disk or stdout.
package calendar

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trips"
)

// ProductID is the iCalendar PRODID value advertised in every export.
const ProductID = "-//Mikko Parkkola//trvl//EN"

// MaxLineLength is the maximum bytes per content line per RFC 5545 §3.1.
// Lines longer than this MUST be folded with CRLF + space.
const MaxLineLength = 75

// ExportICS converts a trip into RFC 5545 compliant iCalendar text.
// It validates that the trip has at least one leg with usable dates before
// generating the .ics body. Returns the ICS string content or an error
// if the trip cannot produce a valid calendar.
func ExportICS(t trips.Trip) (string, error) {
	if t.ID == "" {
		return "", fmt.Errorf("trip has no ID")
	}
	if len(t.Legs) == 0 {
		return "", fmt.Errorf("trip %q has no legs", t.ID)
	}
	ics := WriteICS(&t)
	return ics, nil
}

// WriteICS converts a trip into the iCalendar text representation.
// Each leg becomes one VEVENT. Legs without parseable start/end times are
// emitted as all-day events on the StartTime date. Returns the .ics body
// (CRLF line endings) ready to write to a file or print to stdout.
func WriteICS(t *trips.Trip) string {
	var b strings.Builder
	now := time.Now().UTC()

	writeLine(&b, "BEGIN:VCALENDAR")
	writeLine(&b, "VERSION:2.0")
	writeLine(&b, "PRODID:"+ProductID)
	writeLine(&b, "CALSCALE:GREGORIAN")
	writeLine(&b, "METHOD:PUBLISH")
	if t.Name != "" {
		writeLine(&b, "X-WR-CALNAME:"+escapeText(t.Name))
	}

	for i, leg := range t.Legs {
		writeEvent(&b, t, i, leg, now)
	}

	writeLine(&b, "END:VCALENDAR")
	return b.String()
}

// writeEvent renders one VEVENT block for a single trip leg.
func writeEvent(b *strings.Builder, t *trips.Trip, idx int, leg trips.TripLeg, now time.Time) {
	writeLine(b, "BEGIN:VEVENT")

	// UID must be globally unique. Hash trip ID + leg index + leg fields.
	uid := makeUID(t, idx, leg)
	writeLine(b, "UID:"+uid)
	writeLine(b, "DTSTAMP:"+now.Format("20060102T150405Z"))

	// Start / end times. If parsing fails, fall back to an all-day event.
	startTime, startOK := parseTime(leg.StartTime)
	endTime, endOK := parseTime(leg.EndTime)

	switch {
	case startOK && endOK:
		writeLine(b, "DTSTART:"+startTime.UTC().Format("20060102T150405Z"))
		writeLine(b, "DTEND:"+endTime.UTC().Format("20060102T150405Z"))
	case startOK:
		// Same-day event with 1h default duration.
		writeLine(b, "DTSTART:"+startTime.UTC().Format("20060102T150405Z"))
		writeLine(b, "DTEND:"+startTime.Add(time.Hour).UTC().Format("20060102T150405Z"))
	case startDateOnly(leg.StartTime):
		// All-day event.
		writeLine(b, "DTSTART;VALUE=DATE:"+strings.ReplaceAll(leg.StartTime[:10], "-", ""))
		// All-day end date is exclusive in RFC 5545 — use the day after.
		if d, err := models.ParseDate(leg.StartTime[:10]); err == nil {
			writeLine(b, "DTEND;VALUE=DATE:"+d.AddDate(0, 0, 1).Format("20060102"))
		}
	default:
		// Skip events with no usable date — better to omit than emit garbage.
		writeLine(b, "END:VEVENT")
		return
	}

	writeLine(b, "SUMMARY:"+escapeText(summaryFor(leg)))
	if desc := descriptionFor(leg); desc != "" {
		writeLine(b, "DESCRIPTION:"+escapeText(desc))
	}
	if loc := locationFor(leg); loc != "" {
		writeLine(b, "LOCATION:"+escapeText(loc))
	}
	if leg.BookingURL != "" {
		writeLine(b, "URL:"+leg.BookingURL)
	}
	if leg.Confirmed {
		writeLine(b, "STATUS:CONFIRMED")
	} else {
		writeLine(b, "STATUS:TENTATIVE")
	}

	writeLine(b, "END:VEVENT")
}

// summaryFor builds a human-readable VEVENT title for a leg.
func summaryFor(leg trips.TripLeg) string {
	prefix := emojiFor(leg.Type) + " " + capitalizeFirst(leg.Type)
	switch leg.Type {
	case "hotel":
		// "🏨 Hotel: Hotel Stary, Krakow"
		if leg.Provider != "" {
			return fmt.Sprintf("%s: %s, %s", prefix, leg.Provider, leg.To)
		}
		return fmt.Sprintf("%s: %s", prefix, leg.To)
	default:
		// "✈️ Flight: Helsinki → Krakow"
		from := leg.From
		to := leg.To
		if from != "" && to != "" {
			return fmt.Sprintf("%s: %s → %s", prefix, from, to)
		}
		if to != "" {
			return fmt.Sprintf("%s to %s", prefix, to)
		}
		return prefix
	}
}

// descriptionFor builds a multi-line VEVENT description.
func descriptionFor(leg trips.TripLeg) string {
	var parts []string
	if leg.Provider != "" {
		parts = append(parts, "Provider: "+leg.Provider)
	}
	if leg.Reference != "" {
		parts = append(parts, "Reference: "+leg.Reference)
	}
	if leg.Price > 0 {
		parts = append(parts, fmt.Sprintf("Price: %s %.2f", leg.Currency, leg.Price))
	}
	if leg.BookingURL != "" {
		parts = append(parts, "Booking: "+leg.BookingURL)
	}
	parts = append(parts, "Generated by trvl (https://github.com/MikkoParkkola/trvl)")
	return strings.Join(parts, "\\n")
}

// locationFor returns a sensible LOCATION value for a leg.
func locationFor(leg trips.TripLeg) string {
	if leg.Type == "hotel" {
		if leg.Provider != "" && leg.To != "" {
			return leg.Provider + ", " + leg.To
		}
		return leg.To
	}
	return leg.To
}

func emojiFor(legType string) string {
	switch legType {
	case "flight":
		return "✈️"
	case "train":
		return "🚆"
	case "bus":
		return "🚌"
	case "ferry":
		return "⛴️"
	case "hotel":
		return "🏨"
	case "activity":
		return "🎯"
	default:
		return "📍"
	}
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// makeUID returns a stable globally-unique identifier for a leg event.
// Includes trip ID, leg index, and a hash of the leg content so the UID
// is reproducible across runs (calendars use UID for deduplication).
func makeUID(t *trips.Trip, idx int, leg trips.TripLeg) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s|%d|%s|%s|%s|%s|%s",
		t.ID, idx, leg.Type, leg.From, leg.To, leg.StartTime, leg.EndTime)
	return fmt.Sprintf("trvl-%s-%d-%s@trvl.local",
		t.ID, idx, hex.EncodeToString(h.Sum(nil))[:8])
}

// parseTime tries multiple ISO 8601 formats. Returns (time, true) on
// success, (zero, false) on failure.
func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	formats := []string{
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04Z07:00",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// startDateOnly returns true if s looks like a YYYY-MM-DD date with no time.
func startDateOnly(s string) bool {
	if len(s) < 10 {
		return false
	}
	_, err := models.ParseDate(s[:10])
	return err == nil
}

// escapeText escapes commas, semicolons, and newlines per RFC 5545 §3.3.11.
func escapeText(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		"\n", `\n`,
		",", `\,`,
		";", `\;`,
	)
	return r.Replace(s)
}

// writeLine appends a content line, folding it at MaxLineLength bytes per
// RFC 5545 §3.1. Folded continuation lines start with a single space.
func writeLine(b *strings.Builder, line string) {
	if len(line) <= MaxLineLength {
		b.WriteString(line)
		b.WriteString("\r\n")
		return
	}
	// First chunk is up to MaxLineLength bytes.
	b.WriteString(line[:MaxLineLength])
	b.WriteString("\r\n")
	// Subsequent chunks are MaxLineLength-1 bytes each (room for the leading space).
	rest := line[MaxLineLength:]
	for len(rest) > 0 {
		chunk := MaxLineLength - 1
		if len(rest) < chunk {
			chunk = len(rest)
		}
		b.WriteString(" ")
		b.WriteString(rest[:chunk])
		b.WriteString("\r\n")
		rest = rest[chunk:]
	}
}
