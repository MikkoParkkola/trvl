package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/awards"
)

// searchAwardsTool returns the MCP tool definition for cross-program award scanning.
func searchAwardsTool() ToolDef {
	return ToolDef{
		Name:        "search_awards",
		Title:       "Search Cross-Program Award Availability",
		Description: "Find cheapest redemption path for award seats across loyalty programs (FB, Avios, Aeroplan, Virgin, Asia Miles) including Amex MR, Chase UR, and Bilt transfer partners. Provide pre-fetched award seat fixtures (from seats.aero or known availability); returns ranked sweet-spot alternatives with miles cost, cash equivalent, cents-per-point, and transfer route.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"seats": {
					Type:        "array",
					Description: "Pre-fetched award seat fixtures. Each seat is a JSON object with: program (IATA airline code e.g. VS, AY, AC, BA, FB), origin (IATA), destination (IATA), date (YYYY-MM-DD), cabin (economy/premium_economy/business/first), miles_cost (int), cash_fees (number), cash_equivalent (number), bookable_segments (int).",
				},
				"balances": {
					Type:        "array",
					Description: "User's loyalty point balances. Each entry: program (e.g. VS, AY, MR, UR, Bilt), balance (int).",
				},
				"transfer_ratios": {
					Type:        "array",
					Description: "Custom transfer ratio overrides. Each entry: source, target, numerator (float), denominator (float). Omit to use defaults.",
				},
				"min_cpp": {
					Type:        "number",
					Description: "Minimum cents-per-point to include in results (default 0.5).",
				},
				"cabin": {
					Type:        "string",
					Description: "Filter to specific cabin class.",
				},
				"origin": {
					Type:        "string",
					Description: "Filter to specific origin IATA.",
				},
				"destination": {
					Type:        "string",
					Description: "Filter to specific destination IATA.",
				},
			},
			Required: []string{"seats", "balances"},
		},
		OutputSchema: awardsOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Search Cross-Program Award Availability",
			ReadOnlyHint:   true,
			OpenWorldHint:  false,
			IdempotentHint: true,
		},
	}
}

func awardsOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"count": schemaInt(),
			"sweet_spots": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"program":            schemaString(),
					"origin":             schemaString(),
					"destination":        schemaString(),
					"date":               schemaString(),
					"cabin":              schemaString(),
					"miles_cost":         schemaInt(),
					"cash_fees":          schemaNum(),
					"cash_equivalent":    schemaNum(),
					"booking_program":    schemaString(),
					"source_program":     schemaString(),
					"transfer_route":     schemaString(),
					"miles_spent_native": schemaInt(),
					"miles_spent_source": schemaInt(),
					"cents_per_point":    schemaNum(),
					"affordable":         schemaBool(),
					"reason":             schemaString(),
				},
			}),
		},
		"required": []string{"count", "sweet_spots"},
	}
}

