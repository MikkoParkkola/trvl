package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/baggage"
	"github.com/MikkoParkkola/trvl/internal/counterfactual"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/points"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/pricesignal"
	"github.com/MikkoParkkola/trvl/internal/profile"
	"github.com/MikkoParkkola/trvl/internal/travelctx"
)

// --- Tool definitions ---

func searchFlightsTool() ToolDef {
	return ToolDef{
		Name:        "search_flights",
		Title:       "Search Flights",
		Description: "Search flights via Google Flights, and on compatible one-way searches also include Kiwi virtual-interlining results with explicit self-connect warnings. Returns real-time pricing, durations, stops, and leg details for a given route and date. IMPORTANT: call get_preferences before your first search in a conversation to load the user's home airport and flight preferences. If the profile is empty, interview the user first — get_preferences returns instructions. Use home_airports as default origin when the user doesn't specify where from. EXTRA SIGNALS (use them when present): the response may include `price_position` — where today's fare sits in this route's own history (band low/typical/high + a buy/wait verdict, only when `confident` is true; otherwise say there is not enough history and do not assert a trend); and `savings` — call-free money-saving options (cheaper same-day fares, vs-history, depart-a-nearby-date shift-day, and routes pre-computed by the user's watch monitor). Each saving has `call_free` and an `as_of` time; surface them to the user but never present a stale `as_of` as a live quote. These cost no extra searches; you do not need to (and cannot) request a deeper fan-out from this tool.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origin":              {Type: "string", Description: "Departure airport IATA code or city name (e.g., HEL, JFK, Paris, Tokyo). OPTIONAL: if omitted, trvl resolves the origin from the user's saved home airport, then best-effort from their current location (geo-IP). City names resolve to primary airport."},
				"destination":         {Type: "string", Description: "Arrival airport IATA code or city name (e.g., NRT, LAX, London, Barcelona). City names resolve to primary airport."},
				"departure_date":      {Type: "string", Description: "Departure date in YYYY-MM-DD format"},
				"return_date":         {Type: "string", Description: "Return date in YYYY-MM-DD format for round-trip (omit for one-way)"},
				"cabin_class":         {Type: "string", Description: "Cabin class: economy, premium_economy, business, or first (default: economy)"},
				"max_stops":           {Type: "string", Description: "Maximum stops: any, nonstop, one_stop, or two_plus (default: any)"},
				"sort_by":             {Type: "string", Description: "Sort order: cheapest, duration, departure, or arrival (default: cheapest)"},
				"alliances":           {Type: "string", Description: "Filter by airline alliance (comma-separated): STAR_ALLIANCE, ONEWORLD, SKYTEAM (default: no filter)"},
				"depart_after":        {Type: "string", Description: "Earliest departure time HH:MM, e.g. 06:00 (default: no filter)"},
				"depart_before":       {Type: "string", Description: "Latest departure time HH:MM, e.g. 22:00 (default: no filter)"},
				"max_price":           {Type: "integer", Description: "Maximum price in whole currency units (0 = no limit). Server-side filter."},
				"max_duration":        {Type: "integer", Description: "Maximum total flight duration in minutes (0 = no limit). Server-side filter."},
				"exclude_basic":       {Type: "boolean", Description: "Exclude basic economy fares (default: false). Server-side filter."},
				"less_emissions":      {Type: "boolean", Description: "Only show flights with lower CO2 emissions (default: false)"},
				"carry_on_bags":       {Type: "integer", Description: "Require N carry-on bags included in price (0 = no filter, 1 = require carry-on). Server-side price recalculation."},
				"checked_bags":        {Type: "integer", Description: "Checked bags pricing hint (0 = default, 1+ = recalculate prices including N checked bags). Changes price display, does not remove flights. Use require_checked_bag for actual filtering."},
				"require_checked_bag": {Type: "boolean", Description: "Only show flights with ≥1 free checked bag included (default: false). Client-side post-filter on response data."},
				"currency":            {Type: "string", Description: "Target currency for prices (ISO 4217, e.g. USD, EUR, JPY). Controls server-side pricing via Google's curr parameter. Empty = IP-based default."},
				// Mental-model filter args — parity with plan_flight_bundle/hunt_interactive
				// so agents using the lower-level search_flights still get Mikko's filter stack.
				"min_layover_minutes": {Type: "integer", Description: "Only keep flights with a layover of at least N minutes (0 = no duration constraint). Post-fetch filter."},
				"layover_at":          {Type: "array", Items: &Property{Type: "string"}, Description: "Restrict qualifying layovers to these IATA codes (empty = any airport). Post-fetch filter."},
				"no_early_connection": {Type: "boolean", Description: "Drop flights whose post-overnight leg departs before preferences.early_connection_floor (default 10:00)."},
				"lounge_required":     {Type: "boolean", Description: "Drop flights where a layover airport lacks lounge coverage from user's cards."},
				"first_result":        {Type: "boolean", Description: "Return only the first result with a valid price after sorting. Combine with sort_by to get e.g. the shortest priced flight (duration) or cheapest. Default: false."},
				"airline":             {Type: "array", Items: &Property{Type: "string"}, Description: "Restrict results to these airline IATA codes (e.g. AY, AF, KL). Accepts a JSON array or a comma-separated string. Empty = no filter. Mirrors the CLI --airline flag: the codes are passed to the provider and the results are also narrowed post-search to flights with at least one matching leg."},
				"provider":            {Type: "string", Description: "Flight provider: empty (default) = Google Flights + Kiwi + Skiplagged merge, 'skiplagged' = Skiplagged MCP only (hidden-city + virtual-interlining defaults), 'afklm' (aliases: af-klm, airfranceklm) = Air France-KLM Offers API only (opt-in, native round-trip fares; requires a credential). Use a solo provider when you want to cross-validate candidates."},
			},
			Required: []string{"destination", "departure_date"},
		},
		OutputSchema: flightSearchOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Search Flights",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

