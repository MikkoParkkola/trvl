package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/lounges"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// --- Output schema ---

func loungeSearchOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success": schemaBool(),
			"airport": schemaString(),
			"count":   schemaInt(),
			"lounges": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":            schemaString(),
					"airport":         schemaString(),
					"terminal":        schemaString(),
					"cards":           schemaStringArray(),
					"amenities":       schemaStringArray(),
					"open_hours":      schemaString(),
					"accessible_with": schemaStringArrayDesc("Subset of the user's own lounge cards that grant free entry"),
				},
				"required": []string{"name", "airport"},
			}),
			"source": schemaString(),
			"error":  schemaString(),
		},
		"required": []string{"success", "count"},
	}
}

// --- Tool definition ---

func searchLoungesTool() ToolDef {
	return ToolDef{
		Name:  "search_lounges",
		Title: "Search Airport Lounges",
		Description: "Find airport lounges at a given airport. Returns name, terminal, " +
			"accepted access cards (Priority Pass, Diners Club, LoungeKey, etc.), amenities, " +
			"and opening hours. Results are annotated with the user's own lounge cards " +
			"(from preferences) so you can tell the user exactly which lounges they can enter for free.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"airport": {
					Type:        "string",
					Description: "Airport IATA code (e.g. HEL, LHR, JFK)",
				},
			},
			Required: []string{"airport"},
		},
		OutputSchema: loungeSearchOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Search Airport Lounges",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

// --- Handler ---

func handleSearchLounges(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	airport := strings.ToUpper(strings.TrimSpace(argString(args, "airport")))

	if airport == "" {
		return nil, nil, fmt.Errorf("airport is required")
	}
	if err := models.ValidateIATA(airport); err != nil {
		return nil, nil, fmt.Errorf("invalid airport: %w", err)
	}

	result, err := lounges.SearchLounges(ctx, airport)
	if err != nil {
		return nil, nil, fmt.Errorf("lounge search: %w", err)
	}

	// Annotate with user's lounge cards and frequent flyer status.
	prefs, _ := preferences.Load()
	if prefs != nil {
		ffCards := ffStatusToCards(prefs.FrequentFlyerPrograms)
		lounges.AnnotateAccessFull(result, prefs.LoungeCards, ffCards)
	}

	summary := loungeSummary(result, prefs)
	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}

	return content, result, nil
}

// loungeSummary builds a human-readable summary of the lounge search result.
func loungeSummary(result *lounges.SearchResult, prefs *preferences.Preferences) string {
	if !result.Success {
		if result.Error != "" {
			return fmt.Sprintf("Lounge search for %s failed: %s", result.Airport, result.Error)
		}
		return fmt.Sprintf("No lounge information available for %s.", result.Airport)
	}

	if result.Count == 0 {
		return fmt.Sprintf("No lounges found at %s in our database.", result.Airport)
	}

	summary := fmt.Sprintf("Found %d lounge(s) at %s.", result.Count, result.Airport)

	// If user has lounge cards, count which lounges they can access.
	if prefs != nil && len(prefs.LoungeCards) > 0 {
		accessible := 0
		for _, l := range result.Lounges {
			if len(l.AccessibleWith) > 0 {
				accessible++
			}
		}
		if accessible > 0 {
			cardNames := strings.Join(prefs.LoungeCards, ", ")
			summary += fmt.Sprintf(" You have free access to %d lounge(s) with your card(s): %s.", accessible, cardNames)
		} else {
			summary += " None of these lounges accept your current lounge cards."
		}
	}

	return summary
}

// allianceTierCards maps (normalised alliance, normalised tier) to the card
// name that airline lounges use in their Cards list.
//
// These names match the conventions used in the static lounge dataset and by
// airline alliance published benefit tiers.
var allianceTierCards = map[string]map[string]string{
	"oneworld": {
		"ruby":     "Oneworld Ruby",
		"sapphire": "Oneworld Sapphire",
		"emerald":  "Oneworld Emerald",
	},
	"star_alliance": {
		"silver": "Star Alliance Silver",
		"gold":   "Star Alliance Gold",
	},
	"skyteam": {
		"elite":      "SkyTeam Elite",
		"elite_plus": "SkyTeam Elite Plus",
		"eliteplus":  "SkyTeam Elite Plus",
	},
}

