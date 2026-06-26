package mcp

import (
	"context"
	"fmt"

	"github.com/MikkoParkkola/trvl/internal/hotelarb"
	"github.com/MikkoParkkola/trvl/internal/points"
)

// --- Output schema ---

// calculatePointsValueOutputSchema describes both result shapes: the
// single-program recommendation (default) and the multi-program arbitrage
// comparison returned when `offers` is supplied.
func calculatePointsValueOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			// Single-program recommendation fields.
			"program_slug":    schemaString(),
			"program_name":    schemaString(),
			"cash_price":      schemaNum(),
			"points_required": schemaInt(),
			"cpp":             schemaNumDesc("Effective cents per point for this redemption"),
			"floor_cpp":       schemaNumDesc("Conservative baseline CPP for this program"),
			"ceiling_cpp":     schemaNumDesc("Sweet-spot CPP for this program"),
			"verdict":         map[string]interface{}{"type": "string", "enum": []string{"use points", "pay cash", "borderline"}},
			"explanation":     schemaString(),
			// Multi-program arbitrage fields (present when `offers` is supplied).
			"currency":       schemaStringDesc("Currency label for the arbitrage comparison"),
			"recommendation": map[string]interface{}{"type": "string", "enum": []string{"use_points", "pay_cash"}},
			"reason":         schemaStringDesc("Plain-English summary of the arbitrage recommendation"),
			"best_offer":     pointsOfferValueSchema(),
			"offers":         schemaArrayDesc("Every evaluated points offer in the arbitrage comparison", pointsOfferValueSchema()),
		},
		"required": []string{},
	}
}

// pointsOfferValueSchema describes one evaluated hotel points offer in an
// arbitrage comparison, mirroring hotelarb.PointsOfferValue.
func pointsOfferValueSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"program_slug":     schemaString(),
			"program_name":     schemaString(),
			"points_required":  schemaInt(),
			"cash_fees":        schemaNum(),
			"cents_per_point":  schemaNumDesc("Effective cents per point for this offer"),
			"floor_cpp":        schemaNumDesc("Conservative baseline CPP for this program"),
			"ceiling_cpp":      schemaNumDesc("Sweet-spot CPP for this program"),
			"opportunity_cost": schemaNumDesc("Cash-equivalent cost of redeeming at floor value"),
			"savings_vs_cash":  schemaNumDesc("Cash saved versus paying outright (negative means cash wins)"),
			"verdict":          map[string]interface{}{"type": "string", "enum": []string{"use points", "pay cash"}},
			"reason":           schemaString(),
		},
	}
}

// --- Tool definition ---

func calculatePointsValueTool() ToolDef {
	return ToolDef{
		Name:  "calculate_points_value",
		Title: "Points vs Cash Calculator",
		Description: "Calculate whether redeeming loyalty points or paying cash is better for a specific redemption. " +
			"Returns the effective cents-per-point (cpp), program floor/ceiling valuations, a verdict, and a plain-English explanation. " +
			"Supports 20+ airline/hotel programs and 4 transferable currencies (Amex MR, Chase UR, Citi TYP, Capital One). " +
			"Pass `offers` (2+ programs) to compare hotel point redemptions side-by-side and get a ranked arbitrage recommendation. " +
			"No API keys required — uses published valuation data.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"cash_price": {
					Type:        "number",
					Description: "The cash price of the redemption in your local currency (e.g. 450.00 for a $450 flight)",
				},
				"points_required": {
					Type:        "integer",
					Description: "Number of points or miles required for the redemption (e.g. 60000). Used by the single-program path; ignored when `offers` is supplied.",
				},
				"program": {
					Type:        "string",
					Description: "Loyalty program slug. Examples: finnair-plus, ana-mileage-club, world-of-hyatt, amex-mr, chase-ur. Used by the single-program path; ignored when `offers` is supplied.",
				},
				"offers": {
					Type:        "array",
					Description: "Compare hotel points redemptions across programs. Each element is an object with `program` (slug, string), `points` (number), and optional `cash_fees` (number). Supply 2+ to rank programs side-by-side; when present, the single-program `program`/`points_required` fields are ignored.",
					Items: &Property{
						Type:        "object",
						Description: "program (slug), points (number), optional cash_fees (number)",
					},
				},
				"currency": {
					Type:        "string",
					Description: "Currency label for the arbitrage comparison output (default USD). Only used when `offers` is supplied.",
				},
			},
			Required: []string{"cash_price"},
		},
		OutputSchema: calculatePointsValueOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Points vs Cash Calculator",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  false,
		},
	}
}

