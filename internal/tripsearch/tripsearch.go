// Package hunt implements Mikko's mental-model flight search orchestrator.
//
// It is the single shared implementation behind both the `trvl find` CLI
// command (plus its hidden back-compat alias `trvl hunt`) and the
// `plan_flight_bundle` / `hunt_interactive` MCP tools. Having
// one orchestrator guarantees CLI↔MCP feature parity structurally — adding a
// capability here surfaces it on every adapter without extra wiring.
//
// Request shape: [[Request]]. Result shape: [[Result]]. Top-level
// entrypoint: [[Search]].
//
// Reference: ~/.claude/data/travel_search_mental_model.md — "TRVL IMPROVEMENT
// PROPOSAL, section 7-step algorithm".
package tripsearch

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// Request captures one hunt invocation. Fields are additive — zero values
// mean "use profile default" so callers never need to set the full set.
type Request struct {
	// Origin is typically "home" (expanded from preferences.HomeAirports)
	// or an explicit comma-separated IATA list like "HEL,AMS".
	Origin string

	// Destination is one or more IATA codes, comma-separated.
	Destination string

	// Date is the outbound date (ISO 8601 calendar date, e.g. 2026-04-23).
	Date string

	// ReturnDate is the return date (empty for one-way).
	ReturnDate string

	// Cabin class string ("economy", "business", etc.). Empty → "economy".
	Cabin string

	// MinLayoverMinutes filters to flights with a layover of at least this
	// many minutes. 0 disables the duration constraint.
	MinLayoverMinutes int

	// LayoverAirports restricts qualifying layovers to the listed IATA codes.
	// Empty slice disables the airport constraint.
	LayoverAirports []string

	// NoEarlyConnection, when true, drops flights whose post-overnight leg
	// departs before EarlyConnectionFloor (default 10:00).
	NoEarlyConnection bool

	// LoungeRequired, when true, drops flights where a layover airport lacks
	// lounge coverage from any of the user's lounge cards.
	LoungeRequired bool

	// HiddenCity, when true, runs flights.DetectHiddenCity after the main
	// search and returns candidates where the full origin→hub→beyond
	// itinerary is cheaper than the direct origin→hub flight.
	HiddenCity bool

	// HiddenCityMinSavings is the minimum €/$ saving required to flag a
	// hidden-city candidate. 0 → library default (currently 30).
	HiddenCityMinSavings float64

	// HiddenCityHub is the transit hub used as pivot for hidden-city probing.
	// When empty, Hunt picks the first destination in Destination as the hub
	// (matches "book origin→HUB→beyond, discard beyond" cases).
	HiddenCityHub string

	// TopN caps the returned bundle list (post-ranking). 0 → no cap.
	TopN int

	// PreferencesOverride allows callers (tests, MCP with explicit profile
	// payload) to pass a preferences snapshot instead of reading from disk.
	PreferencesOverride *preferences.Preferences
}

// Result is the synthesised response.
type Result struct {
	// Flights is the ranked, top-N-sliced bundle list.
	Flights []models.FlightResult `json:"flights"`

	// Count is len(Flights) after top-N slicing.
	Count int `json:"count"`

	// TripType is "one_way" or "round_trip", mirroring the underlying search.
	TripType string `json:"trip_type"`

	// Origins is the expanded origin list actually searched (post home-fan +
	// rail-fly). Exposed so callers can explain hacks in use.
	Origins []string `json:"origins"`

	// FiltersApplied summarises which filters ran. Useful for UI + telemetry.
	FiltersApplied FilterLog `json:"filters_applied"`

	// PreFilterCount is the flight count before filters. Helps detect
	// "filters too strict" states.
	PreFilterCount int `json:"pre_filter_count"`

	// HiddenCityCandidates lists detected savings opportunities when
	// Request.HiddenCity was set. Empty slice otherwise.
	HiddenCityCandidates []flights.HiddenCityCandidate `json:"hidden_city_candidates,omitempty"`
}

// FilterLog records which filters executed and how many flights each
// removed. Useful for MCP's interactive relax flow.
type FilterLog struct {
	LongLayover       FilterStep `json:"long_layover"`
	LoungeAccess      FilterStep `json:"lounge_access"`
	NoEarlyConnection FilterStep `json:"no_early_connection"`
}

// FilterStep captures one filter's impact.
type FilterStep struct {
	Ran     bool `json:"ran"`
	Dropped int  `json:"dropped"`
	Kept    int  `json:"kept"`
}

// Progress is an optional progress-reporting callback. CLI supplies nil; MCP
// supplies a function that emits notifications/progress messages.
type Progress func(stage string, done, total int)

// SearchFunc abstracts the flights.Search* family so tests can inject fakes.
type SearchFunc func(ctx context.Context, origins, dests []string, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error)