// airlineProgramNames maps IATA airline codes to their frequent flyer program
// name prefix. Used to generate airline-specific card names like
// "Finnair Plus Gold" from FrequentFlyerStatus{AirlineCode: "AY", Tier: "Gold"}.
var airlineProgramNames = map[string]string{
	"AY": "Finnair Plus",
	"BA": "BA",
	"AA": "AAdvantage",
	"QF": "Qantas Frequent Flyer",
	"CX": "Cathay",
	"JL": "JAL Mileage Bank",
	"QR": "Qatar Airways Privilege Club",
	"IB": "Iberia Plus",
	"MH": "Malaysia Airlines Enrich",
	"RJ": "Royal Plus",
	"AF": "Flying Blue",
	"KL": "Flying Blue",
	"DL": "Delta SkyMiles",
	"KE": "Korean Air SKYPASS",
	"LH": "Miles & More",
	"UA": "MileagePlus",
	"SQ": "KrisFlyer",
	"TG": "Royal Orchid Plus",
	"NH": "ANA Mileage Club",
	"AC": "Aeroplan",
	"TK": "Miles&Smiles",
	"SK": "EuroBonus",
}

// ffStatusToCards converts a slice of FrequentFlyerStatus into synthetic card
// names that can be matched against lounge access cards. For example, a user
// with Oneworld Sapphire status via Finnair produces:
//
//	["Oneworld Sapphire", "Finnair Plus Sapphire"]
//
// The result is suitable for passing as the ffCards argument to
// lounges.AnnotateAccessFull.
func ffStatusToCards(programs []preferences.FrequentFlyerStatus) []string {
	if len(programs) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var cards []string
	add := func(c string) {
		key := strings.ToLower(c)
		if !seen[key] {
			seen[key] = true
			cards = append(cards, c)
		}
	}

	for _, p := range programs {
		alliance := strings.ToLower(strings.TrimSpace(p.Alliance))
		alliance = strings.ReplaceAll(alliance, "-", "_")
		alliance = strings.ReplaceAll(alliance, " ", "_")
		tier := normalizeTierName(p.Tier)
		displayTier := tierDisplayName(alliance, tier)

		// Alliance-level card: "Oneworld Sapphire", "Star Alliance Gold", etc.
		if tiers, ok := allianceTierCards[alliance]; ok {
			if card, ok := tiers[tier]; ok {
				add(card)
			}
		}

		// Airline-specific card: "Finnair Plus Gold", "BA Gold", etc.
		code := strings.ToUpper(strings.TrimSpace(p.AirlineCode))
		if code != "" {
			if program, ok := airlineProgramNames[code]; ok {
				add(program + " " + displayTier)
			}
		}
	}

	return cards
}

// normalizeTierName lowercases and normalises a tier string: "Elite Plus" ->
// "elite_plus", "SAPPHIRE" -> "sapphire".
func normalizeTierName(tier string) string {
	t := strings.ToLower(strings.TrimSpace(tier))
	t = strings.ReplaceAll(t, "-", "_")
	t = strings.ReplaceAll(t, " ", "_")
	return t
}

// tierDisplayName returns the human-readable tier name for card generation.
// e.g. ("oneworld", "sapphire") -> "Sapphire", ("skyteam", "elite_plus") -> "Elite Plus".
func tierDisplayName(alliance, normalizedTier string) string {
	// Look up the full card name and extract the tier portion after the
	// alliance prefix.
	if tiers, ok := allianceTierCards[alliance]; ok {
		if card, ok := tiers[normalizedTier]; ok {
			// Card is "Oneworld Sapphire" — extract everything after the first space.
			if idx := strings.Index(card, " "); idx >= 0 {
				return card[idx+1:]
			}
		}
	}
	// Fallback: title-case the normalised tier.
	parts := strings.Split(normalizedTier, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}
