package flights

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/cache"
	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/fareintel"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/searchctx"
	"golang.org/x/sync/singleflight"
)

// flightGroup deduplicates concurrent in-flight searches with identical parameters.
// Only truly concurrent requests are coalesced; the existing cache layer handles
// TTL-based reuse between sequential calls.
var flightGroup singleflight.Group

const sharedFlightSearchTimeout = 30 * time.Second

// DefaultClient returns a shared batchexec.Client for the flights package.
// The client is created once and reused across all requests, enabling
// connection reuse and shared rate limiting.
func DefaultClient() *batchexec.Client {
	return batchexec.SharedClient()
}

// SearchOptions configures a flight search.
type SearchOptions struct {
	ReturnDate string            // Return date for round-trip (YYYY-MM-DD); empty = one-way
	CabinClass models.CabinClass // Cabin class (default: Economy)
	MaxStops   models.MaxStops   // Maximum stops filter
	SortBy     models.SortBy     // Result sort order
	Airlines   []string          // Restrict to these airline IATA codes
	Adults     int               // Number of adult passengers (default: 1)

	// DepartureFlexDays / ReturnFlexDays enable Kiwi's flexible-date search
	// (cheapest within +/- N days). Range 0-3; values are clamped. 0 = exact.
	DepartureFlexDays int
	ReturnFlexDays    int

	// Currency forces Google Flights to return prices in this currency by
	// setting the gl= (geolocation) query parameter. When empty, no gl= is
	// added and Google uses IP-based geolocation (which may return RUB, PLN,
	// etc. depending on the user's IP). This is used by the optimizer to
	// ensure all candidates are priced in the same currency (EUR).
	Currency string

	// Server-side filters passed to Google Flights batchexecute.
	MaxPrice    int // Max price in whole currency units (0 = no limit)
	MaxDuration int // Max total flight duration in minutes (0 = no limit)
	CarryOnBags int // Carry-on bags filter (0 = no filter, 1+ = require N carry-on bags included)
	CheckedBags int // Checked bags filter (0 = no filter, 1+ = require N checked bags included)
	// Wire format at outer[1][10] is []any{carryOn, checked} — verified via live probe.
	// Scalar int returns 400 Bad Request; array is required.
	ExcludeBasic  bool     // Exclude basic economy fares
	Alliances     []string // Alliance filter; e.g. ["STAR_ALLIANCE", "ONEWORLD", "SKYTEAM"]
	DepartAfter   string   // Earliest departure time "HH:MM" (e.g. "06:00")
	DepartBefore  string   // Latest departure time "HH:MM" (e.g. "22:00")
	LessEmissions bool     // Only show flights with less emissions

	// Client-side post-filters (applied after server response).
	RequireCheckedBag bool // Only show flights with ≥1 free checked bag
	FirstResult       bool // Return only the first flight with Price > 0 after sorting

	// NoHacks opts out of the auto-composed travel-hacks savings engine. The
	// engine is ON by default: a normal search also surfaces the single best
	// cheaper synthesized option (hidden-city, positioning, split, multimodal,
	// …) in result.HackSaving. Set NoHacks to run a pure naive search.
	NoHacks bool

	// SuppressedHacks lists categories (from profile) to avoid injecting as
	// bookable candidates. Checked in post-policy; advisory path may still run.
	SuppressedHacks []string

	// Stealth opts into the operator-authorized stealth transport for the
	// Google Flights fetch. It is DEFAULT OFF (zero value). Even when true,
	// stealth activates ONLY for a request whose host is on the operator
	// allowlist (TRVL_STEALTH_ALLOWLIST); a requested-but-unauthorized host
	// runs the normal path and logs a single refusal line. See internal/stealth.
	Stealth bool
}

// defaults fills in zero-value fields with sensible defaults.
func (o *SearchOptions) defaults() {
	if o.Adults <= 0 {
		o.Adults = 1
	}
	if o.CabinClass == 0 {
		o.CabinClass = models.Economy
	}
}

