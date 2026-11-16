package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/transfer"
	"github.com/MikkoParkkola/trvl/internal/trip"
	"github.com/MikkoParkkola/trvl/internal/weather"
)

var searchGroundByNameFunc = ground.SearchByName

func searchGroundTool() ToolDef {
	return ToolDef{
		Name:        "search_ground",
		Title:       "Ground Transport Search",
		Description: "Search bus, train, and ferry connections between cities. Uses API-first providers across Europe, with browser/curl-assisted fallbacks disabled unless explicitly enabled.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"from":                    {Type: "string", Description: "Departure city name (e.g. Prague, Helsinki, Vienna)"},
				"to":                      {Type: "string", Description: "Arrival city name"},
				"return_date":             {Type: "string", Description: "Return date for a round-trip (ISO calendar date, same format as date); omit for one-way. Outbound and inbound routes are tagged with a direction field."},
				"date":                    {Type: "string", Description: "Departure date (YYYY-MM-DD)"},
				"currency":                {Type: "string", Description: "Price currency (default: EUR)"},
				"type":                    {Type: "string", Description: "Filter: bus, train, ferry, or empty for all"},
				"max_price":               {Type: "number", Description: "Maximum price filter (0 = no limit)"},
				"provider":                {Type: "string", Description: "Restrict to provider: flixbus, regiojet, trainline, sncf, transitous, db, oebb, ns, vr, tallink, dfds, vikingline, eckeroline, ferryhopper"},
				"allow_browser_fallbacks": {Type: "boolean", Description: "Allow browser/curl/cookie-assisted provider fallbacks (default: false)"},
			},
			Required: []string{"from", "to", "date"},
		},
		OutputSchema: groundSearchOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Ground Transport Search",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

func groundSearchOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":     schemaBool(),
			"count":       schemaIntDesc("Routes returned in this response (the tool shows up to 20 per direction, fewer when the result would pass 50 KB)."),
			"total_count": schemaIntDesc("Routes found before the cap; narrow with type, provider or max_price to see the rest."),
			"routes":      groundRoutesOutputSchema(),
			"error":       schemaString(),
			"provider_statuses": schemaArrayDesc("Per-provider outcome. reason is blocked, rate_limited, unavailable, dns, network, timeout or other when a provider did not answer, so a blocked provider is not mistaken for an empty search.", map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":      schemaString(),
					"name":    schemaString(),
					"status":  schemaString(),
					"results": schemaInt(),
					"error":   schemaString(),
					"reason":  schemaString(),
				},
			}),
		},
		"required": []string{"success", "count"},
	}
}

// groundPriceProvenanceSchema mirrors models.PriceProvenance.
func groundPriceProvenanceSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":        "string",
		"enum":        []string{string(models.PriceLive), string(models.PriceEstimated), string(models.PriceUnavailable)},
		"description": "live is a quoted fare; estimated is a static or distance-based figure; unavailable means no fare.",
	}
}

