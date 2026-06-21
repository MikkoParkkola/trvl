package main

import (
	"fmt"
	"os"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/multimodal"
	"github.com/spf13/cobra"
)

func multimodalPlanCmd() *cobra.Command {
	var allowBrowserFallbacks bool

	cmd := &cobra.Command{
		Use:     "multimodal FROM TO DATE",
		Aliases: []string{"plan", "combo"},
		Short:   "Compose end-to-end multimodal itineraries (Rome2Rio discovery + real per-leg pricing)",
		Long: `Compose end-to-end travel itineraries that mix modes (e.g. ferry then fly).

Rome2Rio DISCOVERS candidate mode-chains a single-mode search would never
surface; trvl then PRICES each leg with its existing flight and ground
providers, ASSEMBLES the legs into one true total, RANKS by that total, and
ANNOTATES any travel-hack saving. Legs that cannot be priced fall back to
Rome2Rio's indicative range and are clearly labelled as estimates — never a
fabricated fare.

Rome2Rio sits behind Cloudflare; pass --allow-browser-fallbacks to use the
live browser-assisted discovery path. Without it, discovery returns an honest
typed status offline.

FROM and TO are place names (e.g. "Helsinki", "London").
DATE is the departure date in YYYY-MM-DD format.

Examples:
  trvl multimodal Helsinki London 2026-07-01
  trvl plan "Stockholm" "Tallinn" 2026-07-01 --allow-browser-fallbacks`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			from, to, date := args[0], args[1], args[2]

			planner := multimodal.NewPlanner(allowBrowserFallbacks)
			plan, err := planner.Plan(cmd.Context(), from, to, date)
			if err != nil {
				return err
			}

			if format == "json" {
				return models.FormatJSON(os.Stdout, plan)
			}
			printMultimodalPlan(plan)

			if openFlag && len(plan.Itineraries) > 0 && plan.Itineraries[0].BookingURL != "" {
				_ = openBrowser(plan.Itineraries[0].BookingURL)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&allowBrowserFallbacks, "allow-browser-fallbacks", false, "Allow browser/cookie-assisted Rome2Rio discovery + ground leg pricing")
	return cmd
}

func printMultimodalPlan(plan *multimodal.Plan) {
	if plan.Error != "" {
		_, _ = fmt.Fprintf(os.Stderr, "No multimodal plan: %s\n", plan.Error)
		return
	}
	if len(plan.Itineraries) == 0 {
		_, _ = fmt.Fprintf(os.Stderr, "No multimodal itineraries from %s to %s on %s.\n", plan.From, plan.To, plan.Date)
		for _, n := range plan.Notes {
			_, _ = fmt.Fprintf(os.Stderr, "  - %s\n", n)
		}
		return
	}

	models.Banner(os.Stdout, "🧭",
		fmt.Sprintf("Multimodal · %s → %s", plan.From, plan.To),
		fmt.Sprintf("%d itineraries from %d discovered chains (%s)", len(plan.Itineraries), plan.Discovered, plan.Date))
	fmt.Println()

	for i, it := range plan.Itineraries {
		tag := ""
		if it.Estimated {
			tag = " " + models.Red("(includes estimate)")
		}
		fmt.Printf("%d. %s  %s %.2f  ·  %s%s\n",
			i+1, it.ModeChain, it.Currency, it.TotalPrice, formatDuration(it.DurationMin), tag)
		for _, leg := range it.Legs {
			price := fmt.Sprintf("%s %.2f", leg.Currency, leg.Price)
			label := leg.Provider
			if leg.Estimated {
				label = models.Red("estimate")
			}
			line := fmt.Sprintf("     • %-6s %-18s %s via %s", leg.Mode, leg.From+"→"+leg.To, price, label)
			if leg.Detail != "" {
				line += " " + leg.Detail
			}
			fmt.Println(line)
		}
		printDoorToDoor(os.Stdout, it)
		if it.HackSaving != nil {
			fmt.Printf("     %s %s: save %s %.2f (%s)\n",
				"💡", it.HackSaving.Title, it.Currency, it.HackSaving.Savings, it.HackSaving.Type)
		}
		for _, w := range it.Warnings {
			fmt.Printf("     %s %s\n", models.Red("⚠"), w)
		}
		if it.BookingURL != "" {
			fmt.Printf("     %s\n", it.BookingURL)
		}
		fmt.Println()
	}

	for _, n := range plan.Notes {
		fmt.Printf("Note: %s\n", n)
	}
}
