package flights

import (
	"context"
	"fmt"
	"sort"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// Round-trip support is delivered by COMPOSITION: trvl runs two independent
// one-way searches (outbound origin->destination on the departure date, inbound
// destination->origin on the return date) and pairs the cheapest priced options
// into combined itineraries. This sidesteps the native Google Flights round-trip
// request encoding (tripType=1 + two segments), which Google rejects with a
// travel.frontend.flights.ErrorResponse — the true root cause of issue #198,
// where the misleading "unexpected flight data format" surfaced on every
// --return_date search. Composition reuses the full one-way provider fan-out
// (Google, Kiwi, Ryanair, Wizz, …) for each direction, so a round-trip benefits
// from every working provider instead of a single broken code path.
//
// Composed itineraries are explicitly two separate one-way tickets: each carries
// a warning so callers never mistake the summed price for a single bookable fare.

const (
	// roundTripLegCandidates bounds how many of the cheapest priced one-way
	// options from each direction are considered when pairing, capping the
	// N(outbound) x N(inbound) combinatorial blow-up.
	roundTripLegCandidates = 8
	// roundTripMaxResults bounds the number of composed itineraries returned,
	// sorted cheapest-total first. When more pairings exist than this, the
	// composer reports the truncation in its provider status (never silently).
	roundTripMaxResults = 12
)

// searchRoundTripComposed answers a round-trip query (opts.ReturnDate set) by
// running two one-way searches and composing the results. It reuses
// SearchFlightsWithClient for each leg so every working one-way provider
// contributes; the two sub-searches have distinct singleflight keys (their
// ReturnDate differs from this call's), so there is no self-deadlock.
func searchRoundTripComposed(ctx context.Context, client *batchexec.Client, origin, destination, date string, opts SearchOptions) (*models.FlightSearchResult, error) {
	returnDate := opts.ReturnDate

	// Per-leg options are one-way (clear ReturnDate) and must not collapse to a
	// single result before pairing (clear FirstResult).
	legOpts := opts
	legOpts.ReturnDate = ""
	legOpts.FirstResult = false

	outbound, outErr := SearchFlightsWithClient(ctx, client, origin, destination, date, legOpts)
	inbound, inErr := SearchFlightsWithClient(ctx, client, destination, origin, returnDate, legOpts)

	statuses := make([]models.ProviderStatus, 0, 8)
	var outFlights, inFlights []models.FlightResult
	if outbound != nil {
		outFlights = outbound.Flights
		statuses = append(statuses, prefixLegStatuses("outbound", outbound.ProviderStatuses)...)
	}
	if inbound != nil {
		inFlights = inbound.Flights
		statuses = append(statuses, prefixLegStatuses("inbound", inbound.ProviderStatuses)...)
	}

	composed, truncated := composeRoundTrips(outFlights, inFlights, opts)

	statuses = append(statuses, roundTripComposerStatus(len(outFlights), len(inFlights), len(composed), truncated))

	// If neither leg produced any priced option, surface the underlying errors
	// rather than an empty "success".
	if len(composed) == 0 {
		if outErr != nil || inErr != nil {
			err := joinLegErrors(outErr, inErr)
			return &models.FlightSearchResult{
				Error:            err.Error(),
				TripType:         "round_trip",
				ProviderStatuses: statuses,
				Completeness:     models.ComputeCompleteness(statuses),
			}, err
		}
	}

	return &models.FlightSearchResult{
		Success:          true,
		Count:            len(composed),
		TripType:         "round_trip",
		Flights:          composed,
		ProviderStatuses: statuses,
		Completeness:     models.ComputeCompleteness(statuses),
	}, nil
}

// composeRoundTrips pairs the cheapest priced outbound options with the cheapest
// priced inbound options into combined itineraries, summing prices and
// concatenating legs. It is a pure function (no I/O) so the pairing logic is
// unit-tested deterministically. The returned slice is sorted cheapest-total
// first and bounded to roundTripMaxResults; truncated reports whether more
// pairings existed than were returned.
func composeRoundTrips(outbound, inbound []models.FlightResult, opts SearchOptions) (composed []models.FlightResult, truncated bool) {
	out := topPricedFlights(outbound, roundTripLegCandidates)
	in := topPricedFlights(inbound, roundTripLegCandidates)
	if len(out) == 0 || len(in) == 0 {
		return nil, false
	}

	all := make([]models.FlightResult, 0, len(out)*len(in))
	for _, o := range out {
		for _, i := range in {
			// Only pair like-priced legs. Both directions are normalised to the
			// session currency upstream, so a mismatch means one leg could not be
			// converted; summing across currencies would fabricate a total.
			if o.Currency != i.Currency {
				continue
			}
			all = append(all, composeItinerary(o, i))
		}
	}
	if len(all) == 0 {
		return nil, false
	}

	sortFlightResults(all, opts.SortBy)

	if len(all) > roundTripMaxResults {
		return all[:roundTripMaxResults], true
	}
	return all, false
}

// composeItinerary combines one outbound and one inbound one-way option into a
// single round-trip FlightResult: legs concatenated outbound-then-inbound, price
// and duration summed, stops summed. The result is flagged as two separate
// tickets via a warning and a composed provider label.
func composeItinerary(out, in models.FlightResult) models.FlightResult {
	legs := make([]models.FlightLeg, 0, len(out.Legs)+len(in.Legs))
	legs = append(legs, out.Legs...)
	legs = append(legs, in.Legs...)

	warnings := []string{"composed round-trip: outbound and inbound are two separate one-way tickets, booked independently"}
	warnings = append(warnings, out.Warnings...)
	warnings = append(warnings, in.Warnings...)

	return models.FlightResult{
		Price:    out.Price + in.Price,
		Currency: out.Currency,
		Duration: out.Duration + in.Duration,
		Stops:    out.Stops + in.Stops,
		Provider: composedProviderLabel(out.Provider, in.Provider),
		Legs:     legs,
		Warnings: warnings,
	}
}

// composedProviderLabel builds a transparent provider label for a composed
// round-trip, e.g. "composed (Google Flights + Ryanair)".
func composedProviderLabel(outbound, inbound string) string {
	if outbound == "" {
		outbound = "unknown"
	}
	if inbound == "" {
		inbound = "unknown"
	}
	return fmt.Sprintf("composed (%s + %s)", outbound, inbound)
}

// topPricedFlights returns up to n cheapest flights that carry a usable price
// (Price > 0 and a currency). Unpriced options are excluded so a composed total
// is never a sum that includes a zero.
func topPricedFlights(flights []models.FlightResult, n int) []models.FlightResult {
	priced := make([]models.FlightResult, 0, len(flights))
	for _, f := range flights {
		if f.PriceForRanking() > 0 && f.Currency != "" {
			priced = append(priced, f)
		}
	}
	sort.SliceStable(priced, func(i, j int) bool {
		return compareFlightPrices(priced[i].PriceForRanking(), priced[j].PriceForRanking()) < 0
	})
	if len(priced) > n {
		priced = priced[:n]
	}
	return priced
}

// prefixLegStatuses namespaces a leg's per-provider statuses by direction so a
// round-trip response shows, e.g., "outbound:google_flights" distinctly from
// "inbound:google_flights".
func prefixLegStatuses(direction string, statuses []models.ProviderStatus) []models.ProviderStatus {
	out := make([]models.ProviderStatus, 0, len(statuses))
	for _, s := range statuses {
		s.ID = direction + ":" + s.ID
		s.Name = direction + " " + s.Name
		out = append(out, s)
	}
	return out
}

// roundTripComposerStatus reports the composition step itself so callers can see
// how many one-way options fed the pairing and whether the output was bounded.
func roundTripComposerStatus(outCount, inCount, composedCount int, truncated bool) models.ProviderStatus {
	status := models.ProviderStatus{
		ID:      "roundtrip_composer",
		Name:    "Round-trip composer",
		Status:  models.StatusOK,
		Results: composedCount,
	}
	if composedCount == 0 {
		status.Status = models.StatusCheckedNoHit
		status.Error = fmt.Sprintf("no priced pairing (outbound priced options=%d, inbound priced options=%d)", outCount, inCount)
		status.FixHint = "try different dates, or search each direction one-way"
		return status
	}
	if truncated {
		status.FixHint = fmt.Sprintf("showing cheapest %d composed round-trips; more pairings exist (search one-way for the full per-direction list)", roundTripMaxResults)
	}
	return status
}

// joinLegErrors combines outbound/inbound errors into one, labelling each side.
func joinLegErrors(outErr, inErr error) error {
	switch {
	case outErr != nil && inErr != nil:
		return fmt.Errorf("outbound: %w; inbound: %v", outErr, inErr)
	case outErr != nil:
		return fmt.Errorf("outbound: %w", outErr)
	default:
		return fmt.Errorf("inbound: %w", inErr)
	}
}
