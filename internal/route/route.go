// Package route implements multi-modal routing that combines flights, trains,
// buses, and ferries into optimal itineraries.
//
// The algorithm uses a static hub network of ~25 European cities. For a given
// origin→destination, it identifies geographically reasonable hubs and searches
// for connections through them in parallel. Results are Pareto-filtered on
// price, duration, and number of transfers.
package route

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/logredact"
	"github.com/MikkoParkkola/trvl/internal/models"
)

var (
	searchFlightsFunc      = flights.SearchFlights
	searchGroundByNameFunc = ground.SearchByName
	convertCurrencyFunc    = destinations.ConvertCurrency
)

// Options configures a multi-modal route search.
type Options struct {
	DepartAfter           string  // ISO date or datetime
	ArriveBy              string  // ISO date or datetime
	MaxTransfers          int     // max mode changes (default: 3)
	MaxPrice              float64 // max total price (0 = no limit)
	Currency              string  // display currency (default: EUR)
	Prefer                string  // preferred mode: "train", "bus", "ferry", "flight"
	Avoid                 string  // mode to exclude
	MaxHubs               int     // max intermediate cities (default: 1 for MVP)
	SortBy                string  // "price", "duration", "transfers" (default: price)
	AllowBrowserFallbacks bool
}

func (o *Options) defaults() {
	if o.MaxTransfers <= 0 {
		o.MaxTransfers = 3
	}
	if o.Currency == "" {
		o.Currency = "EUR"
	}
	if o.MaxHubs <= 0 {
		o.MaxHubs = 1
	}
	if o.SortBy == "" {
		o.SortBy = "price"
	}
	o.Prefer = strings.ToLower(strings.TrimSpace(o.Prefer))
	o.Avoid = strings.ToLower(strings.TrimSpace(o.Avoid))
	o.SortBy = strings.ToLower(strings.TrimSpace(o.SortBy))
}

// SearchRoute finds multi-modal itineraries from origin to destination.
//
// origin and destination can be IATA codes (HEL, DBV) or city names.
// date is the travel date as "YYYY-MM-DD".
func SearchRoute(ctx context.Context, origin, destination, date string, opts Options) (*models.RouteSearchResult, error) {
	opts.defaults()

	// Resolve origin and destination to hubs.
	originCity := resolveCity(origin)
	destCity := resolveCity(destination)
	originHub, originOK := LookupHub(originCity)
	destHub, destOK := LookupHub(destCity)

	if !originOK {
		// Create a synthetic hub for the origin.
		originHub = Hub{City: originCity, Airports: []string{strings.ToUpper(origin)}}
	}
	if !destOK {
		destHub = Hub{City: destCity, Airports: []string{strings.ToUpper(destination)}}
	}

	// The origin/destination/date triple is the journey itself: logging it puts
	// a user's travel plan in plaintext at Debug. Flow tracing only needs to
	// know the search ran; the counts logged below carry the diagnostic value.
	slog.Debug("route search")

	// Build candidate paths.
	paths := buildPaths(originHub, destHub, opts)

	slog.Debug("candidate paths", "count", len(paths))

	// Fetch legs for all paths in parallel.
	itineraries := fetchAllPaths(ctx, paths, date, opts)

	slog.Debug("raw itineraries", "count", len(itineraries))

	itineraries = filterItinerariesByConstraints(itineraries, date, opts)

	// Filter: price limit.
	if opts.MaxPrice > 0 {
		filtered := itineraries[:0]
		for _, it := range itineraries {
			if it.TotalPrice <= opts.MaxPrice {
				filtered = append(filtered, it)
			}
		}
		itineraries = filtered
	}

	// Keep best per transport mode to ensure diversity (cheapest flight,
	// cheapest train, cheapest bus, cheapest ferry, cheapest multi-modal),
	// then Pareto-filter the rest.
	itineraries = diverseFilter(itineraries)

	// Sort.
	sortItineraries(itineraries, opts)

	// Cap results.
	if len(itineraries) > 20 {
		itineraries = itineraries[:20]
	}

	result := &models.RouteSearchResult{
		Success:     len(itineraries) > 0,
		Origin:      originHub.City,
		Destination: destHub.City,
		Date:        date,
		Count:       len(itineraries),
		Itineraries: itineraries,
	}
	if !result.Success {
		result.Error = "no routes matched the requested providers or filters"
	}
	return result, nil
}

