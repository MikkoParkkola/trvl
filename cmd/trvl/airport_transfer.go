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

func airportTransferCmd() *cobra.Command {
	var (
		currency     string
		providers    string
		maxPrice     float64
		typeFilter   string
		arrivalAfter string
	)

	cmd := &cobra.Command{
		Use:   "airport-transfer AIRPORT_CODE DESTINATION DATE",
		Short: "Search airport-to-hotel or airport-to-city ground transport",
		Long: `Search airport transfer options from an arrival airport to a hotel,
district, or city destination.

trvl combines exact airport-to-destination transit routing, taxi fare estimates,
and broader airport-city ground providers, then lists the precise airport
matches first.

Examples:
  trvl airport-transfer CDG "Hotel Lutetia Paris" 2026-07-01
  trvl airport-transfer LHR "Paddington Station" 2026-07-01 --arrival-after 14:30
  trvl airport-transfer FCO "Rome Termini" 2026-07-01 --provider transitous
  trvl airport-transfer CDG "Hotel Lutetia Paris" 2026-07-01 --provider taxi
  trvl airport-transfer AMS "Amsterdam Centraal" 2026-07-01 --max-price 25`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 45*time.Second)
			defer cancel()

			input := trip.AirportTransferInput{
				AirportCode: args[0],
				Destination: args[1],
				Date:        args[2],
				ArrivalTime: arrivalAfter,
				Currency:    currency,
				MaxPrice:    maxPrice,
				Type:        typeFilter,
				NoCache:     noCache,
			}
			if providers != "" {
				input.Providers = strings.Split(providers, ",")
			}

			result, err := trip.SearchAirportTransfers(ctx, input)
			if err != nil {
				return err
			}

			if format == "json" {
				return models.FormatJSON(os.Stdout, result)
			}

			return printAirportTransferTable(cmd.Context(), currency, result)
		},
	}

	cmd.Flags().StringVar(&currency, "currency", "", "Convert prices to this currency (e.g. EUR). Empty = provider default")
	cmd.Flags().StringVar(&providers, "provider", "", "Restrict to providers (transitous,taxi,flixbus,regiojet,eurostar,db,sncf,trainline)")
	cmd.Flags().Float64Var(&maxPrice, "max-price", 0, "Maximum price filter")
	cmd.Flags().StringVar(&typeFilter, "type", "", "Filter by type (bus, train, taxi, tram, metro, mixed)")
	cmd.Flags().StringVar(&arrivalAfter, "arrival-after", "", "Only include routes departing at or after this local time (HH:MM)")
	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return airportCompletion(cmd, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func printAirportTransferTable(ctx context.Context, targetCurrency string, result *trip.AirportTransferResult) error {
	if !result.Success {
		if result.Error != "" {
			_, _ = fmt.Fprintf(os.Stderr, "No airport transfer routes found: %s\n", result.Error)
		} else {
			_, _ = fmt.Fprintln(os.Stderr, "No airport transfer routes found.")
		}
		return nil
	}

	lines := []string{fmt.Sprintf("%s -> %s (%s)", result.Airport, result.Destination, result.Date)}
	if result.ArrivalTime != "" {
		lines = append(lines, fmt.Sprintf("After %s", result.ArrivalTime))
	}
	matchSummary := fmt.Sprintf("%d exact airport match(es)", result.ExactMatches)
	if result.CityMatches > 0 {
		matchSummary += fmt.Sprintf(", %d broader city match(es)", result.CityMatches)
	}
	lines = append(lines, matchSummary)
	models.Banner(os.Stdout, "🚌", "Airport Transfer", lines...)
	fmt.Println()

	headers := []string{"Price", "Duration", "Type", "Provider", "Transfers", "Departs", "Arrives", "Seats"}
	_, _, rows := prepareGroundRows(ctx, targetCurrency, result.Routes)
	models.FormatTable(os.Stdout, headers, rows)
	if result.CityMatches > 0 {
		fmt.Println()
		_, _ = fmt.Fprintln(os.Stdout, "Note: exact airport-to-destination matches are listed first; broader airport-city options follow.")
	}
	if hasAirportTransferProvider(result.Routes, "taxi") {
		fmt.Println()
		_, _ = fmt.Fprintln(os.Stdout, "Taxi fares are estimates based on route distance and typical local tariffs.")
	}
	return nil
}

func hasAirportTransferProvider(routes []models.GroundRoute, provider string) bool {
	for _, route := range routes {
		if strings.EqualFold(route.Provider, provider) {
			return true
		}
	}
	return false
}
