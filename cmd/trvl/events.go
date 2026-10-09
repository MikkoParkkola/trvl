package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

func eventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events <location>",
		Short: "Find local events during travel dates",
		Long: `Find concerts, sports, arts, and other events at a destination.
Requires TICKETMASTER_API_KEY environment variable (free at developer.ticketmaster.com).

Examples:
  trvl events "Barcelona" --from 2026-07-01 --to 2026-07-08
  trvl events "New York" --from 2026-12-20 --to 2026-12-31 --format json`,
		Args: cobra.ExactArgs(1),
		RunE: runEvents,
	}

	cmd.Flags().String("from", "", "Start date (YYYY-MM-DD, required)")
	cmd.Flags().String("to", "", "End date (YYYY-MM-DD, required)")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")

	return cmd
}

func runEvents(cmd *cobra.Command, args []string) error {
	location := args[0]
	fromDate, _ := cmd.Flags().GetString("from")
	toDate, _ := cmd.Flags().GetString("to")
	format, _ := cmd.Flags().GetString("format")

	if os.Getenv("TICKETMASTER_API_KEY") == "" {
		return fmt.Errorf("TICKETMASTER_API_KEY is required for event search (free at https://developer.ticketmaster.com)")
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
	defer cancel()

	events, err := destinations.GetEvents(ctx, location, fromDate, toDate)
	if err != nil {
		return fmt.Errorf("events: %w", err)
	}

	if format == "json" {
		return models.FormatJSON(os.Stdout, events)
	}

	return formatEventsCard(events, location, fromDate, toDate)
}

func formatEventsCard(events []models.Event, location, from, to string) error {
	if len(events) == 0 {
		fmt.Printf("\n  No events found in %s from %s to %s.\n\n", location, from, to)
		return nil
	}

	fmt.Printf("\n  EVENTS IN %s (%s to %s)\n", location, from, to)
	fmt.Printf("  %d events found\n\n", len(events))

	headers := []string{"Date", "Time", "Event", "Venue", "Type", "Price"}
	rows := make([][]string, 0, len(events))
	for _, e := range events {
		rows = append(rows, []string{
			e.Date,
			e.Time,
			truncate(e.Name, 35),
			truncate(e.Venue, 25),
			e.Type,
			e.PriceRange,
		})
	}

	fmt.Print("  ")
	models.FormatTable(os.Stdout, headers, rows)
	fmt.Println()

	return nil
}