// resolveCity converts an IATA code to a city name, or returns the input as-is.
func resolveCity(input string) string {
	upper := strings.ToUpper(strings.TrimSpace(input))
	// Check if it's an IATA code mapping to a hub.
	city := CityForAirport(upper)
	if city != upper {
		return city
	}
	// Check the models airport names.
	if name, ok := models.AirportNames[upper]; ok {
		// Extract city from "City Airport" format.
		parts := strings.Fields(name)
		if len(parts) > 0 {
			return parts[0]
		}
	}
	// Return with first letter capitalized.
	if len(input) > 0 {
		return strings.ToUpper(input[:1]) + strings.ToLower(input[1:])
	}
	return input
}

// path is a sequence of cities to visit: [origin, hub1, ..., destination].
type path struct {
	cities []Hub
}

// buildPaths generates candidate paths through hub cities.
func buildPaths(origin, dest Hub, opts Options) []path {
	var paths []path

	// Direct path (0 hubs): origin → destination.
	paths = append(paths, path{cities: []Hub{origin, dest}})

	// 1-hub paths: origin → hub → destination.
	// Use 1.5× detour factor and cap at 6 hubs to keep search under 60s.
	if opts.MaxHubs >= 1 && origin.Lat != 0 && dest.Lat != 0 {
		candidates := CandidateHubs(origin, dest, 1.5)
		if len(candidates) > 4 {
			candidates = candidates[:4]
		}
		for _, hub := range candidates {
			paths = append(paths, path{cities: []Hub{origin, hub, dest}})
		}
	}

	return paths
}

// segResult holds flight and ground search results for one segment (city pair).
type segResult struct {
	key     string
	flights []models.RouteLeg
	ground  []models.RouteLeg
}

// fetchAllPaths searches providers for all path segments in parallel.
func fetchAllPaths(ctx context.Context, paths []path, date string, opts Options) []models.RouteItinerary {
	// Collect all unique city pairs to search.
	type segment struct {
		from, to Hub
	}
	segMap := make(map[string]segment)
	for _, p := range paths {
		for i := 0; i < len(p.cities)-1; i++ {
			key := p.cities[i].City + "→" + p.cities[i+1].City
			segMap[key] = segment{from: p.cities[i], to: p.cities[i+1]}
		}
	}

	// Fetch all segments in parallel.
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	results := make(chan segResult, len(segMap)*2)

	for key, seg := range segMap {
		// Flight search.
		if opts.Avoid != "flight" && len(seg.from.Airports) > 0 && len(seg.to.Airports) > 0 {
			wg.Add(1)
			go func(k string, from, to Hub) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				segCtx, segCancel := context.WithTimeout(ctx, 10*time.Second)
				defer segCancel()
				legs := searchFlightLeg(segCtx, from, to, date, opts)
				if len(legs) > 0 {
					results <- segResult{key: k, flights: legs}
				}
			}(key, seg.from, seg.to)
		}

		// Ground search (train/bus/ferry).
		wg.Add(1)
		go func(k string, from, to Hub) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			segCtx, segCancel := context.WithTimeout(ctx, 10*time.Second)
			defer segCancel()
			legs := searchGroundLeg(segCtx, from, to, date, opts)
			if len(legs) > 0 {
				results <- segResult{key: k, ground: legs}
			}
		}(key, seg.from, seg.to)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results by segment key.
	segResults := make(map[string]*segResult)
	for r := range results {
		existing, ok := segResults[r.key]
		if !ok {
			existing = &segResult{key: r.key}
			segResults[r.key] = existing
		}
		existing.flights = append(existing.flights, r.flights...)
		existing.ground = append(existing.ground, r.ground...)
		slog.Debug("segment result", "key", r.key, "flights", len(r.flights), "ground", len(r.ground))
	}

	// Assemble itineraries from paths.
	return assembleItineraries(paths, segResults, opts)
}

