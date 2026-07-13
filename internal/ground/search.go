package ground

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/breaker"
	"github.com/MikkoParkkola/trvl/internal/cache"
	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/searchctx"
	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
)

// groundGroup deduplicates concurrent in-flight searches with identical parameters.
var groundGroup singleflight.Group

// groundCache caches ground transport search results.
var groundCache = cache.New()

// groundCacheTTL is the TTL for cached ground transport results.
const groundCacheTTL = 10 * time.Minute

const sharedGroundSearchTimeout = 30 * time.Second

// httpClient is a shared HTTP client with sensible timeouts for FlixBus/RegioJet.
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

// Shared rate limiters for FlixBus and RegioJet (used by the shared httpClient).
var (
	flixbusLimiter  = newProviderLimiter(100 * time.Millisecond) // 10 req/s
	regiojetLimiter = newProviderLimiter(100 * time.Millisecond) // 10 req/s
)

// rateLimitedDo executes an HTTP request through the shared client after
// waiting on the provided rate limiter.
func rateLimitedDo(ctx context.Context, limiter *rate.Limiter, req *http.Request) (*http.Response, error) {
	if err := limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	return httpClient.Do(req)
}

// providerResult holds the outcome of a single provider search goroutine.
type providerResult struct {
	routes []models.GroundRoute
	err    error
	name   string
}

// groundBreaker protects the provider fan-out: a provider that fails (network
// error, timeout, rate-limit) DefaultThreshold times in a row is skipped for
// the cooldown instead of being retried on every search, then probed again once
// the cooldown elapses. It is keyed by provider name and shared across searches
// by design — a provider's recent health carries between requests. A
// "not applicable for this route" answer is healthy, not a failure, so it never
// trips the breaker.
var groundBreaker = breaker.New()

// errCircuitBroken marks a provider call skipped because its breaker is open.
// It is a deliberate skip, not a fresh failure, so the result aggregator
// reports it as a circuit_broken status without counting it as an error.
var errCircuitBroken = errors.New("skipped: recent failures tripped the circuit breaker")

// launchProvider starts a provider search in a new goroutine, sending the
// result to the results channel when done. The shared groundBreaker gates the
// call: a tripped provider is skipped without touching the network, and each
// real outcome feeds the breaker (a genuine failure advances it toward
// tripping; a healthy or not-applicable response closes it).
func launchProvider(wg *sync.WaitGroup, results chan<- providerResult, name string, fn func() ([]models.GroundRoute, error)) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		if groundBreaker.Tripped(name) {
			results <- providerResult{name: name, err: errCircuitBroken}
			return
		}
		routes, err := fn()
		// A not-applicable answer (no route for this origin/destination) is a
		// healthy "nothing to sell" response, not a provider failure, so it must
		// not push the breaker toward tripping.
		if err != nil && !isProviderNotApplicable(err) {
			groundBreaker.RecordFailure(name)
		} else {
			groundBreaker.RecordSuccess(name)
		}
		results <- providerResult{routes: routes, err: err, name: name}
	}()
}

// SearchOptions configures a ground transport search.
type SearchOptions struct {
	Currency              string   // Default: EUR
	Providers             []string // Filter to specific providers; empty = all
	ExcludeProviders      []string // Skip these providers even when otherwise enabled
	MaxPrice              float64  // 0 = no limit
	Type                  string   // "bus", "train", or empty for all
	NoCache               bool     // bypass response cache
	AllowBrowserFallbacks bool     // opt in to browser/curl/cookie-assisted providers

	// ReturnDate, when non-empty (ISO calendar date), turns the search into a
	// round-trip: the outbound leg (origin->destination on the departure date)
	// is composed with an inbound leg (destination->origin on the return date).
	// Empty = one-way. Mirrors flights.SearchOptions.ReturnDate.
	ReturnDate string

	// NoHacks opts out of the auto-composed travel-hacks savings engine. The
	// engine is ON by default: a normal search also surfaces the single best
	// cheaper synthesized option (multimodal, cross-border rail, …) in
	// result.HackSaving. Set NoHacks to run a pure naive search.
	NoHacks bool

	// SearchOverride replaces the real ground-transport search for this single
	// call. Nil (the default) runs the production search. Mirrors
	// flights.SearchOptions.SearchOverride: callers in other packages — today
	// internal/hacks' positioning/ferry/open-jaw-ground detectors — inject a
	// synthetic result per call, so tests never depend on live providers and
	// concurrent calls never race on shared mutable state.
	SearchOverride SearchFunc
}

