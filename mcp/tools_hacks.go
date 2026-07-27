package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// detectTravelHacksTool returns the MCP tool definition for hack detection.
func detectTravelHacksTool() ToolDef {
	return ToolDef{
		Name:        "detect_travel_hacks",
		Title:       "Detect Travel Optimization Hacks",
		Description: "Automatically detect money-saving travel hacks for a route: throwaway ticketing, hidden city, positioning flights, split ticketing, overnight transport (saved hotel night), airline stopover programs, and date flexibility.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origin":      {Type: "string", Description: "Origin IATA airport code (e.g. HEL)"},
				"destination": {Type: "string", Description: "Destination IATA airport code (e.g. PRG)"},
				"date":        {Type: "string", Description: "Departure date (YYYY-MM-DD)"},
				"return_date": {Type: "string", Description: "Return date for round-trip analysis (YYYY-MM-DD); enables split and throwaway checks"},
				"currency":    {Type: "string", Description: "Display currency (default: EUR)"},
				"carry_on":    {Type: "boolean", Description: "Carry-on only trip — enables hidden city suggestions"},
				"naive_price": {Type: "number", Description: "Known baseline one-way price for comparison (optional)"},
				"passengers":  {Type: "integer", Description: "Number of passengers (group split hack fires at 3+)"},
			},
			Required: []string{"origin", "destination", "date"},
		},
		OutputSchema: hacksOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Detect Travel Optimization Hacks",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

// hacksResponse is the hacks tool's structured payload. It sits at package level,
// beside the schema, so a test can check that the two agree: the schema shipped
// without declaring complete or note, so the completeness signal this whole change
// exists to provide was absent from the contract an agent reads to discover it. The
// payload had it and the schema did not, which is the same defect as prose claiming
// more than the code knows, one layer out.
type hacksResponse struct {
	Origin      string       `json:"origin"`
	Destination string       `json:"destination"`
	Date        string       `json:"date"`
	Count       int          `json:"count"`
	Hacks       []hacks.Hack `json:"hacks"`
	// Complete is false when the sweep ended before every detector finished. An
	// agent reading this response would otherwise present a truncated list as the
	// full answer, which is the one thing trvl's provider statuses exist to
	// prevent. It carries no omitempty on purpose: a missing field would be
	// indistinguishable from a completed sweep, so it is always stated.
	Complete bool `json:"complete"`
	// Note explains a partial sweep in prose for a reader who does not check the
	// boolean. Absent when the sweep completed, since there is nothing to explain.
	Note string `json:"note,omitempty"`
}

// railFlyBundleSchema describes the rail-and-fly itinerary a multimodal hack can
// carry: several legs priced as one fare, with the connection guarantee stated.
//
// It is a separate function so the shape stays next to nothing else and can be
// compared against internal/hacks.RailFlyBundle by the schema agreement test, which
// walks nested types rather than stopping at the top level. Stopping at the top
// level is exactly how this whole subtree stayed undeclared.
func railFlyBundleSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"legs": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"mode":             schemaString(),
					"from":             schemaString(),
					"to":               schemaString(),
					"provider":         schemaString(),
					"duration_minutes": schemaInt(),
					"cost":             schemaNum(),
					"currency":         schemaString(),
					// estimated separates a modelled fare from a live quote, which
					// is the difference between a saving you can book and one you
					// cannot.
					"estimated": schemaBool(),
				},
				"required": []string{"mode", "from", "to", "provider", "cost", "currency"},
			}),
			"total_cost":            schemaNum(),
			"currency":              schemaString(),
			"change_window_minutes": schemaInt(),
			"connection_guaranteed": schemaBool(),
			"connection_status":     schemaString(),
		},
		"required": []string{"legs", "total_cost", "currency", "change_window_minutes", "connection_guaranteed", "connection_status"},
	}
}

func hacksOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"origin":      schemaString(),
			"destination": schemaString(),
			"date":        schemaString(),
			"count":       schemaInt(),
			"hacks": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":        schemaString(),
					"title":       schemaString(),
					"description": schemaString(),
					"savings":     schemaNum(),
					"currency":    schemaString(),
					"risks":       schemaStringArray(),
					"steps":       schemaStringArray(),
					"citations":   schemaStringArray(),
					// The rail fields were emitted and undeclared, which mattered
					// more than the count suggests. rail_cost_estimated exists so a
					// modelled ground fare is not mistaken for a live quote, and an
					// agent reading the schema had no way to know to look for it: the
					// honesty flag was itself undiscoverable. These carry the AF-KLM
					// rail-and-fly detail that is the reason that provider is wired
					// in at all.
					"rail_cost":           schemaNum(),
					"rail_cost_currency":  schemaString(),
					"rail_provider":       schemaString(),
					"rail_cost_estimated": schemaBool(),
					"rail_cost_note":      schemaString(),
					// The rail-and-fly bundle was undeclared in full, legs and all.
					// It is the itinerary AF-KLM is wired in to produce, where a
					// train leg is ticketed as part of the flight, so an agent that
					// reads the schema to decide what it can ask for could not learn
					// the single most distinctive thing this tool returns.
					"rail_fly_bundle": railFlyBundleSchema(),
				},
				// Only the fields a hack always carries. The rest are omitempty, so
				// requiring them would describe a payload trvl does not send.
				"required": []string{"type", "title", "description", "savings", "currency", "steps"},
			}),
			"complete": schemaBool(),
			"note":     schemaString(),
		},
		// complete is required because its absence and false are different claims,
		// and a caller cannot tell them apart. note is optional: a completed sweep
		// has nothing to explain.
		"required": []string{"origin", "destination", "date", "count", "hacks", "complete"},
	}
}

