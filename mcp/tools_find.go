// Package mcp -- tools_find.go
//
// MCP adapters for internal/find. Two tools:
//
//   - plan_flight_bundle : non-interactive parity with `trvl find` (the CLI
//     command previously named `trvl hunt`; hunt is retained as a hidden
//     alias for back-compat). Takes
//     structured args, runs the full pipeline, returns ranked bundles.
//
//   - find_interactive   : MCP-native flow. Uses elicitation to ask the user
//     which filter to relax when zero results come back, uses progress
//     notifications during multi-origin fan-out, and uses sampling to have
//     the client LLM break ties between close-ranked bundles. This is the
//     surface that showcases what MCP can do over a plain CLI.
package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/tripsearch"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// --- plan_flight_bundle (non-interactive parity) ---

// planFlightBundleTool returns the tool definition for plan_flight_bundle.
func planFlightBundleTool() ToolDef {
	return ToolDef{
		Name:  "plan_flight_bundle",
		Title: "Plan Flight Bundle",
		Description: `Run Mikko's mental-model flight search end-to-end.

Applies: home-fan origin expansion, rail+fly origins (ZYR/ANR/BRU when AMS
involved), long-layover filter, lounge-access filter, no-early-connection
filter, cheapest-first ranking, top-N slicing.

Returns ranked bundles + filter-impact log so the caller can explain which
hacks saved money and which filters dropped candidates.

Non-interactive. For the interactive flow that can ask the user to relax
filters when zero results come back, use find_interactive instead.`,
		InputSchema:  findInputSchema(),
		OutputSchema: findOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Plan Flight Bundle",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

// handlePlanFlightBundle implements the non-interactive tool.
func handlePlanFlightBundle(ctx context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	req := findRequestFromArgs(args)

	reportProgress := func(stage string, done, total int) {
		if progress == nil {
			return
		}
		progress(float64(done), float64(total), stage)
	}

	result, err := tripsearch.Search(ctx, req, nil, reportProgress)
	if err != nil {
		return nil, nil, toolExecutionError("plan_flight_bundle", err)
	}

	summary := formatFindSummary(result)
	blocks, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}
	return blocks, result, nil
}

// --- find_interactive (elicitation + progress + sampling) ---

// findInteractiveTool returns the tool definition for find_interactive.
func findInteractiveTool() ToolDef {
	return ToolDef{
		Name:  "find_interactive",
		Title: "Find Flights (Interactive)",
		Description: `Interactive flight hunt that can ask the user to relax filters when
zero results come back.

Flow:
 1. Run the full Mikko pipeline with default filters on.
 2. If 0 results, elicit the user to pick a filter to relax (lounge /
    no-early-connection / long-layover). Re-run once.
 3. Streams progress notifications at every stage.
 4. When multiple bundles are within ranking range, ask the client LLM
    (sampling) to pick the best one given the user's profile notes.

Requires client to declare elicitation capability during initialize. Falls
back to plan_flight_bundle behavior when elicitation is unavailable.`,
		InputSchema:  findInputSchema(),
		OutputSchema: findOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Find Flights (Interactive)",
			ReadOnlyHint:   true,
			IdempotentHint: false,
			OpenWorldHint:  true,
		},
	}
}

// handleFindInteractive implements the MCP-native interactive flow.
func handleFindInteractive(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	reportProgress := func(stage string, done, total int) {
		if progress != nil {
			progress(float64(done), float64(total), stage)
		}
	}

	req := findRequestFromArgs(args)
	result, err := tripsearch.Search(ctx, req, nil, reportProgress)
	if err != nil {
		return nil, nil, toolExecutionError("find_interactive", err)
	}

	// Step: if zero results and we have elicitation, offer a relax flow.
	if result.Count == 0 && elicit != nil {
		relaxed, relaxErr := relaxAndRetry(ctx, req, result, elicit, reportProgress)
		if relaxErr != nil {
			return nil, nil, toolExecutionError("find_interactive.relax", relaxErr)
		}
		if relaxed != nil {
			result = relaxed
		}
	}

	// Step: if 2+ bundles and sampling is available, ask the LLM to break
	// ties using user profile context. The LLM's pick is hoisted to index 0.
	if sampling != nil && len(result.Flights) >= 2 {
		if pick := sampleBestPick(result, sampling); pick > 0 && pick < len(result.Flights) {
			reorderFlightsFirst(result, pick)
		}
	}

	summary := formatFindSummary(result)
	blocks, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}
	return blocks, result, nil
}