func groundRoutesOutputSchema() interface{} {
	return schemaArray(map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"provider":         schemaString(),
			"type":             schemaString(),
			"direction":        schemaString(),
			"price":            schemaNum(),
			"price_max":        schemaNum(),
			"currency":         schemaString(),
			"price_provenance": groundPriceProvenanceSchema(),
			"sources": schemaArrayDesc("Providers that sold this connection; search_ground lists them only when more than one did.", map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"provider":         schemaString(),
					"price":            schemaNum(),
					"currency":         schemaString(),
					"price_provenance": groundPriceProvenanceSchema(),
					"booking_url":      schemaString(),
				},
			}),
			"duration_minutes": schemaInt(),
			"transfers":        schemaInt(),
			"amenities":        schemaStringArray(),
			"seats_left":       schemaInt(),
			"booking_url":      schemaString(),
			"comparable_price": schemaNumDesc("Price in the search currency, used for ranking; absent when it could not be converted."),
			"cheapest_source":  schemaString(),
			"savings":          schemaNumDesc("How much the cheapest source undercuts the next one."),
			"departure":        groundStopSchema(),
			"arrival":          groundStopSchema(),
			"legs": schemaArrayDesc("Segments of the journey; search_ground omits them when a single leg only repeats the route, and omits a leg city the route or the previous leg already names.", map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":             schemaString(),
					"provider":         schemaString(),
					"departure":        groundStopSchema(),
					"arrival":          groundStopSchema(),
					"duration_minutes": schemaInt(),
					"amenities":        schemaStringArray(),
				},
			}),
			"confidence": map[string]interface{}{
				"type":        "object",
				"description": "How likely the headline price is bookable as shown.",
				"properties": map[string]interface{}{
					"label":   schemaStringDesc("high, medium, low or unrated"),
					"score":   schemaNumDesc("0 to 1; absent when unrated"),
					"basis":   schemaString(),
					"signals": schemaStringArray(),
				},
			},
			"sea_state": map[string]interface{}{
				"type":        "object",
				"description": "Forecast sea state for ferry routes.",
				"properties": map[string]interface{}{
					"wave_height_m":  schemaNum(),
					"swell_height_m": schemaNum(),
					"label":          schemaStringDesc("calm, moderate or rough"),
				},
			},
		},
	})
}

func groundStopSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"city":    schemaString(),
			"station": schemaString(),
			"time":    schemaStringDesc("ISO 8601 local time"),
		},
	}
}

func searchAirportTransfersTool() ToolDef {
	return ToolDef{
		Name:        "search_airport_transfers",
		Title:       "Airport Transfer Search",
		Description: "Search airport-to-hotel or airport-to-city ground transport. Lists exact airport routing first, adds taxi fare estimates, then broader city-level providers.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"airport_code": {Type: "string", Description: "Arrival airport IATA code (e.g. CDG, LHR, FCO)"},
				"destination":  {Type: "string", Description: "Hotel, address, district, or city destination"},
				"date":         {Type: "string", Description: "Travel date (YYYY-MM-DD)"},
				"arrival_time": {Type: "string", Description: "Only include routes departing at or after this local time (HH:MM)"},
				"currency":     {Type: "string", Description: "Price currency (default: EUR)"},
				"type":         {Type: "string", Description: "Filter: bus, train, taxi, tram, metro, mixed, or empty for all"},
				"max_price":    {Type: "number", Description: "Maximum price filter (0 = no limit)"},
				"provider":     {Type: "string", Description: "Restrict to provider: transitous, taxi, flixbus, regiojet, eurostar, db, sncf, trainline"},
			},
			Required: []string{"airport_code", "destination", "date"},
		},
		OutputSchema: airportTransferOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Airport Transfer Search",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

func airportTransferOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":          schemaBool(),
			"airport_code":     schemaString(),
			"airport":          schemaString(),
			"airport_city":     schemaString(),
			"destination":      schemaString(),
			"destination_city": schemaString(),
			"date":             schemaString(),
			"arrival_time":     schemaString(),
			"count":            schemaInt(),
			"exact_matches":    schemaInt(),
			"city_matches":     schemaInt(),
			"routes":           groundRoutesOutputSchema(),
			"error":            schemaString(),
		},
		"required": []string{"success", "airport_code", "airport", "airport_city", "destination", "date", "count", "exact_matches", "city_matches"},
	}
}

