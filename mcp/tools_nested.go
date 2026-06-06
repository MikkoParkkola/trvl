package mcp

// optimize_nested_rt — MCP surface for the nested/overlapping round-trip
// combinator (internal/hacks/nested_rt.go). Prices both visit windows live
// (parallel fan-out across the existing flight providers), feeds the priced
// VisitWindows into the pure ranker, and returns the cheapest pairings vs the
// naive two-round-trip baseline. Serves the two-base commute pattern (e.g.
// HEL+AMS) where overlapping round-trips beat booking two separate returns.
//
// Tracking: MIK-3076.

import (
	"context"
	"fmt"

	"github.com/MikkoParkkola/trvl/internal/hacks"
)

func handleOptimizeNestedRT(ctx context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	return optimizeNestedRT(ctx, args, hacks.DefaultLegPricer)
}

func optimizeNestedRT(ctx context.Context, args map[string]any, price hacks.LegPricer) ([]ContentBlock, interface{}, error) {
	origin, dest, err := validateOriginDest(args)
	if err != nil {
		return nil, nil, err
	}
	w1d, err := validateDate(args, "window1_depart")
	if err != nil {
		return nil, nil, err
	}
	w1r, err := validateDate(args, "window1_return")
	if err != nil {
		return nil, nil, err
	}
	w2d, err := validateDate(args, "window2_depart")
	if err != nil {
		return nil, nil, err
	}
	w2r, err := validateDate(args, "window2_return")
	if err != nil {
		return nil, nil, err
	}

	ranked := hacks.PlanNestedRT(ctx, origin, dest, w1d, w1r, w2d, w2r, price, 3)
	if len(ranked) == 0 {
		return nil, nil, fmt.Errorf("could not price both visit windows for %s<->%s", origin, dest)
	}
	out := nestedRTResult{Origin: origin, Dest: dest, Pairings: ranked}
	for _, p := range ranked {
		if p.SavingsEUR > out.BestSave {
			out.BestSave = p.SavingsEUR
		}
	}

	summary := fmt.Sprintf("Best plan for two %s<->%s visits: %s at %.0f", origin, dest, ranked[0].Kind, ranked[0].Cost)
	if out.BestSave > 0 {
		summary += fmt.Sprintf(" (saves %.0f vs two separate round-trips)", out.BestSave)
	} else {
		summary += " (no saving vs two separate round-trips)"
	}
	blocks, err := buildAnnotatedContentBlocks(summary, out)
	if err != nil {
		return nil, nil, err
	}
	return blocks, out, nil
}

// nestedRTResult is the structured output of optimize_nested_rt.
type nestedRTResult struct {
	Origin   string                `json:"origin"`
	Dest     string                `json:"destination"`
	Pairings []hacks.PairingResult `json:"pairings"`
	BestSave float64               `json:"best_savings_eur"`
}

func nestedRTTool() ToolDef {
	return ToolDef{
		Name:        "optimize_nested_rt",
		Title:       "Nested Round-Trip Optimizer",
		Description: "For two visits between the same two cities, find whether overlapping/nested round-trips beat booking two separate returns. Prices both windows live and ranks the cheapest pairing.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origin":         {Type: "string", Description: "Home IATA code (side A), e.g. HEL"},
				"destination":    {Type: "string", Description: "Visited IATA code (side B), e.g. AMS"},
				"window1_depart": {Type: "string", Description: "First visit outbound date YYYY-MM-DD"},
				"window1_return": {Type: "string", Description: "First visit return date YYYY-MM-DD"},
				"window2_depart": {Type: "string", Description: "Second visit outbound date YYYY-MM-DD"},
				"window2_return": {Type: "string", Description: "Second visit return date YYYY-MM-DD"},
			},
			Required: []string{"origin", "destination", "window1_depart", "window1_return", "window2_depart", "window2_return"},
		},
		Annotations: &ToolAnnotations{Title: "Nested Round-Trip Optimizer", ReadOnlyHint: true, OpenWorldHint: true},
	}
}
