package inboxparser

import (
	"strings"

	"github.com/MikkoParkkola/trvl/internal/trips"
)

// klmProvider recognises KLM (Air France-KLM) flight confirmations. KLM flight
// numbers carry the "KL" carrier prefix.
var klmProvider = provider{
	matches: func(f messageFields) bool {
		return containsAny(f.from, "klm.com", "klm-noreply", "airfranceklm") ||
			containsAny(f.subject, "klm", "your klm booking", "booking confirmation - kl")
	},
	parse: parseKLM,
}

// parseKLM extracts the booking reference and flight legs from a KLM email.
// Body lines are of the form:
//
//	Booking reference: ABC123
//	Flight: KL1166 Amsterdam to Krakow
//	Departure: 2026-06-16T07:30
func parseKLM(f messageFields) (trips.ImportedRecord, []trips.TripLeg, bool) {
	ref := firstNonEmpty(
		valueAfter(f.body, "Booking reference:"),
		valueAfter(f.body, "Booking code:"),
		valueAfter(f.body, "PNR:"),
	)
	from := valueAfter(f.body, "From:")
	to := valueAfter(f.body, "To:")
	depRaw := firstNonEmpty(valueAfter(f.body, "Departure:"), valueAfter(f.body, "Depart:"))
	flight := valueAfter(f.body, "Flight:")

	// Fall back to parsing the "Flight:" line for the city pair if explicit
	// From/To lines are absent.
	if from == "" || to == "" {
		if pf, pt, ok := citiesFromFlightLine(flight); ok {
			if from == "" {
				from = pf
			}
			if to == "" {
				to = pt
			}
		}
	}

	iso, date, timeOK := parseFlexibleTime(depRaw)
	if ref == "" || from == "" || to == "" || !timeOK {
		return trips.ImportedRecord{}, nil, false
	}

	leg := trips.TripLeg{
		Type:      "flight",
		From:      from,
		To:        to,
		Provider:  "KLM",
		StartTime: iso,
		Reference: ref,
		Confirmed: true,
	}
	rec := trips.ImportedRecord{
		Type:       "flight",
		Provider:   "KLM",
		Reference:  ref,
		Source:     "email",
		TravelDate: date,
		From:       from,
		To:         to,
	}
	return rec, []trips.TripLeg{leg}, true
}

// citiesFromFlightLine extracts the origin/destination from a line such as
// "KL1166 Amsterdam to Krakow".
func citiesFromFlightLine(line string) (from, to string, ok bool) {
	low := strings.ToLower(line)
	idx := strings.Index(low, " to ")
	if idx < 0 {
		return "", "", false
	}
	left := strings.TrimSpace(line[:idx])
	right := strings.TrimSpace(line[idx+len(" to "):])
	left = stripFlightCode(left)
	from = cleanCity(left)
	to = cleanCity(right)
	if from == "" || to == "" {
		return "", "", false
	}
	return from, to, true
}

// stripFlightCode removes a leading flight code token (e.g. "KL1166 ") from a
// segment string.
func stripFlightCode(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return s
	}
	first := fields[0]
	if isCarrierCode(first) {
		return strings.TrimSpace(strings.TrimPrefix(s, first))
	}
	return s
}

// isCarrierCode reports whether tok looks like an airline flight code: two
// letters followed by digits (e.g. KL1166).
func isCarrierCode(tok string) bool {
	if len(tok) < 3 {
		return false
	}
	if !isLetter(tok[0]) || !isLetter(tok[1]) {
		return false
	}
	for i := 2; i < len(tok); i++ {
		if tok[i] < '0' || tok[i] > '9' {
			return false
		}
	}
	return true
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// cleanCity drops a trailing parenthetical IATA suffix and trims whitespace.
func cleanCity(s string) string {
	if idx := strings.Index(s, "("); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
