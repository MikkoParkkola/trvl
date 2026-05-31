// Package travelctx resolves the ambient context every trvl search runs
// inside: the current time, the user's timezone, and their likely origin
// (airport / city / country). trvl is location- and time-aware by default
// so that answers and recommendations reflect *where* and *when* the search
// happens — booking lead time and day-of-week are real price levers, and a
// sensible default origin removes a mandatory argument from the common case.
//
// Design constraints honored (see trvl CLAUDE.md "Decisions Locked"):
//
//   - No API keys on the default path. Geo-IP uses a free, keyless endpoint
//     and is strictly best-effort: any failure degrades silently to prefs.
//   - Deterministic default test suite. Time and geo are injected via the
//     Clock and GeoResolver interfaces; the network resolver is never hit in
//     tests. IsCIEnv / TRVL_NO_GEO disable the network path entirely.
//   - Unidirectional imports. travelctx depends only on internal/models and
//     internal/preferences, never the reverse.
//
// Resolution precedence for origin (highest wins):
//
//  1. Explicit caller value (the CLI ORIGIN arg) — never overridden.
//  2. Configured home airport (preferences.HomeAirports[0]) — zero network.
//  3. Geo-IP → nearest known airport — best-effort, cached, kill-switchable.
//
// Time is always available (system clock); it has no privacy cost and is
// never gated.
package travelctx

import (
	"strings"
	"time"
)

// Source records how a resolved field was obtained, so output can be honest
// about provenance ("origin HEL — detected from your location" vs "from your
// saved home airport").
type Source string

const (
	SourceUnknown  Source = ""
	SourceExplicit Source = "explicit"    // caller supplied it directly
	SourcePrefs    Source = "preferences" // from ~/.trvl/preferences.json
	SourceGeoIP    Source = "geoip"       // detected from network location
	SourceClock    Source = "clock"       // system clock
)

// Location is a resolved physical origin. Any field may be zero if it could
// not be determined; callers must tolerate partial data.
type Location struct {
	Airport string  // IATA code, e.g. "HEL" (uppercase)
	City    string  // e.g. "Helsinki"
	Country string  // ISO 3166-1 alpha-2, e.g. "FI"
	Lat     float64 // decimal degrees, 0 if unknown
	Lon     float64 // decimal degrees, 0 if unknown
	Source  Source  // how Airport was resolved
}

// HasAirport reports whether an origin airport was resolved.
func (l Location) HasAirport() bool { return l.Airport != "" }

// Context is the ambient state a search executes in.
type Context struct {
	// Now is the moment the search is running, in the user's timezone.
	Now time.Time
	// Timezone is the IANA name of Now's location, e.g. "Europe/Helsinki".
	Timezone string
	// Origin is the user's resolved current/home location.
	Origin Location
	// TimeSource is always SourceClock today; reserved for future overrides
	// (e.g. a --now flag for reproducible runs).
	TimeSource Source
}

// LeadTimeDays returns whole days from Now until the given departure date.
// Negative values mean the date is in the past. Booking lead time is one of
// the strongest single predictors of fare level, so callers surface this to
// the user and may weight recommendations on it.
func (c Context) LeadTimeDays(departure time.Time) int {
	// Compare calendar days in the context's timezone, not raw 24h spans, so
	// "tomorrow" reads as 1 regardless of the clock time of day.
	loc := c.Now.Location()
	n := time.Date(c.Now.Year(), c.Now.Month(), c.Now.Day(), 0, 0, 0, 0, loc)
	d := departure.In(loc)
	dd := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	return int(dd.Sub(n).Hours() / 24)
}

// BookingWindow classifies how far out a departure is, for advisory copy.
type BookingWindow string

const (
	WindowPast      BookingWindow = "past"       // departure already gone
	WindowLastMin   BookingWindow = "last_min"   // 0–3 days: fares typically peak
	WindowShort     BookingWindow = "short"      // 4–13 days: limited room to optimize
	WindowSweetSpot BookingWindow = "sweet_spot" // 14–60 days: usual best-fare zone (short/medium haul)
	WindowEarly     BookingWindow = "early"      // 61–120 days: book or watch
	WindowVeryEarly BookingWindow = "very_early" // 120+ days: prices often not yet keen
)

// ClassifyWindow maps a lead time in days to a BookingWindow. The bands are
// deliberately coarse, advisory heuristics — not a pricing model.
func ClassifyWindow(leadDays int) BookingWindow {
	switch {
	case leadDays < 0:
		return WindowPast
	case leadDays <= 3:
		return WindowLastMin
	case leadDays <= 13:
		return WindowShort
	case leadDays <= 60:
		return WindowSweetSpot
	case leadDays <= 120:
		return WindowEarly
	default:
		return WindowVeryEarly
	}
}

// Advisory returns a one-line, human-facing note about the booking window,
// or "" for the neutral sweet-spot case where no nudge is warranted.
func (w BookingWindow) Advisory() string {
	switch w {
	case WindowPast:
		return "That departure date is in the past — double-check the year."
	case WindowLastMin:
		return "Last-minute window (≤3 days): fares are usually at their peak; flexibility on dates rarely helps now."
	case WindowShort:
		return "Short lead time (under 2 weeks): limited room to optimize — book once you see a fair fare."
	case WindowEarly:
		return "Booking early (2–4 months out): fares may still soften; worth a price watch before committing."
	case WindowVeryEarly:
		return "Very early (4+ months out): airlines often haven't released keen fares yet; watch rather than commit."
	default:
		return ""
	}
}

// normalizeIATA upper-cases and trims an airport code.
func normalizeIATA(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}
