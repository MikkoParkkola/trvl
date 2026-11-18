package models

import "strings"

// IsGoogleHotelID reports whether id has one of the three shapes Google's
// hotel and rooms lookups resolve: a "/g/…" entity, a "ChIJ…" place ID, or a
// "0x…:0x…" hex feature ID. Another provider's id ("4711", "flatio:98765")
// is never one, whatever it looks like.
func IsGoogleHotelID(id string) bool {
	id = strings.TrimSpace(id)
	switch {
	case strings.HasPrefix(id, "/g/"):
		return len(id) > len("/g/")
	case strings.HasPrefix(id, "ChIJ"):
		return len(id) > len("ChIJ")
	}
	a, b, ok := strings.Cut(id, ":")
	return ok && isHexWord(a) && isHexWord(b)
}

// isHexWord reports a "0x" prefix followed by at least one hex digit.
func isHexWord(s string) bool {
	digits, ok := strings.CutPrefix(s, "0x")
	if !ok || digits == "" {
		return false
	}
	for _, r := range digits {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}