// SearchFlights searches for flights from origin to destination on the given date.
//
// origin and destination are IATA airport codes (e.g. "HEL", "NRT").
// date is the departure date as "YYYY-MM-DD".
//
// Returns a FlightSearchResult with parsed flight options, or an error.
// Uses a shared default client for connection reuse and rate limiting.
func SearchFlights(ctx context.Context, origin, destination, date string, opts SearchOptions) (*models.FlightSearchResult, error) {
	return SearchFlightsWithClient(ctx, DefaultClient(), origin, destination, date, opts)
}

// flightSearchKey builds a singleflight dedup key from the search parameters.
func flightSearchKey(origin, destination, date string, opts SearchOptions) string {
	return fmt.Sprintf("flight|%s|%s|%s|%s|%d|%d|%d|%d|%d|%d|%d|%d|%s|%s|%v|%v|%v|%s|%s|%s",
		origin, destination, date, opts.ReturnDate,
		opts.CabinClass, opts.MaxStops, opts.SortBy, opts.Adults,
		opts.MaxPrice, opts.MaxDuration, opts.CarryOnBags, opts.CheckedBags,
		opts.Currency, canonicalStringSlice(opts.Airlines),
		opts.ExcludeBasic, opts.LessEmissions, opts.RequireCheckedBag,
		canonicalStringSlice(opts.Alliances), opts.DepartAfter, opts.DepartBefore,
	)
}