// relaxAndRetry asks the user which filter to drop, then re-runs Hunt with
// that filter disabled. Returns the new result (or nil when user cancels).
func relaxAndRetry(ctx context.Context, req tripsearch.Request, original *tripsearch.Result, elicit ElicitFunc, progress tripsearch.Progress) (*tripsearch.Result, error) {
	options := relaxOptions(original.FiltersApplied)
	if len(options) == 1 { // only "cancel" — nothing to relax
		return nil, nil
	}

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        stringsToInterfaces(options),
				"description": "Which filter to relax so we can re-run the search",
			},
		},
		"required": []interface{}{"action"},
	}

	msg := fmt.Sprintf("Zero profile-compliant bundles found (pre-filter: %d). Which filter should we relax?",
		original.PreFilterCount)

	reply, err := elicit(msg, schema)
	if err != nil {
		return nil, err
	}
	action, _ := reply["action"].(string)
	if action == "" || action == "cancel" {
		return nil, nil
	}

	relaxed := req
	switch action {
	case "drop_lounge_required":
		relaxed.LoungeRequired = false
	case "drop_no_early_connection":
		relaxed.NoEarlyConnection = false
	case "drop_long_layover":
		relaxed.MinLayoverMinutes = 0
		relaxed.LayoverAirports = nil
	default:
		return nil, fmt.Errorf("relax action %q not supported", action)
	}

	return tripsearch.Search(ctx, relaxed, nil, progress)
}

// relaxOptions returns the list of relax-actions available for the current
// filter-log, plus the sentinel "cancel".
func relaxOptions(log tripsearch.FilterLog) []string {
	options := []string{}
	if log.LoungeAccess.Ran && log.LoungeAccess.Dropped > 0 {
		options = append(options, "drop_lounge_required")
	}
	if log.NoEarlyConnection.Ran && log.NoEarlyConnection.Dropped > 0 {
		options = append(options, "drop_no_early_connection")
	}
	if log.LongLayover.Ran && log.LongLayover.Dropped > 0 {
		options = append(options, "drop_long_layover")
	}
	options = append(options, "cancel")
	return options
}

// sampleBestPick asks the LLM which bundle best matches the user's profile.
// Returns the index into result.Flights, or -1 when the LLM declines.
func sampleBestPick(result *tripsearch.Result, sampling SamplingFunc) int {
	lines := []string{
		"Pick the single best bundle for a traveler who prefers afternoon/evening departures, direct flights when affordable, and rail+fly via Belgium when it saves more than €50. Reply with just the index number (0-indexed).",
		"",
	}
	for i, f := range result.Flights {
		lines = append(lines, fmt.Sprintf("%d. €%.0f  %s  %s",
			i, f.Price, tripsearch.RouteSummary(f), tripsearch.Annotations(f, result.Origins)))
	}
	prompt := strings.Join(lines, "\n")

	reply, err := sampling([]SamplingMessage{{
		Role:    "user",
		Content: SamplingContent{Type: "text", Text: prompt},
	}}, 32)
	if err != nil {
		return -1
	}
	reply = strings.TrimSpace(reply)
	var n int
	if _, err := fmt.Sscanf(reply, "%d", &n); err != nil {
		return -1
	}
	if n < 0 || n >= len(result.Flights) {
		return -1
	}
	return n
}

// reorderFlightsFirst moves the bundle at idx to position 0, preserving the
// relative order of the remaining bundles.
func reorderFlightsFirst(result *tripsearch.Result, idx int) {
	if idx <= 0 || idx >= len(result.Flights) {
		return
	}
	picked := result.Flights[idx]
	out := make([]models.FlightResult, 0, len(result.Flights))
	out = append(out, picked)
	for i, f := range result.Flights {
		if i == idx {
			continue
		}
		out = append(out, f)
	}
	result.Flights = out
}

// --- Shared helpers ---

// findRequestFromArgs parses the MCP arg map into a tripsearch.Request. Missing
// fields fall back to zero values (which hunt.Hunt interprets as "use profile
// default").
func findRequestFromArgs(args map[string]any) tripsearch.Request {
	return tripsearch.Request{
		Origin:            argString(args, "origin"),
		Destination:       argString(args, "destination"),
		Date:              argString(args, "departure_date"),
		ReturnDate:        argString(args, "return_date"),
		Cabin:             argString(args, "cabin"),
		MinLayoverMinutes: argInt(args, "min_layover_minutes", 0),
		LayoverAirports:   argStringSlice(args, "layover_at"),
		NoEarlyConnection: argBool(args, "no_early_connection", false),
		LoungeRequired:    argBool(args, "lounge_required", false),
		HiddenCity:        argBool(args, "hidden_city", false),
		TopN:              argInt(args, "top_n", 0),
	}
}

