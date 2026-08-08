package flights

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/MikkoParkkola/trvl/internal/logredact"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/flights/afklm"
	"github.com/MikkoParkkola/trvl/internal/models"
)

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
	googleNativeRoundTripWarning = "native Google round-trip fare: the price is the full round-trip total; the matching return flight is selected at booking"                       // #nosec G101 -- user-facing warning text
	kiwiNativeRoundTripWarning   = "native Kiwi round-trip fare: the price is the full round-trip total; the matching return flight is selected at booking (open the booking link)" // #nosec G101 -- user-facing warning text
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
	if len(opts.afklmTestFlights) > 0 {
		native := append([]models.FlightResult(nil), opts.afklmTestFlights...)
		target := nativeRoundTripCurrency(opts, nil, nil)
		normalizeFlightCurrencies(ctx, native, target, destinations.ConvertCurrency)
		return native, []models.ProviderStatus{{
			ID: "native_roundtrip:afklm", Name: "AFKLM (native round-trip)", Status: models.StatusOK, Results: len(native),
		}}
	}

	// Fanout suppression (set by SearchMultiAirport on sub-searches): skip AFKLM
	// so its 1-QPS/100-req-day quota is spent at most once per logical search.
	if opts.suppressAFKLM {
		return nil, nil
	}

	newProvider := opts.afklmNewProvider
	if newProvider == nil {
		// PolicyEnvOnly: this is the DEFAULT merge, which the user did not ask
		// for. It reads AFKLM_KEY and nothing else — no Keychain, no 1Password,
		// no subprocess of any kind. An opportunistic provider must not be able
		// to block a search or surface a credential prompt (#507). Users who
		// keep the key in an external store opt in with `--provider afklm`.
		newProvider = func() (*afklm.AFKLMProvider, error) {
			return afklm.NewProvider(ctx, afklm.PolicyEnvOnly)
		}
	}
	p, err := newProvider()
	if errors.Is(err, afklm.ErrNoCredential) {
		// Silent for the user who never enabled AF-KLM: a status line about a
		// provider they have not heard of is noise on every search.
		//
		// Not silent for the user who did enable it. Someone with AFKLM_OP_REF
		// set has told trvl where their key lives, and the default path
		// deliberately will not read it, so AF-KLM goes missing from results
		// they expect it in. Saying nothing there would look like the provider
		// had broken.
		if hint := afklm.DefaultPathSkipHint(); hint != "" {
			return nil, []models.ProviderStatus{{
				ID:      "native_roundtrip:afklm",
				Name:    "AFKLM (native round-trip)",
				Status:  models.StatusNotConfigured,
				FixHint: hint,
			}}
		}
		return nil, nil // silent, zero latency, zero user signal
	}
	if err != nil {
		slog.Debug("afklm: NewProvider error (non-ErrNoCredential); skipping default merge inclusion", "err", logredact.Err(err))
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
	if errors.Is(err, afklm.ErrDailyQuota) {
		slog.Debug("afklm: daily quota reached; skipping default merge inclusion")
		return nil, nil
	}
	if err != nil {
		slog.Debug("afklm: search error in default merge (best-effort; does not fail search)", "err", logredact.Err(err))
		return nil, []models.ProviderStatus{{
			ID:     "native_roundtrip:afklm",
			Name:   "AFKLM (native round-trip)",
			Status: models.ClassifyProviderError(err),
			Error:  err.Error(),
		}}
	}
	if res == nil || !res.Success || len(res.Flights) == 0 {
		if res != nil && res.Error != "" {
			slog.Debug("afklm: soft error from provider", "afklm_error", logredact.Text(res.Error))
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

// retainCompliantNativeRoundTrip truncates a merged round-trip candidate list
// (already sorted cheapest-first by the caller) to at most max entries, but
// guarantees the cheapest window-compliant native round-trip survives the cut
// even when it would otherwise fall beyond the price cutoff.
//
// This addresses #472: the pre-filter truncation runs BEFORE the departure-time
// window filter. When the cheapest native round-trips violate the window and the
// only compliant native fare is pricier (beyond the cutoff), truncation discards
// it, so the later window filter drops the cheap non-compliant fares and leaves
// only composed multi-stops — the user loses the preferred-carrier product. By
// retaining the cheapest compliant native fare here, that fare is still present
// for the window filter to keep and rank.
//
// Compliance uses the same directional-departure window check as the post-search
// filter (earliest/latest are "HH:MM"; an empty bound means no constraint on that
// side). When the window is unset, no native fare is compliant, nothing is
// truncated, or a compliant native fare already survives, this is a plain
// cheapest-max truncation. The retained fare displaces the most expensive kept
// slot, then the kept slice is re-sorted cheapest-first so the returned list
// stays in the same sorted order the caller (and downstream ranking/truncation)
// expects — a raw swap into the last slot would otherwise leave the displaced
// fare out of price order. The input slice is not mutated.
func retainCompliantNativeRoundTrip(merged []models.FlightResult, earliest, latest string, max int) []models.FlightResult {
	if max <= 0 || len(merged) <= max {
		return merged
	}
	head := merged[:max]
	if earliest == "" && latest == "" {
		return head
	}
	for _, f := range head {
		if isCompliantNativeRoundTrip(f, earliest, latest) {
			return head // a compliant native fare already survives
		}
	}
	for i := max; i < len(merged); i++ {
		if isCompliantNativeRoundTrip(merged[i], earliest, latest) {
			out := make([]models.FlightResult, max)
			copy(out, head)
			out[max-1] = merged[i] // displace the most expensive kept slot
			sort.SliceStable(out, func(a, b int) bool {
				return compareFlightPrices(out[a].PriceForRanking(), out[b].PriceForRanking()) < 0
			})
			return out
		}
	}
	return head
}

// isCompliantNativeRoundTrip reports whether f is a genuine native round-trip
// fare (FareRoundTrip) whose directional departures all fall inside the window.
func isCompliantNativeRoundTrip(f models.FlightResult, earliest, latest string) bool {
	return f.FareType == models.FareRoundTrip && directionalDeparturesInWindow(f, earliest, latest)
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
