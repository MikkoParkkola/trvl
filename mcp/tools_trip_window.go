package mcp

import (
	"context"
	"fmt"

	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/tripwindow"
)

// findTripWindowTool returns the MCP tool definition for find_trip_window.
//
// Architecture note — tool description-driven orchestration:
// Rather than using MCP sampling (which is unsupported on most clients) to
// fetch calendar data server-side, the tool description instructs the
// orchestrating LLM to call the user's calendar tool first and pass the results
// in as busy_intervals. This pattern works on every MCP client because it
// relies only on the LLM reading the tool description — no special protocol
// features required.
func findTripWindowTool() ToolDef {
	return ToolDef{
		Name:  "find_trip_window",
		Title: "Find Optimal Travel Window",
		Description: `Find optimal travel windows by intersecting price calendars with user time constraints.

IMPORTANT: Before calling this tool, use the user's calendar tool (Google Calendar,
Apple Calendar, Outlook, or any other calendar MCP tool) to fetch busy intervals
within the search window. Pass them as busy_intervals. If no calendar tool is
available, pass an empty array — this tool works without constraints but will not
avoid your scheduled commitments.

How to use:
1. Fetch busy intervals from the user's calendar for the [window_start, window_end] range.
2. Call this tool with those intervals as busy_intervals.
3. Optionally pass preferred_intervals (e.g. school holidays, long weekends).

Returns ranked candidate windows with estimated round-trip cost for each.
Useful for "when's the best time for trip X?" queries where the user has
date flexibility but real scheduling constraints.`,
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"destination": {
					Type:        "string",
					Description: "Destination city or IATA airport code (e.g. PRG, Tokyo, Barcelona)",
				},
				"origin": {
					Type:        "string",
					Description: "Origin city or IATA airport code. Defaults to first home_airport from user preferences if omitted.",
				},
				"window_start": {
					Type:        "string",
					Description: "Earliest possible departure date (YYYY-MM-DD)",
				},
				"window_end": {
					Type:        "string",
					Description: "Latest possible return date (YYYY-MM-DD)",
				},
				"busy_intervals": {
					Type:        "array",
					Items:       &Property{Type: "object"},
					Description: "User's busy periods to avoid. FETCH FROM USER'S CALENDAR TOOL before calling this. Each item: {\"start\": \"YYYY-MM-DD\", \"end\": \"YYYY-MM-DD\", \"reason\": \"optional label\"}.",
				},
				"preferred_intervals": {
					Type:        "array",
					Items:       &Property{Type: "object"},
					Description: "User's preferred travel windows (e.g. school holidays, long weekends). Same format as busy_intervals. Windows overlapping these are boosted in ranking.",
				},
				"min_nights": {
					Type:        "integer",
					Description: "Minimum trip length in nights (default: 3)",
				},
				"max_nights": {
					Type:        "integer",
					Description: "Maximum trip length in nights (default: 7)",
				},
				"max_candidates": {
					Type:        "integer",
					Description: "Maximum number of windows to return (default: 5)",
				},
				"budget_eur": {
					Type:        "number",
					Description: "Maximum total estimated cost in EUR. Candidates over budget are excluded. 0 = no limit.",
				},
			},
			Required: []string{"destination", "window_start", "window_end"},
		},
		OutputSchema: findTripWindowOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Find Optimal Travel Window",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

func findTripWindowOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"candidates": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"start":              schemaString(),
					"end":                schemaString(),
					"nights":             schemaInt(),
					"estimated_cost":     schemaNum(),
					"currency":           schemaString(),
					"overlaps_preferred": schemaBool(),
					"reasoning":          schemaString(),
				},
				"required": []string{"start", "end", "nights"},
			}),
			"count":       schemaInt(),
			"origin":      schemaString(),
			"destination": schemaString(),
			"error":       schemaString(),
		},
		"required": []string{"count"},
	}
}