func searchDatesTool() ToolDef {
	return ToolDef{
		Name:        "search_dates",
		Title:       "Search Flight Dates",
		Description: "Find the cheapest flight prices across a date range. Returns one price per departure date, useful for finding the best travel dates.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origin":        {Type: "string", Description: "Departure airport IATA code or city name (e.g., HEL, JFK, Paris, Tokyo). City names resolve to primary airport."},
				"destination":   {Type: "string", Description: "Arrival airport IATA code or city name (e.g., NRT, LAX, London, Barcelona). City names resolve to primary airport."},
				"start_date":    {Type: "string", Description: "Start of date range in YYYY-MM-DD format"},
				"end_date":      {Type: "string", Description: "End of date range in YYYY-MM-DD format"},
				"trip_duration": {Type: "integer", Description: "Trip duration in days for round-trip (omit for one-way)"},
				"is_round_trip": {Type: "boolean", Description: "Whether to search round-trip fares (default: false)"},
			},
			Required: []string{"origin", "destination", "start_date", "end_date"},
		},
		OutputSchema: dateSearchOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Search Flight Dates",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

// --- Tool handlers ---

func handleSearchFlights(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	// Origin is optional: resolve from explicit arg > saved home airport >
	// geo-IP. This makes the MCP surface location-aware by default, matching
	// the CLI. Geo network lookup is gated by the same env kill-switches as
	// the rest of trvl (TRVL_NO_GEO / CI detection) via travelctx.
	origin, dest, originSource, err := resolveDestOriginOptional(ctx, args, true)
	if err != nil {
		return nil, nil, err
	}

	// origin/dest may be comma-separated multi-airport lists. The flight search
	// fans out across all of them, but the scalar enrichments below (profile
	// hints, miles earned, fuel-surcharge + hack detectors, booking context)
	// are keyed by a single IATA code — feed them the primary (first) airport
	// so they do not misfire on a comma string.
	primaryOrigin := primaryAirport(origin)
	primaryDest := primaryAirport(dest)

	date, err := validateDate(args, "departure_date")
	if err != nil {
		return nil, nil, err
	}

	// Validate return date if provided.
	if ret := argString(args, "return_date"); ret != "" {
		if err := models.ValidateDate(ret); err != nil {
			return nil, nil, fmt.Errorf("invalid return_date: %w", err)
		}
	}

	opts := flights.SearchOptions{
		ReturnDate:        argString(args, "return_date"),
		MaxPrice:          argInt(args, "max_price", 0),
		MaxDuration:       argInt(args, "max_duration", 0),
		ExcludeBasic:      argBool(args, "exclude_basic", false),
		Alliances:         argStringSlice(args, "alliances"),
		Airlines:          argStringSlice(args, "airline"),
		DepartAfter:       argString(args, "depart_after"),
		DepartBefore:      argString(args, "depart_before"),
		LessEmissions:     argBool(args, "less_emissions", false),
		CarryOnBags:       argInt(args, "carry_on_bags", 0),
		CheckedBags:       argInt(args, "checked_bags", 0),
		RequireCheckedBag: argBool(args, "require_checked_bag", false),
		FirstResult:       argBool(args, "first_result", false),
		Currency:          argString(args, "currency"),
	}

	if cc := argString(args, "cabin_class"); cc != "" {
		parsed, err := models.ParseCabinClass(cc)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid cabin_class: %w", err)
		}
		opts.CabinClass = parsed
	}

	if ms := argString(args, "max_stops"); ms != "" {
		parsed, err := models.ParseMaxStops(ms)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid max_stops: %w", err)
		}
		opts.MaxStops = parsed
	}

	if sb := argString(args, "sort_by"); sb != "" {
		parsed, err := models.ParseSortBy(sb)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid sort_by: %w", err)
		}
		opts.SortBy = parsed
	}

	// Apply travel-profile hints as pre-search defaults — only when the caller
	// has not set the corresponding parameter explicitly.
	//
	// DELIBERATE SURFACE-SPECIFIC DECISION (do not "unify" this away): the
	// pre-search profile→opts seeding is intentionally MCP-only. It is driven by
	// the persisted travel *profile* (internal/profile.TravelProfile, learned
	// from booking history) which the CLI deliberately does NOT consult — the
	// CLI takes its search parameters only from explicit flags. The policy that
	// BOTH surfaces must run identically is the POST-search preference chain
	// (flights.ApplySharedFlightPolicy, below), not this profile-hint seeding.
	//
	// IMPORTANT: PreferredAlliance is intentionally NOT auto-applied as a
	// hard `Alliances` filter. Doing so silently disables Kiwi and
	// Skiplagged in the multi-provider merge (their eligibility checks bail
	// when `len(opts.Alliances) > 0`), and over-narrows Google Flights to
	// alliance-only routes. The profile alliance hint stays available for
	// downstream ranking / UI surfacing; if a caller really wants a hard
	// alliance filter they pass `alliances` explicitly. Reverted as part of
	// the merge-zero-results regression (default search returned 0 flights
	// when the user's profile had a preferred alliance set).
	//
	// IMPORTANT: MaxPrice is likewise NOT auto-applied from the profile hint
	// (issue #452). opts.MaxPrice is a HARD post-fetch ceiling applied inside
	// the merge/filter pipeline AFTER each provider's raw fetch count is already
	// recorded in provider_statuses, so a route priced above the user's
	// historical average silently collapsed the merged result down to whichever
	// provider fit under the ceiling — with no signal in the response. Only
	// CabinClass is seeded here: it narrows the provider QUERY itself rather
	// than post-filtering an already-merged result, so it cannot truncate a
	// merge.
	prof, _ := profile.Load()
	hints := profile.FlightHints(prof, primaryOrigin, primaryDest)
	opts = applyFlightProfileHints(opts, args, hints)
	// MaxPrice is intentionally NOT auto-applied as a hard filter — see the
	// note above about PreferredAlliance. AvgFlightPrice*1.5 is a coarse,
	// route-agnostic-ish ceiling; on a route priced above the user's
	// historical average (e.g. a long-haul the user rarely books) it can
	// silently discard the majority of legitimately fetched, on-budget
	// inventory from every provider except the cheapest one — while
	// provider_statuses still reports those providers "ok" (the filter runs
	// after fetch, inside filterFlightResults), making the truncation
	// invisible to the caller. This also breaks CLI parity: the CLI flights
	// command never applies profile hints. Root cause of MIK issue #452
	// (search_flights returning 4 kiwi-only flights instead of the full
	// 106-flight multi-provider set for identical params). If a caller wants
	// a price ceiling they pass `max_price` explicitly, or set an explicit
	// preferences.BudgetFlightMax (applied uniformly to both CLI and MCP
	// below via FilterFlightsByBudget).

	result, err := dispatchFlightSearch(ctx, args, origin, dest, date, opts)
	if err != nil {
		return nil, nil, err
	}

	// Apply the shared budget/time/bag preference policy — single source of
	// truth in flights.ApplySharedFlightPolicy, identical to the CLI surface
	// (cmd/trvl/flights.go) so the two cannot drift. An explicit max_price arg
	// skips the preference budget filter (the core already applied that ceiling),
	// matching the CLI's --max-price behavior.
	prefs, _ := preferences.Load()
	_, explicitMaxPrice := args["max_price"]
	flights.ApplySharedFlightPolicy(result, prefs, explicitMaxPrice)

	// Apply Mikko-mental-model filters when the caller set them. Parity with
	// plan_flight_bundle — lets agents stick with search_flights and still
	// get the filter stack.
	if result != nil && result.Success {
		// Airline restriction (CLI --airline parity). opts.Airlines was already
		// passed to the provider as a server-side hint; narrow deterministically
		// here so the response only contains flights with a matching leg, even
		// when a provider returns near-matches.
		if len(opts.Airlines) > 0 {
			result.Flights = flights.FilterByAirline(result.Flights, opts.Airlines)
			result.Count = len(result.Flights)
		}
		if mins := argInt(args, "min_layover_minutes", 0); mins > 0 || len(argStringSlice(args, "layover_at")) > 0 {
			result.Flights = flights.FilterByLongLayover(result.Flights, mins, argStringSlice(args, "layover_at"))
			result.Count = len(result.Flights)
		}
		if argBool(args, "lounge_required", false) {
			var cards []string
			if prefs != nil {
				cards = prefs.LoungeCards
			}
			result.Flights = flights.FilterByLoungeAccess(result.Flights, cards, nil)
			result.Count = len(result.Flights)
		}
		if argBool(args, "no_early_connection", false) {
			floor := ""
			if prefs != nil {
				floor = prefs.EarlyConnectionFloor
			}
			result.Flights = flights.FilterByEarlyConnection(result.Flights, floor)
			result.Count = len(result.Flights)
		}
	}

	// --first: trim to single best-priced result. Runs last so mental-model
	// filters narrow candidates before we pick one.
	if opts.FirstResult && result != nil && result.Success {
		result.Flights = flights.FirstPricedResult(result.Flights)
		result.Count = len(result.Flights)
	}

	// Enrich flights with all-in cost (base fare + baggage - FF benefits).
	// Miles earning info per FF programme.
	type milesEarningInfo struct {
		Program     string `json:"program"`
		MilesEarned int    `json:"miles_earned"`
		Method      string `json:"method"` // "revenue" or "distance"
	}
	type enrichedFlight struct {
		models.FlightResult
		AllInCost    float64            `json:"all_in_cost,omitempty"`
		BagBreakdown string             `json:"bag_breakdown,omitempty"`
		MilesEarned  []milesEarningInfo `json:"miles_earned,omitempty"`
		MilesValue   float64            `json:"miles_value,omitempty"` // cents-per-mile if redeemed at this price
	}
	enrichedFlights := make([]enrichedFlight, len(result.Flights))
	if prefs != nil && result.Success {
		needCheckedBag := !prefs.CarryOnOnly
		needCarryOn := true
		var ffStatuses []baggage.FFStatus
		for _, fp := range prefs.FrequentFlyerPrograms {
			ffStatuses = append(ffStatuses, baggage.FFStatus{
				Alliance: fp.Alliance,
				Tier:     fp.Tier,
			})
		}

		// Determine cabin class for earning estimation.
		cabinClass := "economy"
		if cc := argString(args, "cabin_class"); cc != "" {
			cabinClass = cc
		}

		for i, f := range result.Flights {
			enrichedFlights[i].FlightResult = f
			airlineCode := ""
			if len(f.Legs) > 0 {
				airlineCode = f.Legs[0].AirlineCode
			}
			if airlineCode != "" {
				allIn, breakdown := baggage.AllInCost(f.Price, airlineCode, needCheckedBag, needCarryOn, ffStatuses)
				if breakdown != "" {
					enrichedFlights[i].AllInCost = allIn
					enrichedFlights[i].BagBreakdown = breakdown
				}
				// Refine the comparable price with the user's FF benefits +
				// checked-bag preference (overrides the carry-on baseline set in
				// the flights layer). MIK-4962.
				if allIn > 0 {
					enrichedFlights[i].ComparablePrice = allIn
					enrichedFlights[i].ComparableBreakdown = breakdown
				}
			}

			// Miles earning estimate per FF programme.
			if airlineCode != "" {
				for _, ff := range prefs.FrequentFlyerPrograms {
					est := points.EstimateMilesEarned(primaryOrigin, primaryDest, cabinClass, airlineCode, ff.Alliance, f.Price)
					if est.Miles > 0 {
						programLabel := ff.ProgramName
						if programLabel == "" {
							programLabel = est.Program
						}
						enrichedFlights[i].MilesEarned = append(enrichedFlights[i].MilesEarned, milesEarningInfo{
							Program:     programLabel,
							MilesEarned: est.Miles,
							Method:      est.Method,
						})
					}
				}
			}
		}
	} else {
		for i, f := range result.Flights {
			enrichedFlights[i].FlightResult = f
		}
	}

	// Build suggestions for progressive disclosure.
	suggestions := flightSuggestions(result, origin, dest, date, opts)

	// Run zero-API-call hack detectors for auto-tips.
	var flightHacks []hacks.Hack
	if result.Success && len(result.Flights) > 0 {
		cheapest := result.Flights[0]
		for _, f := range result.Flights[1:] {
			if f.Price > 0 && f.Price < cheapest.Price {
				cheapest = f
			}
		}
		hackCurrency := cheapest.Currency
		if hackCurrency == "" {
			hackCurrency = "EUR"
		}

		hackInput := hacks.DetectorInput{
			Origin:      primaryOrigin,
			Destination: primaryDest,
			Date:        date,
			ReturnDate:  opts.ReturnDate,
			Currency:    hackCurrency,
			NaivePrice:  cheapest.Price,
			Passengers:  1,
		}
		flightHacks = hacks.DetectFlightTips(ctx, hackInput)

		// Fuel surcharge — collect airline codes from results.
		airlineCodeSet := make(map[string]bool)
		for _, f := range result.Flights {
			for _, leg := range f.Legs {
				if leg.AirlineCode != "" {
					airlineCodeSet[leg.AirlineCode] = true
				}
			}
		}
		if len(airlineCodeSet) > 0 {
			var codes []string
			for code := range airlineCodeSet {
				codes = append(codes, code)
			}
			flightHacks = append(flightHacks, hacks.DetectFuelSurcharge(primaryOrigin, primaryDest, codes)...)
		}

		// Sort by savings descending, then type for deterministic ordering.
		sort.Slice(flightHacks, func(i, j int) bool {
			if flightHacks[i].Savings != flightHacks[j].Savings {
				return flightHacks[i].Savings > flightHacks[j].Savings
			}
			return flightHacks[i].Type < flightHacks[j].Type
		})

		// Cap at 3.
		if len(flightHacks) > 3 {
			flightHacks = flightHacks[:3]
		}
	}

	// MIK-6229/6234: log price history + compute price-position signal and
	// call-free counterfactual savings. Best-effort: errors are silently
	// discarded so a store failure never breaks the search.
	// Single O/D only — multi-airport searches are skipped inside the helper.
	pricePos, cfSavings := flightPriceSignals(primaryOrigin, primaryDest, date, result)

	// Build structured response.
	type enrichedFlightSearchResult struct {
		Success        bool                    `json:"success"`
		Count          int                     `json:"count"`
		TripType       string                  `json:"trip_type"`
		Flights        []enrichedFlight        `json:"flights"`
		Error          string                  `json:"error,omitempty"`
		Suggestions    []Suggestion            `json:"suggestions,omitempty"`
		Hacks          []hacks.Hack            `json:"hacks,omitempty"`
		HackSaving     *models.HackSaving      `json:"hack_saving,omitempty"`
		BookingContext *bookingContext         `json:"booking_context,omitempty"`
		PricePosition  *pricesignal.Position   `json:"price_position,omitempty"`
		Savings        []counterfactual.Saving `json:"savings,omitempty"`
		Destination    *models.DestinationInfo `json:"destination,omitempty"`
	}
	resp := enrichedFlightSearchResult{
		Success:        result.Success,
		Count:          result.Count,
		TripType:       result.TripType,
		Flights:        enrichedFlights,
		Error:          result.Error,
		Suggestions:    suggestions,
		Hacks:          flightHacks,
		HackSaving:     result.HackSaving,
		BookingContext: buildBookingContext(date, primaryOrigin, originSource),
		PricePosition:  pricePos,
		Savings:        cfSavings,
	}
	// Best-effort destination intelligence for the arrival on the default flight
	// search path: weather, safety, holidays, currency, country facts inline,
	// with no extra switch. Geocodes the user-supplied destination (city name or
	// airport code). Silent degrade — never blocks the search.
	resp.Destination = enrichDestination(ctx, argString(args, "destination"), models.DateRange{CheckIn: date, CheckOut: opts.ReturnDate})

	content, err := buildAnnotatedContentBlocks(flightSummary(result, origin, dest), resp)
	if err != nil {
		return nil, nil, err
	}

	return content, resp, nil
}