// formatFindSummary renders a one-paragraph human-readable summary of the
// result. Used as the user-facing text ContentBlock.
func formatFindSummary(r *tripsearch.Result) string {
	if r == nil || r.Count == 0 {
		pre := 0
		if r != nil {
			pre = r.PreFilterCount
		}
		impact := "no filters active"
		if r != nil {
			impact = filterImpactText(r.FiltersApplied)
		}
		return fmt.Sprintf("Found 0 bundles (pre-filter: %d). Filters: %s", pre, impact)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d bundle(s) across origins %v. Cheapest: €%.0f (%s).\n",
		r.Count, r.Origins, r.Flights[0].Price, tripsearch.RouteSummary(r.Flights[0]))
	if r.PreFilterCount > r.Count {
		fmt.Fprintf(&b, "Filter impact: %s\n", filterImpactText(r.FiltersApplied))
	}
	return b.String()
}

// filterImpactText summarises which filters dropped how many.
func filterImpactText(log tripsearch.FilterLog) string {
	parts := []string{}
	if log.LongLayover.Ran {
		parts = append(parts, fmt.Sprintf("long-layover −%d", log.LongLayover.Dropped))
	}
	if log.LoungeAccess.Ran {
		parts = append(parts, fmt.Sprintf("lounge −%d", log.LoungeAccess.Dropped))
	}
	if log.NoEarlyConnection.Ran {
		parts = append(parts, fmt.Sprintf("no-early-connection −%d", log.NoEarlyConnection.Dropped))
	}
	if len(parts) == 0 {
		return "no filters active"
	}
	return strings.Join(parts, ", ")
}

// findInputSchema is shared by both the non-interactive and interactive tool.
func findInputSchema() InputSchema {
	return InputSchema{
		Type: "object",
		Properties: map[string]Property{
			"origin":              {Type: "string", Description: "Origin IATA code(s), comma-separated, or 'home' to expand from preferences.home_airports"},
			"destination":         {Type: "string", Description: "Destination IATA code(s), comma-separated"},
			"departure_date":      {Type: "string", Description: "ISO 8601 calendar date, e.g. 2026-04-23"},
			"return_date":         {Type: "string", Description: "ISO 8601 calendar date for return (empty = one-way)"},
			"cabin":               {Type: "string", Description: "Cabin class: economy, premium_economy, business, first"},
			"min_layover_minutes": {Type: "integer", Description: "Only keep flights with a layover of at least N minutes (0 = no duration constraint)"},
			"layover_at":          {Type: "array", Description: "Restrict qualifying layovers to these IATA codes (empty = any airport)"},
			"no_early_connection": {Type: "boolean", Description: "Drop flights whose post-overnight leg departs before preferences.early_connection_floor (default 10:00)"},
			"lounge_required":     {Type: "boolean", Description: "Drop flights where a layover airport lacks lounge coverage from user's cards"},
			"hidden_city":         {Type: "boolean", Description: "Also consider hidden-city candidates"},
			"top_n":               {Type: "integer", Description: "Cap returned bundle count (0 = no cap)"},
		},
		Required: []string{"origin", "destination", "departure_date"},
	}
}

// findOutputSchema is the structured-output JSON Schema for both tools.
func findOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"count":            schemaInt(),
			"trip_type":        schemaString(),
			"origins":          schemaStringArray(),
			"pre_filter_count": schemaInt(),
			"filters_applied": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"long_layover":        filterStepSchema(),
					"lounge_access":       filterStepSchema(),
					"no_early_connection": filterStepSchema(),
				},
			},
			"flights": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"price":    schemaNum(),
					"currency": schemaString(),
					"duration": schemaInt(),
					"stops":    schemaInt(),
					"provider": schemaString(),
					"legs":     schemaArray(map[string]interface{}{"type": "object"}),
				},
			}),
		},
	}
}

func filterStepSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"ran":     schemaBool(),
			"dropped": schemaInt(),
			"kept":    schemaInt(),
		},
	}
}

// stringsToInterfaces is a tiny helper for JSON-schema enum values.
func stringsToInterfaces(in []string) []interface{} {
	out := make([]interface{}, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