// SearchFunc matches the signature of SearchByName. It is the type of
// SearchOptions.SearchOverride and lets external packages inject a
// replacement ground-search implementation per call.
type SearchFunc func(ctx context.Context, from, to, date string, opts SearchOptions) (*models.GroundSearchResult, error)

// providerEnabled reports whether a provider should run given an optional
// allow-list (include; empty means all) and a deny-list (exclude, which always
// wins). Pure and case-insensitive so it is unit-tested without the network.
func providerEnabled(name string, include, exclude []string) bool {
	for _, p := range exclude {
		if strings.EqualFold(p, name) {
			return false
		}
	}
	if len(include) == 0 {
		return true
	}
	for _, p := range include {
		if strings.EqualFold(p, name) {
			return true
		}
	}
	return false
}

// currencyConverter converts amount from->to, returning the converted amount
// and a non-empty status/ok signal. Injected so unit tests never hit the network.
type currencyConverter func(ctx context.Context, amount float64, from, to string) (converted float64, ok bool)

// normalizeGroundCurrencies best-effort converts every route's price into the
// target currency for comparable ranking. On success it mutates Price+Currency
// to the target AND sets ComparablePrice. On failure it leaves the route in its
// original currency and ComparablePrice stays 0 (=> incomparable).
func normalizeGroundCurrencies(ctx context.Context, routes []models.GroundRoute, target string, conv currencyConverter) {
	tU := strings.ToUpper(strings.TrimSpace(target))
	if tU == "" || conv == nil {
		return
	}
	for i := range routes {
		r := &routes[i]
		if r.Price <= 0 {
			continue
		}
		cU := strings.ToUpper(strings.TrimSpace(r.Currency))
		if cU == tU {
			r.ComparablePrice = r.Price // already in target
			continue
		}
		if converted, ok := conv(ctx, r.Price, r.Currency, target); ok && converted > 0 {
			r.Price = converted
			r.Currency = target
			r.ComparablePrice = converted
		}
		// else: leave as-is, ComparablePrice=0 => incomparable tail
	}
}

