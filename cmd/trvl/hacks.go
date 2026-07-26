package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/spf13/cobra"
)

func hacksCmd() *cobra.Command {
	var (
		returnDate  string
		carryOnOnly bool
		currency    string
	)

	cmd := &cobra.Command{
		Use:   "hacks ORIGIN DESTINATION DATE",
		Short: "Detect travel optimization hacks (throwaway, hidden city, positioning, …)",
		Long: `Automatically detect money-saving travel hacks for a route and date.

Detects: throwaway ticketing, hidden city, positioning flights, split ticketing,
overnight transport (saved hotel), airline stopover programs, date flexibility.

ORIGIN and DESTINATION are IATA airport codes.
DATE is the departure date in YYYY-MM-DD format.

Examples:
  trvl hacks HEL PRG 2026-04-13
  trvl hacks HEL AMS 2026-04-15 --return 2026-04-22 --carry-on`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			origin := strings.ToUpper(args[0])
			dest := strings.ToUpper(args[1])
			date := args[2]

			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			// Get baseline price for context.
			naivePrice := 0.0
			if r, err := flights.SearchFlights(ctx, origin, dest, date, flights.SearchOptions{}); err == nil && r.Success && len(r.Flights) > 0 {
				for _, f := range r.Flights {
					if f.Price > 0 && (naivePrice == 0 || f.Price < naivePrice) {
						naivePrice = f.Price
					}
				}
			}

			input := hacks.DetectorInput{
				Origin:      origin,
				Destination: dest,
				Date:        date,
				ReturnDate:  returnDate,
				Currency:    currency,
				CarryOnOnly: carryOnOnly,
				NaivePrice:  naivePrice,
			}

			// Thread the traveller's loyalty profile so loyalty-aware detectors
			// (mileage run, back-to-back) prefer the user's own alliances/status.
			// Missing or unreadable preferences leave the zero profile, which
			// preserves the pre-loyalty behaviour.
			if prefs, err := preferences.Load(); err == nil {
				input.Loyalty = hacks.LoyaltyFromPreferences(prefs)
			}

			detected, complete := hacks.DetectAll(ctx, input)
			if !complete {
				fmt.Fprintln(os.Stderr, "Note: the deadline expired before every detector finished; results below are partial.")
			}

			if format == "json" {
				return models.FormatJSON(os.Stdout, map[string]interface{}{
					"origin":      origin,
					"destination": dest,
					"date":        date,
					"count":       len(detected),
					"complete":    complete,
					"hacks":       detected,
				})
			}

			return printHacksTable(origin, dest, date, naivePrice, currency, detected)
		},
	}

	cmd.Flags().StringVar(&returnDate, "return", "", "Return date for round-trip analysis (YYYY-MM-DD)")
	cmd.Flags().BoolVar(&carryOnOnly, "carry-on", false, "Flag carry-on-only trips (enables hidden city suggestions)")
	cmd.Flags().StringVar(&currency, "currency", "EUR", "Display currency")

	cmd.ValidArgsFunction = airportCompletion
	return cmd
}

// printHacksTable renders all detected hacks as a formatted output.
func printHacksTable(origin, dest, date string, naivePrice float64, currency string, detected []hacks.Hack) error {
	header := fmt.Sprintf("Travel Hacks · %s→%s · %s", origin, dest, date)
	if naivePrice > 0 {
		models.Banner(os.Stdout, "💡", header,
			fmt.Sprintf("Baseline: %s %.0f one-way", currency, naivePrice),
			fmt.Sprintf("Found %d optimization opportunity/ies", len(detected)),
		)
	} else {
		models.Banner(os.Stdout, "💡", header,
			fmt.Sprintf("Found %d optimization opportunity/ies", len(detected)),
		)
	}
	fmt.Println()

	if len(detected) == 0 {
		fmt.Println("No hacks detected for this route and date.")
		fmt.Println("Try adding --return DATE to enable split-ticketing and date-flex checks.")
		return nil
	}

	for i, h := range detected {
		printHack(i+1, h)
	}
	return nil
}

func printHack(n int, h hacks.Hack) {
	cur := h.Currency
	if cur == "" {
		cur = "EUR"
	}

	title := fmt.Sprintf("%d. %s", n, models.Bold(h.Title))
	if h.Savings > 0 {
		title += "  " + models.Green(fmt.Sprintf("[saves %s %.0f]", cur, h.Savings))
	}
	fmt.Println(title)
	fmt.Println("   " + h.Description)

	if len(h.Steps) > 0 {
		fmt.Println()
		fmt.Println("   " + models.Dim("How:"))
		for _, s := range h.Steps {
			fmt.Println("   • " + s)
		}
	}

	if len(h.Risks) > 0 {
		fmt.Println()
		fmt.Println("   " + models.Yellow("Risks:"))
		for _, r := range h.Risks {
			fmt.Println("   ! " + r)
		}
	}

	if len(h.Citations) > 0 {
		fmt.Println()
		for _, c := range h.Citations {
			if c != "" {
				fmt.Println("   " + models.Dim(c))
			}
		}
	}
	fmt.Println()
}