// --- Handler ---

func handleCalculatePointsValue(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	cashPrice := argFloat(args, "cash_price", 0)

	if offers := parsePointsOfferArgs(args); len(offers) > 0 {
		return handlePointsArbitrage(cashPrice, argString(args, "currency"), offers)
	}

	pointsRequired := argInt(args, "points_required", 0)
	programSlug := argString(args, "program")

	if programSlug == "" {
		return nil, nil, fmt.Errorf("program is required")
	}
	if cashPrice <= 0 {
		return nil, nil, fmt.Errorf("cash_price must be greater than 0")
	}
	if pointsRequired <= 0 {
		return nil, nil, fmt.Errorf("points_required must be greater than 0")
	}

	rec, err := points.CalculateValue(cashPrice, pointsRequired, programSlug)
	if err != nil {
		return nil, nil, err
	}

	summary := fmt.Sprintf(
		"%s: %.2f¢/pt (floor %.2f¢/pt, ceiling %.2f¢/pt) — %s. %s",
		rec.ProgramName, rec.CPP, rec.FloorCPP, rec.CeilingCPP, rec.Verdict, rec.Explanation,
	)

	content, err := buildAnnotatedContentBlocks(summary, rec)
	if err != nil {
		return nil, nil, err
	}

	return content, rec, nil
}

// handlePointsArbitrage compares hotel points redemptions across programs and
// returns a ranked recommendation. It mirrors the CLI `--offer` path.
func handlePointsArbitrage(cashPrice float64, currency string, offers []hotelarb.PointsOffer) ([]ContentBlock, interface{}, error) {
	if cashPrice <= 0 {
		return nil, nil, fmt.Errorf("cash_price must be greater than 0")
	}

	result, err := hotelarb.ComparePointsArbitrage(hotelarb.PointsArbitrageInput{
		CashPrice: cashPrice,
		Currency:  currency,
		Offers:    offers,
	})
	if err != nil {
		return nil, nil, err
	}

	summary := fmt.Sprintf(
		"Arbitrage across %d programs: %s — best is %s. %s",
		len(result.Offers),
		recommendationLabel(result.Recommendation),
		result.BestOffer.ProgramName,
		result.Reason,
	)

	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}

	return content, result, nil
}

// recommendationLabel renders the arbitrage recommendation as plain words.
func recommendationLabel(r hotelarb.PointsRecommendation) string {
	switch r {
	case hotelarb.RecommendUsePoints:
		return "use points"
	case hotelarb.RecommendPayCash:
		return "pay cash"
	default:
		return string(r)
	}
}

// parsePointsOfferArgs reads the `offers` argument into hotelarb.PointsOffer
// values. Each element must be an object carrying `program` (slug), `points`,
// and optional `cash_fees`, matching the CLI `program:points[:cash_fees]`
// semantics. Returns nil when `offers` is absent or empty.
func parsePointsOfferArgs(args map[string]any) []hotelarb.PointsOffer {
	if args == nil {
		return nil
	}
	raw, ok := args["offers"].([]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	offers := make([]hotelarb.PointsOffer, 0, len(raw))
	for _, elem := range raw {
		obj, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		offers = append(offers, hotelarb.PointsOffer{
			Program:        argString(obj, "program"),
			PointsRequired: argInt(obj, "points", 0),
			CashFees:       argFloat(obj, "cash_fees", 0),
		})
	}
	return offers
}
