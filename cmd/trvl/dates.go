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

func datesCmd() *cobra.Command {
	var (
		fromDate       string
		toDate         string
		duration       int
		roundTrip      bool
		adults         int
		format         string
		legacy         bool
		targetCurrency string
		noGeo          bool
	)

	cmd := &cobra.Command{
		Use:   "dates [ORIGIN] DESTINATION",
		Short: "Find cheapest flight dates across a range",
		Long: `Search for the cheapest flight prices across a date range.

ORIGIN and DESTINATION are IATA airport codes (e.g. HEL, NRT, JFK). ORIGIN is
optional: omit it and trvl resolves it from your saved home airport or, failing
that, your current location (geo-IP, best-effort; disable with --no-geo).

By default, uses Google's CalendarGraph API for fast single-request results.
Falls back to per-date search automatically if CalendarGraph fails.
Use --legacy to force the per-date search approach.

Examples:
  trvl dates NRT --from 2026-06-01 --to 2026-06-30
  trvl dates HEL NRT --from 2026-06-01 --to 2026-06-30
  trvl dates HEL NRT --from 2026-06-01 --to 2026-06-30 --round-trip --duration 7
  trvl dates HEL NRT --from 2026-06-01 --to 2026-06-07 --format json
  trvl dates HEL NRT --from 2026-06-01 --to 2026-06-30 --legacy`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var originArg, destination string
			if len(args) == 2 {
				originArg = args[0]
				destination = strings.ToUpper(args[1])
			} else {
				destination = strings.ToUpper(args[0])
			}
			origin, err := resolveCLIOrigin(cmd.Context(), originArg, format, noGeo)
			if err != nil {
				return err
			}

			if legacy {
				// Legacy: per-date N-call approach.
				opts := flights.DateSearchOptions{
					FromDate:  fromDate,
					ToDate:    toDate,
					Duration:  duration,
					RoundTrip: roundTrip,
					Adults:    adults,
				}

				result, err := flights.SearchDates(cmd.Context(), origin, destination, opts)
				if err != nil {
					return err
				}

				if format == "json" {
					return models.FormatJSON(os.Stdout, result)
				}
				return printDatesTable(cmd.Context(), targetCurrency, result)
			}

			// Default: CalendarGraph (single request, fast).
			opts := flights.CalendarOptions{
				FromDate:   fromDate,
				ToDate:     toDate,
				TripLength: duration,
				RoundTrip:  roundTrip,
				Adults:     adults,
			}

			result, err := flights.SearchCalendar(cmd.Context(), origin, destination, opts)
			if err != nil {
				return err
			}

			if format == "json" {
				return models.FormatJSON(os.Stdout, result)
			}

			return printDatesTable(cmd.Context(), targetCurrency, result)
		},
	}

	cmd.Flags().StringVar(&fromDate, "from", "", "Start of date range (YYYY-MM-DD); default: tomorrow")
	cmd.Flags().StringVar(&toDate, "to", "", "End of date range (YYYY-MM-DD); default: from + 30 days")
	cmd.Flags().IntVar(&duration, "duration", 7, "Trip duration in days (for round-trip)")
	cmd.Flags().BoolVar(&roundTrip, "round-trip", false, "Search round-trip prices")
	cmd.Flags().IntVar(&adults, "adults", 1, "Number of adult passengers")
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table, json")
	cmd.Flags().BoolVar(&legacy, "legacy", false, "Use legacy per-date search (slower, more requests)")
	cmd.Flags().StringVar(&targetCurrency, "currency", "", "Convert prices to this currency (e.g. EUR, USD). Empty = show API default")
	cmd.Flags().BoolVar(&noGeo, "no-geo", false, "Disable geo-IP origin detection (also via TRVL_NO_GEO=1)")

	cmd.ValidArgsFunction = airportCompletion

	return cmd
}

// printDatesTable renders date price results as an ASCII table.
// If targetCurrency is set and differs from API currency, converts prices.
func printDatesTable(ctx context.Context, targetCurrency string, result *models.DateSearchResult) error {
	if !result.Success {
		_, _ = fmt.Fprintf(os.Stderr, "Search failed: %s\n", result.Error)
		return nil
	}

	if result.Count == 0 {
		fmt.Println("No prices found for the given date range.")
		return nil
	}

	// Convert prices if --currency specified.
	if targetCurrency != "" {
		for i := range result.Dates {
			if result.Dates[i].Currency != targetCurrency && result.Dates[i].Price > 0 {
				converted, cur := destinations.ConvertCurrency(ctx, result.Dates[i].Price, result.Dates[i].Currency, targetCurrency)
				result.Dates[i].Price = math.Round(converted)
				result.Dates[i].Currency = cur
			}
		}
	}

	fmt.Printf("Cheapest prices: %s (%s, %d dates)\n\n", result.DateRange, result.TripType, result.Count)

	headers := []string{"Date", "Price"}
	if result.TripType == "round_trip" {
		headers = append(headers, "Return")
	}

	var rows [][]string
	for _, d := range result.Dates {
		row := []string{
			d.Date,
			formatPrice(d.Price, d.Currency),
		}
		if result.TripType == "round_trip" {
			row = append(row, d.ReturnDate)
		}
		rows = append(rows, row)
	}

	models.FormatTable(os.Stdout, headers, rows)
	return nil
}
