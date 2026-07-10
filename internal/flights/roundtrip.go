package flights

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/flights/afklm"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// afklmNewProvider is the constructor used to opportunistically include
// AFKLM native round-trips in the default merge. It is overridden in tests
// to inject a pre-configured provider (via NewProviderWithClient) without
// depending on real credentials or network for credential resolution.
var afklmNewProvider = afklm.NewProvider

// afklmTestFlights is a test seam (prod code never sets it) allowing
// deterministic injection of AFKLM results for testing default-merge
// inclusion without cross-package client construction.
var afklmTestFlights []models.FlightResult

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

// searchNativeRoundTrip queries providers that price a return trip as a single
// native fare (Skiplagged today) and returns only genuine round-trip itineraries
// (FareRoundTrip, both legs present). It is rate-conscious: the composer has
// already suppressed the one-way Skiplagged leg searches via the passed-in ctx,
// so this is the *only* Skiplagged call for the whole round-trip search. Prices
// are normalised to target so they sort against the composed pairs like-for-like.
// A disabled or ineligible provider yields no results and no status, leaving the
// composition-only behaviour byte-unchanged.
func searchNativeRoundTrip(ctx context.Context, client *batchexec.Client, origin, destination, date, returnDate string, opts SearchOptions, target string) ([]models.FlightResult, []models.ProviderStatus) {
	if !skiplaggedSearchEligible(client, opts) {
		return nil, nil
	}

	rtOpts := opts
	rtOpts.ReturnDate = returnDate // request the native return fare (legs cleared it)

	result, err := SearchSkiplagged(ctx, origin, destination, date, rtOpts)
	if err != nil {
		return nil, []models.ProviderStatus{{
			ID:     "native_roundtrip:skiplagged",
			Name:   "Skiplagged (native round-trip)",
			Status: models.ClassifyProviderError(err),
			Error:  err.Error(),
		}}
	}

	var native []models.FlightResult
	if result != nil {
		for _, f := range result.Flights {
			// Keep only true round-trips. A one-way result here would duplicate
			// the outbound leg already covered by composition.
			if f.FareType == models.FareRoundTrip {
				native = append(native, f)
			}
		}
	}
	normalizeFlightCurrencies(ctx, native, target, destinations.ConvertCurrency)

	status := models.ProviderStatus{
		ID:      "native_roundtrip:skiplagged",
		Name:    "Skiplagged (native round-trip)",
		Status:  okOrNoHit(len(native)),
		Results: len(native),
	}
	if len(native) == 0 {
		status.FixHint = "no native round-trip fare returned; composed pairs used instead"
	}
	return native, []models.ProviderStatus{status}
}

// searchGoogleNativeRoundTrip queries Google Flights with a NATIVE round-trip
// request (tripType=1 with the outbound AND return segments in one call) and
// returns genuine round-trip itineraries. Google prices each option at the FULL
// round-trip total — a real return fare, frequently discounted versus two
// one-ways — so these outrank composed split-ticket pairs whenever cheaper.
//
// Google's round-trip flow is two-stage: stage 1 returns the outbound itinerary
// carrying the round-trip total; the specific matched return flight is chosen at
// booking (Google's stage-2 return-leg RPC is undocumented and even mature
// reverse-engineered clients punt on it). So each result is tagged FareRoundTrip
// with the outbound leg and a clarifying note, never a fabricated inbound leg —
// honest data: the genuine round-trip FARE, with the return selected at booking.
//
// Verified live (TestProbeNativeRoundTrip): the native request returns HTTP 200
// with a parseable flight array (74 itineraries on HEL->BCN). Issue #198 was a
// transient rate-limit, not a structural round-trip rejection; routing every
// round-trip to composition-only discarded these real fares.
func searchGoogleNativeRoundTrip(ctx context.Context, client *batchexec.Client, origin, destination, date, returnDate string, opts SearchOptions, target string) ([]models.FlightResult, []models.ProviderStatus) {
	rtOpts := opts
	rtOpts.ReturnDate = returnDate // request the native round-trip fare in one call
	rtOpts.FirstResult = false     // keep the full list so the cheapest can win

	result, err := searchGoogleFlightsWithClient(ctx, client, origin, destination, date, rtOpts)
	if err != nil {
		return nil, []models.ProviderStatus{{
			ID:     "native_roundtrip:google_flights",
			Name:   "Google Flights (native round-trip)",
			Status: models.ClassifyProviderError(err),
			Error:  err.Error(),
		}}
	}

	var native []models.FlightResult
	if result != nil {
		native = tagGoogleNativeRoundTrip(result.Flights, origin, destination, date, returnDate, opts.Currency)
	}
	normalizeFlightCurrencies(ctx, native, target, destinations.ConvertCurrency)

	status := models.ProviderStatus{
		ID:      "native_roundtrip:google_flights",
		Name:    "Google Flights (native round-trip)",
		Status:  okOrNoHit(len(native)),
		Results: len(native),
	}
	if len(native) == 0 {
		status.FixHint = "no native round-trip fare returned; composed pairs used instead"
	}
	return native, []models.ProviderStatus{status}
}

