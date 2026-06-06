package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

// nestedCmd implements `trvl nested` — the CLI surface for the nested/overlapping
// round-trip combinator (MIK-3076). It prices two visit windows between the same
// two cities and reports whether overlapping round-trips beat two separate ones.
func nestedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nested ORIGIN DESTINATION W1_DEPART W1_RETURN W2_DEPART W2_RETURN",
		Short: "Optimize two trips between the same cities (nested/overlapping round-trips)",
		Long: `For two visits between the same two cities, find whether overlapping or
nested round-trips beat booking two separate returns. Prices both windows live
across all flight providers and ranks the cheapest pairing.

Dates are YYYY-MM-DD. Designed for two-base lifestyles (e.g. HEL<->AMS).

Example:
  trvl nested HEL AMS 2026-07-01 2026-07-05 2026-07-20 2026-07-24`,
		Args: cobra.ExactArgs(6),
		RunE: func(cmd *cobra.Command, args []string) error {
			origin := strings.ToUpper(args[0])
			dest := strings.ToUpper(args[1])
			w1d, w1r, w2d, w2r := args[2], args[3], args[4], args[5]
			for _, d := range []string{w1d, w1r, w2d, w2r} {
				if err := models.ValidateDate(d); err != nil {
					return fmt.Errorf("invalid date %q: %w", d, err)
				}
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 90*time.Second)
			defer cancel()

			ranked := hacks.PlanNestedRT(ctx, origin, dest, w1d, w1r, w2d, w2r, hacks.DefaultLegPricer, 3)
			if len(ranked) == 0 {
				return fmt.Errorf("could not price both visit windows for %s<->%s", origin, dest)
			}

			if format == "json" {
				best := 0.0
				for _, p := range ranked {
					if p.SavingsEUR > best {
						best = p.SavingsEUR
					}
				}
				return models.FormatJSON(os.Stdout, map[string]interface{}{
					"origin":           origin,
					"destination":      dest,
					"pairings":         ranked,
					"best_savings_eur": best,
				})
			}

			models.Banner(os.Stdout, "🔁", fmt.Sprintf("Nested round-trips · %s<->%s", origin, dest),
				fmt.Sprintf("Visit 1: %s → %s", w1d, w1r),
				fmt.Sprintf("Visit 2: %s → %s", w2d, w2r),
			)
			fmt.Println()
			for i, p := range ranked {
				line := fmt.Sprintf("%d. %s — %.0f", i+1, models.Bold(string(p.Kind)), p.Cost)
				if p.SavingsEUR > 0 {
					line += "  " + models.Green(fmt.Sprintf("[saves %.0f vs two separate round-trips]", p.SavingsEUR))
				}
				fmt.Println(line)
				if p.Reason != "" {
					fmt.Println("   " + p.Reason)
				}
			}
			return nil
		},
	}
	cmd.ValidArgsFunction = airportCompletion
	return cmd
}
