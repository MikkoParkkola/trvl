package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/nlsearch"
)

// nlSupertoolName is the registered tool / intent name for the natural-language
// travel planner ("supertool", innovation #7).
const nlSupertoolName = "plan_natural"

// nlSupertoolTool returns the MCP tool definition for the natural-language
// travel planner. Unlike search_natural (which parses then immediately
// dispatches a single search), this tool returns a normalized, inspectable Plan
// — travel mode, endpoints, dates, constraints, and the exact search(es) to run
// with which parameters — so a caller can review the routing decision, confirm,
// or dispatch the named searches itself. Parsing is deterministic and
// rule-based (no LLM dependency at runtime).
func nlSupertoolTool() ToolDef {
	return ToolDef{
		Name:  nlSupertoolName,
		Title: "Natural Language Travel Planner",
		Description: "Turn one free-form travel request into a structured routing plan. " +
			"Parses a plain-language query (e.g. \"cheapest way to Tromso next month avoiding " +
			"red-eyes\", \"HEL to NRT and back next week\", \"train from Amsterdam to Berlin\", " +
			"\"hotel in Prague under EUR 120\") into a normalized Plan: travel mode " +
			"(flight, ground, hotel, multimodal, hacks), origin, destination, departure and return " +
			"dates, constraints (no-redeye, cheapest, fastest, nonstop, budget<=X), and the exact " +
			"search tool(s) to dispatch with their parameters. Deterministic, no API keys, works on " +
			"every MCP client. Use this to decide WHICH search to run; then call the named tool " +
			"(search_flights, search_route, search_hotels, detect_travel_hacks) — or call search_natural " +
			"to parse-and-dispatch in one step.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"query": {
					Type:        "string",
					Description: "Natural language travel request",
				},
			},
			Required: []string{"query"},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query":       schemaString(),
				"mode":        schemaStringDesc("flight | ground | hotel | multimodal | hacks"),
				"origin":      schemaString(),
				"destination": schemaString(),
				"date":        schemaString(),
				"return_date": schemaString(),
				"round_trip":  map[string]interface{}{"type": "boolean"},
				"max_budget":  map[string]interface{}{"type": "number"},
				"constraints": schemaStringArrayDesc("e.g. no-redeye, cheapest, fastest, nonstop, budget<=500"),
				"notes":       schemaStringArray(),
				"searches": schemaArrayDesc("Search tool(s) to dispatch with their parameters", map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"tool":   schemaString(),
						"params": schemaObject(),
					},
				}),
			},
			"required": []string{"query", "mode", "constraints", "searches"},
		},
		Annotations: &ToolAnnotations{
			Title:          "Natural Language Travel Planner",
			ReadOnlyHint:   true,
			OpenWorldHint:  false,
			IdempotentHint: true,
		},
	}
}

// handleNLSupertool handles the plan_natural tool: parse a free-form query into
// a structured Plan and return it (it does NOT dispatch the searches itself).
func handleNLSupertool(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	query := strings.TrimSpace(argString(args, "query"))
	if query == "" {
		return nil, nil, fmt.Errorf("query is required")
	}

	sendProgress(progress, 0, 100, "Parsing travel query...")
	today := time.Now().Format("2006-01-02")
	plan := nlsearch.BuildPlan(query, today)
	sendProgress(progress, 100, 100, "Plan ready")

	return []ContentBlock{{Type: "text", Text: buildPlanSummary(plan)}}, plan, nil
}

// buildPlanSummary renders a compact human-readable summary of the Plan.
func buildPlanSummary(p nlsearch.Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Travel plan for %q\n", p.Query)
	fmt.Fprintf(&b, "Mode: %s\n", p.Mode)
	if p.Origin != "" {
		fmt.Fprintf(&b, "From: %s\n", p.Origin)
	} else {
		b.WriteString("From: (unspecified — supply your home origin)\n")
	}
	if p.Destination != "" {
		fmt.Fprintf(&b, "To: %s\n", p.Destination)
	} else {
		b.WriteString("To: (unspecified)\n")
	}
	if p.Date != "" {
		fmt.Fprintf(&b, "Depart: %s\n", p.Date)
	}
	if p.ReturnDate != "" {
		fmt.Fprintf(&b, "Return: %s (round trip)\n", p.ReturnDate)
	}
	if p.MaxBudget > 0 {
		fmt.Fprintf(&b, "Budget ceiling: %g\n", p.MaxBudget)
	}
	if len(p.Constraints) > 0 {
		fmt.Fprintf(&b, "Constraints: %s\n", strings.Join(p.Constraints, ", "))
	}
	if len(p.Searches) > 0 {
		b.WriteString("Dispatch:\n")
		for _, s := range p.Searches {
			fmt.Fprintf(&b, "  - %s\n", s.Tool)
		}
	}
	for _, n := range p.Notes {
		fmt.Fprintf(&b, "Note: %s\n", n)
	}
	return strings.TrimRight(b.String(), "\n")
}