// tagGoogleNativeRoundTrip converts raw Google Flights itineraries from a native
// round-trip request into honest FareRoundTrip results: each carries the
// round-trip total as its price, its leg(s) tagged "outbound" (the matching
// return flight is selected at booking — Google's stage-2 return-leg RPC is
// undocumented), a round-trip booking URL, and a clarifying warning. It never
// mutates the source legs (taggedLegs copies) so cached one-way responses stay
// untouched. Pure and deterministic so it can be unit-tested without the network.
func tagGoogleNativeRoundTrip(flights []models.FlightResult, origin, destination, date, returnDate, currency string) []models.FlightResult {
	return tagNativeRoundTrip(flights, googleNativeRoundTripWarning, buildFlightBookingURL(origin, destination, date, returnDate, currency), date, returnDate)
}

const (
	googleNativeRoundTripWarning = "native Google round-trip fare: the price is the full round-trip total; the matching return flight is selected at booking"
	kiwiNativeRoundTripWarning   = "native Kiwi round-trip fare: the price is the full round-trip total; the matching return flight is selected at booking (open the booking link)"
)

// tagNativeRoundTrip converts raw itineraries from a provider's native
// round-trip request into results. Each flight keeps its price (the full
// round-trip total the provider quoted) and gets that round-trip booking URL
// (empty keeps the provider's own deep link).
//
// Leg directions are tagged by date using the real data returned; never
// fabricate a return leg. FareRoundTrip is applied ONLY when a real paired
// inbound leg is present in the response (hasInbound). Outbound-only shells
// ("return selected at booking") deliberately keep empty FareType so users
// are not misled. The warning is added only for the outbound-only case.
// Never mutates source legs. Pure/deterministic for tests.
func tagNativeRoundTrip(flights []models.FlightResult, warning, bookingURL, departDate, returnDate string) []models.FlightResult {
	if len(flights) == 0 {
		return nil
	}
	out := make([]models.FlightResult, 0, len(flights))
	for _, f := range flights {
		legs, hasInbound := directionTaggedLegs(f.Legs, departDate, returnDate)
		f.Legs = legs
		if hasInbound {
			f.FareType = models.FareRoundTrip
		}
		if bookingURL != "" {
			f.BookingURL = bookingURL
		}
		if !hasInbound && warning != "" {
			f.Warnings = append([]string{warning}, f.Warnings...)
		}
		out = append(out, f)
	}
	return out
}

// directionTaggedLegs returns a copy of legs with each leg's Direction set to
// "outbound"/"inbound" by comparing its departure date to returnDate: a leg
// departing on or after returnDate is the return half. The split only applies
// when returnDate is strictly after departDate. Empty returnDate, a same-day
// turn (returnDate==departDate, not splittable by date), inverted input
// (returnDate<departDate), and unparseable leg times all keep the leg
// "outbound" — the safe, non-fabricating default. hasInbound reports whether
// any inbound leg was found.
func directionTaggedLegs(legs []models.FlightLeg, departDate, returnDate string) (tagged []models.FlightLeg, hasInbound bool) {
	out := make([]models.FlightLeg, len(legs))
	copy(out, legs)
	// ISO calendar dates compare correctly as plain strings, so a strict
	// ">" rejects both same-day (==) and inverted (<) date pairs in one check.
	splittable := returnDate != "" && returnDate > departDate
	for i := range out {
		dir := "outbound"
		if splittable {
			// ISO calendar dates compare correctly as plain strings.
			if d, ok := legDepartDate(out[i]); ok && d >= returnDate {
				dir = "inbound"
				hasInbound = true
			}
		}
		out[i].Direction = dir
	}
	return out, hasInbound
}

