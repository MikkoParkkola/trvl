package main

import (
	"context"
	"fmt"
	"time"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/spf13/cobra"
)

var (
	restaurantQuery string
	restaurantLimit int
)

var restaurantsCmd = &cobra.Command{
	Use:   "restaurants LAT LON",
	Short: "Search restaurants and places nearby using Google Maps",
	Long:  "Search Google Maps for restaurants and places near the given coordinates. Works without any API key.",
	Args:  cobra.ArbitraryArgs,
	RunE:  runRestaurants,
}

func init() {
	restaurantsCmd.Flags().StringVar(&restaurantQuery, "query", "restaurants", "Search query (e.g., breakfast, sushi, cafes)")
	restaurantsCmd.Flags().IntVar(&restaurantLimit, "limit", 10, "Maximum number of results (1-20)")
}

func runRestaurants(cmd *cobra.Command, args []string) error {
	lat, lon, err := parseLatLonArgs(args)
	if err != nil {
		return err
	}

	if lat < -90 || lat > 90 {
		return fmt.Errorf("latitude must be between -90 and 90, got %f", lat)
	}
	if lon < -180 || lon > 180 {
		return fmt.Errorf("longitude must be between -180 and 180, got %f", lon)
	}

	if restaurantLimit < 1 {
		restaurantLimit = 1
	}
	if restaurantLimit > 20 {
		restaurantLimit = 20
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	places, err := destinations.SearchGoogleMapsPlaces(ctx, lat, lon, restaurantQuery, restaurantLimit)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	if len(places) == 0 {
		cmd.Println("No places found.")
		return nil
	}

	cmd.Printf("Found %d places for %q near %.4f, %.4f:\n\n", len(places), restaurantQuery, lat, lon)
	for i, p := range places {
		cmd.Printf("  %d. %s", i+1, p.Name)
		if p.Rating > 0 {
			cmd.Printf(" (%.1f/5)", p.Rating)
		}
		if p.Category != "" {
			cmd.Printf(" - %s", p.Category)
		}
		if p.Distance > 0 {
			cmd.Printf(" [%dm]", int(p.Distance))
		}
		cmd.Println()
		if p.Address != "" {
			cmd.Printf("     %s\n", p.Address)
		}
	}

	return nil
}