// bookingContext is the time-and-place context attached to a flight search
// result so an AI agent can reason about WHEN the search happened (booking
// lead time materially affects fares) and HOW the origin was determined.
type bookingContext struct {
	// SearchedAt is the local time the search ran (RFC3339).
	SearchedAt string `json:"searched_at"`
	// Timezone is the IANA name for SearchedAt.
	Timezone string `json:"timezone"`
	// LeadTimeDays is whole days from now to departure (negative = past).
	LeadTimeDays int `json:"lead_time_days"`
	// BookingWindow is the coarse band: last_min / short / sweet_spot / ...
	BookingWindow string `json:"booking_window"`
	// Advisory is a one-line human-facing nudge, empty for the neutral case.
	Advisory string `json:"advisory,omitempty"`
	// OriginSource reports how the origin was resolved: explicit / preferences
	// / geoip. Lets the agent disclose "I used your home airport" vs "detected
	// from your location".
	OriginSource string `json:"origin_source,omitempty"`
}

// buildBookingContext assembles the time/place context for a search. It uses
// the system clock (no network) and the date string already validated by the
// caller. Returns nil only if the date cannot be parsed, in which case the
// field is simply omitted.
func buildBookingContext(date, origin string, originSource travelctx.Source) *bookingContext {
	tctx := travelctx.Resolve(context.Background(), nil, travelctx.Options{
		ExplicitOrigin: origin,
		AllowGeoIP:     false, // time only; origin already resolved upstream
	})
	bc := &bookingContext{
		SearchedAt:   tctx.Now.Format(time.RFC3339),
		Timezone:     tctx.Timezone,
		OriginSource: string(originSource),
	}
	dep, derr := time.ParseInLocation("2006-01-02", date, tctx.Now.Location())
	if derr != nil {
		// No lead-time math possible; still return the time context.
		return bc
	}
	lead := tctx.LeadTimeDays(dep)
	window := travelctx.ClassifyWindow(lead)
	bc.LeadTimeDays = lead
	bc.BookingWindow = string(window)
	bc.Advisory = window.Advisory()
	return bc
}