// sortGroundRoutes performs the stable cross-currency comparable-price sort
// (used by search and directly by unit tests for determinism verification).
func sortGroundRoutes(routes []models.GroundRoute) {
	sort.SliceStable(routes, func(i, j int) bool {
		a, b := routes[i], routes[j]
		ac, bc := a.ComparablePrice > 0, b.ComparablePrice > 0
		if ac != bc {
			return ac
		}
		if ac && bc {
			pa := a.PriceForRanking()
			pb := b.PriceForRanking()
			ra, rb := pa > 0, pb > 0
			if ra != rb {
				return ra
			}
			if ra && pa != pb {
				return pa < pb
			}
		} else {
			au, bu := strings.ToUpper(strings.TrimSpace(a.Currency)), strings.ToUpper(strings.TrimSpace(b.Currency))
			if au != bu {
				return au < bu
			}
			pa, pb := a.Price, b.Price
			ra, rb := pa > 0, pb > 0
			if ra != rb {
				return ra
			}
			if ra && pa != pb {
				return pa < pb
			}
		}
		if a.Duration != b.Duration {
			return a.Duration < b.Duration
		}
		if a.Departure.Time != b.Departure.Time {
			return a.Departure.Time < b.Departure.Time
		}
		if a.Arrival.Time != b.Arrival.Time {
			return a.Arrival.Time < b.Arrival.Time
		}
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		if a.BookingURL != b.BookingURL {
			return a.BookingURL < b.BookingURL
		}
		if a.Transfers != b.Transfers {
			return a.Transfers < b.Transfers
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return false
	})
}

// SearchByName searches all providers for ground transport between two cities
// given by name. Resolves city names to provider-specific IDs automatically.
func SearchByName(ctx context.Context, from, to, date string, opts SearchOptions) (*models.GroundSearchResult, error) {
	// A per-call override takes over entirely, bypassing defaults, caching,
	// and every provider — the same contract flights.SearchOptions.SearchOverride
	// gives test callers. See SearchOptions.SearchOverride.
	if opts.SearchOverride != nil {
		return opts.SearchOverride(ctx, from, to, date, opts)
	}

	if opts.Currency == "" {
		opts.Currency = "EUR"
	}

	// Round-trip: compose an outbound search with a swapped-endpoint inbound
	// search on the return date. Each direction reuses the full one-way path
	// (cache, singleflight, hacks) by recursing with ReturnDate cleared, so the
	// composition adds no new network code — it only pairs and direction-tags.
	if rd := strings.TrimSpace(opts.ReturnDate); rd != "" && rd != date {
		return searchRoundTripByName(ctx, from, to, date, rd, opts)
	}

	allowBrowserFallbacks := browserFallbacksEnabled(opts)

	// Build cache key from search parameters.
	providerKey := groundProviderKey(opts)
	cacheKey := cache.Key("ground", fmt.Sprintf("%s|%s|%s|%s|%s|%.2f|%s|%t", from, to, date, opts.Currency, providerKey, opts.MaxPrice, opts.Type, allowBrowserFallbacks))

	negKey, negEligible := groundNegKey(from, to, date, opts, providerKey)

	// Check cache unless bypassed.
	if !opts.NoCache {
		if data, ok := groundCache.Get(cacheKey); ok {
			var cached models.GroundSearchResult
			if err := json.Unmarshal(data, &cached); err == nil {
				slog.Debug("ground cache hit", "from", from, "to", to, "date", date)
				maybePrefetchGround(ctx, from, to, date, opts)
				return &cached, nil
			}
		}

		// Negative-result cache: a recent CLEAN "no service" short-circuits the
		// whole provider fan-out for this route+month. Only unfiltered searches
		// participate (negEligible).
		if negEligible && negCacheEnabled() && groundNegCache.Seen(negKey) {
			slog.Debug("ground negative cache hit", "from", from, "to", to, "date", date)
			maybePrefetchGround(ctx, from, to, date, opts)
			return &models.GroundSearchResult{Success: false, Count: 0}, nil
		}
	}

	// Deduplicate concurrent identical in-flight searches. The cache check above
	// already handles TTL-based reuse; singleflight only coalesces truly concurrent
	// requests that both missed the cache.
	result, err := doGroundSearchSingleflight(ctx, cacheKey, func(sharedCtx context.Context) (*models.GroundSearchResult, error) {
		return searchByNameCore(sharedCtx, from, to, date, opts, cacheKey, allowBrowserFallbacks)
	})

	// Speculative prefetch: warm the cache for the likely next query. Best-effort
	// and non-blocking.
	maybePrefetchGround(ctx, from, to, date, opts)

	return result, err
}

// searchRoundTripByName composes a round-trip ground itinerary from two one-way
// searches: the outbound (from->to on date) and the inbound (to->from on
// returnDate). Each direction recurses through SearchByName with ReturnDate
// cleared, so it inherits the full one-way pipeline (cache, singleflight,
// travel-hacks). Routes are direction-tagged and concatenated; the combined
// result is honest about partial success — if only one direction has service,
// it still surfaces with Success=true so the traveller sees the half that runs.
func searchRoundTripByName(ctx context.Context, from, to, date, returnDate string, opts SearchOptions) (*models.GroundSearchResult, error) {
	oneWay := opts
	oneWay.ReturnDate = ""

	outbound, outErr := SearchByName(ctx, from, to, date, oneWay)
	inbound, inErr := SearchByName(ctx, to, from, returnDate, oneWay)

	// Propagate an error only when BOTH directions fail; a single working
	// direction is still useful and must not be masked by the other's failure.
	if outErr != nil && inErr != nil {
		return nil, outErr
	}

	combined := &models.GroundSearchResult{}
	tagDirection := func(res *models.GroundSearchResult, dir string) {
		if res == nil {
			return
		}
		for i := range res.Routes {
			res.Routes[i].Direction = dir
			combined.Routes = append(combined.Routes, res.Routes[i])
		}
		if res.HackSaving != nil && combined.HackSaving == nil {
			combined.HackSaving = res.HackSaving
		}
		// Carry each direction's provider outcomes into the combined envelope so a
		// round-trip is as honest about partial failures as a one-way (otherwise a
		// timed-out inbound provider would vanish). Direction-prefix the id to keep
		// outbound/inbound entries distinct.
		for _, ps := range res.ProviderStatuses {
			ps.ID = dir + ":" + ps.ID
			combined.ProviderStatuses = append(combined.ProviderStatuses, ps)
		}
	}
	tagDirection(outbound, "outbound")
	tagDirection(inbound, "inbound")

	combined.Count = len(combined.Routes)
	combined.Success = combined.Count > 0
	combined.Completeness = models.ComputeCompleteness(combined.ProviderStatuses)
	return combined, nil
}

func doGroundSearchSingleflight(ctx context.Context, key string, fn func(context.Context) (*models.GroundSearchResult, error)) (*models.GroundSearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	ch := groundGroup.DoChan(key, func() (any, error) {
		sharedCtx, cancel := searchctx.DetachedWithin(ctx, sharedGroundSearchTimeout)
		defer cancel()
		return fn(sharedCtx)
	})

	select {
	case <-ctx.Done():
		// Forget timed-out work eagerly so the next caller can launch a fresh
		// execution instead of inheriting the prior caller's deadline-bound run.
		groundGroup.Forget(key)
		return nil, ctx.Err()
	case res := <-ch:
		return sharedGroundResult(res.Val, res.Err)
	}
}

func sharedGroundResult(v any, err error) (*models.GroundSearchResult, error) {
	if err != nil {
		if r, ok := v.(*models.GroundSearchResult); ok {
			return cloneGroundSearchResult(r), err
		}
		return nil, err
	}
	return cloneGroundSearchResult(v.(*models.GroundSearchResult)), nil
}

func cloneGroundSearchResult(shared *models.GroundSearchResult) *models.GroundSearchResult {
	if shared == nil {
		return nil
	}

	// singleflight.Do shares the winner's *GroundSearchResult pointer across
	// concurrent callers. MCP handlers can post-filter Routes and rewrite Count,
	// so each caller needs a private copy of the result header and nested mutable
	// slices/pointers to avoid racing on shared state.
	cp := *shared
	if shared.Routes != nil {
		cp.Routes = make([]models.GroundRoute, len(shared.Routes))
		for i, route := range shared.Routes {
			routeCopy := route
			if route.Amenities != nil {
				routeCopy.Amenities = append([]string(nil), route.Amenities...)
			}
			if route.Legs != nil {
				routeCopy.Legs = make([]models.GroundLeg, len(route.Legs))
				for j, leg := range route.Legs {
					legCopy := leg
					if leg.Amenities != nil {
						legCopy.Amenities = append([]string(nil), leg.Amenities...)
					}
					routeCopy.Legs[j] = legCopy
				}
			}
			if route.SeatsLeft != nil {
				seatsLeft := *route.SeatsLeft
				routeCopy.SeatsLeft = &seatsLeft
			}
			cp.Routes[i] = routeCopy
		}
	}
	return &cp
}

// searchByNameCore performs the actual ground search without singleflight wrapping.
func searchByNameCore(ctx context.Context, from, to, date string, opts SearchOptions, cacheKey string, allowBrowserFallbacks bool) (*models.GroundSearchResult, error) {
	var wg sync.WaitGroup
	results := make(chan providerResult, searchResultBufferCapacity())

	useProvider := func(name string) bool {
		return providerEnabled(name, opts.Providers, opts.ExcludeProviders)
	}

	// Distribusion — ground transport GDS covering bus, ferry, train, airport transfers.
	// Placed first (before individual providers) since it aggregates 2,000+ carriers.
	// Requires DISTRIBUSION_API_KEY to be set; silently skipped otherwise.
	if useProvider("distribusion") && HasDistribusionKey() {
		launchProvider(&wg, results, "distribusion", func() ([]models.GroundRoute, error) {
			return SearchDistribusion(ctx, from, to, date, opts.Currency)
		})
	}

	// FlixBus
	if useProvider("flixbus") {
		launchProvider(&wg, results, "flixbus", func() ([]models.GroundRoute, error) {
			return searchFlixBusByName(ctx, from, to, date, opts)
		})
	}

	// RegioJet
	if useProvider("regiojet") {
		launchProvider(&wg, results, "regiojet", func() ([]models.GroundRoute, error) {
			return searchRegioJetByName(ctx, from, to, date, opts)
		})
	}

	// Rome2Rio — multimodal route DISCOVERY (not bookable fares). Surfaces the
	// set of ways to travel A->B, including multi-leg combinations (e.g. "ferry
	// to a hub, then fly") that single-mode providers never produce, with an
	// indicative price range per option. Real per-leg prices still come from the
	// dedicated providers above; failure is isolated by launchProvider so a
	// Rome2Rio bot-wall never breaks the bookable results.
	if useProvider("rome2rio") {
		launchProvider(&wg, results, "rome2rio", func() ([]models.GroundRoute, error) {
			return SearchRome2Rio(ctx, from, to, allowBrowserFallbacks)
		})
	}

	// Eurostar — only if both cities have Eurostar stations.
	// Search both Snap (last-minute deals) and regular fares in parallel so the
	// user sees both options (e.g. "eurostar snap GBP 39" and "eurostar GBP 130").
	if (useProvider("eurostar") || useProvider("eurostar snap")) && HasEurostarRoute(from, to) {
		// Eurostar cheapestFaresSearch needs a date range (not a single day).
		// Use the requested date as start, +7 days as end.
		endDate := date // fallback
		if t, err := models.ParseDate(date); err == nil {
			endDate = t.AddDate(0, 0, 7).Format("2006-01-02")
		}

		// Snap fares goroutine — Snap fares are released up to 14 days
		// before travel and only on specific routes (see eurostarSnapRoutes).
		// Search today→today+14 so Snap deals surface regardless of the
		// user's search date.
		if HasEurostarSnapRoute(from, to) {
			snapStart := time.Now().Format("2006-01-02")
			snapEnd := time.Now().AddDate(0, 0, 14).Format("2006-01-02")
			launchProvider(&wg, results, "eurostar snap", func() ([]models.GroundRoute, error) {
				return SearchEurostar(ctx, from, to, snapStart, snapEnd, opts.Currency, true)
			})
		}

		// Regular fares goroutine.
		launchProvider(&wg, results, "eurostar", func() ([]models.GroundRoute, error) {
			return SearchEurostar(ctx, from, to, date, endDate, opts.Currency, false)
		})
	}

	// NS (Dutch Railways) — only if at least one city has an NS station.
	if useProvider("ns") && (HasNSStation(from) || HasNSStation(to)) {
		launchProvider(&wg, results, "ns", func() ([]models.GroundRoute, error) {
			return SearchNS(ctx, from, to, date, opts.Currency)
		})
	}

	// Deutsche Bahn — if at least one city has a DB station (covers most European rail).
	if useProvider("db") && (HasDBStation(from) || HasDBStation(to)) {
		launchProvider(&wg, results, "db", func() ([]models.GroundRoute, error) {
			return SearchDeutscheBahn(ctx, from, to, date, opts.Currency)
		})
	}

	// SNCF — only if at least one city is French.
	if useProvider("sncf") && HasSNCFRoute(from, to) {
		launchProvider(&wg, results, "sncf", func() ([]models.GroundRoute, error) {
			return SearchSNCF(ctx, from, to, date, opts.Currency, allowBrowserFallbacks)
		})
	}

	// Trainline — train aggregator (covers SNCF, Eurostar, DB, Trenitalia, etc.)
	if useProvider("trainline") && HasTrainlineStation(from) && HasTrainlineStation(to) {
		launchProvider(&wg, results, "trainline", func() ([]models.GroundRoute, error) {
			return SearchTrainline(ctx, from, to, date, opts.Currency, allowBrowserFallbacks)
		})
	}

	// ÖBB (Austrian Federal Railways) — Austria and neighbouring countries.
	if useProvider("oebb") && HasOebbRoute(from, to) {
		launchProvider(&wg, results, "oebb", func() ([]models.GroundRoute, error) {
			return SearchOebb(ctx, from, to, date, opts.Currency)
		})
	}

	// Digitransit (VR Finnish Railways) — only if at least one city has a Finnish station.
	if (useProvider("digitransit") || useProvider("vr")) && (HasDigitransitStation(from) || HasDigitransitStation(to)) {
		launchProvider(&wg, results, "vr", func() ([]models.GroundRoute, error) {
			return SearchDigitransit(ctx, from, to, date, opts.Currency)
		})
	}

	// Renfe (Spain) — only if at least one city has a Renfe station (Spanish rail).
	if useProvider("renfe") && HasRenfeRoute(from, to) {
		launchProvider(&wg, results, "renfe", func() ([]models.GroundRoute, error) {
			return SearchRenfe(ctx, from, to, date, opts.Currency)
		})
	}

	// Trenitalia — only if both cities are Italian (high-speed + regional).
	if useProvider("trenitalia") && HasTrenitaliaRoute(from, to) {
		launchProvider(&wg, results, "trenitalia", func() ([]models.GroundRoute, error) {
			return SearchTrenitalia(ctx, from, to, date, opts.Currency)
		})
	}

	// Italo (NTV) — only if both cities are on Italo's high-speed network.
	if useProvider("italo") && HasItaloRoute(from, to) {
		launchProvider(&wg, results, "italo", func() ([]models.GroundRoute, error) {
			return SearchItalo(ctx, from, to, date, opts.Currency)
		})
	}

	// Tallink/Silja Line — ferry routes in the Baltic Sea.
	if useProvider("tallink") && HasTallinkRoute(from, to) {
		launchProvider(&wg, results, "tallink", func() ([]models.GroundRoute, error) {
			return SearchTallink(ctx, from, to, date, opts.Currency)
		})
	}

	// Stena Line — ferry routes across the North Sea and Baltic Sea.
	if useProvider("stenaline") && HasStenaLineRoute(from, to) {
		launchProvider(&wg, results, "stenaline", func() ([]models.GroundRoute, error) {
			return SearchStenaLine(ctx, from, to, date, opts.Currency)
		})
	}

	// DFDS — ferry routes across the North Sea and Baltic Sea.
	if useProvider("dfds") && HasDFDSRoute(from, to) {
		launchProvider(&wg, results, "dfds", func() ([]models.GroundRoute, error) {
			return SearchDFDS(ctx, from, to, date, opts.Currency)
		})
	}

	// Viking Line — ferry routes in the Baltic Sea (Helsinki–Tallinn, Helsinki–Stockholm,
	// Turku–Stockholm, Stockholm–Mariehamn).
	if useProvider("vikingline") && HasVikingLineRoute(from, to) {
		launchProvider(&wg, results, "vikingline", func() ([]models.GroundRoute, error) {
			return SearchVikingLine(ctx, from, to, date, opts.Currency)
		})
	}

	// Eckerö Line — Helsinki ↔ Tallinn ferry (M/S Finlandia).
	if useProvider("eckeroline") && HasEckeroLineRoute(from, to) {
		launchProvider(&wg, results, "eckeroline", func() ([]models.GroundRoute, error) {
			return SearchEckeroLine(ctx, from, to, date, opts.Currency)
		})
	}

	// Finnlines — Helsinki ↔ Travemünde, Naantali ↔ Kapellskär, Malmö ↔ Świnoujście.
	if useProvider("finnlines") && HasFinnlinesRoute(from, to) {
		launchProvider(&wg, results, "finnlines", func() ([]models.GroundRoute, error) {
			return SearchFinnlines(ctx, from, to, date, opts.Currency)
		})
	}

	// Ferryhopper — Greek ferry aggregator (Aegean, Ionian, Adriatic seas).
	// Uses the public Ferryhopper MCP endpoint; no API key required.
	// Accepts free-form location names so it is always attempted.
	if useProvider("ferryhopper") {
		launchProvider(&wg, results, "ferryhopper", func() ([]models.GroundRoute, error) {
			return SearchFerryhopper(ctx, from, to, date, opts.Currency)
		})
	}

	// European Sleeper — night train Brussels→Amsterdam→Berlin→Dresden→Prague.
	if useProvider("european_sleeper") && HasEuropeanSleeperRoute(from, to) {
		launchProvider(&wg, results, "european_sleeper", func() ([]models.GroundRoute, error) {
			return SearchEuropeanSleeper(ctx, from, to, date, opts.Currency)
		})
	}

	// Snälltåget — Swedish night trains (Stockholm→Malmö, Stockholm→Åre, Stockholm→Berlin).
	if useProvider("snalltaget") && HasSnalltagetRoute(from, to) {
		launchProvider(&wg, results, "snalltaget", func() ([]models.GroundRoute, error) {
			return SearchSnalltaget(ctx, from, to, date, opts.Currency)
		})
	}

	// Transitous — coordinate-based, always available as a fallback.
	// Requires geocoding city names to coordinates; skipped if geocoding fails.
	if useProvider("transitous") {
		launchProvider(&wg, results, "transitous", func() ([]models.GroundRoute, error) {
			return searchTransitousByName(ctx, from, to, date)
		})
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var allRoutes []models.GroundRoute
	var errors []string
	var statuses []models.ProviderStatus
	for r := range results {
		if r.err != nil {
			if r.err == errCircuitBroken {
				// Deliberately skipped: the provider has been failing and its
				// breaker is open. Report it honestly, but it is not a fresh
				// error so it does not drag the search toward "partial".
				slog.Debug("ground provider circuit-broken", "provider", r.name)
				statuses = append(statuses, models.ProviderStatus{
					ID:      r.name,
					Name:    r.name,
					Status:  models.StatusCircuitBroken,
					Error:   "skipped: recent failures tripped the circuit breaker",
					FixHint: "wait for the cooldown to elapse, then it retries automatically",
				})
				continue
			}
			if isProviderNotApplicable(r.err) {
				slog.Debug("ground provider not applicable", "provider", r.name, "reason", r.err)
				// Attempted but no applicable route for this origin/destination —
				// not a failure, so it must not drag Completeness toward "partial".
				statuses = append(statuses, models.ProviderStatus{
					ID:     r.name,
					Name:   r.name,
					Status: models.StatusSkipped,
				})
			} else {
				slog.Warn("ground provider error", "provider", r.name, "error", r.err)
				errors = append(errors, fmt.Sprintf("%s: %v", r.name, r.err))
				statuses = append(statuses, models.ProviderStatus{
					ID:     r.name,
					Name:   r.name,
					Status: models.ClassifyProviderError(r.err),
					Error:  r.err.Error(),
				})
			}
			continue
		}
		allRoutes = append(allRoutes, r.routes...)
		statuses = append(statuses, models.ProviderStatus{
			ID:      r.name,
			Name:    r.name,
			Status:  models.StatusOK,
			Results: len(r.routes),
		})
	}
	// Deterministic order: the channel drains concurrently, so sort by id for
	// stable JSON output (and stable tests).
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].ID < statuses[j].ID })

	// Normalize currencies for truthful cross-currency ranking BEFORE filter
	// (so MaxPrice uses comparable numbers) and BEFORE resolve. Target mirrors
	// the flights derivation (opts.Currency if set, else first currency observed
	// from raw results). Never hardcodes EUR here.
	target := strings.TrimSpace(opts.Currency)
	if target == "" {
		for _, r := range allRoutes {
			if c := strings.TrimSpace(r.Currency); c != "" {
				target = c
				break
			}
		}
	}
	if target != "" {
		conv := func(ctx context.Context, amount float64, from, to string) (float64, bool) {
			amt, status := destinations.ConvertCurrency(ctx, amount, from, to)
			// Faithful adaptation of ConvertCurrency + flights normalize convention:
			// success when returned status matches the requested to-currency and amt > 0.
			if amt > 0 && strings.EqualFold(strings.TrimSpace(status), strings.TrimSpace(to)) {
				return amt, true
			}
			return amt, false
		}
		normalizeGroundCurrencies(ctx, allRoutes, target, conv)
	}

	// Filter/deduplicate in one pass while preserving the current semantics:
	// unavailable routes are removed first, then duplicate routes are suppressed
	// before MaxPrice and Type filters are applied.
	allRoutes = filterGroundRoutes(allRoutes, opts)

	// Collapse the same physical connection returned by multiple providers into
	// one route carrying every provider as a PriceSource (cheapest headline).
	allRoutes = models.ResolveGroundSources(allRoutes)

	// Sort by comparable price (cross-currency truthful). See sortGroundRoutes.
	sortGroundRoutes(allRoutes)

	annotateGroundConfidence(allRoutes, time.Now())

	result := &models.GroundSearchResult{
		Success:          len(allRoutes) > 0,
		Count:            len(allRoutes),
		Routes:           allRoutes,
		ProviderStatuses: statuses,
		Completeness:     models.ComputeCompleteness(statuses),
	}
	if len(allRoutes) == 0 && len(errors) > 0 {
		result.Error = strings.Join(errors, "; ")
	}

	// Auto-compose the savings engine: surface the single best cheaper
	// synthesized option alongside the naive routes (before caching so cache
	// hits carry it too), unless opted out or this is a nested detector search.
	attachGroundHackSaving(ctx, result, from, to, date, opts)

	// Cache successful results.
	if result.Success && !opts.NoCache {
		if data, err := json.Marshal(result); err == nil {
			groundCache.Set(cacheKey, data, groundCacheTTL)
		}
	}

	// Negative-result cache: mark a route as "no service" only on a CLEAN empty —
	// zero routes AND no provider errors/timeouts. A run with any error is left
	// uncached so it retries. Filtered searches are excluded by groundNegKey.
	if !opts.NoCache && len(allRoutes) == 0 && len(errors) == 0 && negCacheEnabled() {
		if negKey, ok := groundNegKey(from, to, date, opts, groundProviderKey(opts)); ok {
			groundNegCache.Mark(negKey)
		}
	}

	return result, nil
}

func browserFallbacksEnabled(opts SearchOptions) bool {
	if opts.AllowBrowserFallbacks {
		return true
	}

	raw := strings.TrimSpace(os.Getenv("TRVL_ALLOW_BROWSER_FALLBACKS"))
	if raw == "" {
		return false
	}

	enabled, err := strconv.ParseBool(raw)
	return err == nil && enabled
}
