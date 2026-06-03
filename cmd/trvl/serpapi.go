package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/serpapi"
	"github.com/spf13/cobra"
)

func serpapiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serpapi <location>",
		Short: "Search hotels via SerpAPI (accurate prices with provider breakdown)",
		Long: `Search hotels using SerpAPI (google_hotels engine).

SerpAPI is a third-party service that scrapes Google Hotels and returns structured
JSON with real prices from multiple booking providers (Booking.com, Expedia, Trivago, etc.).

WHY USE IT:
  The standard 'trvl hotels' command scrapes Google Hotels directly and may return
  inaccurate or partial prices. SerpAPI handles the anti-bot protection and returns
  verified prices with per-night AND total cost for your exact dates.

SETUP:
  1. Sign up for a free account at https://serpapi.com (250 searches/month, no credit card)
  2. Copy your API key from the dashboard
  3. Export it: export SERPAPI_KEY=your_key_here
  4. Or add to ~/.zshrc: echo 'export SERPAPI_KEY=your_key_here' >> ~/.zshrc

DIFFERENCES FROM 'trvl hotels':
  - trvl hotels:  free, no API key, may show estimated prices
  - trvl serpapi: requires free API key, shows real provider prices with totals

Examples:
  trvl serpapi "Naoussa, Paros" --checkin 2026-08-03 --checkout 2026-08-10 --currency EUR
  trvl serpapi "Rhodes Greece" --checkin 2026-08-05 --checkout 2026-08-12 --format json`,
		Args: cobra.ExactArgs(1),
		RunE: runSerpapi,
	}

	cmd.Flags().String("checkin", "", "Check-in date (YYYY-MM-DD, required)")
	cmd.Flags().String("checkout", "", "Check-out date (YYYY-MM-DD, required)")
	cmd.Flags().String("currency", "EUR", "Currency code (EUR, USD, etc.)")
	cmd.Flags().String("format", "", "Output format: json or table")

	_ = cmd.MarkFlagRequired("checkin")
	_ = cmd.MarkFlagRequired("checkout")

	return cmd
}

func runSerpapi(cmd *cobra.Command, args []string) error {
	location := args[0]
	checkIn, _ := cmd.Flags().GetString("checkin")
	checkOut, _ := cmd.Flags().GetString("checkout")
	currency, _ := cmd.Flags().GetString("currency")
	format, _ := cmd.Flags().GetString("format")

	if serpapi.APIKey() == "" {
		return fmt.Errorf("SERPAPI_KEY environment variable not set.\nGet a free key at https://serpapi.com (250 searches/month free)\nThen: export SERPAPI_KEY=your_key_here")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := serpapi.SearchHotels(ctx, location, checkIn, checkOut, currency)
	if err != nil {
		return fmt.Errorf("serpapi search: %w", err)
	}

	allHotels := append(result.Properties, result.Ads...)
	if len(allHotels) == 0 {
		fmt.Println("No hotels found.")
		return nil
	}

	if format == "json" {
		return models.FormatJSON(os.Stdout, result)
	}

	// Table output
	models.Banner(os.Stdout, "🏨", "Hotels", fmt.Sprintf("%s · %s to %s", location, checkIn, checkOut))
	fmt.Println()

	headers := []string{"Hotel", "Class", "Rating", "€/nt", "Totale", "Provider"}
	rows := make([][]string, 0, len(allHotels))
	for _, h := range allHotels {
		if h.Name == "" {
			continue
		}
		class := ""
		if h.HotelClass > 0 {
			class = fmt.Sprintf("%d★", h.HotelClass)
		}
		rating := ""
		if h.Rating > 0 {
			rating = fmt.Sprintf("%.1f⭐", h.Rating)
		}
		pn := ""
		if h.PricePerNight() > 0 {
			pn = fmt.Sprintf("%.0f", h.PricePerNight())
		}
		total := ""
		if h.TotalPrice() > 0 {
			total = fmt.Sprintf("%.0f", h.TotalPrice())
		}
		providers := ""
		for i, p := range h.Prices {
			if i > 2 {
				providers += "..."
				break
			}
			if providers != "" {
				providers += ", "
			}
			providers += fmt.Sprintf("%s: %.0f", p.Source, p.RatePerNight.Extracted)
		}
		if providers == "" {
			providers = "—"
		}
		rows = append(rows, []string{h.Name, class, rating, pn, total, providers})
	}

	models.FormatTable(os.Stdout, headers, rows)

	if len(result.Properties) > 0 {
		cheapest := result.Properties[0]
		for _, h := range result.Properties[1:] {
			if h.TotalPrice() > 0 && (cheapest.TotalPrice() == 0 || h.TotalPrice() < cheapest.TotalPrice()) {
				cheapest = h
			}
		}
		if cheapest.TotalPrice() > 0 {
			models.Summary(os.Stdout, fmt.Sprintf("Cheapest: %s — %.0f %s total (%.0f/nt)", cheapest.Name, cheapest.TotalPrice(), currency, cheapest.PricePerNight()))
		}
	}

	return nil
}
