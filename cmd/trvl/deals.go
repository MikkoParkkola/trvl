package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/deals"
	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

func dealsCmd() *cobra.Command {
	var (
		from     string
		maxPrice float64
		dealType string
		hours    int
		currency string
	)

	cmd := &cobra.Command{
		Use:   "deals",
		Short: "Aggregate travel deals from free RSS feeds",
		Long: `Search travel deals from Secret Flying, Fly4Free, Holiday Pirates, and The Points Guy.

Fetches the latest deals from 4 free RSS feeds in parallel. No API key required.

Examples:
  trvl deals                              # all recent deals
  trvl deals --from HEL,AMS              # deals from Helsinki or Amsterdam
  trvl deals --from HEL --max-price 200  # under 200 from Helsinki
  trvl deals --type error_fare           # error fares only
  trvl deals --hours 24                  # last 24 hours only`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filter := deals.DealFilter{
				MaxPrice: maxPrice,
				Type:     dealType,
				HoursAgo: hours,
			}
			if from != "" {
				filter.Origins = strings.Split(from, ",")
			}

			result, err := deals.FetchDeals(cmd.Context(), nil, filter)
			if err != nil {
				return err
			}

			if format == "json" {
				return models.FormatJSON(os.Stdout, result)
			}

			return printDealsTable(cmd.Context(), currency, result)
		},
	}

	cmd.Flags().StringVar(&from, "from", "", "Filter by origin airports/cities (comma-separated, e.g. HEL,AMS)")
	cmd.Flags().Float64Var(&maxPrice, "max-price", 0, "Maximum price filter (0 = no limit)")
	cmd.Flags().StringVar(&dealType, "type", "", "Filter by deal type: error_fare, deal, flash_sale, package")
	cmd.Flags().IntVar(&hours, "hours", 48, "Only show deals from last N hours")
	cmd.Flags().StringVar(&currency, "currency", "", "Convert prices to this currency (e.g. EUR, USD). Empty = show as-is")

	return cmd
}

func printDealsTable(ctx context.Context, targetCurrency string, result *deals.DealsResult) error {
	if !result.Success {
		if result.Error != "" {
			_, _ = fmt.Fprintf(os.Stderr, "No deals found: %s\n", result.Error)
		} else {
			_, _ = fmt.Fprintln(os.Stderr, "No deals found.")
		}
		return nil
	}

	// Convert prices if --currency specified.
	if targetCurrency != "" {
		for i := range result.Deals {
			d := &result.Deals[i]
			if d.Currency != targetCurrency && d.Price > 0 {
				converted, cur := destinations.ConvertCurrency(ctx, d.Price, d.Currency, targetCurrency)
				d.Price = math.Round(converted)
				d.Currency = cur
			}
		}
	}

	// Count unique sources.
	sources := map[string]bool{}
	for _, d := range result.Deals {
		sources[d.Source] = true
	}

	models.Banner(os.Stdout, "\U0001F525", "Travel Deals",
		fmt.Sprintf("%d deals from %d sources", result.Count, len(sources)))
	fmt.Println()

	headers := []string{"Price", "Route", "Type", "Source", "Published", "Title"}
	var rows [][]string

	for _, d := range result.Deals {
		price := "-"
		if d.Price > 0 {
			price = fmt.Sprintf("%s %.0f", d.Currency, d.Price)
		}

		route := "-"
		if d.Origin != "" && d.Destination != "" {
			route = fmt.Sprintf("%s -> %s", d.Origin, d.Destination)
		} else if d.Destination != "" {
			route = "-> " + d.Destination
		} else if d.Origin != "" {
			route = d.Origin + " -> ?"
		}

		published := "-"
		if !d.Published.IsZero() {
			published = formatDealAge(d.Published)
		}

		title := d.Title
		titleRunes := []rune(title)
		if len(titleRunes) > 60 {
			title = string(titleRunes[:57]) + "..."
		}

		sourceName := d.Source
		if name, ok := deals.SourceNames[d.Source]; ok {
			sourceName = name
		}

		typeColor := d.Type
		switch d.Type {
		case "error_fare":
			typeColor = models.Red(d.Type)
		case "flash_sale":
			typeColor = models.Yellow(d.Type)
		}

		rows = append(rows, []string{
			models.Green(price),
			route,
			typeColor,
			sourceName,
			published,
			title,
		})
	}

	models.FormatTable(os.Stdout, headers, rows)
	return nil
}

func formatDealAge(t time.Time) string {
	ago := time.Since(t)
	switch {
	case ago < time.Hour:
		return fmt.Sprintf("%dm ago", int(ago.Minutes()))
	case ago < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(ago.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(ago.Hours()/24))
	}
}
