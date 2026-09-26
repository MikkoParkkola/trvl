package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/profile"
)

func applyFlightProfileHints(opts flights.SearchOptions, args map[string]any, hints profile.FlightSearchHints) flights.SearchOptions {
	_ = args
	_ = hints
	return opts
}

// dispatchFlightSearch routes a search_flights call to the right
// provider based on the optional `provider` argument. Empty (or one
// of the legacy aliases) goes through the default Google Flights +
// Kiwi + Skiplagged merge in `flights.SearchFlights` (AFKLM is
// opportunistically merged for round-trips when a credential is
// present via the standard resolution; see searchRoundTripComposed).
// `provider="skiplagged"` dispatches Skiplagged solo;
// `provider="afklm"` (aliases...) forces AFKLM only (still requires
// credential and errors if absent). New providers must explicitly
// register here so the dispatcher remains the single switchboard.
func dispatchFlightSearch(ctx context.Context, args map[string]any, origin, dest, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
	provider := strings.ToLower(strings.TrimSpace(argString(args, "provider")))
	// origin/dest are validated, possibly comma-separated multi-airport lists.
	// Split them so a search spanning >1 airport on either side fans out via
	// SearchMultiAirport (the same path the CLI uses), mirroring its routing.
	origins := flights.ParseAirports(origin)
	dests := flights.ParseAirports(dest)
	if len(origins) == 0 || len(dests) == 0 {
		return nil, fmt.Errorf("origin and destination are required")
	}
	switch provider {
	case "skiplagged":
		if len(origins) != 1 || len(dests) != 1 {
			return nil, fmt.Errorf("provider skiplagged supports exactly one origin and one destination")
		}
		return flights.SearchSkiplagged(ctx, origins[0], dests[0], date, opts)
	case "afklm", "af-klm", "airfranceklm":
		if len(origins) != 1 || len(dests) != 1 {
			return nil, fmt.Errorf("provider afklm supports exactly one origin and one destination")
		}
		return flights.SearchAFKLM(ctx, origins[0], dests[0], date, opts)
	case "", "default", "google", "google_flights", "kiwi":
		if multiAirportRoute(origins, dests) {
			return flights.SearchMultiAirport(ctx, origins, dests, date, opts)
		}
		return flights.SearchFlights(ctx, origins[0], dests[0], date, opts)
	default:
		return nil, fmt.Errorf("unsupported provider %q (valid: skiplagged, afklm, or empty for default Google+Kiwi+Skiplagged merge)", provider)
	}
}

// multiAirportRoute reports whether a search spans more than one airport on
// either side, in which case it must fan out via SearchMultiAirport rather
// than the single-route SearchFlights path.
func multiAirportRoute(origins, dests []string) bool {
	return len(origins) > 1 || len(dests) > 1
}

// primaryAirport returns the first airport code from a (possibly
// comma-separated multi-airport) origin/destination string. Used to feed the
// single-airport scalar enrichments without misfiring on a comma string.
func primaryAirport(s string) string {
	if i := strings.IndexByte(s, ','); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func handleSearchDates(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	origin, dest, err := validateOriginDest(args)
	if err != nil {
		return nil, nil, err
	}

	startDate := argString(args, "start_date")
	endDate := argString(args, "end_date")
	if startDate == "" || endDate == "" {
		return nil, nil, fmt.Errorf("start_date and end_date are required")
	}

	// Validate date range.
	if err := models.ValidateDateRange(startDate, endDate); err != nil {
		return nil, nil, err
	}

	tripLength := argInt(args, "trip_duration", 0)
	roundTrip := argBool(args, "is_round_trip", false)
	if tripLength < 0 {
		return nil, nil, fmt.Errorf("trip_duration must be zero or positive (got %d)", tripLength)
	}
	if roundTrip && tripLength <= 0 {
		return nil, nil, fmt.Errorf("round-trip date search requires a positive trip_duration (days between outbound and return)")
	}
	// A trip_duration only has meaning for round-trips; honor the intent
	// rather than silently ignoring it on a one-way search.
	if tripLength > 0 {
		roundTrip = true
	}

	opts := flights.CalendarOptions{
		FromDate:   startDate,
		ToDate:     endDate,
		TripLength: tripLength,
		RoundTrip:  roundTrip,
	}

	// Use SearchCalendar (1 API call via GetCalendarGraph) instead of the
	// legacy SearchDates (N calls, one per date). Falls back to N-call
	// automatically if CalendarGraph fails.
	result, err := flights.SearchCalendar(ctx, origin, dest, opts)
	if err != nil {
		return nil, nil, err
	}

	summary := fmt.Sprintf("Found prices for %d dates from %s to %s (%s to %s).",
		result.Count, origin, dest, startDate, endDate)
	if result.Count > 0 {
		cheapest := result.Dates[0]
		for _, d := range result.Dates[1:] {
			if d.Price > 0 && d.Price < cheapest.Price {
				cheapest = d
			}
		}
		summary += fmt.Sprintf(" Cheapest: %s %.0f on %s.", cheapest.Currency, cheapest.Price, cheapest.Date)
	}

	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}

	return content, result, nil
}
