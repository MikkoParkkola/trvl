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
	"time"

	"github.com/MikkoParkkola/trvl/internal/find"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

// findCmd returns the primary `trvl find` command (Mikko's orchestrated
// search). findCmdWith takes a cobra Use template so both `find` and the
// hidden `hunt` alias can share the implementation.
func findCmd() *cobra.Command { return findCmdWith("find [ORIGIN] DESTINATION [DATE]", false) }

// huntCmd returns the hidden back-compat alias `trvl hunt`. Delegates to
// findCmd's implementation so behavior stays identical.
func huntCmd() *cobra.Command { return findCmdWith("hunt [ORIGIN] DESTINATION [DATE]", true) }

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

ORIGIN defaults to "home" (expanded from preferences.home_airports).
DATE defaults to the next Saturday at least 14 days out.
With a single argument (destination only) trvl picks both for you.

Example:
  trvl find PRG                                     # origin=home, date=next Saturday 14d+
  trvl find AMS PRG                                 # origin explicit, date inferred
  trvl find home PRG 2026-04-23 --return 2026-06-03 # all three args`,
		Args: cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			origin, dest, date := inferFindArgs(args)
			req := find.Request{
				Origin:            origin,
				Destination:       dest,
				Date:              date,
				ReturnDate:        returnDate,
				Cabin:             cabin,
				MinLayoverMinutes: find.ParseDuration(minLayoverStr),
				LayoverAirports:   layoverAirports,
				NoEarlyConnection: noEarlyConn,
				LoungeRequired:    loungeRequired,
				HiddenCity:        hiddenCity,
				TopN:              topN,
			}
			return runFind(cmd.Context(), req, format, calendarInsert, len(args))
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

// runFind orchestrates one CLI invocation — calls internal/find, prints
// the inferred-defaults banner when the caller omitted positional args,
// and presents the result in the requested format.
//
// argCount is how many positional args the user actually supplied. Used
// only to decide whether to surface the "filled in defaults" banner.
func runFind(ctx context.Context, req find.Request, format string, calendarInsert bool, argCount int) error {
	if format != "json" && argCount < 3 {
		if argCount < 2 {
			fmt.Printf("Inferred origin=%s  date=%s (override with positional args)\n\n",
				req.Origin, req.Date)
		} else {
			fmt.Printf("Inferred date=%s (override with a third positional arg)\n\n",
				req.Date)
		}
	}

	result, err := find.Search(ctx, req, nil, nil)
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

	baseline := baselineDirectPrice(result.Flights)
	fmt.Printf("trvl find: top %d bundles\n\n", len(result.Flights))
	for i, f := range result.Flights {
		hacks := find.Annotations(f, result.Origins)
		savings := ""
		if baseline > 0 && f.Price < baseline && hacks != "" {
			savings = fmt.Sprintf("  [saves €%.0f vs direct]", baseline-f.Price)
		}
		fmt.Printf("%d. €%.0f  %s  %s%s\n", i+1, f.Price, find.RouteSummary(f), hacks, savings)
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
func filterSummary(log find.FilterLog) string {
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
	title, start, end, desc, err := find.CalendarEventForBundle(f)
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


// inferFindArgs maps positional args to (origin, destination, date) using
// profile-first defaults: origin defaults to "home", date defaults to the
// next Saturday at least 14 days out. Destination is always required.
//
// Call shapes:
//
//	1 arg : [dest]               origin=home, date=autoDate
//	2 args: [origin, dest]       date=autoDate
//	3 args: [origin, dest, date] as-is (classic shape)
func inferFindArgs(args []string) (origin, destination, date string) {
	switch len(args) {
	case 1:
		return "home", args[0], nextSaturdayISO(time.Now())
	case 2:
		return args[0], args[1], nextSaturdayISO(time.Now())
	default:
		return args[0], args[1], args[2]
	}
}

// nextSaturdayISO returns the next Saturday that is at least 14 days after
// `from`, formatted as an ISO 8601 date. The 14-day buffer avoids last-
// minute pricing and leaves time for rail+fly logistics.
func nextSaturdayISO(from time.Time) string {
	target := from.AddDate(0, 0, 14)
	// time.Saturday == 6
	offset := (int(time.Saturday) - int(target.Weekday()) + 7) % 7
	target = target.AddDate(0, 0, offset)
	return target.Format("2006-01-02")
}

// baselineDirectPrice returns the cheapest fare across bundles whose first
// leg is NOT a rail-fly origin (ZYR/ANR/BRU). Returns 0 when no direct
// bundles exist — callers suppress the savings callout in that case.
func baselineDirectPrice(fls []models.FlightResult) float64 {
	railFly := map[string]bool{"ZYR": true, "ANR": true, "BRU": true}
	best := 0.0
	for _, f := range fls {
		if len(f.Legs) == 0 {
			continue
		}
		if railFly[f.Legs[0].DepartureAirport.Code] {
			continue
		}
		if best == 0 || f.Price < best {
			best = f.Price
		}
	}
	return best
}
