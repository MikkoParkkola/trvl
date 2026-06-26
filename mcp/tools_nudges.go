package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/travelgraph"
	"github.com/MikkoParkkola/trvl/internal/trips"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// travelNudgesTool returns the MCP tool definition for travel_nudges. It mirrors
// the read-only, no-argument shape of get_preferences: the only optional input
// is a format hint, and the output is the list of grounded nudges.
//
// Like plan_journey and plan_natural, travel_nudges is a smart capability that
// is registered as a handler and reachable via direct tools/call and the travel
// smart router, but kept OUT of the advertised legacyTools surface so the
// compact tool list stays a single router (see registerTools in tools.go).
func travelNudgesTool() ToolDef {
	return ToolDef{
		Name:        "travel_nudges",
		Title:       "Travel Nudges",
		Description: "Returns grounded proactive nudges from the user's personal travel graph: watches that have crossed their target price, and routes confirmed at a confident historic low. Every nudge cites its source record. When nothing has triggered, an empty list is returned — this tool never speculates.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"format": {
					Type:        "string",
					Description: "Optional output hint. \"json\" is the default structured form; ignored otherwise.",
				},
			},
			Required: []string{},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nudges": schemaArray(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"Kind":    schemaString(),
						"Message": schemaString(),
						"Sources": schemaArray(map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"Kind": schemaString(),
								"ID":   schemaString(),
							},
						}),
					},
				}),
				"count": schemaInt(),
			},
		},
		Annotations: &ToolAnnotations{
			Title:          "Travel Nudges",
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}
}

// handleTravelNudges builds the personal travel graph from the same local stores
// as the `trvl nudges` CLI command (watch store + AllHistory, preferences, trips)
// and returns its grounded nudges. Missing or unreadable preference/trip stores
// degrade to defaults/empty rather than failing, matching the CLI's tolerance:
// quiet stores simply yield zero nudges, not a hard error.
func handleTravelNudges(_ context.Context, _ map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	store, err := watch.DefaultStore()
	if err != nil {
		return nil, nil, fmt.Errorf("open watch store: %w", err)
	}
	if err := store.Load(); err != nil {
		return nil, nil, fmt.Errorf("load watches: %w", err)
	}
	ws := store.List()

	// AllHistory returns every price point — both watch-keyed and route-keyed —
	// so historicLowNudge evaluates the full corpus (matches cmd/trvl/nudges.go).
	history := store.AllHistory()

	prefs, err := preferences.Load()
	if err != nil {
		prefs = preferences.Default()
	}

	var ts []trips.Trip
	if tstore, terr := trips.DefaultStore(); terr == nil && tstore.Load() == nil {
		ts = tstore.List()
	}

	g := travelgraph.Build(ws, history, prefs, ts)
	nudges := travelgraph.Nudges(g, time.Now())

	type nudgeResponse struct {
		Nudges []travelgraph.Nudge `json:"nudges"`
		Count  int                 `json:"count"`
	}
	resp := nudgeResponse{Nudges: nudges, Count: len(nudges)}

	var summary string
	if len(nudges) == 0 {
		summary = "No nudges — watches quiet and no historic lows detected."
	} else {
		summary = fmt.Sprintf("%d grounded nudge(s):\n", len(nudges))
		for _, n := range nudges {
			summary += fmt.Sprintf("  [%s] %s\n    sources: %s\n", n.Kind, n.Message, formatNudgeSources(n.Sources))
		}
	}

	content, err := buildAnnotatedContentBlocks(summary, resp)
	if err != nil {
		return nil, nil, err
	}
	return content, resp, nil
}

// formatNudgeSources renders nudge source references as "kind:id" tokens joined
// by commas for the human-readable summary block.
func formatNudgeSources(srcs []travelgraph.SourceRef) string {
	parts := make([]string, len(srcs))
	for i, s := range srcs {
		parts[i] = fmt.Sprintf("%s:%s", s.Kind, s.ID)
	}
	return strings.Join(parts, ", ")
}
