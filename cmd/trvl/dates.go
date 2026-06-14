package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

func datesCmd() *cobra.Command {
	var (
		fromDate       string
		toDate         string
		duration       int
		minDuration    int
		maxDuration    int
		roundTrip      bool
		adults         int
		format         string
		legacy         bool
		targetCurrency string
		noGeo          bool
	)

	cmd := &cobra.Command{
		Use:   "dates [ORIGIN] DESTINATION",
		Short: "Find cheapest flight dates across a range",
		Long: `Search for the cheapest flight prices across a date range.

ORIGIN and DESTINATION are IATA airport codes (e.g. HEL, NRT, JFK). ORIGIN is
optional: omit it and trvl resolves it from your saved home airport or, failing
that, your current location (geo-IP, best-effort; disable with --no-geo).

By default, uses Google's CalendarGraph API for fast single-request results.
Falls back to per-date search automatically if CalendarGraph fails.
Use --legacy to force the per-date search approach.

Examples:
  trvl dates NRT --from 2026-06-01 --to 2026-06-30
  trvl dates HEL NRT --from 2026-06-01 --to 2026-06-30
  trvl dates HEL NRT --from 2026-06-01 --to 2026-06-30 --round-trip --duration 7
  trvl dates HEL NRT --from 2026-06-01 --to 2026-06-07 --format json
  trvl dates HEL NRT --from 2026-06-01 --to 2026-06-30 --legacy`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var originArg, destination string
			if len(args) == 2 {
				originArg = args[0]
				destination = strings.ToUpper(args[1])
			} else {
				destination = strings.ToUpper(args[0])
			}
			origin, err := resolveCLIOrigin(cmd.Context(), originArg, format, noGeo)
			if err != nil {
				return err
			}

			// Resolve the stay-duration range. --duration is the single-length
			// shorthand; --min-duration/--max-duration request a window of stay
			// lengths (the feature @RobertoReale forked fli to add). A range
			// only makes sense for a round trip, so it implies --round-trip.
			durations, err := durationRange(duration, minDuration, maxDuration)
			if err != nil {
				return err
			}
			if len(durations) > 1 {
				roundTrip = true
			}

			merged := &models.DateSearchResult{Success: true, TripType: tripTypeLabel(roundTrip)}
			for _, d := range durations {
				result, err := runDateSearch(cmd.Context(), origin, destination, dateSearchParams{
					fromDate:  fromDate,
					toDate:    toDate,
					duration:  d,
					roundTrip: roundTrip,
					adults:    adults,
					legacy:    legacy,
				})
				if err != nil {
					return err
				}
				mergeDateResults(merged, result)
			}
			finalizeMergedDates(merged)

			if format == "json" {
				return models.FormatJSON(os.Stdout, merged)
			}
			return printDatesTable(cmd.Context(), targetCurrency, merged)
		},
	}

	cmd.Flags().StringVar(&fromDate, "from", "", "Start of date range (YYYY-MM-DD); default: tomorrow")
	cmd.Flags().StringVar(&toDate, "to", "", "End of date range (YYYY-MM-DD); default: from + 30 days")
	cmd.Flags().IntVar(&duration, "duration", 7, "Trip duration in days (for round-trip). Shorthand for --min-duration N --max-duration N")
	cmd.Flags().IntVar(&minDuration, "min-duration", 0, "Minimum stay length in nights for a flexible-duration window (round-trip)")
	cmd.Flags().IntVar(&maxDuration, "max-duration", 0, "Maximum stay length in nights for a flexible-duration window (round-trip)")
	cmd.Flags().BoolVar(&roundTrip, "round-trip", false, "Search round-trip prices")
	cmd.Flags().IntVar(&adults, "adults", 1, "Number of adult passengers")
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table, json")
	cmd.Flags().BoolVar(&legacy, "legacy", false, "Use legacy per-date search (slower, more requests)")
	cmd.Flags().StringVar(&targetCurrency, "currency", "", "Convert prices to this currency (e.g. EUR, USD). Empty = show API default")
	cmd.Flags().BoolVar(&noGeo, "no-geo", false, "Disable geo-IP origin detection (also via TRVL_NO_GEO=1)")

	cmd.ValidArgsFunction = airportCompletion

	return cmd
}

// dateSearchParams bundles the inputs for a single-duration date search.
type dateSearchParams struct {
	fromDate  string
	toDate    string
	duration  int
	roundTrip bool
	adults    int
	legacy    bool
}

