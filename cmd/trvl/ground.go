package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

func groundCmd() *cobra.Command {
	var (
		currency              string
		providers             string
		maxPrice              float64
		typeFilter            string
		allowBrowserFallbacks bool
	)

	cmd := &cobra.Command{
		Use:     "ground FROM TO DATE",
		Aliases: []string{"bus", "train"},
		Short:   "Search ground routes across API-first bus, train, and ferry providers",
		Long: `Search ground transport between two cities.

Uses API-first providers such as FlixBus, RegioJet, SNCF, Trainline, OBB,
DB, NS, VR, Tallink, DFDS, Viking Line, Eckerö Line, and Transitous.
Browser/curl-assisted fallbacks stay disabled unless you opt in.

FROM and TO are city names (e.g. "Prague", "Vienna", "Helsinki").
DATE is the departure date in YYYY-MM-DD format.

Examples:
  trvl ground Prague Vienna 2026-07-01
  trvl bus "Prague" "Krakow" 2026-07-01
  trvl train Prague Vienna 2026-07-01 --type train
  trvl ground Helsinki Tampere 2026-07-01 --provider flixbus
  trvl ground Prague Budapest 2026-07-01 --max-price 30`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			from := args[0]
			to := args[1]
			date := args[2]

			opts := ground.SearchOptions{
				Currency:              currency,
				MaxPrice:              maxPrice,
				Type:                  typeFilter,
				NoCache:               noCache,
				AllowBrowserFallbacks: allowBrowserFallbacks,
			}
			if providers != "" {
				opts.Providers = strings.Split(providers, ",")
			}

			result, err := ground.SearchByName(cmd.Context(), from, to, date, opts)
			if err != nil {
				return err
			}

			// Cache best result for `trvl share --last`.
			if result != nil && result.Success && len(result.Routes) > 0 {
				r := result.Routes[0]
				saveLastSearch(&LastSearch{
					Command:        "ground",
					Origin:         from,
					Destination:    to,
					DepartDate:     date,
					FlightPrice:    r.Price,
					FlightCurrency: r.Currency,
					FlightAirline:  r.Provider,
					TotalPrice:     r.Price,
					TotalCurrency:  r.Currency,
				})
			}

			if format == "json" {
				return models.FormatJSON(os.Stdout, result)
			}

			if err := printGroundTable(cmd.Context(), currency, result); err != nil {
				return err
			}

			if openFlag && result.Success && len(result.Routes) > 0 && result.Routes[0].BookingURL != "" {
				_ = openBrowser(result.Routes[0].BookingURL)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&currency, "currency", "", "Convert prices to this currency (e.g. EUR). Empty = provider default")
	cmd.Flags().StringVar(&providers, "provider", "", "Restrict to providers (e.g. flixbus,regiojet,trainline,sncf,transitous)")
	cmd.Flags().Float64Var(&maxPrice, "max-price", 0, "Maximum price filter")
	cmd.Flags().StringVar(&typeFilter, "type", "", "Filter by type (bus, train, ferry)")
	cmd.Flags().BoolVar(&allowBrowserFallbacks, "allow-browser-fallbacks", false, "Allow browser/curl/cookie-assisted provider fallbacks")

	return cmd
}

func printGroundTable(ctx context.Context, targetCurrency string, result *models.GroundSearchResult) error {
	if !result.Success {
		if result.Error != "" {
			_, _ = fmt.Fprintf(os.Stderr, "No routes found: %s\n", result.Error)
		} else {
			_, _ = fmt.Fprintln(os.Stderr, "No routes found.")
		}
		return nil
	}

	providerCount, provList, rows := prepareGroundRows(ctx, targetCurrency, result.Routes)
	models.Banner(os.Stdout, "🚂", fmt.Sprintf("Ground Transport · %d providers", providerCount),
		fmt.Sprintf("Found %d routes (%s)", result.Count, strings.Join(provList, ", ")))
	fmt.Println()

	headers := []string{"Price", "Duration", "Type", "Provider", "Transfers", "Departs", "Arrives", "Seats"}
	models.FormatTable(os.Stdout, headers, rows)
	return nil
}

func prepareGroundRows(ctx context.Context, targetCurrency string, routes []models.GroundRoute) (int, []string, [][]string) {
	if targetCurrency != "" {
		for i := range routes {
			r := &routes[i]
			if r.Currency != targetCurrency && r.Price > 0 {
				r.Currency = convertRoundedDisplayAmounts(
					ctx,
					r.Currency,
					targetCurrency,
					2,
					&r.Price,
					&r.PriceMax,
				)
			}
		}
	}

	providers := map[string]bool{}
	for _, r := range routes {
		providers[r.Provider] = true
	}
	provList := make([]string, 0, len(providers))
	for p := range providers {
		provList = append(provList, p)
	}

	rows := make([][]string, 0, len(routes))
	for _, r := range routes {
		price := "-"
		if r.Price > 0 {
			price = fmt.Sprintf("%s %.2f", r.Currency, r.Price)
			if r.PriceMax > 0 && r.PriceMax != r.Price {
				price = fmt.Sprintf("%s %.2f-%.2f", r.Currency, r.Price, r.PriceMax)
			}
		}

		dur := formatDuration(r.Duration)
		transfers := "Direct"
		if r.Transfers > 0 {
			transfers = fmt.Sprintf("%d", r.Transfers)
		}

		depTime := formatGroundTime(r.Departure.Time)
		arrTime := formatGroundTime(r.Arrival.Time)

		seats := "-"
		if r.SeatsLeft != nil {
			seats = fmt.Sprintf("%d", *r.SeatsLeft)
			if *r.SeatsLeft <= 5 {
				seats = models.Red(seats + "!")
			}
		}

		rows = append(rows, []string{
			models.Green(price),
			dur,
			r.Type,
			r.Provider,
			transfers,
			depTime,
			arrTime,
			seats,
		})
	}

	return len(providers), provList, rows
}

func formatGroundTime(isoTime string) string {
	// Extract just the time portion from ISO 8601
	if len(isoTime) >= 16 {
		return isoTime[11:16] // HH:MM
	}
	return isoTime
}
