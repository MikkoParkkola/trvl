package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/lounges"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/spf13/cobra"
)

func loungesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lounges AIRPORT",
		Short: "Search airport lounges and check your access",
		Long: `Find airport lounges and see which ones you can enter for free.

Results are annotated with your lounge cards and frequent flyer status
from ~/.trvl/preferences.json.

Examples:
  trvl lounges HEL
  trvl lounges LHR --format json
  trvl lounges JFK`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: airportCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			airport := strings.ToUpper(strings.TrimSpace(args[0]))
			if err := models.ValidateIATA(airport); err != nil {
				return fmt.Errorf("invalid airport: %w", err)
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), cliTimeout)
			defer cancel()

			result, err := lounges.SearchLounges(ctx, airport)
			if err != nil {
				return fmt.Errorf("lounge search: %w", err)
			}

			// Annotate with user's lounge cards and frequent flyer status.
			prefs, _ := preferences.Load()
			if prefs != nil {
				ffCards := loungeFFCards(prefs.FrequentFlyerPrograms)
				lounges.AnnotateAccessFull(result, prefs.LoungeCards, ffCards)
			}

			if format == "json" {
				return models.FormatJSON(os.Stdout, result)
			}

			if !result.Success {
				if result.Error != "" {
					_, _ = fmt.Fprintf(os.Stderr, "Error: %s\n", result.Error)
				} else {
					_, _ = fmt.Fprintf(os.Stderr, "No lounge information available for %s.\n", airport)
				}
				return nil
			}

			return printLoungesTable(result, prefs)
		},
	}
	return cmd
}

func printLoungesTable(result *lounges.SearchResult, prefs *preferences.Preferences) error {
	subtitle := fmt.Sprintf("%d lounge(s)", result.Count)
	if prefs != nil && len(prefs.LoungeCards) > 0 {
		accessible := 0
		for _, l := range result.Lounges {
			if len(l.AccessibleWith) > 0 {
				accessible++
			}
		}
		if accessible > 0 {
			subtitle += fmt.Sprintf(" · %d free with your cards", accessible)
		}
	}
	models.Banner(os.Stdout, "🛋️", fmt.Sprintf("Lounges · %s", result.Airport), subtitle)
	fmt.Println()

	if result.Count == 0 {
		fmt.Println("No lounges found.")
		return nil
	}

	headers := []string{"Name", "Terminal", "Type", "Cards", "Amenities", "Hours", "Accessible"}
	var rows [][]string
	for _, l := range result.Lounges {
		cards := strings.Join(l.Cards, ", ")
		if len(cards) > 40 {
			cards = cards[:37] + "..."
		}

		amenities := strings.Join(l.Amenities, ", ")
		if len(amenities) > 30 {
			amenities = amenities[:27] + "..."
		}

		accessible := ""
		if len(l.AccessibleWith) > 0 {
			accessible = models.Green("✓")
		}

		rows = append(rows, []string{
			l.Name,
			l.Terminal,
			l.Type,
			cards,
			amenities,
			l.OpenHours,
			accessible,
		})
	}

	models.FormatTable(os.Stdout, headers, rows)
	return nil
}

// loungeFFCards converts frequent flyer status entries into card names
// suitable for lounge access matching.
func loungeFFCards(programs []preferences.FrequentFlyerStatus) []string {
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
		tier := strings.ToLower(strings.TrimSpace(p.Tier))
		tier = strings.ReplaceAll(tier, "-", "_")
		tier = strings.ReplaceAll(tier, " ", "_")

		// Alliance-level card: "Oneworld Sapphire", "Star Alliance Gold", etc.
		if tiers, ok := loungeAllianceTierCards[alliance]; ok {
			if card, ok := tiers[tier]; ok {
				add(card)
			}
		}

		// Airline-specific card: "Finnair Plus Gold", "BA Gold", etc.
		code := strings.ToUpper(strings.TrimSpace(p.AirlineCode))
		if code != "" {
			if program, ok := loungeAirlineProgramNames[code]; ok {
				display := loungeTierDisplay(alliance, tier)
				add(program + " " + display)
			}
		}
	}
	return cards
}

func loungeTierDisplay(alliance, normalizedTier string) string {
	if tiers, ok := loungeTierDisplayNames[alliance]; ok {
		if display, ok := tiers[normalizedTier]; ok {
			return display
		}
	}
	parts := strings.Split(normalizedTier, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

var loungeAllianceTierCards = map[string]map[string]string{
	"oneworld":      {"ruby": "Oneworld Ruby", "sapphire": "Oneworld Sapphire", "emerald": "Oneworld Emerald"},
	"star_alliance": {"silver": "Star Alliance Silver", "gold": "Star Alliance Gold"},
	"skyteam":       {"elite": "SkyTeam Elite", "elite_plus": "SkyTeam Elite Plus", "eliteplus": "SkyTeam Elite Plus"},
}

var loungeTierDisplayNames = map[string]map[string]string{
	"oneworld":      {"ruby": "Ruby", "sapphire": "Sapphire", "emerald": "Emerald"},
	"star_alliance": {"silver": "Silver", "gold": "Gold"},
	"skyteam":       {"elite": "Elite", "elite_plus": "Elite Plus", "eliteplus": "Elite Plus"},
}

var loungeAirlineProgramNames = map[string]string{
	"AY": "Finnair Plus", "BA": "BA", "AA": "AAdvantage",
	"QF": "Qantas Frequent Flyer", "CX": "Cathay", "JL": "JAL Mileage Bank",
	"QR": "Qatar Airways Privilege Club", "IB": "Iberia Plus",
	"MH": "Malaysia Airlines Enrich", "RJ": "Royal Plus",
	"AF": "Flying Blue", "KL": "Flying Blue", "DL": "Delta SkyMiles",
	"KE": "Korean Air SKYPASS", "LH": "Miles & More", "UA": "MileagePlus",
	"SQ": "KrisFlyer", "TG": "Royal Orchid Plus", "NH": "ANA Mileage Club",
	"AC": "Aeroplan", "TK": "Miles&Smiles", "SK": "EuroBonus",
}