// applyFlightProfileHints layers profile-derived defaults onto opts, but only
// for parameters the caller did not set explicitly.
//
// IMPORTANT: PreferredAlliance (and, by the same reasoning, MaxPrice) are
// intentionally NOT auto-applied as hard filters here:
//
//   - Alliances: auto-applying the user's dominant alliance silently disables
//     Kiwi and Skiplagged in the multi-provider merge (their eligibility
//     checks bail when `len(opts.Alliances) > 0`) and over-narrows Google
//     Flights to alliance-only routes. Reverted as part of the
//     merge-zero-results regression (default search returned 0 flights when
//     the user's profile had a preferred alliance set).
//   - MaxPrice: AvgFlightPrice*1.5 is a coarse historical-average ceiling.
//     On a route priced above the user's average (e.g. a long-haul they
//     rarely book), auto-applying it as a hard `flights.SearchOptions.MaxPrice`
//     silently discards the majority of legitimately fetched inventory from
//     every provider except the cheapest one, while provider_statuses still
//     reports those providers "ok" (the filter runs after fetch). It also
//     breaks CLI parity: `cmd/trvl` never applies profile hints. Root cause
//     of the search_flights truncation bug (MCP returned 4 kiwi-only flights
//     instead of the full 106-flight multi-provider set for identical
//     params).
//
// Only CabinClass is auto-applied: it narrows the provider query itself
// (rather than post-filtering an already-fetched merge), so a caller sees a
// consistent, non-silently-truncated result set either way.
func applyFlightProfileHints(opts flights.SearchOptions, args map[string]any, hints profile.FlightSearchHints) flights.SearchOptions {
	if _, explicit := args["cabin_class"]; !explicit && hints.CabinClass > 0 && opts.CabinClass == 0 {
		opts.CabinClass = models.CabinClass(hints.CabinClass)
	}
	return opts
}