// legDepartDate extracts the date prefix of a leg's departure time
// ("2026-07-22T18:40" -> "2026-07-22"), reporting false when the value is too
// short or not date-shaped so the caller falls back to the safe default.
func legDepartDate(leg models.FlightLeg) (string, bool) {
	t := leg.DepartureTime
	if len(t) < 10 || t[4] != '-' || t[7] != '-' {
		return "", false
	}
	return t[:10], true
}

// searchKiwiNativeRoundTrip queries Kiwi for a native round-trip fare (returnDate
// set) and returns honest FareRoundTrip itineraries. Verified live
// (TestProbeKiwiRoundTrip / TestProbeKiwiOneWayPrice): Kiwi returns the outbound
// leg priced at the full round-trip total — HEL->BCN one-way is ~102 EUR, the
// round-trip request's cheapest is ~296 EUR — with the matching return chosen at
// booking via the Kiwi deep link. kiwiEligibleOptions wrongly rejects ReturnDate
// as "unsupported" (the same false premise that hid Google's native round-trip),
// so this bypasses that one check while still honouring Kiwi's real option limits
// (airline/alliance/baggage filters) and the production shared-client guard.
func searchKiwiNativeRoundTrip(ctx context.Context, client *batchexec.Client, origin, destination, date, returnDate string, opts SearchOptions, target string) ([]models.FlightResult, []models.ProviderStatus) {
	// Eligibility minus the (incorrect) ReturnDate exclusion: respect Kiwi's
	// genuine filter limits and the shared-client guard, but allow round-trips.
	probe := opts
	probe.ReturnDate = ""
	if !kiwiSearchEligible(client, probe) {
		return nil, nil // unsupported filters / non-shared client — composition covers it
	}

	rtOpts := opts
	rtOpts.ReturnDate = returnDate
	currency := opts.Currency
	if currency == "" {
		currency = "EUR"
	}

	flights, err := SearchKiwiFlights(ctx, origin, destination, date, currency, rtOpts)
	if err != nil {
		return nil, []models.ProviderStatus{{
			ID:     "native_roundtrip:kiwi",
			Name:   "Kiwi (native round-trip)",
			Status: models.ClassifyProviderError(err),
			Error:  err.Error(),
		}}
	}

	// Keep Kiwi's own deep link (bookingURL=="") — it already encodes the return.
	native := tagNativeRoundTrip(flights, kiwiNativeRoundTripWarning, "", date, returnDate)
	normalizeFlightCurrencies(ctx, native, target, destinations.ConvertCurrency)

	status := models.ProviderStatus{
		ID:      "native_roundtrip:kiwi",
		Name:    "Kiwi (native round-trip)",
		Status:  okOrNoHit(len(native)),
		Results: len(native),
	}
	if len(native) == 0 {
		status.FixHint = "no native round-trip fare returned; composed pairs used instead"
	}
	return native, []models.ProviderStatus{status}
}

