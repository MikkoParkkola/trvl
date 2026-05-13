package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trip"
	"github.com/spf13/cobra"
)

func suggestCmd() *cobra.Command {
	var (
		around         string
		flex           int
		roundTrip      bool
		duration       int
		formatOut      string
		targetCurrency string
	)

	cmd := &cobra.Command{
		Use:   "suggest ORIGIN DESTINATION",
		Short: "Get smart date suggestions with pricing insights",
		Long: `Find the cheapest travel dates and get pricing insights.

ORIGIN and DESTINATION are IATA airport codes (e.g. HEL, BCN, NRT).
Analyzes prices around your target date and returns the cheapest options
along with insights like weekday vs weekend savings.

Examples:
  trvl suggest HEL BCN --around 2026-07-15 --flex 7
  trvl suggest HEL BCN --around 2026-07-15 --flex 14 --round-trip --duration 7
  trvl suggest JFK LHR --around 2026-08-01 --format json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			origin := strings.ToUpper(args[0])
			dest := strings.ToUpper(args[1])

			if err := models.ValidateIATA(origin); err != nil {
				return fmt.Errorf("invalid origin: %w", err)
			}
			if err := models.ValidateIATA(dest); err != nil {
				return fmt.Errorf("invalid destination: %w", err)
			}

			if around == "" {
				around = time.Now().AddDate(0, 1, 0).Format("2006-01-02")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			opts := trip.SmartDateOptions{
				TargetDate: around,
				FlexDays:   flex,
				RoundTrip:  roundTrip,
				Duration:   duration,
			}

			result, err := trip.SuggestDates(ctx, origin, dest, opts)
			if err != nil {
				return err
			}

			if formatOut == "json" {
				return models.FormatJSON(os.Stdout, result)
			}

			return printSuggestTable(cmd.Context(), targetCurrency, result)
		},
	}

	cmd.Flags().StringVar(&around, "around", "", "Target date to search around (YYYY-MM-DD); default: +1 month")
	cmd.Flags().IntVar(&flex, "flex", 7, "Days of flexibility around target date")
	cmd.Flags().BoolVar(&roundTrip, "round-trip", false, "Search round-trip prices")
	cmd.Flags().IntVar(&duration, "duration", 7, "Trip duration in days (for round-trip)")
	cmd.Flags().StringVar(&formatOut, "format", "table", "Output format: table, json")
	cmd.Flags().StringVar(&targetCurrency, "currency", "", "Convert prices to this currency (e.g. EUR, USD). Empty = show API default")

	cmd.ValidArgsFunction = airportCompletion

	return cmd
}

func printSuggestTable(ctx context.Context, targetCurrency string, result *trip.SmartDateResult) error {
	if !result.Success {
		_, _ = fmt.Fprintf(os.Stderr, "Date suggestion failed: %s\n", result.Error)
		return nil
	}

	// Convert prices if --currency specified.
	if targetCurrency != "" {
		for i := range result.CheapestDates {
			d := &result.CheapestDates[i]
			if d.Currency != targetCurrency && d.Price > 0 {
				converted, cur := destinations.ConvertCurrency(ctx, d.Price, d.Currency, targetCurrency)
				d.Price = math.Round(converted)
				d.Currency = cur
			}
		}
		if result.Currency != targetCurrency && result.AveragePrice > 0 {
			converted, cur := destinations.ConvertCurrency(ctx, result.AveragePrice, result.Currency, targetCurrency)
			result.AveragePrice = math.Round(converted)
			result.Currency = cur
		}
	}

	fmt.Printf("Smart dates: %s -> %s (avg %s %.0f)\n\n", result.Origin, result.Destination, result.Currency, result.AveragePrice)

	// Print cheapest dates.
	headers := []string{"Date", "Day", "Price"}
	var rows [][]string
	for _, d := range result.CheapestDates {
		row := []string{
			d.Date,
			d.DayOfWeek,
			fmt.Sprintf("%s %.0f", d.Currency, d.Price),
		}
		rows = append(rows, row)
	}
	models.FormatTable(os.Stdout, headers, rows)

	// Print insights.
	if len(result.Insights) > 0 {
		fmt.Println()
		for _, ins := range result.Insights {
			fmt.Printf("  %s\n", ins.Description)
		}
	}

	return nil
}