// handleFindTripWindow handles the find_trip_window tool.
func handleFindTripWindow(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	destination := argString(args, "destination")
	origin := argString(args, "origin")
	windowStart := argString(args, "window_start")
	windowEnd := argString(args, "window_end")

	if destination == "" || windowStart == "" || windowEnd == "" {
		return nil, nil, fmt.Errorf("destination, window_start, and window_end are required")
	}

	// Resolve origin from preferences if not supplied.
	if origin == "" {
		if prefs, err := preferences.Load(); err == nil && prefs != nil && len(prefs.HomeAirports) > 0 {
			origin = prefs.HomeAirports[0]
		}
	}

	sendProgress(progress, 0, 100, "Parsing inputs...")

	busy := parseIntervalArg(args, "busy_intervals")
	preferred := parseIntervalArg(args, "preferred_intervals")

	in := tripwindow.Input{
		Origin:             origin,
		Destination:        destination,
		WindowStart:        windowStart,
		WindowEnd:          windowEnd,
		BusyIntervals:      busy,
		PreferredIntervals: preferred,
		MinNights:          argInt(args, "min_nights", 0),
		MaxNights:          argInt(args, "max_nights", 0),
		MaxCandidates:      argInt(args, "max_candidates", 0),
		BudgetEUR:          argFloat(args, "budget_eur", 0),
	}

	if err := tripwindow.ValidateInput(in); err != nil {
		return nil, nil, err
	}

	sendProgress(progress, 10, 100, fmt.Sprintf("Searching trip windows to %s...", destination))

	candidates, err := tripwindow.Find(ctx, in)
	if err != nil {
		return nil, nil, fmt.Errorf("find trip windows: %w", err)
	}

	sendProgress(progress, 100, 100, "Done.")

	type response struct {
		Candidates  []tripwindow.Candidate `json:"candidates"`
		Count       int                    `json:"count"`
		Origin      string                 `json:"origin,omitempty"`
		Destination string                 `json:"destination"`
	}
	resp := response{
		Candidates:  candidates,
		Count:       len(candidates),
		Origin:      origin,
		Destination: destination,
	}

	summary := buildTripWindowSummary(candidates, origin, destination, len(busy))

	content, err := buildAnnotatedContentBlocks(summary, resp)
	if err != nil {
		return nil, nil, err
	}
	return content, resp, nil
}

// buildTripWindowSummary formats a human-readable summary of the candidates.
func buildTripWindowSummary(candidates []tripwindow.Candidate, origin, destination string, busyCount int) string {
	if len(candidates) == 0 {
		msg := fmt.Sprintf("No open travel windows found to %s", destination)
		if busyCount > 0 {
			msg += fmt.Sprintf(" after excluding %d busy interval(s)", busyCount)
		}
		msg += "."
		return msg
	}

	fromStr := ""
	if origin != "" {
		fromStr = fmt.Sprintf(" from %s", origin)
	}
	summary := fmt.Sprintf("Found %d travel window(s)%s to %s", len(candidates), fromStr, destination)
	if busyCount > 0 {
		summary += fmt.Sprintf(" (excluding %d busy period(s))", busyCount)
	}
	summary += ".\n\n"

	for i, c := range candidates {
		line := fmt.Sprintf("%d. %s – %s (%d nights)", i+1, c.Start, c.End, c.Nights)
		if c.EstimatedCost > 0 {
			line += fmt.Sprintf(", est. %s %.0f RT", c.Currency, c.EstimatedCost)
		}
		if c.OverlapsPreferred {
			line += " [preferred]"
		}
		summary += line + "\n"
	}
	return summary
}

// parseIntervalArg extracts a []tripwindow.Interval from an args map key.
// The value must be a JSON array of objects with "start", "end", and optional "reason".
func parseIntervalArg(args map[string]any, key string) []tripwindow.Interval {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []tripwindow.Interval
	for _, elem := range arr {
		m, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		start, _ := m["start"].(string)
		end, _ := m["end"].(string)
		reason, _ := m["reason"].(string)
		if start == "" || end == "" {
			continue
		}
		out = append(out, tripwindow.Interval{Start: start, End: end, Reason: reason})
	}
	return out
}
