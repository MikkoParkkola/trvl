package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

func pricetrendsCmd() *cobra.Command {
	var (
		when     string
		currency string
		oneWay   bool
	)
	cmd := &cobra.Command{
		Use:   "pricetrends ORIGIN DESTINATION",
		Short: "Cheapest cached fares for a route (opt-in: Travelpayouts/Aviasales)",
		Long: `Show indicative "cheapest price seen" fares for a route from Travelpayouts
(Aviasales). These are cached price SIGNALS aggregated across airlines/OTAs —
useful for spotting cheap dates — NOT live bookable itineraries.

OPT-IN: set TRAVELPAYOUTS_TOKEN (free at travelpayouts.com/developers/api).
Disabled by default. Note: Travelpayouts is a Russia-origin meta-search; this
source never runs unless you explicitly supply your own token.

Examples:
  trvl pricetrends AMS HEL 2026-06
  trvl pricetrends HEL BCN 2026-07-01 --one-way --format json`,
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !flights.TravelpayoutsConfigured() {
				return fmt.Errorf("pricetrends is opt-in: set TRAVELPAYOUTS_TOKEN (free key from travelpayouts.com/developers/api)")
			}
			origin, destination := args[0], args[1]
			if len(args) == 3 {
				when = args[2]
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
			defer cancel()

			signals, err := flights.SearchTravelpayoutsPrices(ctx, origin, destination, when, currency, oneWay)
			if err != nil {
				return err
			}
			f, _ := cmd.Flags().GetString("format")
			if f == "json" {
				return models.FormatJSON(os.Stdout, map[string]any{"success": true, "signals": signals})
			}
			if len(signals) == 0 {
				fmt.Println("No cached price signals found for that route/date.")
				return nil
			}
			models.Banner(os.Stdout, "📈", fmt.Sprintf("Price signals · %s→%s", origin, destination),
				"indicative cached fares — verify live before booking")
			fmt.Println()
			headers := []string{"Depart", "Return", "Airline", "Stops", "Price"}
			rows := make([][]string, 0, len(signals))
			for _, s := range signals {
				ret := s.ReturnAt
				if ret == "" {
					ret = "-"
				}
				rows = append(rows, []string{
					s.DepartAt, ret, s.Airline, fmt.Sprintf("%d", s.Transfers),
					fmt.Sprintf("%s %.0f", s.Currency, s.Price),
				})
			}
			models.FormatTable(os.Stdout, headers, rows)
			fmt.Println()
			fmt.Println("  Source: Travelpayouts/Aviasales (cached, indicative)")
			return nil
		},
	}
	cmd.Flags().StringVar(&currency, "currency", "EUR", "Currency for prices")
	cmd.Flags().BoolVar(&oneWay, "one-way", false, "One-way fares only")
	return cmd
}
