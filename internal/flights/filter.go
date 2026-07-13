package flights

import (
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// FilterFlightsByTimePreference drops flights whose directional departure time(s)
// fall outside the [earliest, latest] window. Both bounds are "HH:MM" strings
// in 24-hour format (e.g. "06:00", "23:00"). An empty string means no bound.
//
// For one-way results (or untagged legs) the first leg's departure is checked.
// For round-trips, both the outbound start (first leg) departure and the first
// inbound (return) leg's departure are checked; the flight is dropped if either
// violates the window. This ensures the return leg is not a second-class citizen.
//
// The departure time is extracted from a leg's DepartureTime field, which may be
// formatted as "YYYY-MM-DDTHH:MM" or "HH:MM" (we parse the clock portion).
//
// The function never mutates the input slice and always returns a valid
// (possibly empty) slice.
//
// This is a thin backward-compatible wrapper over FilterFlightsByTimeWindow with
// a zero tolerance: every flight outside the window is hard-dropped, so the
// returned set is exactly the flights inside the window (legacy semantics).
func FilterFlightsByTimePreference(flights []models.FlightResult, earliest, latest string) []models.FlightResult {
	if earliest == "" && latest == "" {
		return flights
	}
	kept, _, _ := FilterFlightsByTimeWindow(flights, earliest, latest, 0)
	return kept
}

// FilterFlightsByTimeWindow partitions flights by how their directional departure
// time(s) relate to the SOFT [earliest, latest] window, with a tolerance of
// hardFloorMinutes:
//
//   - kept          — every flight that is NOT hard-dropped, in input order. This
//     is the set callers should carry forward; it is the union of the flights
//     fully inside the window and the soft-penalised near-misses.
//   - softPenalised — the subset of kept whose worst directional departure falls
//     OUTSIDE the window but within hardFloorMinutes of it (a near-miss). These
//     are surfaced separately so a caller can down-rank them.
//   - hardDropped    — flights whose worst directional departure falls MORE than
//     hardFloorMinutes outside the window. These are excluded from kept.
//
// hardFloorMinutes == 0 reproduces the legacy hard cutoff: any departure outside
// the window is hard-dropped and softPenalised is always empty. Bounds are
// "HH:MM" 24-hour strings; an empty bound means no constraint on that side. The
// function never mutates the input slice and always returns valid (possibly
// empty, non-nil) slices.
func FilterFlightsByTimeWindow(flights []models.FlightResult, earliest, latest string, hardFloorMinutes int) (kept, softPenalised, hardDropped []models.FlightResult) {
	kept = make([]models.FlightResult, 0, len(flights))
	softPenalised = make([]models.FlightResult, 0)
	hardDropped = make([]models.FlightResult, 0)
	for _, f := range flights {
		switch timeWindowClass(f, earliest, latest, hardFloorMinutes) {
		case 2:
			hardDropped = append(hardDropped, f)
		case 1:
			kept = append(kept, f)
			softPenalised = append(softPenalised, f)
		default:
			kept = append(kept, f)
		}
	}
	return kept, softPenalised, hardDropped
}

// FilterFlightsByBudget drops flights whose price exceeds maxPrice.
// When maxPrice <= 0, all flights are kept.
func FilterFlightsByBudget(flights []models.FlightResult, maxPrice float64) []models.FlightResult {
	if maxPrice <= 0 {
		return flights
	}
	out := make([]models.FlightResult, 0, len(flights))
	for _, f := range flights {
		if f.Price > 0 && f.Price > maxPrice {
			continue
		}
		out = append(out, f)
	}
	return out
}

// FilterByAirline keeps only flights operated (on at least one leg) by one of
// the requested airline IATA codes. Matching is case-insensitive and trims
// surrounding whitespace on both the requested codes and the leg codes.
//
// An empty (or all-blank) airlines list is a no-op: the input slice is returned
// unchanged. A flight with no legs, or whose legs carry no matching airline
// code, is dropped when a filter is active, since it cannot satisfy the
// restriction.
//
// This mirrors the CLI `--airline` semantics (restrict to these carriers) as a
// deterministic post-search narrowing over whatever the provider returned. The
// function never mutates the input slice and always returns a valid (possibly
// empty) slice.
func FilterByAirline(flights []models.FlightResult, airlines []string) []models.FlightResult {
	wanted := make(map[string]struct{}, len(airlines))
	for _, a := range airlines {
		if code := strings.ToUpper(strings.TrimSpace(a)); code != "" {
			wanted[code] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return flights
	}
	out := make([]models.FlightResult, 0, len(flights))
	for _, f := range flights {
		for _, leg := range f.Legs {
			code := strings.ToUpper(strings.TrimSpace(leg.AirlineCode))
			if _, ok := wanted[code]; ok {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// FirstPricedResult returns the first flight with Price > 0 from a pre-sorted slice.
// Returns nil if no priced flight exists.
func FirstPricedResult(flights []models.FlightResult) []models.FlightResult {
	for _, f := range flights {
		if f.Price > 0 {
			return []models.FlightResult{f}
		}
	}
	return nil
}

// extractDepartureHHMM extracts the "HH:MM" departure time from the first leg.
// Returns "" if the flight has no legs or the time cannot be parsed.
// (Preserved for existing callers and tests; now one-way behaviour is unchanged.)
func extractDepartureHHMM(f models.FlightResult) string {
	if len(f.Legs) == 0 {
		return ""
	}
	return extractHHMM(f.Legs[0].DepartureTime)
}

// extractHHMM parses clock portion from a time string (shared by directional checks).
func extractHHMM(dt string) string {
	if dt == "" {
		return ""
	}
	// Handle "2026-06-15T10:30" or "10:30" or "2026-06-15 10:30".
	if idx := strings.LastIndex(dt, "T"); idx >= 0 && idx+6 <= len(dt) {
		return dt[idx+1 : idx+6]
	}
	if idx := strings.LastIndex(dt, " "); idx >= 0 && idx+6 <= len(dt) {
		return dt[idx+1 : idx+6]
	}
	// Bare "HH:MM"
	if len(dt) == 5 && dt[2] == ':' {
		return dt
	}
	return ""
}

// directionalDeparturesInWindow returns true if all checked directional
// departure times are inside the window (or no times to check).
// Checks outbound/first leg + first inbound leg (if present).
func directionalDeparturesInWindow(f models.FlightResult, earliest, latest string) bool {
	if len(f.Legs) == 0 {
		return true // keep as before
	}
	ts := collectDirectionalDepTimes(f)
	for _, t := range ts {
		if t == "" {
			continue
		}
		if earliest != "" && t < earliest {
			return false
		}
		if latest != "" && t > latest {
			return false
		}
	}
	return true
}

// timeWindowClass classifies a flight against the SOFT [earliest, latest] window
// with a tolerance of hardFloorMinutes, returning:
//
//	0 — every checked directional departure is inside the window
//	1 — at least one departure is outside the window but within the tolerance
//	    (a soft near-miss; the flight is kept but should be down-ranked)
//	2 — at least one departure is more than the tolerance outside the window
//	    (a hard miss; the flight should be dropped)
//
// The worst (largest) deviation across the checked directional departures wins,
// so a flight is only class 0 when BOTH its outbound and return starts are inside
// the window. A flight with no parseable departure times is class 0 (kept), which
// preserves the legacy "keep when we cannot tell" behaviour.
func timeWindowClass(f models.FlightResult, earliest, latest string, hardFloorMinutes int) int {
	if earliest == "" && latest == "" {
		return 0
	}
	worst := 0
	for _, t := range collectDirectionalDepTimes(f) {
		dev := windowDeviationMinutes(t, earliest, latest)
		if dev <= 0 {
			continue
		}
		class := 1
		if dev > hardFloorMinutes {
			class = 2
		}
		if class > worst {
			worst = class
		}
	}
	return worst
}

// windowDeviationMinutes returns how many minutes the "HH:MM" time t falls
// outside the [earliest, latest] window (0 when inside, or when t cannot be
// parsed — an unparseable time never triggers a penalty). Bounds are optional.
func windowDeviationMinutes(t, earliest, latest string) int {
	tm, ok := clockToMinutes(t)
	if !ok {
		return 0
	}
	if earliest != "" {
		if em, ok := clockToMinutes(earliest); ok && tm < em {
			return em - tm
		}
	}
	if latest != "" {
		if lm, ok := clockToMinutes(latest); ok && tm > lm {
			return tm - lm
		}
	}
	return 0
}

// clockToMinutes parses an "HH:MM" 24-hour string into minutes-since-midnight.
// Returns ok=false for any malformed input.
func clockToMinutes(hhmm string) (int, bool) {
	tm, err := time.Parse("15:04", hhmm)
	if err != nil {
		return 0, false
	}
	return tm.Hour()*60 + tm.Minute(), true
}

// collectDirectionalDepTimes returns the HH:MM strings for the legs that
// represent journey starts: the first leg (outbound/one-way) and the first
// inbound leg if the result is a round-trip.
func collectDirectionalDepTimes(f models.FlightResult) []string {
	var ts []string
	if len(f.Legs) == 0 {
		return ts
	}
	// Outbound or one-way start: first leg.
	if d := extractHHMM(f.Legs[0].DepartureTime); d != "" {
		ts = append(ts, d)
	}
	// First inbound (return) departure, if present.
	for _, leg := range f.Legs {
		if leg.Direction == "inbound" {
			if d := extractHHMM(leg.DepartureTime); d != "" {
				ts = append(ts, d)
			}
			break
		}
	}
	return ts
}
