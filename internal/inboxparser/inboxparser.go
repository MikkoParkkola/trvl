// Package inboxparser turns raw RFC-822 confirmation emails into structured
// trip artifacts. It detects bookings from a small set of known providers
// (KLM, Booking.com, Airbnb) using sender, subject, and body heuristics, and
// returns an imported record plus one or more trip legs.
//
// Parsing is a pure function over the raw message bytes: no network access, no
// live mailbox fetch. A thin live-fetch adapter can sit on top of
// ParseConfirmation, but it is not required to use this package.
package inboxparser

import (
	"net/mail"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/trips"
)

// ParseConfirmation parses a raw RFC-822 confirmation email and, when the
// provider is recognised, returns an imported record and at least one trip
// leg. Unrecognised mail returns ok=false with zero values and never panics.
//
// On success rec.Provider, rec.Reference, and at least one leg with From, To,
// and StartTime are populated.
func ParseConfirmation(raw []byte) (rec trips.ImportedRecord, legs []trips.TripLeg, ok bool) {
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		return trips.ImportedRecord{}, nil, false
	}

	from := strings.ToLower(msg.Header.Get("From"))
	subject := msg.Header.Get("Subject")
	body := readBody(msg)
	fields := messageFields{from: from, subject: subject, body: body}

	for _, p := range providers {
		if !p.matches(fields) {
			continue
		}
		rec, legs, ok = p.parse(fields)
		if ok {
			return rec, legs, true
		}
	}
	return trips.ImportedRecord{}, nil, false
}

// messageFields holds the normalised parts of a message a provider parser reads.
type messageFields struct {
	from    string // lower-cased From header
	subject string // raw Subject header
	body    string // decoded body text
}

// provider describes one recognisable confirmation source.
type provider struct {
	// matches reports whether this provider recognises the message.
	matches func(messageFields) bool
	// parse extracts the record and legs once matched. It returns ok=false if
	// the message matched the provider but lacked the minimum fields.
	parse func(messageFields) (trips.ImportedRecord, []trips.TripLeg, bool)
}

// providers is the ordered detection chain. The first match wins.
var providers = []provider{klmProvider, bookingProvider, airbnbProvider}

// readBody returns the message body as a string, tolerating read errors.
func readBody(msg *mail.Message) string {
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := msg.Body.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return sb.String()
}

// containsAny reports whether haystack contains any of the (case-insensitive)
// needles.
func containsAny(haystack string, needles ...string) bool {
	low := strings.ToLower(haystack)
	for _, n := range needles {
		if strings.Contains(low, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

// firstMatch returns the first capture group of the first line matching the
// given prefix label (e.g. "Confirmation:"). It is a small, dependency-free
// alternative to regular expressions for the simple "Label: value" lines used
// in the fixtures.
func valueAfter(body, label string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if idx := indexFold(trimmed, label); idx == 0 {
			return strings.TrimSpace(trimmed[len(label):])
		}
	}
	return ""
}

// indexFold returns the index of sub in s using case-insensitive comparison,
// or -1. It only needs to report a leading match, so a prefix check suffices.
func indexFold(s, sub string) int {
	if len(sub) > len(s) {
		return -1
	}
	if strings.EqualFold(s[:len(sub)], sub) {
		return 0
	}
	return -1
}

// parseFlexibleTime parses the date/time formats used in the fixtures and
// returns an ISO-8601 string suitable for a TripLeg, plus the date portion.
// Returns ok=false when no format matches.
func parseFlexibleTime(s string) (iso string, date string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false
	}
	formats := []string{
		"2006-01-02T15:04",
		"2006-01-02 15:04",
		"2006-01-02T15:04:05",
		"2006-01-02",
		"02 Jan 2006 15:04",
		"02 Jan 2006",
		"Jan 2, 2006",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			hasTime := strings.ContainsAny(s, ":")
			if hasTime {
				iso = t.Format("2006-01-02T15:04")
			} else {
				iso = t.Format("2006-01-02")
			}
			return iso, t.Format("2006-01-02"), true
		}
	}
	return "", "", false
}
