package flights

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// Round-trip support combines two complementary sources:
//
//  1. NATIVE round-trip fares — Google Flights (tripType=1 with both segments in
//     one request) and Skiplagged both price a return trip as a single, often
//     discounted fare. These are true round-trip tickets and are preferred when
//     cheaper. (Google's native path was wrongly disabled after #198, which was a
//     transient rate-limit, not a structural rejection — verified live, the
//     native request returns a parseable flight array. See
//     searchGoogleNativeRoundTrip / searchNativeRoundTrip.)
//  2. COMPOSITION — two independent one-way searches (outbound origin->destination,
//     inbound destination->origin) paired into combined itineraries. This reuses
//     the full one-way provider fan-out (Google, Kiwi, Ryanair, Wizz, …) and
//     uniquely covers cross-carrier combinations (e.g. Google out + Ryanair back)
//     that no single native fare offers.
//
// All candidates are merged and sorted cheapest-first, so a discounted native
// round-trip naturally outranks a pricier composed pair. FareType keeps the two
// honestly distinguishable: composed itineraries are explicitly two separate
// one-way tickets (FareSplitTickets) and carry a warning so callers never mistake
// the summed price for a single bookable fare; native fares are FareRoundTrip.

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

	// Leg sub-searches run with auto-compose disabled (a single round-trip-level
	// savings pass is attached below) and with the one-way Skiplagged provider
	// suppressed: Skiplagged is queried once as a native round-trip instead (see
	// searchNativeRoundTrip), so two redundant one-way Skiplagged calls would
	// only waste its rate budget.
	legCtx := disableSkiplaggedOneWay(disableHacksCompose(ctx))

	statuses := make([]models.ProviderStatus, 0, 8)

	// Google Flights prices a round-trip as a single native fare (tripType=1, both
	// segments in one request) — one bookable ticket, often with a genuine return
	// discount, which is the superior answer to a round-trip query. Query it FIRST,
	// before the two one-way leg searches below, so it gets fresh Google rate
	// budget: run as the 3rd consecutive Google call it was reliably rate-limited
	// into zero results (MIK-6612), silently leaving users only the pricier
	// split-ticket pairs. Currency comes from opts (the dominant signal); the
	// composed/outbound fallback is not yet available here and only matters on the
	// rare unset-currency path, where normalisation harmlessly no-ops. If this
	// shifts a 429 onto an inbound leg, split-ticket simply has fewer options — an
	// acceptable trade to secure the native return fare the user actually asked for.
	googleRT, googleStatuses := searchGoogleNativeRoundTrip(ctx, client, origin, destination, date, returnDate, opts, nativeRoundTripCurrency(opts, nil, nil))
	statuses = append(statuses, googleStatuses...)

	outbound, outErr := SearchFlightsWithClient(legCtx, client, origin, destination, date, legOpts)
	inbound, inErr := SearchFlightsWithClient(legCtx, client, destination, origin, returnDate, legOpts)

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

	// Native round-trip pass (Skiplagged, then the Google fares fetched above, then
	// Kiwi): providers that price a return as a single fare can beat the sum of two
	// one-ways (a real return discount) and are one bookable ticket. Query them
	// WITH ReturnDate intact (the legs clear it) and merge their genuine round-trip
	// itineraries into the candidate pool. The cheapest sorts first, so a
	// discounted native fare naturally outranks a more expensive composed pair;
	// FareType keeps the two honestly distinguishable. Skiplagged and Kiwi run
	// after the legs — distinct providers, no shared Google rate budget.
	nativeRT, nativeStatuses := searchNativeRoundTrip(legCtx, client, origin, destination, date, returnDate, opts, nativeRoundTripCurrency(opts, composed, outFlights))
	statuses = append(statuses, nativeStatuses...)
	nativeRT = append(nativeRT, googleRT...)

	// Kiwi likewise prices a round-trip as one native fare (outbound leg at the
	// full return total, return chosen at booking). kiwiEligibleOptions wrongly
	// excluded ReturnDate as unsupported — verified live it is supported — so
	// query it natively too; its discounted returns then compete in the merge.
	kiwiRT, kiwiStatuses := searchKiwiNativeRoundTrip(ctx, client, origin, destination, date, returnDate, opts, nativeRoundTripCurrency(opts, composed, outFlights))
	statuses = append(statuses, kiwiStatuses...)
	nativeRT = append(nativeRT, kiwiRT...)

	// Opportunistic AFKLM native round-trip (the only provider returning genuine
	// both-bound single-carrier round-trips) on the DEFAULT merge path.
	// Included IFF a credential resolves (env AFKLM_KEY / keychain / 1Password)
	// via the existing NewProvider/ErrNoCredential check — no new plumbing.
	// When no credential: silent fast skip (no error, no warning, no latency).
	// Any AFKLM error is non-fatal (log + continue). --provider afklm remains
	// "AFKLM only"; this path only augments the default Google+Kiwi+Skiplagged.
	afklmRT, afklmSt := searchAFKLMNativeRoundTrip(ctx, origin, destination, date, returnDate, opts)
	statuses = append(statuses, afklmSt...)
	nativeRT = append(nativeRT, afklmRT...)

	// One hop further: native fares that priced the return "at booking" carry
	// only an outbound leg, so there is nothing for a return-leg condition filter
	// (carrier, stops, times) to act on. Enrich them with REAL return-leg detail
	// reused from the inbound one-ways already fetched above (no extra network),
	// bounded to the cheapest few and reported in status so nothing is silently
	// dropped. Native fares that already carry both legs are left untouched.
	if len(nativeRT) > 0 {
		var enrichStatus models.ProviderStatus
		nativeRT, enrichStatus = enrichNativeReturnLegs(nativeRT, inFlights, roundTripNativeReturnCandidates)
		statuses = append(statuses, enrichStatus)
	}

	if len(nativeRT) > 0 {
		merged := make([]models.FlightResult, 0, len(nativeRT)+len(composed))
		merged = append(merged, nativeRT...)
		merged = append(merged, composed...)
		sortFlightResults(merged, opts.SortBy)
		if len(merged) > roundTripMaxResults {
			merged = merged[:roundTripMaxResults]
			truncated = true
		}
		composed = merged
	}

	statuses = append(statuses, roundTripComposerStatus(len(outFlights), len(inFlights), len(composed), truncated))

	// If neither leg produced any priced option, surface the underlying errors
	// rather than an empty "success".
	if len(composed) == 0 && (outErr != nil || inErr != nil) {
		// When a leg failed because providers were rate-limited / blocked /
		// unavailable (a typed, retryable upstream condition), report a
		// rate_limited status and a retryable, user-facing message — NEVER the
		// legacy "unexpected flight data format" string (#198 + #228). A genuine
		// parse bug or other hard failure still falls through to joinLegErrors.
		if legErrorsRateLimited(outErr, inErr) {
			const msg = "flight providers are rate-limiting or temporarily unavailable; retry in ~60s"
			statuses = append(statuses, models.ProviderStatus{
				ID:      "roundtrip_composer_upstream",
				Name:    "Round-trip composer (upstream)",
				Status:  models.StatusRateLimited,
				Error:   msg,
				FixHint: "retry in ~60s, or search each direction one-way",
			})
			err := fmt.Errorf("%s: %w", msg, models.ErrRateLimited)
			return &models.FlightSearchResult{
				Error:            msg,
				TripType:         "round_trip",
				ProviderStatuses: statuses,
				Completeness:     models.ComputeCompleteness(statuses),
			}, err
		}
		err := joinLegErrors(outErr, inErr)
		return &models.FlightSearchResult{
			Error:            err.Error(),
			TripType:         "round_trip",
			ProviderStatuses: statuses,
			Completeness:     models.ComputeCompleteness(statuses),
		}, err
	}

	result := &models.FlightSearchResult{
		Success:          true,
		Count:            len(composed),
		TripType:         "round_trip",
		Flights:          composed,
		ProviderStatuses: statuses,
		Completeness:     models.ComputeCompleteness(statuses),
	}
	// Round-trip-level savings pass (split / throwaway / hidden-city become
	// applicable once ReturnDate is known). Uses the original ctx so it is not
	// suppressed by the leg-level disable above.
	attachFlightHackSaving(ctx, result, origin, destination, date, opts)
	return result, nil
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
// tickets via a warning and a composed provider label. Each leg is tagged with
// its Direction ("outbound"/"inbound") so consumers can tell the return legs
// apart in the otherwise-flat Legs slice (return-ticket data, not just one-way).
func composeItinerary(out, in models.FlightResult) models.FlightResult {
	legs := make([]models.FlightLeg, 0, len(out.Legs)+len(in.Legs))
	legs = append(legs, taggedLegs(out.Legs, "outbound")...)
	legs = append(legs, taggedLegs(in.Legs, "inbound")...)

	warnings := []string{"composed round-trip: outbound and inbound are two separate one-way tickets, booked independently"}
	warnings = append(warnings, out.Warnings...)
	warnings = append(warnings, in.Warnings...)

	return models.FlightResult{
		Price:    out.Price + in.Price,
		Currency: out.Currency,
		Duration: out.Duration + in.Duration,
		Stops:    out.Stops + in.Stops,
		Provider: composedProviderLabel(out.Provider, in.Provider),
		FareType: models.FareSplitTickets,
		Legs:     legs,
		Warnings: warnings,
	}
}

// taggedLegs returns a copy of legs with each leg's Direction set, leaving the
// source slice untouched (the one-way leg results are cached/shared upstream, so
// mutating them in place would leak the round-trip tag into a one-way response).
func taggedLegs(legs []models.FlightLeg, direction string) []models.FlightLeg {
	out := make([]models.FlightLeg, len(legs))
	for i, leg := range legs {
		leg.Direction = direction
		out[i] = leg
	}
	return out
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

// legErrorsRateLimited reports whether the round-trip leg failures were caused by
// a typed, retryable upstream rate-limit/block/unavailable condition
// (models.ErrRateLimited) rather than a genuine parse bug or hard error. It
// returns true when at least one present leg error carries the rate-limit signal
// and none of the present leg errors is a non-rate-limit failure — i.e. the legs
// failed ONLY because providers were rate-limited. This is the gate that lets the
// composer emit a soft, retryable status instead of a hard failure.
func legErrorsRateLimited(outErr, inErr error) bool {
	sawRateLimit := false
	for _, e := range []error{outErr, inErr} {
		if e == nil {
			continue
		}
		if !errors.Is(e, models.ErrRateLimited) {
			return false
		}
		sawRateLimit = true
	}
	return sawRateLimit
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