// searchAFKLMNativeRoundTrip attempts to include AFKLM native round-trips in
// the default merge. It reuses afklm.NewProvider (overridable) to detect
// credential presence via ErrNoCredential — no new auth code. On no credential
// it returns immediately with zero results/status (silent). Search errors are
// best-effort (status recorded but search continues). AFKLM always returns
// full both-leg round-trips when ReturnDate is supplied.
func searchAFKLMNativeRoundTrip(ctx context.Context, origin, destination, date, returnDate string, opts SearchOptions) ([]models.FlightResult, []models.ProviderStatus) {
	// test seam: synthetic results (used by unit tests for deterministic AFKLM merge coverage)
	if len(afklmTestFlights) > 0 {
		native := append([]models.FlightResult(nil), afklmTestFlights...)
		target := nativeRoundTripCurrency(opts, nil, nil)
		normalizeFlightCurrencies(ctx, native, target, destinations.ConvertCurrency)
		return native, []models.ProviderStatus{{
			ID: "native_roundtrip:afklm", Name: "AFKLM (native round-trip)", Status: models.StatusOK, Results: len(native),
		}}
	}

	p, err := afklmNewProvider()
	if errors.Is(err, afklm.ErrNoCredential) {
		return nil, nil // silent, zero latency, zero user signal
	}
	if err != nil {
		slog.Debug("afklm: NewProvider error (non-ErrNoCredential); skipping default merge inclusion", "err", err)
		return nil, nil
	}

	res, err := p.SearchFlights(ctx, origin, destination, date, models.FlightSearchOptions{
		ReturnDate: returnDate,
		CabinClass: opts.CabinClass,
		MaxStops:   opts.MaxStops,
		SortBy:     opts.SortBy,
		Airlines:   opts.Airlines,
		Adults:     opts.Adults,
		Currency:   opts.Currency,
	})
	if err != nil {
		slog.Debug("afklm: search error in default merge (best-effort; does not fail search)", "err", err)
		return nil, []models.ProviderStatus{{
			ID:     "native_roundtrip:afklm",
			Name:   "AFKLM (native round-trip)",
			Status: models.ClassifyProviderError(err),
			Error:  err.Error(),
		}}
	}
	if res == nil || !res.Success || len(res.Flights) == 0 {
		if res != nil && res.Error != "" {
			slog.Debug("afklm: soft error from provider", "afklm_error", res.Error)
		}
		return nil, nil
	}

	native := append([]models.FlightResult(nil), res.Flights...)
	target := nativeRoundTripCurrency(opts, nil, nil)
	normalizeFlightCurrencies(ctx, native, target, destinations.ConvertCurrency)

	st := models.ProviderStatus{
		ID:      "native_roundtrip:afklm",
		Name:    "AFKLM (native round-trip)",
		Status:  okOrNoHit(len(native)),
		Results: len(native),
	}
	return native, []models.ProviderStatus{st}
}

// nativeRoundTripCurrency picks the currency to normalise native round-trip fares
// to, so they rank against the composed pairs in one currency: the caller's
// explicit currency if set, else the currency the composed pairs are already in,
// else the outbound legs' currency. Empty (no signal) lets normalisation no-op.
func nativeRoundTripCurrency(opts SearchOptions, composed, outbound []models.FlightResult) string {
	if opts.Currency != "" {
		return opts.Currency
	}
	if len(composed) > 0 && composed[0].Currency != "" {
		return composed[0].Currency
	}
	if len(outbound) > 0 {
		return outbound[0].Currency
	}
	return ""
}

// roundTripNativeReturnCandidates bounds how many native round-trip fares get
// their return leg enriched with real inbound detail. Native fares that come
// back outbound-only ("return selected at booking") are enriched cheapest-first
// and only the top N; the rest keep their honest booking-time note. The cap is
// reported in provider status so coverage is never silently dropped.
const roundTripNativeReturnCandidates = 3

// flightHasInboundLeg reports whether a result already carries a tagged return
// leg, so enrichment skips native fares that genuinely returned both halves.
func flightHasInboundLeg(f models.FlightResult) bool {
	for _, leg := range f.Legs {
		if leg.Direction == "inbound" {
			return true
		}
	}
	return false
}