func canonicalStringSlice(values []string) string {
	if len(values) == 0 {
		return ""
	}
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// SearchFlightsWithClient is like SearchFlights but accepts a pre-built client,
// useful for reusing connections across multiple requests.
func SearchFlightsWithClient(ctx context.Context, client *batchexec.Client, origin, destination, date string, opts SearchOptions) (*models.FlightSearchResult, error) {
	opts.defaults()

	if origin == "" || destination == "" || date == "" {
		return &models.FlightSearchResult{
			Error: "origin, destination, and date are required",
		}, fmt.Errorf("origin, destination, and date are required")
	}
	if client == nil {
		return &models.FlightSearchResult{
			Error: "client is required",
		}, fmt.Errorf("client is required")
	}

	// Round-trip fans out to native round-trip fares (Google, Skiplagged — priced
	// as a single, often discounted return) AND composition (two one-way searches
	// paired, covering cross-carrier combos), merged cheapest-first. The native
	// Google path is queried inside searchRoundTripComposed; the composition leg
	// sub-searches re-enter this function with ReturnDate cleared (one-way path).
	if opts.ReturnDate != "" {
		return searchRoundTripComposed(ctx, client, origin, destination, date, opts)
	}

	key := flightSearchKey(origin, destination, date, opts)
	result, err := doFlightSearchSingleflight(ctx, key, func(sharedCtx context.Context) (*models.FlightSearchResult, error) {
		return searchFlightsCore(sharedCtx, client, origin, destination, date, opts)
	})

	// Speculative prefetch: warm the cache for the likely next query (return leg
	// and +/-1 day around this date). Best-effort and non-blocking — it never
	// affects the result we just computed. Disabled inside prefetch-spawned and
	// round-trip leg calls so it cannot recurse.
	maybePrefetchFlights(ctx, client, origin, destination, date, opts)

	return result, err
}

// --- Negative-result cache + speculative prefetch (innovation #4) ---------

// flightNegCacheTTL bounds how long a clean "no service" from a point-to-point
// carrier suppresses re-querying that carrier for the same route+month. Short
// enough that a newly opened route surfaces within half an hour.
const flightNegCacheTTL = 30 * time.Minute

// flightNegCache records (carrier, origin, destination, month) tuples that a
// direct point-to-point carrier reported as genuinely unserved. Only the direct
// carriers (Ryanair, Wizz Air, Transavia, easyJet) participate: a clean empty
// from them is a route-structural fact (they don't fly A->B), unlike an
// aggregator's empty result which is usually date-specific (sold out). On by
// default; set TRVL_NEGCACHE=0 to disable.
var flightNegCache = cache.NewNegativeCache(flightNegCacheTTL)

func negCacheEnabled() bool {
	return envBool("TRVL_NEGCACHE", true)
}

// flightPrefetchEnabled gates speculative prefetch. Off by default: prefetch
// adds provider load (extra background searches), so it is opt-in via
// TRVL_PREFETCH=1.
func flightPrefetchEnabled() bool {
	return envBool("TRVL_PREFETCH", false)
}

// envBool parses a boolean env var, returning def when unset / unparseable.
func envBool(name string, def bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return v
}

// withFlightNegCache wraps a point-to-point carrier's run function with the
// negative-result cache. On a recent clean "no service" it returns a synthetic
// checked_no_hit outcome without re-querying. Otherwise it runs the provider and
// marks the route negative only on a CLEAN no-result (succeeded with zero
// flights). An error / timeout / rate-limit / skip is never marked.
func withFlightNegCache(provider, origin, destination, date string, run func(context.Context) providerOutcome) func(context.Context) providerOutcome {
	key := cache.NegativeKey(provider, origin, destination, date)
	return func(ctx context.Context) providerOutcome {
		if negCacheEnabled() && flightNegCache.Seen(key) {
			return providerOutcome{
				succeeded: true,
				status: models.ProviderStatus{
					ID:      provider,
					Name:    provider,
					Status:  models.StatusCheckedNoHit,
					Results: 0,
				},
			}
		}
		out := run(ctx)
		if negCacheEnabled() && out.succeeded && len(out.flights) == 0 {
			flightNegCache.Mark(key)
		}
		return out
	}
}

// prefetchCtxKey marks a context as belonging to a prefetch (also any nested)
// search so speculative prefetch does not recurse.
type prefetchCtxKey struct{}

func prefetchDisabled(ctx context.Context) bool {
	return ctx.Value(prefetchCtxKey{}) != nil
}

// flightPrefetchTarget is one likely-next one-way search to warm.
type flightPrefetchTarget struct {
	origin      string
	destination string
	date        string
}

// flightPrefetchTargets computes the likely-next one-way searches to warm given
// a just-completed one-way search: the return leg (reverse route, +7 days) plus
// the two adjacent days (date plus one, date minus one) on the same route. Pure
// and deterministic; targets that cannot be derived (unparseable date) are
// omitted.
func flightPrefetchTargets(origin, destination, date string) []flightPrefetchTarget {
	var targets []flightPrefetchTarget
	t, err := models.ParseDate(date)
	if err != nil {
		return targets
	}
	plus1 := t.AddDate(0, 0, 1).Format("2006-01-02")
	minus1 := t.AddDate(0, 0, -1).Format("2006-01-02")
	ret := t.AddDate(0, 0, 7).Format("2006-01-02")
	targets = append(targets,
		flightPrefetchTarget{origin: destination, destination: origin, date: ret}, // return leg
		flightPrefetchTarget{origin: origin, destination: destination, date: plus1},
		flightPrefetchTarget{origin: origin, destination: destination, date: minus1},
	)
	return targets
}

// flightPrefetchConcurrency bounds simultaneous prefetch searches so warming
// never opens an unbounded number of upstream connections.
const flightPrefetchConcurrency = 2

// flightPrefetchFn performs a single warm search. It is a seam so tests can
// observe prefetch without hitting the network. The default warms the shared
// client cache via the normal one-way search path, with prefetch disabled on the
// detached context to prevent recursion. Assigned in init to break the static
// initialization cycle with SearchFlightsWithClient.
var flightPrefetchFn func(ctx context.Context, client *batchexec.Client, t flightPrefetchTarget, opts SearchOptions)

func init() {
	flightPrefetchFn = func(ctx context.Context, client *batchexec.Client, t flightPrefetchTarget, opts SearchOptions) {
		_, _ = SearchFlightsWithClient(ctx, client, t.origin, t.destination, t.date, opts)
	}
}

// maybePrefetchFlights dispatches best-effort speculative prefetch. It returns
// immediately; warming runs in the background where a prefetch failure (panic
// included) can never affect the primary search.
func maybePrefetchFlights(ctx context.Context, client *batchexec.Client, origin, destination, date string, opts SearchOptions) {
	if !flightPrefetchEnabled() || prefetchDisabled(ctx) || opts.ReturnDate != "" {
		return
	}
	targets := flightPrefetchTargets(origin, destination, date)
	if len(targets) == 0 {
		return
	}

	// Warm one-way legs only, without the heavy hacks engine: prefetch fills the
	// cache, it does not need to compute savings.
	warmOpts := opts
	warmOpts.ReturnDate = ""
	warmOpts.NoHacks = true

	go runFlightPrefetch(ctx, client, targets, warmOpts)
}

// runFlightPrefetch warms the given targets with bounded concurrency. Each warm
// is isolated: a panic in one never affects its peers nor the caller. Kept as a
// single unexported function so tests can drive it directly with a fake
// flightPrefetchFn.
func runFlightPrefetch(parent context.Context, client *batchexec.Client, targets []flightPrefetchTarget, opts SearchOptions) {
	detached, cancel := searchctx.DetachedWithin(parent, sharedFlightSearchTimeout)
	defer cancel()
	prefetchCtx := context.WithValue(detached, prefetchCtxKey{}, struct{}{})

	sem := make(chan struct{}, flightPrefetchConcurrency)
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(t flightPrefetchTarget) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() { _ = recover() }() // best-effort: never propagate
			flightPrefetchFn(prefetchCtx, client, t, opts)
		}(t)
	}
	wg.Wait()
}