// searchFlightLeg searches for flights between two hubs.
func searchFlightLeg(ctx context.Context, from, to Hub, date string, opts Options) []models.RouteLeg {
	if len(from.Airports) == 0 || len(to.Airports) == 0 {
		return nil
	}

	// Search the primary airport pair.
	origin := from.Airports[0]
	dest := to.Airports[0]

	result, err := searchFlightsFunc(ctx, origin, dest, date, flights.SearchOptions{})
	if err != nil {
		slog.Debug("route flight search failed", "err", logredact.Err(err))
		return nil
	}

	if !result.Success || len(result.Flights) == 0 {
		return nil
	}

	// Convert top 5 results to RouteLeg.
	limit := 5
	if len(result.Flights) < limit {
		limit = len(result.Flights)
	}

	var legs []models.RouteLeg
	for _, f := range result.Flights[:limit] {
		airline := ""
		depTime := ""
		arrTime := ""
		if len(f.Legs) > 0 {
			airline = f.Legs[0].Airline
			depTime = f.Legs[0].DepartureTime
			arrTime = f.Legs[len(f.Legs)-1].ArrivalTime
		}
		provider := airline
		if f.Provider != "" && !strings.EqualFold(f.Provider, "google_flights") {
			provider = f.Provider
		}
		if provider == "" {
			provider = airline
		}
		price := f.Price
		currency := f.Currency
		if opts.Currency != "" && currency != "" && currency != opts.Currency && price > 0 {
			converted, convertedCurrency := convertCurrencyFunc(ctx, price, currency, opts.Currency)
			price = math.Round(converted*100) / 100
			currency = convertedCurrency
		}
		legs = append(legs, models.RouteLeg{
			Mode:      "flight",
			Provider:  provider,
			From:      from.City,
			To:        to.City,
			FromCode:  origin,
			ToCode:    dest,
			Departure: depTime,
			Arrival:   arrTime,
			Duration:  f.Duration,
			Price:     price,
			Currency:  currency,
			Transfers: f.Stops,
		})
	}
	return legs
}

// searchGroundLeg searches for ground transport between two hubs.
// It prefers routes with real prices over schedule-only (price=0) results.
func searchGroundLeg(ctx context.Context, from, to Hub, date string, opts Options) []models.RouteLeg {
	result, err := searchGroundByNameFunc(ctx, from.City, to.City, date, ground.SearchOptions{
		Currency:              opts.Currency,
		AllowBrowserFallbacks: opts.AllowBrowserFallbacks,
	})
	if err != nil {
		slog.Debug("route ground search failed", "err", logredact.Err(err))
		return nil
	}

	if !result.Success || len(result.Routes) == 0 {
		return nil
	}

	var allowed []models.GroundRoute
	for _, r := range result.Routes {
		if opts.Avoid != "" && strings.EqualFold(r.Type, opts.Avoid) {
			continue
		}
		allowed = append(allowed, r)
	}
	if len(allowed) == 0 {
		return nil
	}

	// Separate priced routes from schedule-only (price=0) routes.
	// For multi-modal routing, priced routes are far more useful.
	var priced, scheduleOnly []models.GroundRoute
	for _, r := range allowed {
		if r.Price > 0 {
			priced = append(priced, r)
		} else {
			scheduleOnly = append(scheduleOnly, r)
		}
	}

	// Take top 5 priced routes + 1 schedule-only for timetable reference.
	var selected []models.GroundRoute
	limit := 5
	if len(priced) < limit {
		limit = len(priced)
	}
	selected = append(selected, priced[:limit]...)
	if len(scheduleOnly) > 0 && len(selected) == 0 {
		selected = append(selected, scheduleOnly[0])
	}

	var legs []models.RouteLeg
	for _, r := range selected {
		price := r.Price
		currency := r.Currency
		if opts.Currency != "" && currency != "" && currency != opts.Currency && price > 0 {
			converted, convertedCurrency := convertCurrencyFunc(ctx, price, currency, opts.Currency)
			price = math.Round(converted*100) / 100
			currency = convertedCurrency
		}
		legs = append(legs, models.RouteLeg{
			Mode:       r.Type,
			Provider:   r.Provider,
			From:       from.City,
			To:         to.City,
			Departure:  r.Departure.Time,
			Arrival:    r.Arrival.Time,
			Duration:   r.Duration,
			Price:      price,
			Currency:   currency,
			Transfers:  r.Transfers,
			BookingURL: r.BookingURL,
		})
	}
	return legs
}

// assembleItineraries combines segment results into complete itineraries.
func assembleItineraries(paths []path, segResults map[string]*segResult, opts Options) []models.RouteItinerary {
	var itineraries []models.RouteItinerary

	for _, p := range paths {
		if len(p.cities) < 2 {
			continue
		}

		// For direct (2-city) paths, each leg option is an itinerary.
		if len(p.cities) == 2 {
			key := p.cities[0].City + "→" + p.cities[1].City
			sr, ok := segResults[key]
			if !ok {
				continue
			}
			allLegs := append(sr.flights, sr.ground...)
			for _, leg := range allLegs {
				if opts.Avoid != "" && strings.EqualFold(leg.Mode, opts.Avoid) {
					continue
				}
				it := singleLegItinerary(leg)
				itineraries = append(itineraries, it)
			}
			continue
		}

		// For hub paths (3+ cities), combine segments.
		itineraries = append(itineraries, combineSegments(p, segResults, opts)...)
	}

	return itineraries
}