func handleSearchGround(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	from := models.ResolveLocationName(argString(args, "from"))
	to := models.ResolveLocationName(argString(args, "to"))
	date := argString(args, "date")

	if from == "" || to == "" || date == "" {
		return nil, nil, fmt.Errorf("from, to, and date are required")
	}

	opts := ground.SearchOptions{
		Currency:              argString(args, "currency"),
		ReturnDate:            argString(args, "return_date"),
		MaxPrice:              argFloat(args, "max_price", 0),
		Type:                  argString(args, "type"),
		AllowBrowserFallbacks: argBool(args, "allow_browser_fallbacks", false),
	}
	if p := argString(args, "provider"); p != "" {
		opts.Providers = strings.Split(p, ",")
	}

	// Explicit `type` arg is still respected as a hard filter (see
	// filterGroundRoutes). We deliberately do NOT seed opts.Type from
	// profile.GroundHints (or profile.Load) here. A profile-derived
	// preferred mode must not become a hard filter that silently drops
	// other modes (bus/train/ferry) the user never explicitly excluded.
	// Same silent-truncation class as flights #453 (and the prior hotel
	// HotelHints.MaxPrice seeding). After this change both CLI and MCP
	// show all modes unless the caller passes `type`/`--type` explicitly.
	// Profile mode-preference as a soft ranking signal remains future work.

	result, err := searchGroundByNameFunc(ctx, from, to, date, opts)
	if err != nil {
		return nil, nil, toolExecutionError("Ground transport search", err)
	}

	if !result.Success {
		if result.Error != "" {
			return nil, nil, withProviderFailures(toolResultError("Ground transport search", result.Error), result.ProviderStatuses)
		}
		// Evidence guard: only claim a definitive "no routes" when every relevant
		// provider was actually reached. If some timed out / rate-limited / failed,
		// absence is not established — say so instead of lying (mirrors flights).
		msg := fmt.Sprintf("No ground routes found from %s to %s on %s", from, to, date)
		if !result.Completeness.MayClaimExhaustive() {
			msg = fmt.Sprintf("No ground routes returned from %s to %s on %s yet. %s",
				from, to, date, result.Completeness.IncompleteNote())
		}
		return []ContentBlock{{Type: "text", Text: msg}}, result, nil
	}

	// Best-effort, free sea-state enrichment for ferry legs only. Never blocks
	// the core result: failures and missing coordinates leave routes unchanged.
	enrichFerrySeaState(ctx, result.Routes)

	// Best-effort destination intelligence for the arrival city on the default
	// ground search path: weather, safety, holidays, currency, country facts
	// inline, with no extra switch. Silent degrade — never blocks the search.
	result.Destination = enrichDestination(ctx, to, models.DateRange{CheckIn: date, CheckOut: opts.ReturnDate})

	// trvl sizes the result itself (no limit knob): the best-ranked rows per
	// direction, then fewer if both copies would still pass the budget.
	total := len(result.Routes)
	found, dirs := groundDirectionCounts(result.Routes)
	shown := capGroundRoutes(result.Routes, groundRowsPerDirection)
	trimmed := false
	for {
		resp := groundSearchResponse{GroundSearchResult: result, TotalCount: total, Routes: make([]groundRouteOut, 0, len(shown))}
		result.Count = len(shown)
		for _, r := range shown {
			resp.Routes = append(resp.Routes, compactGroundRoute(r))
		}
		summary := buildGroundRouteSummary(
			fmt.Sprintf("Found %d ground routes from %s to %s on %s", total, from, to, date),
			shown, len(shown),
		)
		if len(shown) < total {
			shownBy, _ := groundDirectionCounts(shown)
			parts := make([]string, 0, len(dirs))
			for _, d := range dirs {
				label := d
				if label == "" {
					label = "routes"
				}
				parts = append(parts, fmt.Sprintf("%d of %d %s", shownBy[d], found[d], label))
			}
			summary += "\n\nShowing " + strings.Join(parts, ", ") + "; narrow with type, provider or max_price."
			if trimmed {
				summary += " Fewer rows fit the 50 KB result budget."
			}
		}
		// Partial-failure caveat: some providers returned routes but others timed
		// out / failed, so this set is not exhaustive — say so rather than imply the
		// cheapest shown is the cheapest available.
		if note := result.Completeness.IncompleteNote(); note != "" {
			summary += "\n\n" + note
		}

		content, err := buildAnnotatedContentBlocks(summary, resp)
		if err != nil {
			return nil, nil, err
		}
		size, err := json.Marshal(ToolCallResult{Content: content, StructuredContent: resp})
		if err != nil {
			return nil, nil, fmt.Errorf("marshal result: %w", err)
		}
		if len(shown) <= 1 || len(size) < groundResultBudget {
			return content, resp, nil
		}
		shown = dropLowestRankedGroundRoute(shown)
		trimmed = true
	}
}

