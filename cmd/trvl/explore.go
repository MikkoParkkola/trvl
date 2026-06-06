package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/explore"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

func exploreCmd() *cobra.Command {
	var (
		fromDate       string
		toDate         string
		tripType       string
		stops          string
		format         string
		targetCurrency string
		noGeo          bool
	)

	cmd := &cobra.Command{
		Use:   "explore [ORIGIN]",
		Short: "Discover cheapest destinations from an airport",
		Long: `Explore flight destinations from an airport, sorted by price.

ORIGIN is an IATA airport code (e.g. HEL, JFK, NRT). It is optional: omit it
and trvl resolves it from your saved home airport or current location (geo-IP,
best-effort; disable with --no-geo).
Returns a list of destinations with the cheapest available prices.

Examples:
  trvl explore
  trvl explore HEL
  trvl explore HEL --from 2026-07-01 --to 2026-07-14
  trvl explore JFK --type one-way
  trvl explore HEL --format json`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			originArg := ""
			if len(args) == 1 {
				originArg = args[0]
			}
			origin, err := resolveCLIOrigin(cmd.Context(), originArg, format, noGeo)
			if err != nil {
				return err
			}

			if err := models.ValidateIATA(origin); err != nil {
				return fmt.Errorf("invalid origin: %w", err)
			}

			// Default dates: next 30 days.
			if fromDate == "" {
				fromDate = time.Now().AddDate(0, 0, 7).Format("2006-01-02")
			}
			if toDate == "" {
				from, err := models.ParseDate(fromDate)
				if err != nil {
					return fmt.Errorf("invalid --from date: %w", err)
				}
				toDate = from.AddDate(0, 0, 23).Format("2006-01-02")
			}

			// Validate dates.
			if err := models.ValidateDate(fromDate); err != nil {
				return fmt.Errorf("invalid --from: %w", err)
			}
			if err := models.ValidateDateRange(fromDate, toDate); err != nil {
				return err
			}

			opts := explore.ExploreOptions{
				DepartureDate: fromDate,
				Adults:        1,
			}

			// Trip type: round-trip includes a return date.
			if tripType != "one-way" {
				opts.ReturnDate = toDate
			}

			client := batchexec.NewClient()
			client.SetNoCache(noCache)
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			result, err := explore.SearchExplore(ctx, client, origin, opts)
			if err != nil {
				return err
			}

			// Filter by stops if requested.
			if stops == "nonstop" {
				var filtered []models.ExploreDestination
				for _, d := range result.Destinations {
					if d.Stops == 0 {
						filtered = append(filtered, d)
					}
				}
				result.Destinations = filtered
				result.Count = len(filtered)
			}

			// Sort by price.
			sort.Slice(result.Destinations, func(i, j int) bool {
				return result.Destinations[i].Price < result.Destinations[j].Price
			})

			if format == "json" {
				return models.FormatJSON(os.Stdout, result)
			}

			return printExploreTable(cmd.Context(), targetCurrency, result, origin)
		},
	}

	cmd.Flags().StringVar(&fromDate, "from", "", "Start date (YYYY-MM-DD); default: 7 days from now")
	cmd.Flags().StringVar(&toDate, "to", "", "End date (YYYY-MM-DD); default: from + 23 days")
	cmd.Flags().StringVar(&tripType, "type", "round-trip", "Trip type: round-trip, one-way")
	cmd.Flags().StringVar(&stops, "stops", "any", "Stops filter: any, nonstop")
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table, json")
	cmd.Flags().StringVar(&targetCurrency, "currency", "", "Convert prices to this currency (e.g. EUR, USD). Empty = show API default")
	cmd.Flags().BoolVar(&noGeo, "no-geo", false, "Disable geo-IP origin detection (also via TRVL_NO_GEO=1)")

	cmd.ValidArgsFunction = airportCompletion

	return cmd
}

// printExploreTable renders explore results as an ASCII table.
// The explore API returns prices in the IP-local currency (no currency label).
// We detect the actual currency via a quick flight search, then convert if needed.
func printExploreTable(ctx context.Context, targetCurrency string, result *models.ExploreResult, origin string) error {
	if !result.Success {
		_, _ = fmt.Fprintf(os.Stderr, "Explore failed: %s\n", result.Error)
		return nil
	}

	if result.Count == 0 {
		fmt.Println("No destinations found.")
		return nil
	}

	// Detect the actual currency the API returned (same IP = same currency).
	// Use a quick flight search to the first destination to discover it.
	sourceCurrency := "EUR" // fallback
	if len(result.Destinations) > 0 {
		detected := flights.DetectSourceCurrency(ctx, origin, result.Destinations[0].AirportCode)
		if detected != "" {
			sourceCurrency = detected
		}
	}

	// Convert prices if --currency specified and differs from source.
	displayCurrency := sourceCurrency
	if targetCurrency != "" {
		if targetCurrency == sourceCurrency {
			displayCurrency = targetCurrency
		} else {
			pricePointers := make([]*float64, 0, len(result.Destinations))
			for i := range result.Destinations {
				if result.Destinations[i].Price > 0 {
					pricePointers = append(pricePointers, &result.Destinations[i].Price)
				}
			}
			displayCurrency = convertRoundedDisplayAmounts(ctx, sourceCurrency, targetCurrency, 0, pricePointers...)
		}
	}

	fmt.Printf("Found %d destinations from %s\n\n", result.Count, origin)

	headers := []string{"Destination", "Airport", "Price", "Airline", "Stops"}
	var rows [][]string

	for _, d := range result.Destinations {
		dest := d.CityName
		if dest == "" {
			dest = d.AirportCode
		}
		if d.Country != "" {
			dest += ", " + d.Country
		}

		rows = append(rows, []string{
			dest,
			d.AirportCode,
			fmt.Sprintf("%s %.0f", displayCurrency, d.Price),
			d.AirlineName,
			formatStops(d.Stops),
		})
	}

	models.FormatTable(os.Stdout, headers, rows)
	return nil
}
