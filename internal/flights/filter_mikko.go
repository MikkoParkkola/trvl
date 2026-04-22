package flights

// filter_mikko.go -- Mikko-specific post-search flight filters.
//
// These filters implement Tier 1-2 of Mikko Parkkola's travel search mental
// model. They are additive: none break existing behaviour. All live in this
// separate file per the constraint "Do NOT modify existing filters".
//
// Filters in this file:
//   - FilterByLongLayover  (Task C: --min-layover / --layover-at)
//   - FilterByLoungeAccess (Task D: --lounge-required)
//   - FilterByAamuyo       (Task E: --no-aamuyo)

import (
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// FilterByLongLayover keeps only flights that have at least one layover whose
// duration meets minLayoverMinutes AND whose airport matches one of the
// layoverAirports list (case-insensitive, IATA codes).
//
// When layoverAirports is empty the airport constraint is skipped (any airport
// qualifies).  When minLayoverMinutes == 0 the duration constraint is skipped.
// Both zero means "no filter" -- all flights pass.
//
// Uses models.FlightLeg.LayoverMinutes which is set to 0 for the first leg;
// non-zero legs carry the gap to the previous arrival.
func FilterByLongLayover(flts []models.FlightResult, minLayoverMinutes int, layoverAirports []string) []models.FlightResult {
	if minLayoverMinutes == 0 && len(layoverAirports) == 0 {
		return flts
	}

	// Normalise airport list for O(1) lookup.
	airports := make(map[string]bool, len(layoverAirports))
	for _, a := range layoverAirports {
		airports[strings.ToUpper(strings.TrimSpace(a))] = true
	}

	out := make([]models.FlightResult, 0, len(flts))
	for _, f := range flts {
		if hasQualifyingLayover(f, minLayoverMinutes, airports) {
			out = append(out, f)
		}
	}
	return out
}

// hasQualifyingLayover returns true when the flight has at least one layover
// that satisfies both the duration and airport constraints.
func hasQualifyingLayover(f models.FlightResult, minMinutes int, airports map[string]bool) bool {
	for i, leg := range f.Legs {
		if i == 0 {
			continue // first leg has no preceding layover
		}
		// Duration check.
		if minMinutes > 0 && leg.LayoverMinutes < minMinutes {
			continue
		}
		// Airport check: the layover airport is the departure airport of THIS leg
		// (i.e. where the passenger waits).
		if len(airports) > 0 {
			code := strings.ToUpper(strings.TrimSpace(leg.DepartureAirport.Code))
			if !airports[code] {
				continue
			}
		}
		return true
	}
	return false
}

// loungeCardExpansion maps the shorthand lounge card names used in
// --lounge-required logic to the verbose card strings present in the
// internal/lounges static dataset. This keeps the filter decoupled from the
// lounges package while still matching correctly.
//
// Card families per mental model:
//   - PP  -> Priority Pass family (PP, LoungeKey, Dragon Pass, Diners Club)
//   - FB  -> Flying Blue Gold/Platinum (SkyTeam-eligible)
//   - OW  -> Oneworld Sapphire/Emerald
var loungeCardExpansion = map[string][]string{
	"PP": {"Priority Pass", "LoungeKey", "Dragon Pass", "Diners Club"},
	"FB": {"Flying Blue Gold", "Flying Blue Platinum", "SkyTeam Elite Plus"},
	"OW": {"Oneworld Sapphire", "Oneworld Emerald"},
}

// defaultLoungeCards are the cards assumed for Mikko when no explicit lounge
// cards are configured in preferences.
var defaultLoungeCards = []string{"PP", "FB", "OW"}

// loungeQueryFn is the query function used by FilterByLoungeAccess.
// Signature: airport IATA code -> set of card names covered at that airport.
// Injected so tests can provide a stub without network access.
type loungeQueryFn func(airport string) map[string]bool

// realLoungeQuery is the production implementation backed by the static
// coverage map seeded by seedLoungeAirportCoverage (coverage.go).
var realLoungeQuery loungeQueryFn = func(airport string) map[string]bool {
	return loungeAirportCoverage[strings.ToUpper(airport)]
}

// FilterByLoungeAccess drops flights where any layover airport lacks lounge
// coverage from at least one of the user's lounge cards.
//
// userCards contains shorthand card codes (PP, FB, OW) or verbose names.
// When userCards is empty, defaultLoungeCards (PP+FB+OW) are used.
//
// queryFn is a function returning the set of card names with coverage at an
// airport. Pass nil to use the production static-dataset query.
func FilterByLoungeAccess(flts []models.FlightResult, userCards []string, queryFn loungeQueryFn) []models.FlightResult {
	if queryFn == nil {
		queryFn = realLoungeQuery
	}
	if len(userCards) == 0 {
		userCards = defaultLoungeCards
	}

	// Expand shorthand codes to verbose card names used in static data.
	expanded := expandLoungeCards(userCards)

	out := make([]models.FlightResult, 0, len(flts))
	for _, f := range flts {
		if loungeOKForFlight(f, expanded, queryFn) {
			out = append(out, f)
		}
	}
	return out
}

// loungeOKForFlight returns true when every layover airport in the flight has
// lounge coverage from at least one of the user's expanded cards.
// Flights with no layovers always pass (nothing to check).
func loungeOKForFlight(f models.FlightResult, userCards map[string]bool, queryFn loungeQueryFn) bool {
	for i, leg := range f.Legs {
		if i == 0 {
			continue // first leg -- no layover before this
		}
		if leg.LayoverMinutes == 0 {
			continue // short/zero-minute connection, not a real overnight, skip
		}
		airport := strings.ToUpper(strings.TrimSpace(leg.DepartureAirport.Code))
		if airport == "" {
			continue
		}
		coverage := queryFn(airport)
		if !anyCardCovered(userCards, coverage) {
			return false // this layover airport lacks lounge access -- drop flight
		}
	}
	return true
}

// anyCardCovered returns true when at least one of the user's cards appears in
// the airport's coverage set.
func anyCardCovered(userCards map[string]bool, coverage map[string]bool) bool {
	for card := range userCards {
		if coverage[card] {
			return true
		}
	}
	return false
}

// expandLoungeCards converts shorthand codes (PP, FB, OW) and verbose card
// names into a deduplicated set of the verbose names present in static data.
func expandLoungeCards(cards []string) map[string]bool {
	out := make(map[string]bool)
	for _, c := range cards {
		upper := strings.ToUpper(strings.TrimSpace(c))
		if expanded, ok := loungeCardExpansion[upper]; ok {
			for _, v := range expanded {
				out[v] = true
			}
		} else {
			// Verbose name passed directly -- include as-is.
			out[c] = true
		}
	}
	return out
}

// aamuyoFloorDefault is used when no preference is set.
// boardingFloorDefault is the default earliest departure time after an
// overnight layover. Unhurried wake + breakfast per Mikko's travel model.
const boardingFloorDefault = "10:00"

// overnightLayoverMinutes is the layover duration that triggers the
// early-connection rule. Per mental model: 8h+ layover = overnight.
const overnightLayoverMinutes = 8 * 60

// FilterByEarlyConnection drops flights that violate the no-early-connection
// rule: when there is an overnight layover (>= 8 h), the subsequent leg must
// depart at or after boardingFloor (HH:MM, 24-hour format, local time).
//
// boardingFloor="" uses the default ("10:00"). Flights with no overnight
// layovers are not affected.
//
// Rationale: after sleeping at a transfer hub, the traveler wants to wake
// without hurry and have breakfast before the next departure. Per-user
// configurable via preferences.EarlyConnectionFloor (or legacy AamuyoFloor).
func FilterByEarlyConnection(flts []models.FlightResult, boardingFloor string) []models.FlightResult {
	if boardingFloor == "" {
		boardingFloor = boardingFloorDefault
	}

	out := make([]models.FlightResult, 0, len(flts))
	for _, f := range flts {
		if earlyConnectionOK(f, boardingFloor) {
			out = append(out, f)
		}
	}
	return out
}

// FilterByAamuyo is a deprecated alias for FilterByEarlyConnection. Retained
// for backwards compatibility with callers written before the rename.
//
// Deprecated: Use FilterByEarlyConnection instead.
func FilterByAamuyo(flts []models.FlightResult, boardingFloor string) []models.FlightResult {
	return FilterByEarlyConnection(flts, boardingFloor)
}

// earlyConnectionOK returns true when the flight does not violate the rule.
func earlyConnectionOK(f models.FlightResult, floor string) bool {
	for i, leg := range f.Legs {
		if i == 0 {
			continue
		}
		if leg.LayoverMinutes < overnightLayoverMinutes {
			continue
		}
		// Overnight layover found. Check departure time of THIS leg.
		depHHMM := extractLegDepartureHHMM(leg)
		if depHHMM == "" {
			continue // can't determine -- keep the flight
		}
		if depHHMM < floor {
			return false // too early after overnight layover
		}
	}
	return true
}

// extractLegDepartureHHMM extracts the HH:MM portion from a single FlightLeg's
// DepartureTime field. Handles ISO datetime (with T separator), space-separated,
// and bare HH:MM formats.
func extractLegDepartureHHMM(leg models.FlightLeg) string {
	dt := leg.DepartureTime
	if dt == "" {
		return ""
	}
	if idx := strings.LastIndex(dt, "T"); idx >= 0 && idx+6 <= len(dt) {
		return dt[idx+1 : idx+6]
	}
	if idx := strings.LastIndex(dt, " "); idx >= 0 && idx+6 <= len(dt) {
		return dt[idx+1 : idx+6]
	}
	if len(dt) >= 5 && dt[2] == ':' {
		return dt[:5]
	}
	return ""
}