// groundRowsPerDirection caps each direction of a search_ground result.
const groundRowsPerDirection = 20

// groundResultBudget bounds the handler's result; the 2 KB under the 50 KB
// promise leaves room for the resource links and envelope added afterwards.
const groundResultBudget = 48_000

// groundDirectionCounts counts routes per direction ("" for one-way) and
// lists the directions in first-seen order.
func groundDirectionCounts(routes []models.GroundRoute) (map[string]int, []string) {
	counts := map[string]int{}
	var order []string
	for _, r := range routes {
		if counts[r.Direction] == 0 {
			order = append(order, r.Direction)
		}
		counts[r.Direction]++
	}
	return counts, order
}

// capGroundRoutes keeps the first n routes of each direction in search order.
// Ground search ranks live fares first and unpriced routes last, so these are
// the best-evidenced, cheapest rows.
func capGroundRoutes(routes []models.GroundRoute, n int) []models.GroundRoute {
	kept := map[string]int{}
	out := make([]models.GroundRoute, 0, min(len(routes), 2*n))
	for _, r := range routes {
		if kept[r.Direction] < n {
			kept[r.Direction]++
			out = append(out, r)
		}
	}
	return out
}

// dropLowestRankedGroundRoute removes the last row of the direction with the
// most rows, so a long outbound list shrinks before a short inbound one.
func dropLowestRankedGroundRoute(routes []models.GroundRoute) []models.GroundRoute {
	counts, order := groundDirectionCounts(routes)
	longest := order[0]
	for _, d := range order {
		if counts[d] > counts[longest] {
			longest = d
		}
	}
	for i := len(routes) - 1; i >= 0; i-- {
		if routes[i].Direction == longest {
			return slices.Delete(routes, i, i+1)
		}
	}
	return routes
}

func handleSearchAirportTransfers(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	input := trip.AirportTransferInput{
		AirportCode: argString(args, "airport_code"),
		Destination: argString(args, "destination"),
		Date:        argString(args, "date"),
		ArrivalTime: argString(args, "arrival_time"),
		Currency:    argString(args, "currency"),
		MaxPrice:    argFloat(args, "max_price", 0),
		Type:        argString(args, "type"),
	}
	if p := argString(args, "provider"); p != "" {
		input.Providers = strings.Split(p, ",")
	}
	if input.AirportCode == "" || input.Destination == "" || input.Date == "" {
		return nil, nil, fmt.Errorf("airport_code, destination, and date are required")
	}

	result, err := trip.SearchAirportTransfers(ctx, input)
	if err != nil {
		return nil, nil, err
	}
	if !result.Success {
		msg := fmt.Sprintf("No airport transfer routes found from %s to %s on %s", result.Airport, result.Destination, result.Date)
		if result.Error != "" {
			msg += ": " + result.Error
		}
		return []ContentBlock{{Type: "text", Text: msg}}, result, nil
	}

	summary := buildGroundRouteSummary(
		fmt.Sprintf("Found %d airport transfer routes from %s to %s on %s", result.Count, result.Airport, result.Destination, result.Date),
		result.Routes, 10,
	)
	if result.ArrivalTime != "" {
		summary += fmt.Sprintf("\n\nFiltered to departures at or after %s.", result.ArrivalTime)
	}
	if result.CityMatches > 0 {
		summary += fmt.Sprintf("\n\nExact airport matches are listed first (%d exact, %d broader city).", result.ExactMatches, result.CityMatches)
	}
	if groundRoutesHaveProvider(result.Routes, "taxi") {
		summary += "\n\nTaxi fares are estimates based on route distance and typical local tariffs."
	}

	// Door-to-door comparison card (Option C): enrich the raw routes into a
	// choosable set of modes with time/price/pros/cons/grounded steps and
	// cheapest/fastest/best-value/luggage labels. Additive + backward-compatible.
	comparison := transfer.BuildOptions(result.Routes, result.AirportCode, result.Airport, result.Destination)
	if len(comparison.Options) > 1 {
		summary += "\n\nCompare modes and choose what suits you — see cheapest / fastest / best-value / most-luggage-friendly in the structured 'comparison'."
	}
	response := airportTransferResponse{AirportTransferResult: result, Comparison: comparison}

	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}
	return content, response, nil
}