// dispatchFlightSearch routes a search_flights call to the right
// provider based on the optional `provider` argument. Empty (or one
// of the legacy aliases) goes through the default Google Flights +
// Kiwi merge in `flights.SearchFlights`. `provider="skiplagged"`
// dispatches to the Skiplagged MCP-backed provider and
// `provider="afklm"` (aliases af-klm, airfranceklm) to the
// Air France-KLM Offers API; both are opt-in only and never
// participate in the default-on path. New providers must explicitly
// register here so the dispatcher remains the single switchboard.
func dispatchFlightSearch(ctx context.Context, args map[string]any, origin, dest, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
	provider := strings.ToLower(strings.TrimSpace(argString(args, "provider")))
	// origin/dest are validated, possibly comma-separated multi-airport lists.
	// Split them so a search spanning >1 airport on either side fans out via
	// SearchMultiAirport (the same path the CLI uses), mirroring its routing.
	origins := flights.ParseAirports(origin)
	dests := flights.ParseAirports(dest)
	if len(origins) == 0 || len(dests) == 0 {
		return nil, fmt.Errorf("origin and destination are required")
	}
	switch provider {
	case "skiplagged":
		if len(origins) != 1 || len(dests) != 1 {
			return nil, fmt.Errorf("provider skiplagged supports exactly one origin and one destination")
		}
		return flights.SearchSkiplagged(ctx, origins[0], dests[0], date, opts)
	case "afklm", "af-klm", "airfranceklm":
		if len(origins) != 1 || len(dests) != 1 {
			return nil, fmt.Errorf("provider afklm supports exactly one origin and one destination")
		}
		return flights.SearchAFKLM(ctx, origins[0], dests[0], date, opts)
	case "", "default", "google", "google_flights", "kiwi":
		if multiAirportRoute(origins, dests) {
			return flights.SearchMultiAirport(ctx, origins, dests, date, opts)
		}
		return flights.SearchFlights(ctx, origins[0], dests[0], date, opts)
	default:
		return nil, fmt.Errorf("unsupported provider %q (valid: skiplagged, afklm, or empty for default Google+Kiwi+Skiplagged merge)", provider)
	}
}

