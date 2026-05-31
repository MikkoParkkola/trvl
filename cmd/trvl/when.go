package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/tripwindow"
	"github.com/spf13/cobra"
)

func whenCmd() *cobra.Command {
	var (
		origin        string
		windowStart   string
		windowEnd     string
		busyFlags     []string
		preferFlags   []string
		minNights     int
		maxNights     int
		maxCandidates int
		budgetEUR     float64
		formatOut     string
		noGeo         bool
	)

	cmd := &cobra.Command{
		Use:   "when --to DESTINATION --from YYYY-MM-DD --until YYYY-MM-DD",
		Short: "Find optimal travel windows within a date range",
		Long: `Find the best time to travel to a destination within a search window.

Enumerates candidate trip windows, filters out your busy periods, and
estimates the cheapest round-trip cost for each. Results are ranked by
price with preferred-interval windows boosted to the front.

Busy periods can be passed with --busy (one flag per interval) or are read
automatically from the calendar_blocked list in ~/.trvl/preferences.json.

Examples:
  trvl when --to PRG --from 2026-05-01 --until 2026-08-31 --min-nights 3 --max-nights 5
  trvl when --to PRG --from 2026-05-01 --until 2026-08-31 \
      --busy 2026-05-12:2026-05-16 --busy 2026-06-20:2026-06-30 --budget 500
  trvl when --to BCN --from 2026-07-01 --until 2026-09-30 --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve destination from --to flag (cobra sets it via PersistentPreRunE
			// of the root; here it's the first positional arg or --to).
			dest, _ := cmd.Flags().GetString("to")
			if dest == "" {
				return fmt.Errorf("--to DESTINATION is required")
			}
			if windowStart == "" {
				return fmt.Errorf("--from YYYY-MM-DD is required")
			}
			if windowEnd == "" {
				return fmt.Errorf("--until YYYY-MM-DD is required")
			}

			// Resolve origin: explicit --origin flag, else saved home airport,
			// else best-effort geo-IP location (disable with --no-geo).
			resolved, err := resolveCLIOrigin(cmd.Context(), origin, formatOut, noGeo)
			if err != nil {
				return fmt.Errorf("--origin is required (or set home_airports in ~/.trvl/preferences.json): %w", err)
			}
			origin = resolved

			// Validate origin IATA.
			if err := models.ValidateIATA(origin); err != nil {
				return fmt.Errorf("invalid origin: %w", err)
			}

			// Parse --busy flags.
			var busy []tripwindow.Interval
			for _, b := range busyFlags {
				iv, err := tripwindow.ParseBusyFlag(b)
				if err != nil {
					return fmt.Errorf("--busy %q: %w", b, err)
				}
				busy = append(busy, iv)
			}

			// Parse --prefer flags.
			var preferred []tripwindow.Interval
			for _, p := range preferFlags {
				iv, err := tripwindow.ParseBusyFlag(p)
				if err != nil {
					return fmt.Errorf("--prefer %q: %w", p, err)
				}
				preferred = append(preferred, iv)
			}

			in := tripwindow.Input{
				Origin:             origin,
				Destination:        dest,
				WindowStart:        windowStart,
				WindowEnd:          windowEnd,
				BusyIntervals:      busy,
				PreferredIntervals: preferred,
				MinNights:          minNights,
				MaxNights:          maxNights,
				MaxCandidates:      maxCandidates,
				BudgetEUR:          budgetEUR,
			}

			if err := tripwindow.ValidateInput(in); err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 90*time.Second)
			defer cancel()

			candidates, err := tripwindow.Find(ctx, in)
			if err != nil {
				return err
			}

			// Cache best result for `trvl share --last`.
			if len(candidates) > 0 {
				best := candidates[0]
				saveLastSearch(&LastSearch{
					Command:        "when",
					Origin:         origin,
					Destination:    dest,
					DepartDate:     best.Start,
					ReturnDate:     best.End,
					Nights:         best.Nights,
					FlightPrice:    best.FlightCost,
					FlightCurrency: best.Currency,
					HotelPrice:     best.HotelCost,
					HotelCurrency:  best.Currency,
					HotelName:      best.HotelName,
					TotalPrice:     best.EstimatedCost,
					TotalCurrency:  best.Currency,
				})
			}

			if formatOut == "json" {
				return models.FormatJSON(os.Stdout, map[string]any{
					"candidates":  candidates,
					"count":       len(candidates),
					"origin":      origin,
					"destination": dest,
				})
			}

			return printWhenTable(candidates, origin, dest)
		},
	}

	cmd.Flags().String("to", "", "Destination city or IATA code (required)")
	_ = cmd.MarkFlagRequired("to")
	cmd.Flags().StringVar(&origin, "origin", "", "Origin IATA code (default: first home_airport from preferences)")
	cmd.Flags().BoolVar(&noGeo, "no-geo", false, "Disable geo-IP origin detection (also via TRVL_NO_GEO=1)")
	cmd.Flags().StringVar(&windowStart, "from", "", "Earliest departure date YYYY-MM-DD (required)")
	_ = cmd.MarkFlagRequired("from")
	cmd.Flags().StringVar(&windowEnd, "until", "", "Latest return date YYYY-MM-DD (required)")
	_ = cmd.MarkFlagRequired("until")
	cmd.Flags().StringArrayVar(&busyFlags, "busy", nil, "Busy interval to avoid: YYYY-MM-DD:YYYY-MM-DD (repeatable)")
	cmd.Flags().StringArrayVar(&preferFlags, "prefer", nil, "Preferred interval: YYYY-MM-DD:YYYY-MM-DD (repeatable)")
	cmd.Flags().IntVar(&minNights, "min-nights", 3, "Minimum trip length in nights")
	cmd.Flags().IntVar(&maxNights, "max-nights", 7, "Maximum trip length in nights")
	cmd.Flags().IntVar(&maxCandidates, "top", 5, "Number of windows to show")
	cmd.Flags().Float64Var(&budgetEUR, "budget", 0, "Maximum estimated cost in EUR (0 = no limit)")
	cmd.Flags().StringVar(&formatOut, "format", "table", "Output format: table, json")

	return cmd
}

func printWhenTable(candidates []tripwindow.Candidate, origin, dest string) error {
	if len(candidates) == 0 {
		fmt.Printf("No open travel windows found from %s to %s.\n", origin, dest)
		return nil
	}

	fmt.Printf("Travel windows from %s to %s\n\n", origin, dest)

	headers := []string{"#", "Departure", "Return", "Nights", "Flight", "Hotel", "Total", "Notes"}
	var rows [][]string
	for i, c := range candidates {
		flight := "—"
		if c.FlightCost > 0 {
			flight = fmt.Sprintf("%s %.0f", c.Currency, c.FlightCost)
		}
		hotel := "—"
		if c.HotelCost > 0 {
			hotel = fmt.Sprintf("%s %.0f", c.Currency, c.HotelCost)
		}
		total := "—"
		if c.EstimatedCost > 0 {
			total = fmt.Sprintf("%s %.0f", c.Currency, c.EstimatedCost)
		}
		notes := ""
		if c.OverlapsPreferred {
			notes = "preferred"
		}
		if c.HotelName != "" {
			if notes != "" {
				notes += "; "
			}
			notes += c.HotelName
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", i+1),
			c.Start,
			c.End,
			fmt.Sprintf("%d", c.Nights),
			flight,
			hotel,
			total,
			notes,
		})
	}

	models.FormatTable(os.Stdout, headers, rows)
	return nil
}
