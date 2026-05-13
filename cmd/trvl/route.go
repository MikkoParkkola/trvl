package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/route"
	"github.com/spf13/cobra"
)

func routeCmd() *cobra.Command {
	var (
		departAfter           string
		arriveBy              string
		maxTransfers          int
		maxPrice              float64
		currency              string
		prefer                string
		avoid                 string
		sortBy                string
		allowBrowserFallbacks bool
	)

	cmd := &cobra.Command{
		Use:   "route ORIGIN DESTINATION [DATE]",
		Short: "Multi-modal routing — flights + trains + buses + ferries",
		Long: `Find optimal multi-modal itineraries combining flights, trains, buses,
and ferries. Searches hub cities across Europe for transfer connections.

ORIGIN and DESTINATION are city names or IATA codes.
DATE is the travel date (YYYY-MM-DD); defaults to tomorrow.

Examples:
  trvl route Helsinki Dubrovnik 2026-04-10
  trvl route HEL DBV 2026-04-10
  trvl route Helsinki Tallinn 2026-04-10 --prefer ferry
  trvl route HEL BCN 2026-07-01 --avoid bus --sort duration`,
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			origin := args[0]
			dest := args[1]
			date := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
			if len(args) >= 3 {
				date = args[2]
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 90*time.Second)
			defer cancel()

			// Apply display currency from preferences when --currency not specified.
			if currency == "" {
				if prefs, err := preferences.Load(); err == nil {
					currency = prefs.DisplayCurrency
				}
			}

			opts := route.Options{
				DepartAfter:           departAfter,
				ArriveBy:              arriveBy,
				MaxTransfers:          maxTransfers,
				MaxPrice:              maxPrice,
				Currency:              currency,
				Prefer:                prefer,
				Avoid:                 avoid,
				SortBy:                sortBy,
				AllowBrowserFallbacks: allowBrowserFallbacks,
			}

			result, err := route.SearchRoute(ctx, origin, dest, date, opts)
			if err != nil {
				return err
			}

			if format == "json" {
				return models.FormatJSON(os.Stdout, result)
			}

			if err := printRouteTable(ctx, currency, result); err != nil {
				return err
			}

			if openFlag && result.Success && len(result.Itineraries) > 0 {
				for _, leg := range result.Itineraries[0].Legs {
					if leg.BookingURL != "" {
						_ = openBrowser(leg.BookingURL)
						break
					}
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&departAfter, "depart-after", "", "Depart after this time (HH:MM or ISO 8601)")
	cmd.Flags().StringVar(&arriveBy, "arrive-by", "", "Arrive by this time (HH:MM or ISO 8601)")
	cmd.Flags().IntVar(&maxTransfers, "max-transfers", 3, "Maximum mode changes")
	cmd.Flags().Float64Var(&maxPrice, "max-price", 0, "Maximum total price (0 = no limit)")
	cmd.Flags().StringVar(&currency, "currency", "", "Display currency (default: EUR)")
	cmd.Flags().StringVar(&prefer, "prefer", "", "Preferred mode: train, bus, ferry, flight")
	cmd.Flags().StringVar(&avoid, "avoid", "", "Avoid mode: flight, bus, train, ferry")
	cmd.Flags().StringVar(&sortBy, "sort", "price", "Sort by: price, duration, transfers")
	cmd.Flags().BoolVar(&allowBrowserFallbacks, "allow-browser-fallbacks", false, "Allow browser/curl/cookie-assisted ground-provider fallbacks")

	return cmd
}

func printRouteTable(_ context.Context, targetCurrency string, result *models.RouteSearchResult) error {
	if !result.Success {
		if result.Error != "" {
			_, _ = fmt.Fprintf(os.Stderr, "No routes found: %s\n", result.Error)
		} else {
			_, _ = fmt.Fprintln(os.Stderr, "No routes found.")
		}
		return nil
	}

	models.Banner(os.Stdout, "🗺️", fmt.Sprintf("Route · %s → %s", result.Origin, result.Destination),
		fmt.Sprintf("Found %d itineraries for %s", result.Count, result.Date))
	fmt.Println()

	for i, it := range result.Itineraries {
		printItinerary(i+1, it, targetCurrency)
		if i < len(result.Itineraries)-1 {
			fmt.Println()
		}
	}
	return nil
}

func printItinerary(num int, it models.RouteItinerary, targetCurrency string) {
	priceStr := formatRoutePrice(it.TotalPrice, it.Currency)
	durStr := formatDuration(it.TotalDuration)
	transferStr := "direct"
	if it.Transfers > 0 {
		transferStr = fmt.Sprintf("%d transfer", it.Transfers)
		if it.Transfers > 1 {
			transferStr += "s"
		}
	}

	fmt.Printf("  %s  %s · %s · %s\n",
		models.Bold(fmt.Sprintf("Option %d:", num)),
		models.Green(priceStr),
		durStr,
		transferStr,
	)
	fmt.Println()

	headers := []string{"Mode", "Route", "Provider", "Depart", "Arrive", "Duration", "Price"}
	var rows [][]string
	for _, leg := range it.Legs {
		legRoute := fmt.Sprintf("%s → %s", leg.From, leg.To)
		if leg.FromCode != "" && leg.ToCode != "" {
			legRoute = fmt.Sprintf("%s → %s", leg.FromCode, leg.ToCode)
		}

		price := "-"
		if leg.Price > 0 {
			price = fmt.Sprintf("%s %.0f", leg.Currency, leg.Price)
		}

		_ = targetCurrency // currency conversion per leg handled by caller

		depTime := formatGroundTime(leg.Departure)
		arrTime := formatGroundTime(leg.Arrival)

		rows = append(rows, []string{
			modeIcon(leg.Mode),
			legRoute,
			leg.Provider,
			depTime,
			arrTime,
			formatDuration(leg.Duration),
			models.Green(price),
		})
	}
	models.FormatTable(os.Stdout, headers, rows)
}

func modeIcon(mode string) string {
	switch strings.ToLower(mode) {
	case "flight":
		return "✈️  flight"
	case "train":
		return "🚂 train"
	case "bus":
		return "🚌 bus"
	case "ferry":
		return "⛴️  ferry"
	default:
		return mode
	}
}

func formatRoutePrice(price float64, currency string) string {
	if price <= 0 {
		return "-"
	}
	rounded := math.Round(price)
	return fmt.Sprintf("%s %.0f", currency, rounded)
}