func doFlightSearchSingleflight(ctx context.Context, key string, fn func(context.Context) (*models.FlightSearchResult, error)) (*models.FlightSearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	ch := flightGroup.DoChan(key, func() (any, error) {
		sharedCtx, cancel := searchctx.DetachedWithin(ctx, sharedFlightSearchTimeout)
		defer cancel()
		return fn(sharedCtx)
	})

	select {
	case <-ctx.Done():
		// The first caller may time out before the shared worker returns and
		// singleflight evicts the key. Forget it now so a fresh caller does not
		// attach to an already-doomed execution.
		flightGroup.Forget(key)
		return nil, ctx.Err()
	case res := <-ch:
		return sharedFlightResult(res.Val, res.Err)
	}
}

// searchFlightsCore performs the actual flight search without singleflight wrapping.
func searchFlightsCore(ctx context.Context, client *batchexec.Client, origin, destination, date string, opts SearchOptions) (*models.FlightSearchResult, error) {
	googleResult, googleErr := searchGoogleFlightsWithClient(ctx, client, origin, destination, date, opts)
	googleSucceeded := googleErr == nil
	currency := flightSearchCurrency(googleResult)

	var googleFlights []models.FlightResult
	if googleSucceeded && googleResult != nil {
		googleFlights = googleResult.Flights
	}

	googleStatus := models.ProviderStatus{ID: "google_flights", Name: "Google Flights"}
	if googleSucceeded {
		googleStatus.Status = okOrNoHit(len(googleFlights))
		googleStatus.Results = len(googleFlights)
	} else {
		googleStatus.Status = models.ClassifyProviderError(googleErr)
		googleStatus.Error = googleErr.Error()
	}

	// Secondary providers depend only on the session currency (derived from the
	// Google result above), so they run concurrently with bounded parallelism,
	// per-provider timeout, and failure isolation. Outcomes are returned in task
	// order so the merged status list and downstream merge stay deterministic
	// regardless of completion order.
	tasks := []providerTask{
		{name: "kiwi", run: func(ctx context.Context) providerOutcome {
			return runKiwiProvider(ctx, client, origin, destination, date, currency, opts)
		}},
		{name: "skiplagged", run: func(ctx context.Context) providerOutcome {
			return runSkiplaggedProvider(ctx, client, origin, destination, date, opts)
		}},
		{name: "ryanair", run: withFlightNegCache("ryanair", origin, destination, date, func(ctx context.Context) providerOutcome {
			return runRyanairProvider(ctx, client, origin, destination, date, currency, opts)
		})},
		{name: "wizzair", run: withFlightNegCache("wizzair", origin, destination, date, func(ctx context.Context) providerOutcome {
			return runWizzairProvider(ctx, client, origin, destination, date, currency, opts)
		})},
		{name: "transavia", run: withFlightNegCache("transavia", origin, destination, date, func(ctx context.Context) providerOutcome {
			return runTransaviaProvider(ctx, client, origin, destination, date, currency, opts)
		})},
		{name: "easyjet", run: withFlightNegCache("easyjet", origin, destination, date, func(ctx context.Context) providerOutcome {
			return runEasyjetProvider(ctx, client, origin, destination, date, currency, opts)
		})},
		{name: "vueling", run: withFlightNegCache("vueling", origin, destination, date, func(ctx context.Context) providerOutcome {
			return runVuelingProvider(ctx, client, origin, destination, date, currency, opts)
		})},
		{name: "norwegian", run: withFlightNegCache("norwegian", origin, destination, date, func(ctx context.Context) providerOutcome {
			return runNorwegianProvider(ctx, client, origin, destination, date, currency, opts)
		})},
	}
	outcomes := runProviderTasks(ctx, tasks, providerConcurrencyLimit, perProviderTimeout)
	kiwiOut := outcomes[0]
	skiplaggedOut := outcomes[1]
	ryanairOut := outcomes[2]
	wizzairOut := outcomes[3]
	transaviaOut := outcomes[4]
	easyjetOut := outcomes[5]
	vuelingOut := outcomes[6]
	norwegianOut := outcomes[7]

	statuses := []models.ProviderStatus{
		googleStatus,
		kiwiOut.status,
		skiplaggedOut.status,
		ryanairOut.status,
		wizzairOut.status,
		transaviaOut.status,
		easyjetOut.status,
		vuelingOut.status,
		norwegianOut.status,
	}

	kiwiFlights := kiwiOut.flights
	kiwiErr := kiwiOut.err
	kiwiSucceeded := kiwiOut.succeeded
	skiplaggedFlights := skiplaggedOut.flights
	skiplaggedErr := skiplaggedOut.err
	skiplaggedSucceeded := skiplaggedOut.succeeded
	ryanairFlights := ryanairOut.flights
	ryanairErr := ryanairOut.err
	ryanairSucceeded := ryanairOut.succeeded
	wizzairFlights := wizzairOut.flights
	wizzairErr := wizzairOut.err
	wizzairSucceeded := wizzairOut.succeeded
	transaviaFlights := transaviaOut.flights
	transaviaErr := transaviaOut.err
	transaviaSucceeded := transaviaOut.succeeded
	easyjetFlights := easyjetOut.flights
	easyjetErr := easyjetOut.err
	easyjetSucceeded := easyjetOut.succeeded
	vuelingFlights := vuelingOut.flights
	vuelingErr := vuelingOut.err
	vuelingSucceeded := vuelingOut.succeeded
	norwegianFlights := norwegianOut.flights
	norwegianErr := norwegianOut.err
	norwegianSucceeded := norwegianOut.succeeded

	// Normalize all provider prices to the session currency so resolution and
	// ranking compare like with like (Skiplagged returns USD, Kiwi its own).
	// Best-effort and offline-safe: same-currency conversions never hit the net.
	target := currency
	if opts.Currency != "" {
		target = opts.Currency
	}
	normalizeFlightCurrencies(ctx, googleFlights, target, destinations.ConvertCurrency)
	normalizeFlightCurrencies(ctx, kiwiFlights, target, destinations.ConvertCurrency)
	normalizeFlightCurrencies(ctx, skiplaggedFlights, target, destinations.ConvertCurrency)
	normalizeFlightCurrencies(ctx, ryanairFlights, target, destinations.ConvertCurrency)
	normalizeFlightCurrencies(ctx, wizzairFlights, target, destinations.ConvertCurrency)
	normalizeFlightCurrencies(ctx, transaviaFlights, target, destinations.ConvertCurrency)
	normalizeFlightCurrencies(ctx, easyjetFlights, target, destinations.ConvertCurrency)
	normalizeFlightCurrencies(ctx, vuelingFlights, target, destinations.ConvertCurrency)
	normalizeFlightCurrencies(ctx, norwegianFlights, target, destinations.ConvertCurrency)

	mergedFlights := mergeFlightResults(googleFlights, kiwiFlights, skiplaggedFlights, opts, ryanairFlights, wizzairFlights, transaviaFlights, easyjetFlights, vuelingFlights, norwegianFlights)

	// Cross-shop re-pricing (MIK-4956 Phase C): re-price each segment of a
	// Kiwi-discovered self-connect itinerary on Google Flights and append a
	// "book-direct" separate-tickets alternative. Gated + append-only, so it
	// never alters the existing merge/booking flow. A segment that cannot be
	// priced yields a non-definitive status -> completeness goes partial below
	// (no fabricated total). Only runs when Google succeeded (it is the pricer).
	if crossShopEnabled() && googleSucceeded {
		alts, xshopStatus := enrichCrossShop(ctx, mergedFlights, googleSegmentPricer(target), crossShopDefaultWindow, opts)
		statuses = append(statuses, xshopStatus)
		if len(alts) > 0 {
			mergedFlights = append(mergedFlights, alts...)
		}
	}

	if googleSucceeded || kiwiSucceeded || skiplaggedSucceeded || ryanairSucceeded || wizzairSucceeded || transaviaSucceeded || easyjetSucceeded || vuelingSucceeded || norwegianSucceeded {
		annotateFlightConfidence(mergedFlights, time.Now())
		result := &models.FlightSearchResult{
			Success:          true,
			Count:            len(mergedFlights),
			TripType:         tripTypeForSearch(opts),
			Flights:          mergedFlights,
			ProviderStatuses: statuses,
			Completeness:     models.ComputeCompleteness(statuses),
		}
		// Auto-compose the savings engine: surface the single best cheaper
		// synthesized option (hidden-city, positioning, split, …) alongside the
		// naive flights, unless opted out or this is a nested detector search.
		attachFlightHackSaving(ctx, result, origin, destination, date, opts)
		return result, nil
	}

	errs := []error{}
	if googleErr != nil {
		errs = append(errs, googleErr)
	}
	if kiwiErr != nil {
		errs = append(errs, kiwiErr)
	}
	if skiplaggedErr != nil {
		errs = append(errs, skiplaggedErr)
	}
	if ryanairErr != nil {
		errs = append(errs, ryanairErr)
	}
	if wizzairErr != nil {
		errs = append(errs, wizzairErr)
	}
	if transaviaErr != nil {
		errs = append(errs, transaviaErr)
	}
	if easyjetErr != nil {
		errs = append(errs, easyjetErr)
	}
	if vuelingErr != nil {
		errs = append(errs, vuelingErr)
	}
	if norwegianErr != nil {
		errs = append(errs, norwegianErr)
	}
	if len(errs) > 0 {
		err := errors.Join(errs...)
		return &models.FlightSearchResult{
			Error:            err.Error(),
			ProviderStatuses: statuses,
		}, err
	}

	return &models.FlightSearchResult{
		Error:            "unreachable flight search state",
		ProviderStatuses: statuses,
	}, fmt.Errorf("unreachable flight search state")
}