// enrichNativeReturnLegs fills in real return-leg detail for native round-trip
// fares that came back outbound-only (the "return selected at booking" case).
// It REUSES the inbound one-way results the composer already fetched (D->O on
// the return date) — no extra network — attaching a real inbound itinerary's
// legs to each of the top-N cheapest such native fares so downstream per-leg
// condition filters (carrier, stops, times, aircraft) apply to the return half
// instead of an empty outbound-only result.
//
// The native round-trip TOTAL stays the headline price: it is the single
// bookable fare and already includes the return, so the inbound one-way price
// is NOT summed in (that would double-count). Each enriched fare gets an honest
// warning naming the real return shown and its indicative standalone price, and
// noting the confirmed return is chosen at booking and may differ; the
// booking-time-only warning is dropped once a real return is shown.
//
// Bounded + logged: only n fares are enriched; the returned status reports how
// many were enriched vs left capped. With no inbound one-ways fetched this is a
// no-op and native fares keep their existing warning (the provider could not
// price the return per-leg without a booking session; composition covers
// per-leg detail when it found inbound options). Never mutates inbound source
// legs (taggedLegs copies), and one-way searches never reach this path.
func enrichNativeReturnLegs(native, inbound []models.FlightResult, n int) ([]models.FlightResult, models.ProviderStatus) {
	status := models.ProviderStatus{
		ID:   "native_roundtrip_return_enrich",
		Name: "Native round-trip return enrichment",
	}

	returns := topPricedFlights(inbound, n)
	if len(returns) == 0 {
		status.Status = models.StatusCheckedNoHit
		status.FixHint = "no inbound one-way option fetched; native fares keep 'return selected at booking'"
		return native, status
	}

	// Index native fares cheapest-first so the top-N enriched are the most
	// likely to surface; pair each enriched fare with a distinct real return
	// where available (clamped to the cheapest when fewer returns than fares).
	order := make([]int, 0, len(native))
	for i := range native {
		order = append(order, i)
	}
	sort.SliceStable(order, func(a, b int) bool {
		return compareFlightPrices(native[order[a]].PriceForRanking(), native[order[b]].PriceForRanking()) < 0
	})

	out := make([]models.FlightResult, len(native))
	copy(out, native)

	enriched, eligible := 0, 0
	for _, idx := range order {
		f := out[idx]
		if flightHasInboundLeg(f) {
			continue // already two-legged (full native RT or post-enrich)
		}
		// Only enrich outbound-only *native round-trip shells* (Google/Kiwi
		// "return selected at booking"). These carry the specific booking-time
		// warning set by tagNativeRoundTrip. Plain one-ways (e.g. from
		// composition) and full fares do not; they must stay untouched.
		// (AFKLM always returns both legs for round-trips.)
		isNativeOutboundShell := false
		for _, w := range f.Warnings {
			if w == googleNativeRoundTripWarning || w == kiwiNativeRoundTripWarning {
				isNativeOutboundShell = true
				break
			}
		}
		if !isNativeOutboundShell {
			continue
		}
		// We no longer require FareRoundTrip tag here because tagNativeRoundTrip
		// now sets it only for responses that already carry a real paired leg
		// (or we set it here after attaching one).
		eligible++
		if enriched >= n {
			continue // capped — reported below, never silently dropped
		}
		out[idx] = attachReturnLeg(f, returns[min(enriched, len(returns)-1)])
		enriched++
	}

	status.Status = okOrNoHit(enriched)
	status.Results = enriched
	switch {
	case eligible == 0:
		status.FixHint = "no outbound-only native fare to enrich (all carried both legs)"
	case eligible > enriched:
		status.FixHint = fmt.Sprintf("enriched cheapest %d of %d outbound-only native fares with real return-leg detail; the rest keep 'return selected at booking' (search one-way D->O for the full return list)", enriched, eligible)
	default:
		status.FixHint = fmt.Sprintf("enriched all %d outbound-only native fares with real return-leg detail", enriched)
	}
	return out, status
}

// attachReturnLeg returns a copy of an outbound-only native round-trip fare with
// the real inbound itinerary's legs appended (tagged "inbound"), the now-false
// booking-time warning removed, and an honest warning recording the indicative
// return shown. The native TOTAL price is unchanged. Inbound source legs are
// copied (taggedLegs), never mutated.
func attachReturnLeg(f, ret models.FlightResult) models.FlightResult {
	legs := make([]models.FlightLeg, 0, len(f.Legs)+len(ret.Legs))
	legs = append(legs, f.Legs...)
	legs = append(legs, taggedLegs(ret.Legs, "inbound")...)
	f.Legs = legs

	// Now that a real paired return leg is attached, tag as genuine round-trip fare.
	f.FareType = models.FareRoundTrip

	provider := ret.Provider
	if provider == "" {
		provider = "unspecified carrier"
	}
	price := ""
	if ret.Price > 0 && ret.Currency != "" {
		price = fmt.Sprintf(", ~%.0f %s one-way", ret.Price, ret.Currency)
	}
	warn := fmt.Sprintf("return leg shown is a real return option via %s%s; the native fare's confirmed return is selected at booking and may differ", provider, price)

	warnings := make([]string, 0, len(f.Warnings)+1)
	warnings = append(warnings, warn)
	for _, w := range f.Warnings {
		// A real return is now shown, so the booking-time-only note is misleading.
		if w == googleNativeRoundTripWarning || w == kiwiNativeRoundTripWarning {
			continue
		}
		warnings = append(warnings, w)
	}
	f.Warnings = warnings
	return f
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
