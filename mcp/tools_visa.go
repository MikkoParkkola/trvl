package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/visa"
)

// checkVisaTool returns the MCP tool definition for visa requirement lookups.
func checkVisaTool() ToolDef {
	return ToolDef{
		Name:        "check_visa",
		Title:       "Visa Requirements",
		Description: "Check visa and entry requirements for a passport→destination country pair. Returns visa status (visa-free, visa-required, visa-on-arrival, e-visa, freedom-of-movement), maximum stay duration, and notes. Uses ISO 3166-1 alpha-2 country codes (e.g. FI=Finland, JP=Japan, US=United States).",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"passport":    {Type: "string", Description: "Passport country code (ISO 3166-1 alpha-2, e.g. FI, US, GB, JP)"},
				"destination": {Type: "string", Description: "Destination country code (ISO 3166-1 alpha-2, e.g. JP, TH, US, DE)"},
			},
			Required: []string{"passport", "destination"},
		},
		OutputSchema: visaOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Visa Requirements",
			ReadOnlyHint:   true,
			OpenWorldHint:  false,
			IdempotentHint: true,
		},
	}
}

func visaOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success": schemaBool(),
			"requirement": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"passport":    schemaString(),
					"destination": schemaString(),
					"status":      map[string]interface{}{"type": "string", "enum": []string{"visa-free", "visa-required", "visa-on-arrival", "e-visa", "freedom-of-movement"}},
					"max_stay":    schemaString(),
					"notes":       schemaString(),
				},
			},
			"error": schemaString(),
		},
		"required": []string{"success", "requirement"},
	}
}

func handleCheckVisa(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	passport := argString(args, "passport")
	destination := argString(args, "destination")

	sendProgress(progress, 30, 100, fmt.Sprintf("Looking up %s → %s visa requirements...", strings.ToUpper(passport), strings.ToUpper(destination)))

	result := visa.Lookup(passport, destination)

	sendProgress(progress, 100, 100, "Done")

	summary := buildVisaSummary(result)
	content := []ContentBlock{
		{Type: "text", Text: summary, Annotations: &ContentAnnotation{Audience: []string{"user"}, Priority: 1.0}},
		{Type: "text", Text: "Structured visa data attached.", Annotations: &ContentAnnotation{Audience: []string{"assistant"}, Priority: 0.5}},
	}
	return content, result, nil
}

func buildVisaSummary(result visa.Result) string {
	if !result.Success {
		return fmt.Sprintf("Visa lookup failed: %s", result.Error)
	}

	req := result.Requirement
	from := visa.CountryName(req.Passport)
	to := visa.CountryName(req.Destination)
	emoji := visa.StatusEmoji(req.Status)

	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Visa requirements: %s (%s) → %s (%s)\n\n", from, req.Passport, to, req.Destination)
	_, _ = fmt.Fprintf(&sb, "  %s Status: %s\n", emoji, req.Status)

	if req.MaxStay != "" {
		_, _ = fmt.Fprintf(&sb, "  Max stay: %s\n", req.MaxStay)
	}

	if req.Notes != "" {
		_, _ = fmt.Fprintf(&sb, "\n  %s\n", req.Notes)
	}

	sb.WriteString("\n  Note: Data is advisory only. Always verify with the destination embassy before travel.")
	return sb.String()
}