// airportTransferResponse extends the legacy AirportTransferResult with the
// door-to-door comparison card. The embedded pointer promotes all original
// JSON fields, so existing consumers are unaffected; `comparison` is additive.
type airportTransferResponse struct {
	*trip.AirportTransferResult
	Comparison models.TransferComparison `json:"comparison"`
}

// enrichFerrySeaState attaches a coarse Open-Meteo Marine sea-state forecast to
// ferry routes only. It is context-gated and silent-failure: non-ferry routes
// are skipped, and a missing departure port or a failed/timed-out marine lookup
// leaves the route unchanged. Ports are geocoded at most once per call.
func enrichFerrySeaState(ctx context.Context, routes []models.GroundRoute) {
	// Short, bounded budget so the enrichment can never stall the response.
	enrichCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	cache := make(map[string]*models.SeaState)
	for i := range routes {
		if routes[i].Type != "ferry" {
			continue
		}
		port := strings.TrimSpace(routes[i].Departure.City)
		if port == "" {
			port = strings.TrimSpace(routes[i].Departure.Station)
		}
		if port == "" {
			continue
		}

		if cached, ok := cache[port]; ok {
			routes[i].SeaState = cached
			continue
		}

		state, err := weather.SeaStateForCity(enrichCtx, port)
		if err != nil {
			cache[port] = nil // negative-cache so we don't retry the same port
			continue
		}
		s := state
		cache[port] = &s
		routes[i].SeaState = &s
	}
}

func groundRoutesHaveProvider(routes []models.GroundRoute, provider string) bool {
	for _, route := range routes {
		if strings.EqualFold(route.Provider, provider) {
			return true
		}
	}
	return false
}

func buildGroundRouteSummary(header string, routes []models.GroundRoute, maxRows int) string {
	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString(":\n\n")

	limit := min(len(routes), maxRows)
	// A round trip lists outbound then inbound rows; a header before each
	// direction keeps inbound rows from reading as more outbound options.
	prevDir := ""
	for i, r := range routes[:limit] {
		if r.Direction != "" && r.Direction != prevDir {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(strings.ToUpper(r.Direction[:1]) + r.Direction[1:] + ":\n")
		}
		prevDir = r.Direction
		price := groundPriceLabel(r)
		dur := fmt.Sprintf("%dh%02dm", r.Duration/60, r.Duration%60)
		transfers := "direct"
		if r.Transfers > 0 {
			transfers = fmt.Sprintf("%d transfers", r.Transfers)
		}

		depTime := safeTimeSlice(r.Departure.Time)
		arrTime := safeTimeSlice(r.Arrival.Time)

		_, _ = fmt.Fprintf(&sb, "%d. **%s** %s | %s | %s | %s %s→%s",
			i+1, price, r.Type, dur, transfers, r.Provider, depTime, arrTime)

		if r.SeatsLeft != nil && *r.SeatsLeft <= 10 {
			_, _ = fmt.Fprintf(&sb, " | %d seats left", *r.SeatsLeft)
		}
		if len(r.Amenities) > 0 {
			_, _ = fmt.Fprintf(&sb, " | %s", strings.Join(r.Amenities, ", "))
		}
		sb.WriteString("\n")
	}
	if len(routes) > limit {
		_, _ = fmt.Fprintf(&sb, "\n... and %d more routes", len(routes)-limit)
	}
	return sb.String()
}

