package main

import (
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

func hiddenCityCmd() *cobra.Command {
	var (
		carrier         string
		price           float64
		currency        string
		carryOnOnly     bool
		separateTickets bool
		layoverMinutes  int
		directBaseline  float64
		maxLayoverRisk  int
		topK            int
		departDate      string
	)

	cmd := &cobra.Command{
		Use:   "hidden-city ORIGIN HUB HUB_BEYOND",
		Short: "Find hidden-city savings (disembark at layover, skip final leg)",
		Long: `Evaluate a hidden-city routing where you board ORIGIN→HUB→HUB_BEYOND
but disembark at HUB, using the cheaper long-haul ticket.

Only safe with carry-on luggage — checked bags travel to HUB_BEYOND.

ORIGIN, HUB, and HUB_BEYOND are IATA airport codes.

Examples:
  trvl hidden-city AMS HEL RIX --price 89.50 --carrier AY --layover-min 90 --carry-on --date 2026-05-15
  trvl hidden-city AMS FRA MUC --price 110 --carrier LH --layover-min 60 --date 2026-06-01`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			origin := strings.ToUpper(args[0])
			hub := strings.ToUpper(args[1])
			hubBeyond := strings.ToUpper(args[2])

			if price <= 0 {
				return fmt.Errorf("--price is required and must be > 0")
			}
			if currency == "" {
				currency = "EUR"
			}

			offer := hacks.HiddenCityOffer{
				Origin:          origin,
				Hub:             hub,
				HubBeyond:       hubBeyond,
				Carrier:         strings.ToUpper(carrier),
				Price:           price,
				Currency:        currency,
				CarryOnOnly:     carryOnOnly,
				SeparateTickets: separateTickets,
				LayoverMinutes:  layoverMinutes,
			}

			candidates := hacks.ExpandMatrix([]hacks.HiddenCityOffer{offer}, hacks.MatrixOptions{
				AllowHiddenCity: true,
				DirectBaseline:  directBaseline,
				DepartDate:      departDate,
				MaxLayoverRisk:  maxLayoverRisk,
				TopK:            topK,
			})

			if len(candidates) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No viable hidden-city candidates (risk score above threshold).\n")
				return nil
			}

			for _, c := range candidates {
				fmt.Fprintf(cmd.OutOrStdout(), "%s → %s (skip last leg to %s)\n", c.Origin, c.HubBeyond, c.Hub)
				fmt.Fprintf(cmd.OutOrStdout(), "  Carrier: %s  Price: %s %.2f", c.Carrier, c.Currency, c.Price)
				if c.SavingsPct > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "  Saves: %.0f%% (%.2f %s)", c.SavingsPct, c.SavingsEUR, c.Currency)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "\n  Risk: %s — %s\n", colorizeHiddenCityRisk(c.LayoverRisk), c.Reason)
				fmt.Fprintf(cmd.OutOrStdout(), "  Booking: %s\n\n", c.BookingURL)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&carrier, "carrier", "", "Airline IATA code (e.g. AY, KL, LH)")
	cmd.Flags().Float64Var(&price, "price", 0, "Ticket price for the full Origin→HubBeyond routing (required)")
	cmd.Flags().StringVar(&currency, "currency", "EUR", "Price currency (default: EUR)")
	cmd.Flags().BoolVar(&carryOnOnly, "carry-on", false, "Carry-on only — safer for hidden-city (no checked bags routed to HubBeyond)")
	cmd.Flags().BoolVar(&separateTickets, "separate-tickets", false, "Itinerary spans two PNRs (separate-ticket risk)")
	cmd.Flags().IntVar(&layoverMinutes, "layover-min", 120, "Scheduled layover at Hub in minutes")
	cmd.Flags().Float64Var(&directBaseline, "baseline", 0, "Cheapest known direct Origin→Hub price for savings computation")
	cmd.Flags().IntVar(&maxLayoverRisk, "max-risk", 60, "Drop candidates with risk score above this (0-100, default 60)")
	cmd.Flags().IntVar(&topK, "top-k", 3, "Maximum candidates to return")
	cmd.Flags().StringVar(&departDate, "date", "", "Departure date (YYYY-MM-DD) for booking URL")
	_ = cmd.MarkFlagRequired("price")

	return cmd
}

// colorizeHiddenCityRisk returns a color-coded risk label.
func colorizeHiddenCityRisk(risk int) string {
	switch {
	case risk < 25:
		return models.Green(fmt.Sprintf("%d/100", risk))
	case risk < 50:
		return models.Yellow(fmt.Sprintf("%d/100", risk))
	default:
		return models.Red(fmt.Sprintf("%d/100", risk))
	}
}