// DefaultSearch is the production wrapper around flights.SearchMultiAirport /
// flights.SearchFlights. Use this unless you are in a test.
func DefaultSearch(ctx context.Context, origins, dests []string, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
	if len(origins) > 1 || len(dests) > 1 {
		return flights.SearchMultiAirport(ctx, origins, dests, date, opts)
	}
	return flights.SearchFlights(ctx, origins[0], dests[0], date, opts)
}

// Search runs the orchestrator pipeline end-to-end.
//
// Steps (mirrors travel_search_mental_model.md):
//  1. Resolve profile + expand origin (home-fan)
//  2. Add rail-fly origins when AMS is present
//  3. Primary flight search (multi-airport aware)
//  4. Apply filter stack
//  5. Rank cheapest-first, slice top N
//
// The search argument is typically DefaultSearch; tests inject fakes.
// progress is optional (nil-safe) and is called at each stage transition.
func Search(ctx context.Context, req Request, search SearchFunc, progress Progress) (*Result, error) {
	if search == nil {
		search = DefaultSearch
	}
	if progress == nil {
		progress = func(string, int, int) {}
	}

	// Step 1+2: origin expansion.
	progress("origin_expansion", 0, 5)
	prefs, err := resolvePrefs(req.PreferencesOverride)
	if err != nil {
		return nil, fmt.Errorf("load preferences: %w", err)
	}
	origins, err := ExpandOrigins(req.Origin, prefs)
	if err != nil {
		return nil, err
	}
	origins = AddRailFlyOrigins(origins)

	destinations := flights.ParseAirports(req.Destination)
	if len(destinations) == 0 {
		return nil, fmt.Errorf("destination required")
	}

	// Step 3: primary search.
	progress("flight_search", 1, 5)
	cabinClass, _ := models.ParseCabinClass(req.Cabin)
	opts := flights.SearchOptions{
		ReturnDate: req.ReturnDate,
		CabinClass: cabinClass,
		SortBy:     models.SortCheapest,
		Adults:     1,
	}
	result, err := search(ctx, origins, destinations, req.Date, opts)
	if err != nil {
		return nil, fmt.Errorf("flight search: %w", err)
	}
	if result == nil || !result.Success {
		return nil, fmt.Errorf("flight search returned no results")
	}

	preFilterCount := len(result.Flights)

	// Step 4: filter stack.
	progress("filter_stack", 2, 5)
	flts, log := ApplyFilters(result.Flights, req, prefs)

	// Step 5: rank + slice.
	progress("ranking", 3, 5)
	sort.SliceStable(flts, func(i, j int) bool {
		return flts[i].Price < flts[j].Price
	})
	if req.TopN > 0 && len(flts) > req.TopN {
		flts = flts[:req.TopN]
	}

	// Optional hidden-city probe — runs before "done" so progress reflects it.
	var hcCandidates []flights.HiddenCityCandidate
	if req.HiddenCity && len(origins) > 0 {
		progress("hidden_city_probe", 4, 5)
		hub := req.HiddenCityHub
		if hub == "" && len(destinations) > 0 {
			hub = destinations[0]
		}
		if hub != "" {
			// Wrap the multi-airport search into the single-pair signature
			// DetectHiddenCity expects. Uses same CabinClass/opts.
			pair := func(c context.Context, o, d, dt string) (*models.FlightSearchResult, error) {
				return search(c, []string{o}, []string{d}, dt, opts)
			}
			if cands, cerr := flights.DetectHiddenCity(ctx, origins[0], hub, req.Date, pair, req.HiddenCityMinSavings); cerr == nil {
				hcCandidates = cands
			}
		}
	}

	progress("done", 5, 5)
	return &Result{
		Flights:              flts,
		Count:                len(flts),
		TripType:             result.TripType,
		Origins:              origins,
		FiltersApplied:       log,
		PreFilterCount:       preFilterCount,
		HiddenCityCandidates: hcCandidates,
	}, nil
}

// affinityThreshold is the minimum AirportAffinity score for an airport to
// be included in home-fan expansion. Three wins signals a reliable pattern.
const affinityThreshold = 3

// ExpandOrigins resolves "home" and applies nearby-airport fan-out from
// preferences. When the origin is explicit (e.g. "HEL,AMS") it still fans out
// each listed airport to its nearby siblings.
//
// When origin is "home", airports with AirportAffinity >= affinityThreshold
// are also included — this implements the "tool learns my preferences" loop
// where successful searches reinforce future fan-out. The explicit-origin path
// is NOT affected.
func ExpandOrigins(originArg string, prefs *preferences.Preferences) ([]string, error) {
	fanned := map[string]bool{}

	if strings.EqualFold(strings.TrimSpace(originArg), "home") {
		if prefs == nil {
			return nil, fmt.Errorf("home origin requires preferences")
		}
		for _, h := range prefs.HomeAirports {
			fanned[h] = true
			for _, nb := range prefs.NearbyAirportsFor(h) {
				fanned[nb] = true
			}
		}
		// Include high-affinity airports learned from past searches.
		for iata, score := range prefs.AirportAffinity {
			if score >= affinityThreshold {
				fanned[strings.ToUpper(strings.TrimSpace(iata))] = true
			}
		}
	} else {
		for _, o := range flights.ParseAirports(originArg) {
			fanned[o] = true
			if prefs != nil {
				for _, nb := range prefs.NearbyAirportsFor(o) {
					fanned[nb] = true
				}
			}
		}
	}

	out := make([]string, 0, len(fanned))
	for a := range fanned {
		out = append(out, a)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("no origins after expansion")
	}
	return out, nil
}