// annotateFlightConfidence attaches an honest bookability Confidence to every
// flight (innovation #3). Prices are scored from the freshness of the just-
// fetched quote, provider reliability, and multi-source corroboration via the
// shared fareintel scorer. Synthetic separate-ticket itineraries are flagged as
// higher risk. Results with no usable signal are left unrated, never faked.
func annotateFlightConfidence(flights []models.FlightResult, now time.Time) {
	for i := range flights {
		f := &flights[i]
		provider := f.Provider
		if provider == "" {
			provider = f.CheapestSource
		}
		c := fareintel.ScoreConfidence(fareintel.ConfidenceInput{
			Price:           f.PriceForRanking(),
			Currency:        f.Currency,
			Provider:        provider,
			RetrievedAt:     now,
			Now:             now,
			Sources:         f.Sources,
			SeparateTickets: f.BookDirect || f.SelfConnect,
		})
		f.Confidence = &c
	}
}

func cloneFlightSearchResult(shared *models.FlightSearchResult) *models.FlightSearchResult {
	if shared == nil {
		return nil
	}

	// singleflight.Do shares the winning *FlightSearchResult pointer across all
	// concurrent callers for the same key. MCP handlers post-filter Flights and
	// rewrite Count, so each caller needs its own result header and independent
	// nested slices/pointers to avoid racing on shared state.
	cp := *shared
	if shared.Flights != nil {
		cp.Flights = make([]models.FlightResult, len(shared.Flights))
		for i, flight := range shared.Flights {
			flightCopy := flight
			if flight.Warnings != nil {
				flightCopy.Warnings = append([]string(nil), flight.Warnings...)
			}
			if flight.Legs != nil {
				flightCopy.Legs = append([]models.FlightLeg(nil), flight.Legs...)
			}
			if flight.Sources != nil {
				flightCopy.Sources = append([]models.PriceSource(nil), flight.Sources...)
			}
			if flight.SegmentSources != nil {
				flightCopy.SegmentSources = append([]models.PriceSource(nil), flight.SegmentSources...)
			}
			if flight.CarryOnIncluded != nil {
				carryOn := *flight.CarryOnIncluded
				flightCopy.CarryOnIncluded = &carryOn
			}
			if flight.CheckedBagsIncluded != nil {
				checkedBags := *flight.CheckedBagsIncluded
				flightCopy.CheckedBagsIncluded = &checkedBags
			}
			cp.Flights[i] = flightCopy
		}
	}
	return &cp
}

