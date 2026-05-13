package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trip"
	"github.com/spf13/cobra"
)

func multiCityCmd() *cobra.Command {
	var (
		visit          []string
		dates          string
		formatOut      string
		targetCurrency string
	)

	cmd := &cobra.Command{
		Use:   "multi-city HOME",
		Short: "Optimize routing order for a multi-city trip",
		Long: `Find the cheapest order to visit multiple cities.

HOME is your home IATA airport code (e.g. HEL, JFK).
The optimizer tries all permutations of the visit order to find
the route with the lowest total flight cost.

Maximum 6 cities (6! = 720 permutations).

Examples:
  trvl multi-city HEL --visit BCN,ROM,PAR --dates 2026-07-01,2026-07-21
  trvl multi-city JFK --visit LHR,CDG,FCO,BCN --dates 2026-08-01,2026-08-21
  trvl multi-city HEL --visit BCN,ATH --dates 2026-06-01,2026-06-14 --format json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home := strings.ToUpper(args[0])

			if err := models.ValidateIATA(home); err != nil {
				return fmt.Errorf("invalid home airport: %w", err)
			}

			if len(visit) == 0 {
				return fmt.Errorf("--visit is required (comma-separated IATA codes)")
			}

			// Normalize city codes.
			cities := make([]string, len(visit))
			for i, c := range visit {
				cities[i] = strings.ToUpper(strings.TrimSpace(c))
				if err := models.ValidateIATA(cities[i]); err != nil {
					return fmt.Errorf("invalid city %q: %w", c, err)
				}
			}

			// Parse dates.
			var departDate, returnDate string
			if dates != "" {
				parts := strings.SplitN(dates, ",", 2)
				departDate = strings.TrimSpace(parts[0])
				if len(parts) > 1 {
					returnDate = strings.TrimSpace(parts[1])
				}
			}
			if departDate == "" {
				return fmt.Errorf("--dates is required (format: YYYY-MM-DD,YYYY-MM-DD)")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
			defer cancel()

			opts := trip.MultiCityOptions{
				DepartDate: departDate,
				ReturnDate: returnDate,
			}

			result, err := trip.OptimizeMultiCity(ctx, home, cities, opts)
			if err != nil {
				return err
			}

			// Cache for `trvl share --last`.
			if result != nil && result.Success {
				dest := strings.Join(result.OptimalOrder, " -> ")
				saveLastSearch(&LastSearch{
					Command:        "multi-city",
					Origin:         home,
					Destination:    dest,
					DepartDate:     departDate,
					ReturnDate:     returnDate,
					FlightPrice:    result.TotalCost,
					FlightCurrency: result.Currency,
					TotalPrice:     result.TotalCost,
					TotalCurrency:  result.Currency,
				})
			}

			if formatOut == "json" {
				return models.FormatJSON(os.Stdout, result)
			}

			return printMultiCityTable(cmd.Context(), targetCurrency, result)
		},
	}

	cmd.Flags().StringSliceVar(&visit, "visit", nil, "Cities to visit (comma-separated IATA codes, e.g. BCN,ROM,PAR)")
	cmd.Flags().StringVar(&dates, "dates", "", "Travel dates as start,end (YYYY-MM-DD,YYYY-MM-DD)")
	cmd.Flags().StringVar(&formatOut, "format", "table", "Output format: table, json")
	cmd.Flags().StringVar(&targetCurrency, "currency", "", "Convert prices to this currency (e.g. EUR, USD). Empty = show API default")

	_ = cmd.MarkFlagRequired("visit")
	_ = cmd.MarkFlagRequired("dates")

	cmd.ValidArgsFunction = airportCompletion

	return cmd
}

func printMultiCityTable(ctx context.Context, targetCurrency string, result *trip.MultiCityResult) error {
	if !result.Success {
		_, _ = fmt.Fprintf(os.Stderr, "Multi-city optimization failed: %s\n", result.Error)
		return nil
	}

	// Convert segment prices if --currency specified.
	if targetCurrency != "" {
		for i := range result.Segments {
			s := &result.Segments[i]
			if s.Currency != targetCurrency && s.Price > 0 {
				s.Currency = convertRoundedDisplayAmounts(
					ctx,
					s.Currency,
					targetCurrency,
					0,
					&s.Price,
				)
			}
		}
		if result.Currency != targetCurrency {
			result.Currency = convertRoundedDisplayAmounts(
				ctx,
				result.Currency,
				targetCurrency,
				0,
				&result.TotalCost,
				&result.Savings,
			)
		}
	}

	fmt.Printf("Multi-city from %s: optimal order found (%d permutations checked)\n\n",
		result.HomeAirport, result.Permutations)

	// Print route.
	route := append([]string{result.HomeAirport}, result.OptimalOrder...)
	route = append(route, result.HomeAirport)
	fmt.Printf("Route: %s\n\n", strings.Join(route, " -> "))

	// Print segments.
	headers := []string{"From", "To", "Price"}
	var rows [][]string
	for _, s := range result.Segments {
		fromName := models.LookupAirportName(s.From)
		toName := models.LookupAirportName(s.To)
		rows = append(rows, []string{
			fmt.Sprintf("%s (%s)", fromName, s.From),
			fmt.Sprintf("%s (%s)", toName, s.To),
			formatPrice(s.Price, s.Currency),
		})
	}

	rows = append(rows, []string{"", "", ""})
	rows = append(rows, []string{"Total", "", fmt.Sprintf("%s %.0f", result.Currency, result.TotalCost)})
	if result.Savings > 0 {
		rows = append(rows, []string{"Savings vs worst order", "", fmt.Sprintf("%s %.0f", result.Currency, result.Savings)})
	}

	models.FormatTable(os.Stdout, headers, rows)
	return nil
}
