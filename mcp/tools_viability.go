package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/trip"
)

func assessTripTool() ToolDef {
	return ToolDef{
		Name:        "assess_trip",
		Title:       "Assess Trip",
		Description: "Pre-check trip viability before booking. Checks flights, hotels, visa, and weather in parallel and returns a GO/WAIT/NO_GO verdict with cost breakdown.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origin":      {Type: "string", Description: "Origin airport IATA code"},
				"destination": {Type: "string", Description: "Destination airport IATA code or city"},
				"depart_date": {Type: "string", Description: "Departure date (YYYY-MM-DD)"},
				"return_date": {Type: "string", Description: "Return date (YYYY-MM-DD)"},
				"guests":      {Type: "integer", Description: "Number of guests (default: 1)"},
				"passport":    {Type: "string", Description: "Passport country ISO code for visa check (e.g. FI)"},
				"currency":    {Type: "string", Description: "Target currency (e.g. EUR)"},
			},
			Required: []string{"origin", "destination", "depart_date", "return_date"},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"success":    schemaBool(),
				"verdict":    schemaStringDesc("GO, WAIT, or NO_GO"),
				"reason":     schemaString(),
				"total_cost": schemaNum(),
				"currency":   schemaString(),
				"nights":     schemaInt(),
				"checks": schemaArray(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"dimension": schemaString(),
						"status":    schemaString(),
						"summary":   schemaString(),
						"cost":      schemaNum(),
						"currency":  schemaString(),
					},
				}),
			},
			"required": []string{"success", "verdict"},
		},
		Annotations: &ToolAnnotations{
			Title:          "Assess Trip",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

func handleAssessTrip(ctx context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	result, err := trip.AssessTrip(ctx, trip.ViabilityInput{
		Origin:      strings.ToUpper(argString(args, "origin")),
		Destination: argString(args, "destination"),
		DepartDate:  argString(args, "depart_date"),
		ReturnDate:  argString(args, "return_date"),
		Guests:      argInt(args, "guests", 1),
		Passport:    strings.ToUpper(argString(args, "passport")),
		Currency:    argString(args, "currency"),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("assess_trip: %w", err)
	}

	emoji := "\u2705"
	switch result.Verdict {
	case "WAIT":
		emoji = "\u26a0\ufe0f"
	case "NO_GO":
		emoji = "\u274c"
	}
	summary := fmt.Sprintf("%s %s \u2014 %s", emoji, result.Verdict, result.Reason)
	if result.TotalCost > 0 {
		summary += fmt.Sprintf(" | Total: %.0f %s", result.TotalCost, result.Currency)
	}

	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}
	return content, result, nil
}