func sharedFlightResult(v any, err error) (*models.FlightSearchResult, error) {
	if err != nil {
		if r, ok := v.(*models.FlightSearchResult); ok {
			return cloneFlightSearchResult(r), err
		}
		return &models.FlightSearchResult{Error: err.Error()}, err
	}
	return cloneFlightSearchResult(v.(*models.FlightSearchResult)), nil
}

// isFlightPayload reports whether a decoded Google Flights `inner` payload is a
// genuine flight response. Real responses are JSON arrays (parseFlights walks
// indices [2]/[3]); error/challenge payloads (ErrorResponse objects, "sorry"
// interstitials) decode to objects or scalars. This is the reliable, conservative
// signal that separates an upstream error from malformed-but-real flight data.
func isFlightPayload(inner any) bool {
	_, ok := inner.([]any)
	return ok
}

// googleUpstreamStatusError maps an HTTP status to a typed, retryable rate-limit
// error when Google signals it is rate-limiting, blocking, or overloaded
// (429/403/503). It returns nil for every other status so the normal parse path
// (including the generic non-200 branch) is untouched. The message is clean and
// never contains the legacy "unexpected flight data format" string.
func googleUpstreamStatusError(status int) error {
	switch status {
	case 429:
		return fmt.Errorf("google flights rate-limited (HTTP 429): %w", models.ErrRateLimited)
	case 403:
		return fmt.Errorf("google flights blocked the request (HTTP 403): %w", models.ErrRateLimited)
	case 503:
		return fmt.Errorf("google flights temporarily unavailable (HTTP 503): %w", models.ErrRateLimited)
	}
	return nil
}

