package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/multimodal"
)

func planMultimodalTool() ToolDef {
	return ToolDef{
		Name:        "plan_multimodal",
		Title:       "Multimodal Itinerary Composer",
		Description: "Compose end-to-end multimodal itineraries (e.g. ferry then fly). Rome2Rio discovers candidate mode-chains; trvl prices each leg with its existing flight and ground providers, sums them into a true total, ranks by that total, and annotates travel-hack savings. Legs that cannot be priced fall back to Rome2Rio's indicative range, clearly labelled as estimates — never fabricated fares.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"from":                    {Type: "string", Description: "Origin place name (e.g. Helsinki, London)"},
				"to":                      {Type: "string", Description: "Destination place name"},
				"date":                    {Type: "string", Description: "Departure date (YYYY-MM-DD)"},
				"allow_browser_fallbacks": {Type: "boolean", Description: "Allow browser/cookie-assisted Rome2Rio discovery + ground leg pricing (default: false; offline returns a typed status)"},
			},
			Required: []string{"from", "to", "date"},
		},
		OutputSchema: multimodalPlanOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Multimodal Itinerary Composer",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

func multimodalPlanOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"from":        schemaString(),
			"to":          schemaString(),
			"date":        schemaString(),
			"discovered":  schemaInt(),
			"priced":      schemaInt(),
			"notes":       schemaStringArray(),
			"error":       schemaString(),
			"itineraries": multimodalItinerariesOutputSchema(),
		},
		"required": []string{"from", "to", "date", "discovered", "priced"},
	}
}

func multimodalItinerariesOutputSchema() interface{} {
	return schemaArray(map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"from":             schemaString(),
			"to":               schemaString(),
			"date":             schemaString(),
			"total_price":      schemaNum(),
			"currency":         schemaString(),
			"duration_minutes": schemaInt(),
			"transfers":        schemaInt(),
			"mode_chain":       schemaString(),
			"estimated":        schemaBool(),
			"source":           schemaString(),
			"booking_url":      schemaString(),
			"warnings":         schemaStringArray(),
			"risks":            schemaStringArray(),
			"legs": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"mode":             schemaString(),
					"from":             schemaString(),
					"to":               schemaString(),
					"price":            schemaNum(),
					"currency":         schemaString(),
					"duration_minutes": schemaInt(),
					"estimated":        schemaBool(),
					"provider":         schemaString(),
					"booking_url":      schemaString(),
					"detail":           schemaString(),
				},
			}),
		},
	})
}

func handlePlanMultimodal(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	from := argString(args, "from")
	to := argString(args, "to")
	date := argString(args, "date")
	if from == "" || to == "" || date == "" {
		return nil, nil, fmt.Errorf("from, to, and date are required")
	}
	allowBrowser := argBool(args, "allow_browser_fallbacks", false)

	planner := multimodal.NewPlanner(allowBrowser)
	plan, err := planner.Plan(ctx, from, to, date)
	if err != nil {
		return nil, nil, toolExecutionError("Multimodal plan", err)
	}
	if plan.Error != "" {
		return nil, nil, toolResultError("Multimodal plan", plan.Error)
	}
	if len(plan.Itineraries) == 0 {
		msg := fmt.Sprintf("No multimodal itineraries from %s to %s on %s", from, to, date)
		if len(plan.Notes) > 0 {
			msg += " — " + strings.Join(plan.Notes, "; ")
		}
		return []ContentBlock{{Type: "text", Text: msg}}, plan, nil
	}

	content, err := buildAnnotatedContentBlocks(multimodalPlanSummary(plan), plan)
	if err != nil {
		return nil, nil, err
	}
	return content, plan, nil
}

func multimodalPlanSummary(plan *multimodal.Plan) string {
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "%d multimodal itineraries from %s to %s on %s (from %d discovered chains):\n\n",
		len(plan.Itineraries), plan.From, plan.To, plan.Date, plan.Discovered)

	limit := len(plan.Itineraries)
	if limit > 10 {
		limit = 10
	}
	for i, it := range plan.Itineraries[:limit] {
		tag := ""
		if it.Estimated {
			tag = " [includes estimate]"
		}
		_, _ = fmt.Fprintf(&sb, "%d. **%s %.2f** %s | %dh%02dm%s\n",
			i+1, it.Currency, it.TotalPrice, it.ModeChain, it.DurationMin/60, it.DurationMin%60, tag)
		for _, leg := range it.Legs {
			label := leg.Provider
			if leg.Estimated {
				label = "estimate"
			}
			_, _ = fmt.Fprintf(&sb, "   - %s %s→%s: %s %.2f via %s\n",
				leg.Mode, leg.From, leg.To, leg.Currency, leg.Price, label)
		}
		if it.HackSaving != nil {
			_, _ = fmt.Fprintf(&sb, "   💡 %s: save %s %.2f (%s)\n",
				it.HackSaving.Title, it.Currency, it.HackSaving.Savings, it.HackSaving.Type)
		}
	}
	if len(plan.Notes) > 0 {
		_, _ = fmt.Fprintf(&sb, "\n%s", strings.Join(plan.Notes, "\n"))
	}
	return sb.String()
}
