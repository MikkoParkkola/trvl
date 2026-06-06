// Package models — canon.go provides pure, dependency-free canonicalizers that
// normalize the many formats heterogeneous provider APIs use for the same
// semantic value (money, time, duration, cabin class, place). Adapters MUST call
// these instead of reimplementing parsing inline. Pure stdlib + models' own
// static tables (airports.go) only — no internal/* imports (locked architecture).
//
// Tracking: MIK-4949 (parent MIK-4948).
package models

import (
	"strconv"
	"strings"
	"time"
)

// ParseMoney normalizes a price string + optional currency into a Price.
// Handles: "120", "120.50", "120,50" (EU decimal comma), "€120", "$1,200.00",
// "1.200,50" (EU thousands), and an explicit currency arg that wins over any
// symbol detected in the amount. Minor-unit conversion is the caller's job.
func ParseMoney(amount, currency string) Price {
	cur := normalizeCurrency(currency)
	raw := strings.TrimSpace(amount)
	if cur == "" {
		cur = currencyFromSymbol(raw)
	}
	return Price{Amount: parseAmount(raw), Currency: cur}
}

func normalizeCurrency(c string) string {
	c = strings.ToUpper(strings.TrimSpace(c))
	if len(c) == 3 {
		return c
	}
	return currencyFromSymbol(c)
}

func currencyFromSymbol(s string) string {
	switch {
	case strings.Contains(s, "€"), strings.EqualFold(s, "eur"):
		return "EUR"
	case strings.Contains(s, "£"), strings.EqualFold(s, "gbp"):
		return "GBP"
	case strings.Contains(s, "$"), strings.EqualFold(s, "usd"):
		return "USD"
	}
	return ""
}

// parseAmount extracts a float from a money string, auto-detecting whether comma
// or dot is the decimal separator based on which appears last.
func parseAmount(s string) float64 {
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == '.' || r == ',' {
			b.WriteRune(r)
		}
	}
	t := b.String()
	if t == "" {
		return 0
	}
	lastComma := strings.LastIndex(t, ",")
	lastDot := strings.LastIndex(t, ".")
	sep := lastComma
	if lastDot > sep {
		sep = lastDot
	}
	if sep >= 0 {
		afterLen := len(t) - sep - 1
		switch {
		case afterLen == 3:
			// Separator groups thousands (e.g. "1,299", "1.500") -> integer.
			t = strings.ReplaceAll(t, ",", "")
			t = strings.ReplaceAll(t, ".", "")
		case t[sep] == ',':
			// Comma is the decimal point; dots are thousands.
			t = strings.ReplaceAll(t, ".", "")
			t = strings.Replace(t, ",", ".", -1)
		default:
			// Dot is the decimal point; commas are thousands.
			t = strings.ReplaceAll(t, ",", "")
		}
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return 0
	}
	return f
}

// temporalLayouts are tried in order by ParseTemporal.
var temporalLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04",
	"20060102150405",
	"20060102",
	"2006-01-02",
	"02.01.2006 15:04",
	"02.01.2006",
	"02/01/2006",
}

// ParseTemporal normalizes a timestamp string from any supported provider format
// into a time.Time. A pure-digit string of 10 or 13 chars is treated as a Unix
// epoch (seconds / milliseconds). On failure the zero time and false are returned.
func ParseTemporal(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if isAllDigits(s) && (len(s) == 10 || len(s) == 13) {
		n, err := strconv.ParseInt(s, 10, 64)
		if err == nil {
			if len(s) == 13 {
				return time.UnixMilli(n).UTC(), true
			}
			return time.Unix(n, 0).UTC(), true
		}
	}
	for _, layout := range temporalLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// ParseDuration normalizes a duration into whole minutes. Handles: plain integer
// minutes ("330"), ISO-8601 ("PT5H30M", "PT45M", "PT2H"), colon time ("5:30"),
// and compact ("5h30", "5h", "90m"). Returns 0 on unparseable input.
func ParseDuration(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if isAllDigits(s) {
		n, _ := strconv.Atoi(s)
		return n
	}
	up := strings.ToUpper(s)
	if strings.HasPrefix(up, "PT") {
		return parseISO8601Duration(up[2:])
	}
	if strings.Contains(s, ":") {
		parts := strings.SplitN(s, ":", 2)
		h, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		m, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		return h*60 + m
	}
	if i := strings.IndexAny(up, "H"); i >= 0 {
		h, _ := strconv.Atoi(strings.TrimSpace(up[:i]))
		rest := strings.TrimRight(strings.TrimSpace(up[i+1:]), "M")
		m := 0
		if rest != "" {
			m, _ = strconv.Atoi(rest)
		}
		return h*60 + m
	}
	if strings.HasSuffix(up, "M") {
		m, _ := strconv.Atoi(strings.TrimSpace(up[:len(up)-1]))
		return m
	}
	return 0
}

func parseISO8601Duration(s string) int {
	total := 0
	num := strings.Builder{}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			num.WriteRune(r)
		case r == 'H':
			n, _ := strconv.Atoi(num.String())
			total += n * 60
			num.Reset()
		case r == 'M':
			n, _ := strconv.Atoi(num.String())
			total += n
			num.Reset()
		default:
			num.Reset()
		}
	}
	return total
}

// PlaceKind discriminates what kind of location a Place represents.
type PlaceKind string

const (
	PlaceAirport PlaceKind = "airport"
	PlaceCity    PlaceKind = "city"
	PlaceUnknown PlaceKind = "unknown"
)

// Place is the canonical normalized location, derived from static tables only
// (airports.go). Network geocoding lives in domain packages, NOT here.
type Place struct {
	Kind PlaceKind `json:"kind"`
	Code string    `json:"code,omitempty"` // IATA when known
	Name string    `json:"name,omitempty"`
	City string    `json:"city,omitempty"`
}

// ParsePlace normalizes a location token (IATA code, airport name, or city name)
// into a Place using the static airport tables. No network access.
func ParsePlace(s string) Place {
	t := strings.TrimSpace(s)
	if t == "" {
		return Place{Kind: PlaceUnknown}
	}
	if up := strings.ToUpper(t); IsIATACode(up) {
		return Place{Kind: PlaceAirport, Code: up, Name: LookupAirportName(up), City: ResolveAirportCity(up)}
	}
	if codes := ResolveCityToAirports(t); len(codes) > 0 {
		return Place{Kind: PlaceCity, Name: ResolveLocationName(t), City: ResolveLocationName(t)}
	}
	return Place{Kind: PlaceUnknown, Name: ResolveLocationName(t)}
}

// Provider wire-format date layouts. The outbound mirror of ParseTemporal:
// canonical -> the string shape a given provider's API expects.
const (
	DateLayoutDMY       = "02/01/2006" // day/month/year (e.g. Kiwi)
	DateLayoutYMD       = "2006-01-02" // ISO date (most providers)
	DateLayoutCompact   = "20060102"   // compact (e.g. some rail APIs)
	DateLayoutDottedDMY = "02.01.2006" // dotted EU (e.g. DB/OEBB)
)

// FormatProviderDate parses a canonical date/time string (any format ParseTemporal
// accepts) and re-emits it in the requested provider layout. Returns the input
// trimmed and ok=false when it cannot be parsed, so callers can decide whether to
// pass through or error.
func FormatProviderDate(canonical, layout string) (string, bool) {
	t, ok := ParseTemporal(canonical)
	if !ok {
		return strings.TrimSpace(canonical), false
	}
	return t.Format(layout), true
}