// singleLegItinerary wraps a single leg into an itinerary.
func singleLegItinerary(leg models.RouteLeg) models.RouteItinerary {
	return models.RouteItinerary{
		Legs:          []models.RouteLeg{leg},
		TotalPrice:    leg.Price,
		Currency:      leg.Currency,
		TotalDuration: leg.Duration,
		Transfers:     0,
		DepartTime:    leg.Departure,
		ArriveTime:    leg.Arrival,
	}
}

// combineSegments builds multi-leg itineraries for a hub path.
// For a 3-city path [A, H, B], it combines leg1 (A→H) with leg2 (H→B).
func combineSegments(p path, segResults map[string]*segResult, opts Options) []models.RouteItinerary {
	if len(p.cities) != 3 {
		return nil // MVP: only 1-hub paths
	}

	key1 := p.cities[0].City + "→" + p.cities[1].City
	key2 := p.cities[1].City + "→" + p.cities[2].City

	sr1, ok1 := segResults[key1]
	sr2, ok2 := segResults[key2]
	if !ok1 || !ok2 {
		slog.Debug("combine missing segment", "key1", key1, "ok1", ok1, "key2", key2, "ok2", ok2)
		return nil
	}
	slog.Debug("combine segments", "key1", key1, "flights1", len(sr1.flights), "ground1", len(sr1.ground),
		"key2", key2, "flights2", len(sr2.flights), "ground2", len(sr2.ground))

	// Take top 2 flights + top 2 ground per segment to ensure mode diversity.
	legs1 := selectDiverseLegs(sr1.flights, sr1.ground, 2)
	legs2 := selectDiverseLegs(sr2.flights, sr2.ground, 2)

	var itineraries []models.RouteItinerary
	for _, l1 := range legs1 {
		if opts.Avoid != "" && strings.EqualFold(l1.Mode, opts.Avoid) {
			continue
		}
		for _, l2 := range legs2 {
			if opts.Avoid != "" && strings.EqualFold(l2.Mode, opts.Avoid) {
				continue
			}

			// Check temporal feasibility.
			if l1.Arrival != "" && l2.Departure != "" {
				if !IsConnectionFeasible(l1.Arrival, l2.Departure, l1.Mode, l2.Mode) {
					slog.Debug("skip infeasible", "l1", l1.Mode, "l2", l2.Mode, "arr", l1.Arrival, "dep", l2.Departure)
					continue
				}
				slog.Debug("feasible connection", "l1", l1.Mode, "l2", l2.Mode, "arr", l1.Arrival, "dep", l2.Departure)
			}

			totalPrice := l1.Price + l2.Price
			currency := l1.Currency
			if l2.Currency != "" {
				currency = l2.Currency
			}

			totalDuration := computeTotalDuration(l1, l2)
			transfers := 1 // one mode change at the hub
			if l1.Mode == l2.Mode {
				transfers = 0
			}
			transfers += l1.Transfers + l2.Transfers

			if opts.MaxTransfers > 0 && transfers > opts.MaxTransfers {
				continue
			}

			itineraries = append(itineraries, models.RouteItinerary{
				Legs:          []models.RouteLeg{l1, l2},
				TotalPrice:    math.Round(totalPrice*100) / 100,
				Currency:      currency,
				TotalDuration: totalDuration,
				Transfers:     transfers,
				DepartTime:    l1.Departure,
				ArriveTime:    l2.Arrival,
			})
		}
	}

	return itineraries
}

// computeTotalDuration computes the total duration including connection time.
func computeTotalDuration(l1, l2 models.RouteLeg) int {
	// Try to compute from actual times.
	if l1.Departure != "" && l2.Arrival != "" {
		depart := parseTime(l1.Departure)
		arrive := parseTime(l2.Arrival)
		if !depart.IsZero() && !arrive.IsZero() && arrive.After(depart) {
			return int(arrive.Sub(depart).Minutes())
		}
	}
	// Fallback: sum durations + minimum connection time.
	conn := MinConnectionTime(l1.Mode, l2.Mode)
	return l1.Duration + l2.Duration + int(conn.Minutes())
}

// parseTime attempts to parse an ISO 8601 datetime string.
// It delegates to parseFlexTime (defined in timing.go) which shares the same layouts.
func parseTime(s string) time.Time {
	t, _ := parseFlexTime(s)
	return t
}

