// Package main -- find.go
//
// `trvl find` — the primary user-facing trip search command. Profile-driven,
// applies Mikko's mental-model pipeline with sensible defaults so a single
// `trvl find PRG` expands origin from preferences, fans out to rail+fly and
// nearby airports, filters on lounge access + no-early-connection, ranks by
// price, and returns the top bundles.
//
// Relationship to other commands:
//   - `trvl flights`: low-level, flag-rich. Use when you want precise control.
//   - `trvl search`: natural-language entry point.
//   - `trvl find`:   profile-aware, opinionated defaults, minimum typing.
//
// Back-compat: `trvl hunt` is retained as a hidden alias of `trvl find`.
//
// Reference: ~/.claude/data/travel_search_mental_model.md section "TRVL
// IMPROVEMENT PROPOSAL".
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/MikkoParkkola/trvl/internal/hunt"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

// findCmd returns the primary `trvl find` command (Mikko's orchestrated
// search). findCmdWith takes a cobra Use template so both `find` and the
// hidden `hunt` alias can share the implementation.
func findCmd() *cobra.Command { return findCmdWith("find ORIGIN DESTINATION DATE", false) }

// huntCmd returns the hidden back-compat alias `trvl hunt`. Delegates to
// findCmd's implementation so behavior stays identical.
func huntCmd() *cobra.Command { return findCmdWith("hunt ORIGIN DESTINATION DATE", true) }

func findCmdWith(use string, hidden bool) *cobra.Command {
	var (
		returnDate      string
		cabin           string
		format          string
		minLayoverStr   string
		layoverAirports []string
		noEarlyConn     bool
		loungeRequired  bool
		hiddenCity      bool
		topN            int
		calendarInsert  bool
	)

	cmd := &cobra.Command{
		Use:    use,
		Hidden: hidden,
		Short:  "Orchestrated flight search applying Mikko's mental model",
		Long: `Run Mikko's full 7-step flight search algorithm end-to-end:

1. Multi-airport origin spread (home + nearby from preferences)
2. RT via primary airline (google_flights)
3. Rail+fly origins (ZYR/ANR/BRU) when AMS involved
4. Hidden-city skip-last-leg detection (optional)
5. Post-search filters (time, lounge, no-early-connection)
6. Rank: cheapest profile-compliant first
7. Top N bundles presented

ORIGIN is typically "home" (expanded from preferences.home_airports).
DATE is ISO 8601 (2026-04-23).

Example:
  trvl find home PRG 2026-04-23 --return 2026-06-03 --no-early-connection --lounge-required`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := hunt.HuntRequest{
				Origin:            args[0],
				Destination:       args[1],
				Date:              args[2],
				ReturnDate:        returnDate,
				Cabin:             cabin,
				MinLayoverMinutes: hunt.ParseDuration(minLayoverStr),
				LayoverAirports:   layoverAirports,
				NoEarlyConnection: noEarlyConn,
				LoungeRequired:    loungeRequired,
				HiddenCity:        hiddenCity,
				TopN:              topN,
			}
			return runFind(cmd.Context(), req, format, calendarInsert)
		},
	}

	cmd.Flags().StringVar(&returnDate, "return", "", "Return date in ISO 8601 format")
	cmd.Flags().StringVar(&cabin, "cabin", "economy", "Cabin class")
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table, json")
	cmd.Flags().StringVar(&minLayoverStr, "min-layover", "", "Minimum layover duration (e.g. 12h)")
	cmd.Flags().StringSliceVar(&layoverAirports, "layover-at", nil, "Restrict layovers to these airports")
	cmd.Flags().BoolVar(&noEarlyConn, "no-early-connection", true, "After overnight layover, require next departure at or after preferences.early_connection_floor (default 10:00) — the 'unhurried wake + breakfast' rule")
	cmd.Flags().BoolVar(&noEarlyConn, "no-aamuyo", true, "Deprecated alias for --no-early-connection")
	_ = cmd.Flags().MarkHidden("no-aamuyo")
	cmd.Flags().BoolVar(&loungeRequired, "lounge-required", true, "Require lounge at transit airports (default on)")
	cmd.Flags().BoolVar(&hiddenCity, "hidden-city", false, "Also detect hidden-city candidates")
	cmd.Flags().IntVar(&topN, "top", 3, "Number of top bundles to present")
	cmd.Flags().BoolVar(&calendarInsert, "calendar", false, "Insert chosen bundle (top 1) into Google Calendar via gws CLI")

	return cmd
}

// runFind orchestrates one CLI invocation — it calls internal/hunt and
// presents the result in the requested format.
func runFind(ctx context.Context, req hunt.HuntRequest, format string, calendarInsert bool) error {
	result, err := hunt.Hunt(ctx, req, nil, nil)
	if err != nil {
		return err
	}

	if format == "json" {
		// Reassemble the classic FlightSearchResult shape so downstream
		// tooling and tests consuming the old schema keep working.
		fsr := &models.FlightSearchResult{
			Success:  true,
			TripType: result.TripType,
			Flights:  result.Flights,
			Count:    result.Count,
		}
		return models.FormatJSON(os.Stdout, fsr)
	}

	if len(result.Flights) == 0 {
		fmt.Println("No profile-compliant flights found. Loosen filters or extend search window.")
		if result.PreFilterCount > 0 {
			fmt.Printf("(Pre-filter count: %d → 0 after %s)\n",
				result.PreFilterCount, filterSummary(result.FiltersApplied))
		}
		return nil
	}

	fmt.Printf("🎯 Mikko-find: top %d bundles\n\n", len(result.Flights))
	for i, f := range result.Flights {
		hacks := hunt.Annotations(f, result.Origins)
		fmt.Printf("%d. €%.0f  %s  %s\n", i+1, f.Price, hunt.RouteSummary(f), hacks)
	}

	if calendarInsert && len(result.Flights) > 0 {
		if err := insertBundleCalendar(result.Flights[0]); err != nil {
			fmt.Fprintf(os.Stderr, "calendar insert failed: %v\n", err)
		}
	}

	return nil
}

// filterSummary renders which filters dropped how many flights. Used in the
// "no results" explainer so the user knows which knob to loosen.
func filterSummary(log hunt.HuntFilterLog) string {
	parts := []string{}
	if log.LongLayover.Ran {
		parts = append(parts, fmt.Sprintf("long-layover=-%d", log.LongLayover.Dropped))
	}
	if log.LoungeAccess.Ran {
		parts = append(parts, fmt.Sprintf("lounge=-%d", log.LoungeAccess.Dropped))
	}
	if log.NoEarlyConnection.Ran {
		parts = append(parts, fmt.Sprintf("no-early-connection=-%d", log.NoEarlyConnection.Dropped))
	}
	if len(parts) == 0 {
		return "no filters"
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += ", " + p
	}
	return out
}

// insertBundleCalendar shells out to `gws calendar insert` for the flight.
// Non-fatal on failure — printed to stderr.
func insertBundleCalendar(f models.FlightResult) error {
	title, start, end, desc, err := hunt.CalendarEventForBundle(f)
	if err != nil {
		return err
	}
	cmd := exec.Command("gws", "calendar", "insert",
		"--summary", title,
		"--start", start,
		"--end", end,
		"--description", desc,
	)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