// detectorNames is a labels-only subset of detector categories used purely to
// animate progress while DetectAll runs. It is NOT the authoritative detector
// set: DetectAll registers more detectors than are named here, and the count of
// hacks actually returned comes from DetectAll, not from len(detectorNames).
// These labels exist only to give the progress bar human-readable checkpoints.
var detectorNames = []string{
	"throwaway ticketing", "hidden city", "positioning flights",
	"split ticketing", "night transport", "airline stopovers",
	"date flexibility", "open jaw", "ferry positioning",
	"multi-stop routing", "currency arbitrage", "calendar conflicts",
	"Tuesday booking", "low-cost carriers",
	"multimodal skip flight", "multimodal positioning",
	"multimodal open jaw ground", "multimodal return split",
	"advance purchase", "group split",
}

func handleDetectTravelHacks(ctx context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	origin := strings.ToUpper(argString(args, "origin"))
	destination := strings.ToUpper(argString(args, "destination"))
	date := argString(args, "date")
	returnDate := argString(args, "return_date")
	currency := argString(args, "currency")
	if currency == "" {
		currency = "EUR"
	}
	carryOn := argBool(args, "carry_on", false)
	naivePrice := argFloat(args, "naive_price", 0)
	passengers := argInt(args, "passengers", 1)

	sendProgress(progress, 0, 100, fmt.Sprintf("Analysing %s→%s for travel hacks...", origin, destination))

	input := hacks.DetectorInput{
		Origin:      origin,
		Destination: destination,
		Date:        date,
		ReturnDate:  returnDate,
		Currency:    currency,
		CarryOnOnly: carryOn,
		NaivePrice:  naivePrice,
		Passengers:  passengers,
	}

	// Thread the traveller's loyalty profile so loyalty-aware detectors
	// (mileage run, back-to-back) prefer the user's own alliances/status.
	// Missing or unreadable preferences leave the zero profile, which preserves
	// the pre-loyalty behaviour — keeping this surface fed identically to the CLI.
	if prefs, err := preferences.Load(); err == nil {
		input.Loyalty = hacks.LoyaltyFromPreferences(prefs)
	}

	// Emit progress for each detector group (detectors run in parallel internally).
	n := float64(len(detectorNames))
	for i, name := range detectorNames {
		sendProgress(progress, float64(i+1)/n*80, 100, fmt.Sprintf("Checking %s...", name))
	}

	detected, complete := hacks.DetectAll(ctx, input)

	if !complete {
		sendProgress(progress, 90, 100, "Not every detector was confirmed to finish; scoring the ones that delivered...")
	} else {
		sendProgress(progress, 90, 100, "Scoring and filtering results...")
	}

	resp := hacksResponse{
		Origin:      origin,
		Destination: destination,
		Date:        date,
		Count:       len(detected),
		Hacks:       detected,
		Complete:    complete,
	}
	if !complete {
		resp.Note = "partial: not every detector was confirmed to finish, so more hacks may exist"
	}
	if resp.Hacks == nil {
		resp.Hacks = []hacks.Hack{}
	}

	if complete {
		sendProgress(progress, 100, 100, fmt.Sprintf("Found %d hacks", len(detected)))
	} else {
		sendProgress(progress, 100, 100, fmt.Sprintf("Found %d hacks (partial: not every detector was confirmed to finish)", len(detected)))
	}

	summary := buildHacksSummary(origin, destination, date, detected, complete)
	content, err := buildAnnotatedContentBlocks(summary, resp)
	if err != nil {
		return nil, nil, err
	}
	return content, resp, nil
}