// paretoFilter removes itineraries that are dominated on all three criteria.
func paretoFilter(its []models.RouteItinerary) []models.RouteItinerary {
	if len(its) <= 1 {
		return its
	}
	var result []models.RouteItinerary
	for i, a := range its {
		dominated := false
		for j, b := range its {
			if i == j {
				continue
			}
			if b.TotalPrice <= a.TotalPrice &&
				b.TotalDuration <= a.TotalDuration &&
				b.Transfers <= a.Transfers &&
				(b.TotalPrice < a.TotalPrice || b.TotalDuration < a.TotalDuration || b.Transfers < a.Transfers) {
				dominated = true
				break
			}
		}
		if !dominated {
			result = append(result, a)
		}
	}
	return result
}

func filterItinerariesByConstraints(its []models.RouteItinerary, date string, opts Options) []models.RouteItinerary {
	departAfter, hasDepartAfter := parseConstraintTime(date, opts.DepartAfter)
	arriveBy, hasArriveBy := parseConstraintTime(date, opts.ArriveBy)
	if !hasDepartAfter && !hasArriveBy {
		return its
	}

	filtered := its[:0]
	for _, it := range its {
		if hasDepartAfter {
			depart, ok := parseFlexTime(it.DepartTime)
			if !ok || depart.Before(departAfter) {
				continue
			}
		}
		if hasArriveBy {
			arrive, ok := parseFlexTime(it.ArriveTime)
			if !ok || arrive.After(arriveBy) {
				continue
			}
		}
		filtered = append(filtered, it)
	}
	return filtered
}

func itineraryPreferenceRank(it models.RouteItinerary, prefer string) int {
	if prefer == "" {
		return 0
	}
	for _, leg := range it.Legs {
		if strings.EqualFold(leg.Mode, prefer) {
			return 0
		}
	}
	return 1
}

// sortItineraries sorts by the requested criterion.
func sortItineraries(its []models.RouteItinerary, opts Options) {
	sort.Slice(its, func(i, j int) bool {
		iPref := itineraryPreferenceRank(its[i], opts.Prefer)
		jPref := itineraryPreferenceRank(its[j], opts.Prefer)
		if iPref != jPref {
			return iPref < jPref
		}

		switch opts.SortBy {
		case "duration":
			if its[i].TotalDuration == its[j].TotalDuration {
				return its[i].TotalPrice < its[j].TotalPrice
			}
			return its[i].TotalDuration < its[j].TotalDuration
		case "transfers":
			if its[i].Transfers == its[j].Transfers {
				return its[i].TotalPrice < its[j].TotalPrice
			}
			return its[i].Transfers < its[j].Transfers
		default: // "price"
			if its[i].TotalPrice == its[j].TotalPrice {
				return its[i].TotalDuration < its[j].TotalDuration
			}
			return its[i].TotalPrice < its[j].TotalPrice
		}
	})
}

// selectDiverseLegs picks the top N flights and top N ground legs to ensure
// both air and ground options are represented in hub combinations.
func selectDiverseLegs(flightLegs, groundLegs []models.RouteLeg, n int) []models.RouteLeg {
	var result []models.RouteLeg
	limit := n
	if len(flightLegs) < limit {
		limit = len(flightLegs)
	}
	result = append(result, flightLegs[:limit]...)

	limit = n
	if len(groundLegs) < limit {
		limit = len(groundLegs)
	}
	result = append(result, groundLegs[:limit]...)
	return result
}

// itineraryModeKey returns a string representing the transport mode(s) of an itinerary.
func itineraryModeKey(it models.RouteItinerary) string {
	if len(it.Legs) == 1 {
		return it.Legs[0].Mode
	}
	var modes []string
	for _, l := range it.Legs {
		modes = append(modes, l.Mode)
	}
	return strings.Join(modes, "+")
}

// diverseFilter keeps the cheapest itinerary per transport mode, then
// Pareto-filters the remainder. This ensures the user sees the best flight,
// best train, best bus, best ferry, and best multi-modal option even when
// one mode dominates on price.
func diverseFilter(its []models.RouteItinerary) []models.RouteItinerary {
	if len(its) <= 1 {
		return its
	}

	// Sort by price so the first per mode is the cheapest.
	sort.Slice(its, func(i, j int) bool {
		return its[i].TotalPrice < its[j].TotalPrice
	})

	// Keep cheapest per mode.
	kept := make(map[string]bool)
	var result []models.RouteItinerary
	var rest []models.RouteItinerary

	for _, it := range its {
		key := itineraryModeKey(it)
		if !kept[key] {
			kept[key] = true
			result = append(result, it)
		} else {
			rest = append(rest, it)
		}
	}

	// Pareto-filter the remainder and add non-dominated ones.
	filtered := paretoFilter(rest)
	result = append(result, filtered...)

	return result
}