// AddRailFlyOrigins appends ZYR/ANR/BRU when AMS is among origins. Rationale:
// direct rail connections from AMS Centraal to these Belgian stations make
// them viable "rail + fly" origins.
func AddRailFlyOrigins(origins []string) []string {
	hasAMS := false
	for _, o := range origins {
		if strings.EqualFold(o, "AMS") {
			hasAMS = true
			break
		}
	}
	if !hasAMS {
		return origins
	}
	existing := map[string]bool{}
	for _, o := range origins {
		existing[strings.ToUpper(o)] = true
	}
	for _, rf := range []string{"ZYR", "ANR", "BRU"} {
		if !existing[rf] {
			origins = append(origins, rf)
		}
	}
	sort.Strings(origins)
	return origins
}

// ApplyFilters runs the Mikko filter stack in canonical order and returns
// both the filtered list and a step-by-step log.
func ApplyFilters(flts []models.FlightResult, req Request, prefs *preferences.Preferences) ([]models.FlightResult, FilterLog) {
	log := FilterLog{}

	if req.MinLayoverMinutes > 0 || len(req.LayoverAirports) > 0 {
		before := len(flts)
		flts = flights.FilterByLongLayover(flts, req.MinLayoverMinutes, req.LayoverAirports)
		log.LongLayover = FilterStep{Ran: true, Dropped: before - len(flts), Kept: len(flts)}
	}

	if req.LoungeRequired {
		var cards []string
		if prefs != nil {
			cards = prefs.LoungeCards
		}
		before := len(flts)
		flts = flights.FilterByLoungeAccess(flts, cards, nil)
		log.LoungeAccess = FilterStep{Ran: true, Dropped: before - len(flts), Kept: len(flts)}
	}

	if req.NoEarlyConnection {
		floor := ""
		if prefs != nil {
			floor = prefs.EarlyConnectionFloor
		}
		before := len(flts)
		flts = flights.FilterByEarlyConnection(flts, floor)
		log.NoEarlyConnection = FilterStep{Ran: true, Dropped: before - len(flts), Kept: len(flts)}
	}

	return flts, log
}

// RouteSummary produces a short arrow-separated route description. Used by
// both CLI presenter and MCP content blocks.
func RouteSummary(f models.FlightResult) string {
	if len(f.Legs) == 0 {
		return "?"
	}
	parts := []string{f.Legs[0].DepartureAirport.Code}
	for _, l := range f.Legs {
		parts = append(parts, l.ArrivalAirport.Code)
	}
	return strings.Join(parts, "→")
}

// Annotations builds a short tag string explaining any hacks in use for a
// given flight.
func Annotations(f models.FlightResult, origins []string) string {
	tags := []string{}
	if len(f.Legs) > 0 {
		orig := f.Legs[0].DepartureAirport.Code
		for _, rf := range []string{"ZYR", "ANR", "BRU"} {
			if orig == rf {
				tags = append(tags, "[rail+fly]")
			}
		}
	}
	if f.Stops > 0 {
		tags = append(tags, fmt.Sprintf("%dstop", f.Stops))
	}
	return strings.Join(tags, " ")
}

// resolvePrefs returns the caller-supplied override when non-nil, else loads
// from disk.
func resolvePrefs(override *preferences.Preferences) (*preferences.Preferences, error) {
	if override != nil {
		return override, nil
	}
	return preferences.Load()
}

// CalendarEventForBundle builds title/start/end/description for an external
// calendar insert (e.g. `gws calendar insert`). Returned strings are safe to
// pass to shell-quoting layers.
func CalendarEventForBundle(f models.FlightResult) (title, start, end, desc string, err error) {
	if len(f.Legs) == 0 {
		return "", "", "", "", fmt.Errorf("empty itinerary")
	}
	title = fmt.Sprintf("✈️ %s→%s (%s%s)",
		f.Legs[0].DepartureAirport.Code,
		f.Legs[len(f.Legs)-1].ArrivalAirport.Code,
		f.Legs[0].Airline,
		f.Legs[0].FlightNumber,
	)
	start = f.Legs[0].DepartureTime
	end = f.Legs[len(f.Legs)-1].ArrivalTime
	desc = fmt.Sprintf("Booked via trvl find\nPrice: %s%.0f\nRoute: %s",
		f.Currency, f.Price, RouteSummary(f))
	return
}

// ParseDuration parses a user-facing duration string ("12h", "90m") into
// minutes. Returns 0 on empty string or parse error.
func ParseDuration(s string) int {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return int(d.Minutes())
}
