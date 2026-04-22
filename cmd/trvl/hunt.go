// Package main -- hunt.go
//
// `trvl hunt` orchestrator command. Runs Mikko's mental-model flight search
// algorithm end-to-end: multi-airport origin spread, rail+fly, hidden-city
// detection, time/lounge filters, and (optional) Google Calendar insert for
// the chosen bundle.
//
// Reference: travel_search_mental_model.md section "TRVL IMPROVEMENT PROPOSAL".

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/spf13/cobra"
)

// huntCmd returns the `trvl hunt` command.
func huntCmd() *cobra.Command {
	var (
		returnDate      string
		cabin           string
		format          string
		minLayoverStr   string
		layoverAirports []string
		noEarlyConn        bool
		loungeRequired  bool
		hiddenCity      bool
		topN            int
		calendarInsert  bool
	)

	cmd := &cobra.Command{
		Use:   "hunt ORIGIN DESTINATION DATE",
		Short: "Orchestrated flight search applying Mikko's mental model",
		Long: `Run Mikko's full 7-step flight search algorithm end-to-end:

1. Multi-airport origin spread (home + nearby from preferences)
2. RT via primary airline (google_flights)
3. Rail+fly origins (ZYR/ANR/BRU) when AMS involved
4. Hidden-city skip-last-leg detection (optional)
5. Post-search filters (time, lounge, aamuyö)
6. Rank: cheapest profile-compliant first
7. Top N bundles presented

ORIGIN is typically "home" (expanded from preferences.home_airports).
DATE is ISO 8601 (2026-04-23).

Example:
  trvl hunt home PRG 2026-04-23 --return 2026-06-03 --no-early-connection --lounge-required`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHunt(cmd.Context(), huntOpts{
				origin:          args[0],
				destination:     args[1],
				date:            args[2],
				returnDate:      returnDate,
				cabin:           cabin,
				format:          format,
				minLayoverStr:   minLayoverStr,
				layoverAirports: layoverAirports,
				noEarlyConn:        noEarlyConn,
				loungeRequired:  loungeRequired,
				hiddenCity:      hiddenCity,
				topN:            topN,
				calendarInsert:  calendarInsert,
			})
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
	cmd.Flags().BoolVar(&loungeRequired, "lounge-required", true, "Require lounge at transit airports (default on for hunt)")
	cmd.Flags().BoolVar(&hiddenCity, "hidden-city", false, "Also detect hidden-city candidates")
	cmd.Flags().IntVar(&topN, "top", 3, "Number of top bundles to present")
	cmd.Flags().BoolVar(&calendarInsert, "calendar", false, "Insert chosen bundle (top 1) into Google Calendar via gws CLI")

	return cmd
}

type huntOpts struct {
	origin, destination, date, returnDate string
	cabin, format, minLayoverStr          string
	layoverAirports                       []string
	noEarlyConn, loungeRequired, hiddenCity  bool
	topN                                  int
	calendarInsert                        bool
}

func runHunt(ctx context.Context, o huntOpts) error {
	// Step 1+2: resolve home, expand to multi-airport origins.
	origins, err := expandHuntOrigins(o.origin)
	if err != nil {
		return err
	}

	// Step 3: add rail+fly when AMS present.
	origins = addRailFlyOrigins(origins)

	destinations := flights.ParseAirports(o.destination)

	cabinClass, _ := models.ParseCabinClass(o.cabin)
	opts := flights.SearchOptions{
		ReturnDate: o.returnDate,
		CabinClass: cabinClass,
		SortBy:     models.SortCheapest,
		Adults:     1,
	}

	// Step 4: primary search (multi-airport aware).
	var result *models.FlightSearchResult
	if len(origins) > 1 || len(destinations) > 1 {
		result, err = flights.SearchMultiAirport(ctx, origins, destinations, o.date, opts)
	} else {
		result, err = flights.SearchFlights(ctx, origins[0], destinations[0], o.date, opts)
	}
	if err != nil {
		return fmt.Errorf("flight search: %w", err)
	}
	if result == nil || !result.Success {
		return fmt.Errorf("flight search returned no results")
	}

	// Step 5: apply filters.
	result.Flights = applyHuntFilters(result.Flights, o)
	result.Count = len(result.Flights)

	// Step 6: rank and slice top N.
	sort.SliceStable(result.Flights, func(i, j int) bool {
		return result.Flights[i].Price < result.Flights[j].Price
	})
	if len(result.Flights) > o.topN {
		result.Flights = result.Flights[:o.topN]
		result.Count = o.topN
	}

	// Step 7: present.
	if o.format == "json" {
		return models.FormatJSON(os.Stdout, result)
	}
	if len(result.Flights) == 0 {
		fmt.Println("No profile-compliant flights found. Loosen filters or extend search window.")
		return nil
	}
	fmt.Printf("🎯 Mikko-hunt: top %d bundles\n\n", len(result.Flights))
	for i, f := range result.Flights {
		hacks := huntAnnotations(f, origins)
		fmt.Printf("%d. €%.0f  %s  %s\n", i+1, f.Price, hackRouteSummary(f), hacks)
	}

	// Step 8: optional calendar insert for top bundle.
	if o.calendarInsert && len(result.Flights) > 0 {
		if err := insertBundleCalendar(result.Flights[0], o); err != nil {
			fmt.Fprintf(os.Stderr, "calendar insert failed: %v\n", err)
		}
	}

	return nil
}

// expandHuntOrigins resolves "home" and applies home-fan expansion.
func expandHuntOrigins(originArg string) ([]string, error) {
	if strings.EqualFold(strings.TrimSpace(originArg), "home") {
		prefs, err := preferences.Load()
		if err != nil {
			return nil, err
		}
		fanned := map[string]bool{}
		for _, h := range prefs.HomeAirports {
			fanned[h] = true
			for _, nb := range prefs.NearbyAirportsFor(h) {
				fanned[nb] = true
			}
		}
		out := make([]string, 0, len(fanned))
		for a := range fanned {
			out = append(out, a)
		}
		sort.Strings(out)
		return out, nil
	}
	origins := flights.ParseAirports(originArg)
	// Apply nearby expansion for each origin.
	prefs, err := preferences.Load()
	if err == nil && prefs != nil {
		fanned := map[string]bool{}
		for _, o := range origins {
			fanned[o] = true
			for _, nb := range prefs.NearbyAirportsFor(o) {
				fanned[nb] = true
			}
		}
		origins = origins[:0]
		for a := range fanned {
			origins = append(origins, a)
		}
		sort.Strings(origins)
	}
	return origins, nil
}

// addRailFlyOrigins appends ZYR/ANR/BRU when AMS is among origins.
func addRailFlyOrigins(origins []string) []string {
	hasAMS := false
	for _, o := range origins {
		if strings.EqualFold(o, "AMS") {
			hasAMS = true
		}
	}
	if !hasAMS {
		return origins
	}
	for _, rf := range []string{"ZYR", "ANR", "BRU"} {
		already := false
		for _, o := range origins {
			if strings.EqualFold(o, rf) {
				already = true
			}
		}
		if !already {
			origins = append(origins, rf)
		}
	}
	return origins
}

// applyHuntFilters runs Mikko's filter stack.
func applyHuntFilters(flts []models.FlightResult, o huntOpts) []models.FlightResult {
	if o.minLayoverStr != "" || len(o.layoverAirports) > 0 {
		mins := 0
		if o.minLayoverStr != "" {
			if d, err := time.ParseDuration(o.minLayoverStr); err == nil {
				mins = int(d.Minutes())
			}
		}
		flts = flights.FilterByLongLayover(flts, mins, o.layoverAirports)
	}
	if o.loungeRequired {
		var cards []string
		if prefs, err := preferences.Load(); err == nil {
			cards = prefs.LoungeCards
		}
		flts = flights.FilterByLoungeAccess(flts, cards, nil)
	}
	if o.noEarlyConn {
		floor := ""
		if prefs, err := preferences.Load(); err == nil {
			floor = prefs.EarlyConnectionFloor
		}
		flts = flights.FilterByEarlyConnection(flts, floor)
	}
	return flts
}

// hackRouteSummary produces a short route description.
func hackRouteSummary(f models.FlightResult) string {
	if len(f.Legs) == 0 {
		return "?"
	}
	parts := []string{f.Legs[0].DepartureAirport.Code}
	for _, l := range f.Legs {
		parts = append(parts, l.ArrivalAirport.Code)
	}
	return strings.Join(parts, "→")
}

// huntAnnotations builds a short tag string explaining any hacks in use.
func huntAnnotations(f models.FlightResult, origins []string) string {
	tags := []string{}
	if len(f.Legs) > 0 {
		orig := f.Legs[0].DepartureAirport.Code
		for _, rf := range []string{"ZYR", "ANR", "BRU"} {
			if orig == rf {
				tags = append(tags, "[rail+fly]")
			}
		}
	}
	if f.Stops > 0 {
		tags = append(tags, fmt.Sprintf("%dstop", f.Stops))
	}
	return strings.Join(tags, " ")
}

// insertBundleCalendar shells out to `gws calendar insert` for the flight.
// Non-fatal on failure — printed to stderr.
func insertBundleCalendar(f models.FlightResult, o huntOpts) error {
	if len(f.Legs) == 0 {
		return fmt.Errorf("empty itinerary")
	}
	title := fmt.Sprintf("✈️ %s→%s (%s%s)",
		f.Legs[0].DepartureAirport.Code,
		f.Legs[len(f.Legs)-1].ArrivalAirport.Code,
		f.Legs[0].Airline,
		f.Legs[0].FlightNumber,
	)
	start := f.Legs[0].DepartureTime
	end := f.Legs[len(f.Legs)-1].ArrivalTime
	desc := fmt.Sprintf("Booked via trvl hunt\nPrice: %s%.0f\nRoute: %s", f.Currency, f.Price, hackRouteSummary(f))

	// Use `gws calendar insert` CLI (per user's documented gws preference).
	cmd := exec.Command("gws", "calendar", "insert",
		"--summary", title,
		"--start", start,
		"--end", end,
		"--description", desc,
	)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
