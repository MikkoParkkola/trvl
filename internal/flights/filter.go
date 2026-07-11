package flights

import (
	"strings"

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
func FilterFlightsByTimePreference(flights []models.FlightResult, earliest, latest string) []models.FlightResult {
	if earliest == "" && latest == "" {
		return flights
	}

	out := make([]models.FlightResult, 0, len(flights))
	for _, f := range flights {
		if !directionalDeparturesInWindow(f, earliest, latest) {
			continue
		}
		out = append(out, f)
	}
	return out
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
