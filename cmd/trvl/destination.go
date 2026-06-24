package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

func destinationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destination <location>",
		Short: "Get travel intelligence for a destination",
		Long: `Get comprehensive travel context for any city: weather forecast, country
info, public holidays, safety advisory, and currency exchange rates.

All data comes from free APIs -- no API keys required.

Examples:
  trvl destination "Tokyo" --dates 2026-06-15,2026-06-18
  trvl destination "Barcelona" --format json
  trvl destination "New York"`,
		Args: cobra.ExactArgs(1),
		RunE: runDestination,
	}

	cmd.Flags().String("dates", "", "Travel dates as YYYY-MM-DD,YYYY-MM-DD (optional)")

	return cmd
}

func runDestination(cmd *cobra.Command, args []string) error {
	location := args[0]
	datesStr, _ := cmd.Flags().GetString("dates")
	format, _ := cmd.Flags().GetString("format")

	var dates models.DateRange
	if datesStr != "" {
		parts := strings.SplitN(datesStr, ",", 2)
		if len(parts) == 2 {
			dates.CheckIn = strings.TrimSpace(parts[0])
			dates.CheckOut = strings.TrimSpace(parts[1])
		} else if len(parts) == 1 {
			dates.CheckIn = strings.TrimSpace(parts[0])
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	info, err := destinations.GetDestinationInfo(ctx, location, dates)
	if err != nil {
		return fmt.Errorf("destination info: %w", err)
	}

	if format == "json" {
		return models.FormatJSON(os.Stdout, info)
	}

	return formatDestinationCard(info)
}

// showDestinationFooter enriches the arrival location best-effort and prints a
// compact destination footer beneath search results. A blank location or a
// failed lookup prints nothing, so search commands call it unconditionally on
// their human-readable output path.
func showDestinationFooter(ctx context.Context, location string, dates models.DateRange) {
	printDestinationFooter(enrichArrival(ctx, location, dates))
}

// enrichArrival returns best-effort destination intelligence for an arrival
// location, or nil when the location is blank and when the lookup fails. It is
// the shared enricher behind both the human-readable footer and the JSON
// `destination` field, so text and JSON output stay at parity.
func enrichArrival(ctx context.Context, location string, dates models.DateRange) *models.DestinationInfo {
	return destinations.EnrichBestEffort(ctx, location, dates)
}

// printDestinationFooter renders a compact one-block destination summary
// beneath search results (hotels, flights, ground). Unlike formatDestinationCard
// — the full card behind `trvl destination` — this is a tight footer: current
// weather, safety level, holidays falling during the stay, and the currency
// rate, each printed only when present. A nil info prints nothing, so callers
// can pass best-effort enrichment straight through without guarding.
func printDestinationFooter(info *models.DestinationInfo) {
	if info == nil {
		return
	}
	var lines []string
	if w := info.Weather.Current; w.Description != "" || w.TempHigh != 0 || w.TempLow != 0 {
		lines = append(lines, fmt.Sprintf("Weather: %s, %.0f–%.0f°C", w.Description, w.TempLow, w.TempHigh))
	}
	if info.Safety.Source != "" {
		lines = append(lines, fmt.Sprintf("Safety: %.1f/5.0 — %s", info.Safety.Level, info.Safety.Advisory))
	}
	if len(info.Holidays) > 0 {
		names := make([]string, 0, len(info.Holidays))
		for _, h := range info.Holidays {
			names = append(names, fmt.Sprintf("%s (%s)", h.Name, h.Date))
		}
		lines = append(lines, "Holidays during stay: "+strings.Join(names, ", "))
	}
	if info.Currency.LocalCurrency != "" && info.Currency.ExchangeRate > 0 {
		lines = append(lines, fmt.Sprintf("Currency: 1 %s = %.2f %s",
			info.Currency.BaseCurrency, info.Currency.ExchangeRate, info.Currency.LocalCurrency))
	}
	if len(lines) == 0 {
		return
	}
	fmt.Println()
	fmt.Printf("  Destination: %s\n", info.Location)
	for _, l := range lines {
		fmt.Printf("    %s\n", l)
	}
}

func formatDestinationCard(info *models.DestinationInfo) error {
	// Header
	fmt.Printf("\n  %s\n", info.Location)
	if info.Timezone != "" {
		fmt.Printf("  Timezone: %s\n", info.Timezone)
	}
	fmt.Println()

	// Country
	if info.Country.Name != "" {
		fmt.Println("  COUNTRY")
		fmt.Printf("    %s (%s) - %s\n", info.Country.Name, info.Country.Code, info.Country.Region)
		if info.Country.Capital != "" {
			fmt.Printf("    Capital: %s\n", info.Country.Capital)
		}
		if len(info.Country.Languages) > 0 {
			fmt.Printf("    Languages: %s\n", strings.Join(info.Country.Languages, ", "))
		}
		if len(info.Country.Currencies) > 0 {
			fmt.Printf("    Currencies: %s\n", strings.Join(info.Country.Currencies, ", "))
		}
		fmt.Println()
	}

	// Weather
	if len(info.Weather.Forecast) > 0 {
		fmt.Println("  WEATHER (7-day forecast)")
		headers := []string{"Date", "High", "Low", "Rain", "Conditions"}
		rows := make([][]string, 0, len(info.Weather.Forecast))
		for _, d := range info.Weather.Forecast {
			rows = append(rows, []string{
				d.Date,
				fmt.Sprintf("%.0f C", d.TempHigh),
				fmt.Sprintf("%.0f C", d.TempLow),
				fmt.Sprintf("%.1f mm", d.Precipitation),
				d.Description,
			})
		}
		fmt.Print("  ")
		models.FormatTable(os.Stdout, headers, rows)
		fmt.Println()
	}

	// Holidays
	if len(info.Holidays) > 0 {
		fmt.Println("  HOLIDAYS (during travel)")
		for _, h := range info.Holidays {
			fmt.Printf("    %s  %s (%s)\n", h.Date, h.Name, h.Type)
		}
		fmt.Println()
	}

	// Safety
	if info.Safety.Source != "" {
		fmt.Println("  SAFETY")
		fmt.Printf("    Level: %.1f/5.0 - %s\n", info.Safety.Level, info.Safety.Advisory)
		fmt.Printf("    Source: %s (updated %s)\n", info.Safety.Source, info.Safety.LastUpdated)
		fmt.Println()
	}

	// Currency
	if info.Currency.LocalCurrency != "" {
		fmt.Println("  CURRENCY")
		fmt.Printf("    %s = %.2f %s\n", info.Currency.BaseCurrency, info.Currency.ExchangeRate, info.Currency.LocalCurrency)
		if info.Currency.ExchangeRate > 0 {
			fmt.Printf("    1 %s = %.4f %s\n", info.Currency.LocalCurrency, 1.0/info.Currency.ExchangeRate, info.Currency.BaseCurrency)
		}
		fmt.Println()
	}

	return nil
}