// buildHacksSummary writes the human-readable half of the response.
//
// It takes complete because the prose has to agree with the structured data. An
// empty partial sweep read "No travel hacks detected", which states a finding the
// sweep never made: nothing was detected because it ran out of time, not because
// there was nothing there. A reader acting on that text would conclude the route
// has no savings.
func buildHacksSummary(origin, destination, date string, detected []hacks.Hack, complete bool) string {
	route := origin + "→" + destination + " on " + date
	if len(detected) == 0 {
		if !complete {
			// "Retry with more time" told the caller to do something that often
			// cannot work: the sweep also stops at its own bounds, 20s per detector
			// and 25s overall, which no request parameter raises. An agent acting on
			// that advice would keep extending its deadline and keep getting the
			// same answer. The replacement then blamed a slow provider, which is
			// equally unsupported: a short caller deadline, or a detector doing
			// local work past its allowance, produces the same incompleteness with
			// every provider healthy. State the bound, diagnose nothing.
			return "No travel hacks found for " + route + ". " +
				"Not every detector was confirmed to finish, so this is not a finding that none exist. " +
				"Retrying may return more."
		}
		return "No travel hacks detected for " + route + "."
	}
	var sb strings.Builder
	if complete {
		sb.WriteString("Travel hacks for " + route + ":\n\n")
	} else {
		sb.WriteString("Travel hacks for " + route + " (partial: not every detector was confirmed to finish, so more may exist):\n\n")
	}
	for i, h := range detected {
		_, _ = fmt.Fprintf(&sb, "%d. %s", i+1, h.Title)
		if h.Savings > 0 {
			_, _ = fmt.Fprintf(&sb, " — saves %s %.0f", h.Currency, h.Savings)
		}
		sb.WriteString("\n")
		sb.WriteString("   " + h.Description + "\n\n")
	}
	return sb.String()
}

// detectAccommodationHacksTool returns the MCP tool definition for accommodation
// split detection.
func detectAccommodationHacksTool() ToolDef {
	return ToolDef{
		Name:        "detect_accommodation_hacks",
		Title:       "Detect Accommodation Split Opportunities",
		Description: "Find cheaper hotel stays by splitting a long booking across 2-3 properties. Accounts for moving costs (EUR 15/move) and only reports splits saving at least EUR 50 and 15% vs a single booking.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"city":       {Type: "string", Description: "City name (e.g. Prague, Amsterdam)"},
				"checkin":    {Type: "string", Description: "Check-in date (YYYY-MM-DD)"},
				"checkout":   {Type: "string", Description: "Check-out date (YYYY-MM-DD)"},
				"currency":   {Type: "string", Description: "Display currency (default: EUR)"},
				"max_splits": {Type: "integer", Description: "Maximum properties to split across, 2 or 3 (default: 3)"},
				"guests":     {Type: "integer", Description: "Number of guests (default: 2)"},
			},
			Required: []string{"city", "checkin", "checkout"},
		},
		OutputSchema: accommodationHacksOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Detect Accommodation Split Opportunities",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

func accommodationHacksOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"city":     schemaString(),
			"checkin":  schemaString(),
			"checkout": schemaString(),
			"count":    schemaInt(),
			"hacks": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":        schemaString(),
					"title":       schemaString(),
					"description": schemaString(),
					"savings":     schemaNum(),
					"currency":    schemaString(),
					"risks":       schemaStringArray(),
					"steps":       schemaStringArray(),
					"citations":   schemaStringArray(),
				},
			}),
		},
		"required": []string{"city", "checkin", "checkout", "count", "hacks"},
	}
}

func handleDetectAccommodationHacks(ctx context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	city := argString(args, "city")
	checkin := argString(args, "checkin")
	checkout := argString(args, "checkout")
	currency := argString(args, "currency")
	if currency == "" {
		currency = "EUR"
	}
	maxSplits := argInt(args, "max_splits", 3)
	guests := argInt(args, "guests", 2)

	in := hacks.AccommodationSplitInput{
		City:      city,
		CheckIn:   checkin,
		CheckOut:  checkout,
		Currency:  currency,
		MaxSplits: maxSplits,
		Guests:    guests,
	}

	detected := hacks.DetectAccommodationSplit(ctx, in)

	type response struct {
		City     string       `json:"city"`
		CheckIn  string       `json:"checkin"`
		CheckOut string       `json:"checkout"`
		Count    int          `json:"count"`
		Hacks    []hacks.Hack `json:"hacks"`
	}

	resp := response{
		City:     city,
		CheckIn:  checkin,
		CheckOut: checkout,
		Count:    len(detected),
		Hacks:    detected,
	}
	if resp.Hacks == nil {
		resp.Hacks = []hacks.Hack{}
	}

	summary := buildAccomHacksSummary(city, checkin, checkout, detected)
	content, err := buildAnnotatedContentBlocks(summary, resp)
	if err != nil {
		return nil, nil, err
	}
	return content, resp, nil
}

func buildAccomHacksSummary(city, checkin, checkout string, detected []hacks.Hack) string {
	if len(detected) == 0 {
		return fmt.Sprintf("No accommodation split opportunities found for %s (%s to %s).", city, checkin, checkout)
	}
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Accommodation hacks for %s (%s to %s):\n\n", city, checkin, checkout)
	for i, h := range detected {
		_, _ = fmt.Fprintf(&sb, "%d. %s", i+1, h.Title)
		if h.Savings > 0 {
			_, _ = fmt.Fprintf(&sb, " — saves %s %.0f", h.Currency, h.Savings)
		}
		sb.WriteString("\n")
		sb.WriteString("   " + h.Description + "\n\n")
	}
	return sb.String()
}