func handleSearchAwards(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	// Parse seats.
	seatsRaw, _ := args["seats"].([]interface{})
	seats := make([]awards.AwardSeat, 0, len(seatsRaw))
	for _, raw := range seatsRaw {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		seat := awards.AwardSeat{
			Program:          stringField(m, "program"),
			Origin:           stringField(m, "origin"),
			Destination:      stringField(m, "destination"),
			Date:             stringField(m, "date"),
			Cabin:            stringField(m, "cabin"),
			MilesCost:        intField(m, "miles_cost"),
			CashFees:         floatField(m, "cash_fees"),
			CashEquivalent:   floatField(m, "cash_equivalent"),
			BookableSegments: intField(m, "bookable_segments"),
		}
		seats = append(seats, seat)
	}

	// Parse balances.
	balancesRaw, _ := args["balances"].([]interface{})
	balances := make([]awards.PointBalance, 0, len(balancesRaw))
	for _, raw := range balancesRaw {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		balances = append(balances, awards.PointBalance{
			Program: stringField(m, "program"),
			Balance: intField(m, "balance"),
		})
	}

	// Parse optional transfer_ratios.
	var ratios []awards.TransferRatio
	if ratiosRaw, ok := args["transfer_ratios"].([]interface{}); ok && len(ratiosRaw) > 0 {
		ratios = make([]awards.TransferRatio, 0, len(ratiosRaw))
		for _, raw := range ratiosRaw {
			m, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			ratios = append(ratios, awards.TransferRatio{
				Source:      stringField(m, "source"),
				Target:      stringField(m, "target"),
				Numerator:   floatField(m, "numerator"),
				Denominator: floatField(m, "denominator"),
			})
		}
	}

	// Parse filters.
	minCPP := argFloat(args, "min_cpp", 0.5)
	cabinFilter := strings.ToLower(argString(args, "cabin"))
	originFilter := strings.ToUpper(argString(args, "origin"))
	destFilter := strings.ToUpper(argString(args, "destination"))

	// Find sweet spots.
	spots := awards.FindSweetSpots(seats, balances, ratios)

	// Apply filters.
	filtered := spots[:0]
	for _, s := range spots {
		if s.CentsPerPoint < minCPP {
			continue
		}
		if cabinFilter != "" && !strings.EqualFold(s.Seat.Cabin, cabinFilter) {
			continue
		}
		if originFilter != "" && !strings.EqualFold(s.Seat.Origin, originFilter) {
			continue
		}
		if destFilter != "" && !strings.EqualFold(s.Seat.Destination, destFilter) {
			continue
		}
		filtered = append(filtered, s)
	}

	// Build output rows.
	type sweetSpotRow struct {
		Program          string  `json:"program"`
		Origin           string  `json:"origin"`
		Destination      string  `json:"destination"`
		Date             string  `json:"date"`
		Cabin            string  `json:"cabin"`
		MilesCost        int     `json:"miles_cost"`
		CashFees         float64 `json:"cash_fees"`
		CashEquivalent   float64 `json:"cash_equivalent"`
		BookingProgram   string  `json:"booking_program"`
		SourceProgram    string  `json:"source_program"`
		TransferRoute    string  `json:"transfer_route"`
		MilesSpentNative int     `json:"miles_spent_native"`
		MilesSpentSource int     `json:"miles_spent_source"`
		CentsPerPoint    float64 `json:"cents_per_point"`
		Affordable       bool    `json:"affordable"`
		Reason           string  `json:"reason"`
	}

	rows := make([]sweetSpotRow, 0, len(filtered))
	for _, s := range filtered {
		rows = append(rows, sweetSpotRow{
			Program:          s.Seat.Program,
			Origin:           s.Seat.Origin,
			Destination:      s.Seat.Destination,
			Date:             s.Seat.Date,
			Cabin:            s.Seat.Cabin,
			MilesCost:        s.Seat.MilesCost,
			CashFees:         s.CashFees,
			CashEquivalent:   s.CashEquivalent,
			BookingProgram:   s.BookingProgram,
			SourceProgram:    s.SourceProgram,
			TransferRoute:    s.TransferRoute,
			MilesSpentNative: s.MilesSpentNative,
			MilesSpentSource: s.MilesSpentSource,
			CentsPerPoint:    s.CentsPerPoint,
			Affordable:       s.Affordable,
			Reason:           s.Reason,
		})
	}

	type response struct {
		Count      int            `json:"count"`
		SweetSpots []sweetSpotRow `json:"sweet_spots"`
	}

	resp := response{
		Count:      len(rows),
		SweetSpots: rows,
	}
	if resp.SweetSpots == nil {
		resp.SweetSpots = []sweetSpotRow{}
	}

	summary := buildAwardsSummary(filtered)
	content := []ContentBlock{
		{Type: "text", Text: summary, Annotations: &ContentAnnotation{Audience: []string{"user"}, Priority: 1.0}},
		{Type: "text", Text: "Structured award sweet-spot data attached.", Annotations: &ContentAnnotation{Audience: []string{"assistant"}, Priority: 0.5}},
	}
	return content, resp, nil
}

func buildAwardsSummary(spots []awards.SweetSpot) string {
	if len(spots) == 0 {
		return "No award sweet spots found matching the criteria."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d award sweet spot(s):\n\n", len(spots)))
	for i, s := range spots {
		affordable := "no"
		if s.Affordable {
			affordable = "yes"
		}
		sb.WriteString(fmt.Sprintf("%d. %s->%s %s %s - %s\n",
			i+1, s.Seat.Origin, s.Seat.Destination, s.Seat.Date, s.Seat.Cabin, s.TransferRoute))
		sb.WriteString(fmt.Sprintf("   %d pts (%d native) | %.2f cpp | fees %.2f | %s affordable\n",
			s.MilesSpentSource, s.MilesSpentNative, s.CentsPerPoint, s.CashFees, affordable))
		sb.WriteString(fmt.Sprintf("   %s\n\n", s.Reason))
	}
	return sb.String()
}

// --- field helpers for map[string]interface{} parsing ---

func stringField(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

func intField(m map[string]interface{}, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return 0
}

func floatField(m map[string]interface{}, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}