// durationRange resolves the requested stay-length range into the explicit list
// of night counts to search. --duration is the single-length default;
// --min-duration/--max-duration request a window. Either bound alone fills in
// the other. Returns an error for an invalid (min > max or non-positive) range.
func durationRange(duration, minDuration, maxDuration int) ([]int, error) {
	if minDuration == 0 && maxDuration == 0 {
		if duration <= 0 {
			return []int{1}, nil
		}
		return []int{duration}, nil
	}
	lo, hi := minDuration, maxDuration
	if lo == 0 {
		lo = hi
	}
	if hi == 0 {
		hi = lo
	}
	if lo <= 0 {
		return nil, fmt.Errorf("--min-duration must be at least 1")
	}
	if lo > hi {
		return nil, fmt.Errorf("--min-duration (%d) cannot exceed --max-duration (%d)", lo, hi)
	}
	out := make([]int, 0, hi-lo+1)
	for d := lo; d <= hi; d++ {
		out = append(out, d)
	}
	return out, nil
}

// runDateSearch runs one date search for a single stay length via the default
// CalendarGraph path or the legacy per-date path.
func runDateSearch(ctx context.Context, origin, destination string, p dateSearchParams) (*models.DateSearchResult, error) {
	if p.legacy {
		return flights.SearchDates(ctx, origin, destination, flights.DateSearchOptions{
			FromDate:  p.fromDate,
			ToDate:    p.toDate,
			Duration:  p.duration,
			RoundTrip: p.roundTrip,
			Adults:    p.adults,
		})
	}
	return flights.SearchCalendar(ctx, origin, destination, flights.CalendarOptions{
		FromDate:   p.fromDate,
		ToDate:     p.toDate,
		TripLength: p.duration,
		RoundTrip:  p.roundTrip,
		Adults:     p.adults,
	})
}

// mergeDateResults appends one search's dated prices into the accumulator.
// Failed sub-searches (Success=false) contribute nothing.
func mergeDateResults(into, from *models.DateSearchResult) {
	if from == nil || !from.Success {
		return
	}
	into.Dates = append(into.Dates, from.Dates...)
	if into.DateRange == "" {
		into.DateRange = from.DateRange
	}
}

// finalizeMergedDates dedupes (by depart+return date), sorts by price ascending,
// and sets the count on a merged multi-duration result.
func finalizeMergedDates(r *models.DateSearchResult) {
	seen := make(map[string]bool, len(r.Dates))
	deduped := r.Dates[:0]
	for _, d := range r.Dates {
		key := d.Date + "|" + d.ReturnDate
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, d)
	}
	r.Dates = deduped
	sort.SliceStable(r.Dates, func(i, j int) bool {
		return r.Dates[i].Price < r.Dates[j].Price
	})
	r.Count = len(r.Dates)
}

// tripTypeLabel maps the round-trip flag to the result's trip_type string.
func tripTypeLabel(roundTrip bool) string {
	if roundTrip {
		return "round_trip"
	}
	return "one_way"
}

// printDatesTable renders date price results as an ASCII table.
// If targetCurrency is set and differs from API currency, converts prices.
func printDatesTable(ctx context.Context, targetCurrency string, result *models.DateSearchResult) error {
	if !result.Success {
		_, _ = fmt.Fprintf(os.Stderr, "Search failed: %s\n", result.Error)
		return nil
	}

	if result.Count == 0 {
		fmt.Println("No prices found for the given date range.")
		return nil
	}

	// Convert prices if --currency specified.
	if targetCurrency != "" {
		for i := range result.Dates {
			if result.Dates[i].Currency != targetCurrency && result.Dates[i].Price > 0 {
				converted, cur := destinations.ConvertCurrency(ctx, result.Dates[i].Price, result.Dates[i].Currency, targetCurrency)
				result.Dates[i].Price = math.Round(converted)
				result.Dates[i].Currency = cur
			}
		}
	}

	fmt.Printf("Cheapest prices: %s (%s, %d dates)\n\n", result.DateRange, result.TripType, result.Count)

	headers := []string{"Date", "Price"}
	if result.TripType == "round_trip" {
		headers = append(headers, "Return")
	}

	var rows [][]string
	for _, d := range result.Dates {
		row := []string{
			d.Date,
			formatPrice(d.Price, d.Currency),
		}
		if result.TripType == "round_trip" {
			row = append(row, d.ReturnDate)
		}
		rows = append(rows, row)
	}

	models.FormatTable(os.Stdout, headers, rows)
	return nil
}
