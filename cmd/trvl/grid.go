package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

func gridCmd() *cobra.Command {
	var (
		departFrom     string
		departTo       string
		returnFrom     string
		returnTo       string
		format         string
		targetCurrency string
	)

	cmd := &cobra.Command{
		Use:   "grid ORIGIN DESTINATION",
		Short: "Show a departure x return price grid",
		Long: `Show a 2D price grid of departure date x return date combinations.

ORIGIN and DESTINATION are IATA airport codes (e.g. HEL, NRT, JFK).
The grid shows the cheapest round-trip price for each date combination.

Examples:
  trvl grid HEL NRT --depart-from 2026-07-01 --depart-to 2026-07-07 --return-from 2026-07-08 --return-to 2026-07-14
  trvl grid HEL BCN --depart-from 2026-06-15 --depart-to 2026-06-20 --return-from 2026-06-22 --return-to 2026-06-28
  trvl grid HEL NRT --format json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			origin := strings.ToUpper(args[0])
			destination := strings.ToUpper(args[1])

			if err := models.ValidateIATA(origin); err != nil {
				return fmt.Errorf("invalid origin: %w", err)
			}
			if err := models.ValidateIATA(destination); err != nil {
				return fmt.Errorf("invalid destination: %w", err)
			}

			opts := flights.GridOptions{
				DepartFrom: departFrom,
				DepartTo:   departTo,
				ReturnFrom: returnFrom,
				ReturnTo:   returnTo,
			}

			result, err := flights.SearchPriceGrid(cmd.Context(), origin, destination, opts)
			if err != nil {
				return err
			}

			if format == "json" {
				return models.FormatJSON(os.Stdout, result)
			}

			return printGridTable(cmd.Context(), targetCurrency, result, origin, destination)
		},
	}

	cmd.Flags().StringVar(&departFrom, "depart-from", "", "Start of departure range (YYYY-MM-DD); default: tomorrow")
	cmd.Flags().StringVar(&departTo, "depart-to", "", "End of departure range (YYYY-MM-DD); default: depart-from + 6 days")
	cmd.Flags().StringVar(&returnFrom, "return-from", "", "Start of return range (YYYY-MM-DD); default: depart-to + 1 day")
	cmd.Flags().StringVar(&returnTo, "return-to", "", "End of return range (YYYY-MM-DD); default: return-from + 6 days")
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table, json")
	cmd.Flags().StringVar(&targetCurrency, "currency", "", "Convert prices to this currency (e.g. EUR, USD). Empty = show API default")

	cmd.ValidArgsFunction = airportCompletion

	return cmd
}

// printGridTable renders a price grid as a 2D ASCII table.
// If targetCurrency is set and differs from cell currency, converts prices.
func printGridTable(ctx context.Context, targetCurrency string, result *models.PriceGrid, origin, destination string) error {
	if !result.Success {
		_, _ = fmt.Fprintf(os.Stderr, "Grid search failed: %s\n", result.Error)
		return nil
	}

	if result.Count == 0 {
		fmt.Println("No price data found for the given date ranges.")
		return nil
	}

	fmt.Printf("Price grid %s -> %s (%d combinations)\n\n", origin, destination, result.Count)

	// Build a lookup map for quick price access.
	priceMap := make(map[string]float64)
	currencyMap := make(map[string]string)
	for _, c := range result.Cells {
		key := c.DepartureDate + "|" + c.ReturnDate
		priceMap[key] = c.Price
		currencyMap[key] = c.Currency
	}

	// Build table: rows = departure dates, columns = return dates.
	headers := []string{"Depart \\ Return"}
	for _, rd := range result.ReturnDates {
		// Use short date format (MM-DD) for column headers.
		headers = append(headers, rd[5:]) // strip year prefix
	}

	var rows [][]string
	for _, dd := range result.DepartureDates {
		row := []string{dd[5:]} // short date
		for _, rd := range result.ReturnDates {
			key := dd + "|" + rd
			if p, ok := priceMap[key]; ok {
				cur := currencyMap[key]
				if cur == "" {
					// Use currency from any other cell in the grid.
					for _, v := range currencyMap {
						if v != "" {
							cur = v
							break
						}
					}
				}
				if targetCurrency != "" && cur != targetCurrency && p > 0 {
					converted, convertedCur := destinations.ConvertCurrency(ctx, p, cur, targetCurrency)
					p = math.Round(converted)
					cur = convertedCur
				}
				row = append(row, fmt.Sprintf("%s %.0f", cur, p))
			} else {
				row = append(row, "-")
			}
		}
		rows = append(rows, row)
	}

	models.FormatTable(os.Stdout, headers, rows)
	return nil
}