// multiAirportRoute reports whether a search spans more than one airport on
// either side, in which case it must fan out via SearchMultiAirport rather
// than the single-route SearchFlights path.
func multiAirportRoute(origins, dests []string) bool {
	return len(origins) > 1 || len(dests) > 1
}

// primaryAirport returns the first airport code from a (possibly
// comma-separated multi-airport) origin/destination string. Used to feed the
// single-airport scalar enrichments without misfiring on a comma string.
func primaryAirport(s string) string {
	if i := strings.IndexByte(s, ','); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func handleSearchDates(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	origin, dest, err := validateOriginDest(args)
	if err != nil {
		return nil, nil, err
	}

	startDate := argString(args, "start_date")
	endDate := argString(args, "end_date")
	if startDate == "" || endDate == "" {
		return nil, nil, fmt.Errorf("start_date and end_date are required")
	}

	// Validate date range.
	if err := models.ValidateDateRange(startDate, endDate); err != nil {
		return nil, nil, err
	}

	tripLength := argInt(args, "trip_duration", 0)
	roundTrip := argBool(args, "is_round_trip", false)
	if tripLength < 0 {
		return nil, nil, fmt.Errorf("trip_duration must be zero or positive (got %d)", tripLength)
	}
	if roundTrip && tripLength <= 0 {
		return nil, nil, fmt.Errorf("round-trip date search requires a positive trip_duration (days between outbound and return)")
	}
	// A trip_duration only has meaning for round-trips; honor the intent
	// rather than silently ignoring it on a one-way search.
	if tripLength > 0 {
		roundTrip = true
	}

	opts := flights.CalendarOptions{
		FromDate:   startDate,
		ToDate:     endDate,
		TripLength: tripLength,
		RoundTrip:  roundTrip,
	}

	// Use SearchCalendar (1 API call via GetCalendarGraph) instead of the
	// legacy SearchDates (N calls, one per date). Falls back to N-call
	// automatically if CalendarGraph fails.
	result, err := flights.SearchCalendar(ctx, origin, dest, opts)
	if err != nil {
		return nil, nil, err
	}

	summary := fmt.Sprintf("Found prices for %d dates from %s to %s (%s to %s).",
		result.Count, origin, dest, startDate, endDate)
	if result.Count > 0 {
		cheapest := result.Dates[0]
		for _, d := range result.Dates[1:] {
			if d.Price > 0 && d.Price < cheapest.Price {
				cheapest = d
			}
		}
		summary += fmt.Sprintf(" Cheapest: %s %.0f on %s.", cheapest.Currency, cheapest.Price, cheapest.Date)
	}

	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}

	return content, result, nil
}