// groundPriceLabel marks a price the way the CLI table does (estimatedPrice in
// cmd/trvl/ground.go): only a live fare reads as a plain price, any other
// priced route is an estimate, and a route without a fare never shows a number.
func groundPriceLabel(r models.GroundRoute) string {
	switch {
	case r.Price <= 0 || r.PriceProvenance == models.PriceUnavailable:
		return "price unavailable"
	case r.PriceProvenance == models.PriceLive:
		if r.PriceMax > 0 && r.PriceMax != r.Price {
			return fmt.Sprintf("%s %.2f-%.2f", r.Currency, r.Price, r.PriceMax)
		}
		return fmt.Sprintf("%s %.2f", r.Currency, r.Price)
	}
	symbols := map[string]string{"EUR": "€", "GBP": "£", "USD": "$"}
	if sym, ok := symbols[strings.ToUpper(r.Currency)]; ok {
		return fmt.Sprintf("~%s%.0f (est.)", sym, r.Price)
	}
	return fmt.Sprintf("~%s %.0f (est.)", r.Currency, r.Price)
}

// safeTimeSlice extracts HH:MM from an ISO 8601 timestamp, or returns the raw string.
func safeTimeSlice(t string) string {
	if len(t) >= 16 {
		return t[11:16]
	}
	return t
}

// groundSearchResponse is the search_ground payload: the search result with
// its routes capped and compacted. Routes shadows the embedded field, so the
// rest of GroundSearchResult keeps its JSON shape.
type groundSearchResponse struct {
	*models.GroundSearchResult
	Routes     []groundRouteOut `json:"routes"`
	TotalCount int              `json:"total_count"`
}

// groundRouteOut drops what repeats the route: legs that add nothing, leg
// cities already known, and confidence fields the label already carries.
type groundRouteOut struct {
	models.GroundRoute
	Legs       []groundLegOut       `json:"legs,omitempty"`
	Confidence *groundConfidenceOut `json:"confidence,omitempty"`
}

type groundLegOut struct {
	models.GroundLeg
	Departure groundStopOut `json:"departure"`
	Arrival   groundStopOut `json:"arrival"`
}

// groundStopOut is models.GroundStop with an omittable city.
type groundStopOut struct {
	City    string `json:"city,omitempty"`
	Station string `json:"station,omitempty"`
	Time    string `json:"time"`
}

type groundConfidenceOut struct {
	Score   float64  `json:"score,omitempty"`
	Label   string   `json:"label"`
	Basis   string   `json:"basis"`
	Signals []string `json:"signals,omitempty"`
}

func compactGroundRoute(r models.GroundRoute) groundRouteOut {
	if len(r.Sources) <= 1 {
		r.Sources = nil
	}
	out := groundRouteOut{GroundRoute: r}
	if c := r.Confidence; c != nil {
		out.Confidence = &groundConfidenceOut{Score: c.Score, Label: c.Label, Basis: c.Basis, Signals: c.Signals}
	}
	if len(r.Legs) == 1 {
		l := r.Legs[0]
		if l.Type == r.Type && l.Provider == r.Provider && l.Departure == r.Departure && l.Arrival == r.Arrival {
			return out
		}
	}
	prevArrival := ""
	for _, l := range r.Legs {
		known := func(city string) bool {
			return city == r.Departure.City || city == r.Arrival.City || city == prevArrival
		}
		leg := groundLegOut{GroundLeg: l, Departure: groundStopOut(l.Departure), Arrival: groundStopOut(l.Arrival)}
		if known(l.Departure.City) {
			leg.Departure.City = ""
		}
		if known(l.Arrival.City) {
			leg.Arrival.City = ""
		}
		prevArrival = l.Arrival.City
		out.Legs = append(out.Legs, leg)
	}
	return out
}
