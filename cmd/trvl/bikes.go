package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/MikkoParkkola/trvl/internal/mobility"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

func bikesCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "bikes CITY",
		Short: "Find bike-share stations near a city (free, no API key)",
		Long: `Find the nearest bike-share network and its live station availability for any
city using CityBikes (free, no API key). Shows free bikes and empty docks.

Examples:
  trvl bikes "Amsterdam"
  trvl bikes "Helsinki" --limit 15 --format json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 25*time.Second)
			defer cancel()

			res, err := mobility.FindBikes(ctx, args[0], limit)
			if err != nil {
				return err
			}
			f, _ := cmd.Flags().GetString("format")
			if f == "json" {
				return models.FormatJSON(os.Stdout, res)
			}
			if !res.Success {
				_, _ = fmt.Fprintf(os.Stderr, "Bike-share lookup failed: %s\n", res.Error)
				return nil
			}
			models.Banner(os.Stdout, "🚲", fmt.Sprintf("Bike-share · %s", res.Network.Name),
				fmt.Sprintf("network in %s, %.1f km from search point", res.Network.City, res.DistanceKm))
			fmt.Println()
			headers := []string{"Station", "Free bikes", "Empty docks"}
			rows := make([][]string, 0, len(res.Stations))
			for _, s := range res.Stations {
				rows = append(rows, []string{s.Name, fmt.Sprintf("%d", s.FreeBikes), fmt.Sprintf("%d", s.EmptySlots)})
			}
			models.FormatTable(os.Stdout, headers, rows)
			fmt.Println()
			fmt.Println("  " + res.Attribution)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Max stations to show (nearest first)")
	return cmd
}