func searchGoogleFlightsWithClient(ctx context.Context, client *batchexec.Client, origin, destination, date string, opts SearchOptions) (*models.FlightSearchResult, error) {
	filters := buildFilters(origin, destination, date, opts)

	encoded, err := batchexec.EncodeFlightFilters(filters)
	if err != nil {
		return &models.FlightSearchResult{
			Error: fmt.Sprintf("encode filters: %v", err),
		}, fmt.Errorf("encode filters: %w", err)
	}

	gl := CurrencyToGL(opts.Currency)
	status, body, err := client.SearchFlightsGLStealth(ctx, encoded, gl, opts.Stealth)
	if err != nil {
		return &models.FlightSearchResult{
			Error: fmt.Sprintf("request failed: %v", err),
		}, fmt.Errorf("request failed: %w", err)
	}

	// Classify rate-limit / block / challenge by HTTP status BEFORE parsing.
	// Google serves 429 (rate limit), 403 (block), and 503 (overload) with a
	// non-flight body; surfacing these as a typed, retryable rate-limit (rather
	// than a generic "unexpected status" or hard block) is what lets the round-trip
	// composer degrade to a soft WARN instead of hard-failing the search (#228).
	if rlErr := googleUpstreamStatusError(status); rlErr != nil {
		return &models.FlightSearchResult{Error: rlErr.Error()}, rlErr
	}
	if status != 200 {
		return &models.FlightSearchResult{
			Error: fmt.Sprintf("unexpected status %d", status),
		}, fmt.Errorf("unexpected status %d", status)
	}

	inner, err := batchexec.DecodeFlightResponse(body)
	if err != nil {
		return &models.FlightSearchResult{
			Error: fmt.Sprintf("decode response: %v", err),
		}, fmt.Errorf("decode response: %w", err)
	}

	// A genuine Google Flights response decodes `inner` to a JSON ARRAY (parseFlights
	// walks indices [2]/[3]). Google's travel.frontend.flights.ErrorResponse — and the
	// interstitial challenge / "sorry" pages served when soft-rate-limited at HTTP 200 —
	// decode to a JSON OBJECT (or scalar), never the flight array. A non-array inner is
	// therefore an upstream error/challenge payload, NOT a malformed-but-real flight
	// response (which is still an array, just with empty buckets -> a distinct no-hit).
	// Classifying it as a typed rate-limit here keeps the legacy "unexpected flight data
	// format" string out of every user/agent-facing field (#198 + #228); the raw detail
	// survives only in a debug log.
	if !isFlightPayload(inner) {
		slog.Debug("google flights returned non-flight payload", "detail", "unexpected flight data format")
		rlErr := fmt.Errorf("google flights returned a non-flight error/challenge payload: %w", models.ErrRateLimited)
		return &models.FlightSearchResult{Error: rlErr.Error()}, rlErr
	}

	rawFlights, err := batchexec.ExtractFlightData(inner)
	if err != nil {
		return &models.FlightSearchResult{
			Error: fmt.Sprintf("extract flights: %v", err),
		}, fmt.Errorf("extract flights: %w", err)
	}

	flights := parseFlights(rawFlights)

	// Google returned flight entries but the positional parser decoded none of
	// them — the array shape rotated. That is a parser failure, not a route with
	// no flights; surface it as a typed error (StatusFailed) so the breaker and
	// the aggregator treat the provider as unreliable rather than empty-but-healthy.
	if perr := parseFailureIfAllDropped(len(flights), len(rawFlights)); perr != nil {
		slog.Debug("google flights: all entries unparseable", "entries", len(rawFlights))
		return &models.FlightSearchResult{Error: perr.Error()}, perr
	}

	// Add booking URLs. Prices are in the API's native currency (IP-based).
	// Currency conversion, if needed, happens in the CLI display layer.
	for i := range flights {
		flights[i].Provider = "google_flights"
		flights[i].BookingURL = buildFlightBookingURL(origin, destination, date, "", "")
	}

	return &models.FlightSearchResult{
		Success:  true,
		Count:    len(flights),
		TripType: tripTypeForSearch(opts),
		Flights:  flights,
	}, nil
}
