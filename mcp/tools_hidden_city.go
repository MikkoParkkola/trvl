package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/hacks"
)

func searchHiddenCityTool() ToolDef {
	return ToolDef{
		Name:        "search_hidden_city",
		Title:       "Hidden-City Matrix Search",
		Description: "Find cheaper flights by boarding Origin\u2192Hub\u2192HubBeyond but disembarking at Hub, skipping the final leg. Only safe with carry-on luggage. Supply priced offers from the origin\u00d7hub-extension grid; returns top-K ranked candidates with booking URLs. Gate on the user's risk_posture.hidden_city.acceptable preference (allow_hidden_city must be true to get results).",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"offers":            {Type: "array", Items: &Property{Type: "object"}, Description: "Priced candidates from the Origin\u00d7hub-beyond grid. Each offer is a JSON object with fields: origin (IATA), hub (IATA), hub_beyond (IATA), carrier (IATA airline code), price (number), currency (string), carry_on_only (bool), separate_tickets (bool), layover_minutes (int)."},
				"allow_hidden_city": {Type: "boolean", Description: "Must reflect user's risk_posture.hidden_city.acceptable flag. Defaults to false \u2014 return empty when false."},
				"direct_baseline":   {Type: "number", Description: "Cheapest known direct Origin\u2192Hub price for savings calculation. Omit when unknown."},
				"depart_date":       {Type: "string", Description: "Departure date (YYYY-MM-DD) for booking URL generation."},
				"max_layover_risk":  {Type: "integer", Description: "0-100 risk ceiling; candidates above this score are dropped (default 60)."},
				"top_k":             {Type: "integer", Description: "Maximum candidates to return (default 3)."},
			},
			Required: []string{"offers"},
		},
		OutputSchema: hiddenCityOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Hidden-City Matrix Search",
			ReadOnlyHint:   true,
			OpenWorldHint:  false,
			IdempotentHint: true,
		},
	}
}

func hiddenCityOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"count":   schemaInt(),
			"allowed": schemaBool(),
			"candidates": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"origin":       schemaString(),
					"hub":          schemaString(),
					"hub_beyond":   schemaString(),
					"carrier":      schemaString(),
					"price":        schemaNum(),
					"currency":     schemaString(),
					"savings_eur":  schemaNum(),
					"savings_pct":  schemaNum(),
					"layover_risk": schemaInt(),
					"reason":       schemaString(),
					"booking_url":  schemaString(),
				},
			}),
		},
		"required": []string{"count", "allowed", "candidates"},
	}
}

func parseHiddenCityOffers(args map[string]any) []hacks.HiddenCityOffer {
	offersRaw, ok := args["offers"].([]interface{})
	if !ok {
		return nil
	}
	out := make([]hacks.HiddenCityOffer, 0, len(offersRaw))
	for _, raw := range offersRaw {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		getString := func(key string) string {
			v, _ := m[key].(string)
			return v
		}
		getFloat := func(key string) float64 {
			switch v := m[key].(type) {
			case float64:
				return v
			case int:
				return float64(v)
			default:
				return 0
			}
		}
		getBool := func(key string) bool {
			v, _ := m[key].(bool)
			return v
		}
		getInt := func(key string) int {
			switch v := m[key].(type) {
			case float64:
				return int(v)
			case int:
				return v
			default:
				return 0
			}
		}
		out = append(out, hacks.HiddenCityOffer{
			Origin:          getString("origin"),
			Hub:             getString("hub"),
			HubBeyond:       getString("hub_beyond"),
			Carrier:         getString("carrier"),
			Price:           getFloat("price"),
			Currency:        getString("currency"),
			CarryOnOnly:     getBool("carry_on_only"),
			SeparateTickets: getBool("separate_tickets"),
			LayoverMinutes:  getInt("layover_minutes"),
		})
	}
	return out
}

func handleSearchHiddenCity(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	allowHiddenCity := argBool(args, "allow_hidden_city", false)
	directBaseline := argFloat(args, "direct_baseline", 0)
	departDate := argString(args, "depart_date")
	maxLayoverRisk := argInt(args, "max_layover_risk", 0)
	topK := argInt(args, "top_k", 0)

	offers := parseHiddenCityOffers(args)

	opt := hacks.MatrixOptions{
		AllowHiddenCity: allowHiddenCity,
		DirectBaseline:  directBaseline,
		DepartDate:      departDate,
		MaxLayoverRisk:  maxLayoverRisk,
		TopK:            topK,
	}
	candidates := hacks.ExpandMatrix(offers, opt)

	type response struct {
		Count      int                         `json:"count"`
		Allowed    bool                        `json:"allowed"`
		Candidates []hacks.HiddenCityCandidate `json:"candidates"`
	}
	resp := response{
		Count:      len(candidates),
		Allowed:    allowHiddenCity,
		Candidates: candidates,
	}
	if resp.Candidates == nil {
		resp.Candidates = []hacks.HiddenCityCandidate{}
	}

	summary := buildHiddenCitySummary(candidates, allowHiddenCity)
	content, err := buildAnnotatedContentBlocks(summary, resp)
	if err != nil {
		return nil, nil, err
	}
	return content, resp, nil
}

func buildHiddenCitySummary(candidates []hacks.HiddenCityCandidate, allowed bool) string {
	if !allowed {
		return "Hidden-city search is disabled. Set risk_posture.hidden_city.acceptable=true in your preferences to enable it."
	}
	if len(candidates) == 0 {
		return "No hidden-city candidates found within the risk threshold."
	}
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Found %d hidden-city candidate(s):\n\n", len(candidates))
	for i, c := range candidates {
		_, _ = fmt.Fprintf(&sb, "%d. %s\u2192%s (skip to %s) \u2014 %s %.2f", i+1, c.Origin, c.HubBeyond, c.Hub, c.Currency, c.Price)
		if c.SavingsPct > 0 {
			_, _ = fmt.Fprintf(&sb, " (saves %.0f%%)", c.SavingsPct)
		}
		_, _ = fmt.Fprintf(&sb, ", risk %d/100\n   %s\n   Booking: %s\n\n", c.LayoverRisk, c.Reason, c.BookingURL)
	}
	return strings.TrimRight(sb.String(), "\n")
}
