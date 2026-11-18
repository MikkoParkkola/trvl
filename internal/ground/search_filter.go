package ground

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/fareintel"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// groundProviderKey returns the canonical provider-set component of the cache
// key (sorted provider list, or "all").
func groundProviderKey(opts SearchOptions) string {
	if len(opts.Providers) == 0 {
		return "all"
	}
	sorted := make([]string, len(opts.Providers))
	copy(sorted, opts.Providers)
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// annotateGroundConfidence attaches an honest bookability Confidence to every
// ground route (innovation #3), scored from the just-fetched quote freshness,
// provider reliability, and multi-source corroboration via the shared fareintel
// scorer. Routes with no usable signal are left unrated, never faked.
func annotateGroundConfidence(routes []models.GroundRoute, now time.Time) {
	for i := range routes {
		r := &routes[i]
		c := fareintel.ScoreConfidence(fareintel.ConfidenceInput{
			Price:           r.Price,
			Currency:        r.Currency,
			Provider:        r.Provider,
			RetrievedAt:     now,
			Now:             now,
			Sources:         r.Sources,
			SeparateTickets: r.Transfers > 0 && r.Type == "mixed",
			PriceProvenance: r.PriceProvenance,
		})
		r.Confidence = &c
	}
}

// isProviderNotApplicable reports a provider that does not serve the
// requested pair: a healthy "nothing to sell", never a failure. Only
// models.ErrNotApplicable counts; the message wording is never read. A
// deadline or a limiter wait past it is a timeout, never "not served".
func isProviderNotApplicable(err error) bool {
	return errors.Is(err, models.ErrNotApplicable) && !isProviderTimeout(err)
}

// isProviderTimeout reports a provider cut off by the search deadline, either
// mid-request or while waiting on its rate limiter.
func isProviderTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "would exceed context deadline") ||
		strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "Client.Timeout exceeded")
}

type groundRouteKey struct {
	provider      string
	departureTime string
	arrivalTime   string
	priceCents    int64
}

func filterGroundRoutes(routes []models.GroundRoute, opts SearchOptions) []models.GroundRoute {
	filtered := routes[:0]
	seen := make(map[groundRouteKey]struct{}, len(routes))
	hasTypeFilter := opts.Type != ""

	for _, route := range routes {
		if !shouldKeepGroundRoute(route) {
			continue
		}

		key := groundRouteDedupKey(route)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		// A budget is checked in the comparison currency. A route the budget
		// cannot be checked against (no fare, or a fare that could not be
		// converted) is dropped rather than assumed to fit.
		if opts.MaxPrice > 0 && (route.PriceProvenance == models.PriceUnavailable ||
			route.ComparablePrice <= 0 || route.ComparablePrice > opts.MaxPrice) {
			continue
		}
		if hasTypeFilter && !strings.EqualFold(route.Type, opts.Type) {
			continue
		}

		filtered = append(filtered, route)
	}

	return filtered
}

func groundRouteDedupKey(route models.GroundRoute) groundRouteKey {
	return groundRouteKey{
		provider:      route.Provider,
		departureTime: route.Departure.Time,
		arrivalTime:   route.Arrival.Time,
		priceCents:    roundedPriceCents(route.Price),
	}
}

func roundedPriceCents(price float64) int64 {
	return int64(math.Round(price * 100))
}

func deduplicateGroundRoutes(routes []models.GroundRoute) []models.GroundRoute {
	seen := make(map[groundRouteKey]struct{}, len(routes))
	result := routes[:0]
	for _, r := range routes {
		key := groundRouteDedupKey(r)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			result = append(result, r)
		}
	}
	return result
}

// shouldKeepGroundRoute keeps every route, priced or not: a route without a
// fare is shown as unavailable rather than silently dropped. The one
// exception is RegioJet, whose Price 0 means the connection is sold out.
func shouldKeepGroundRoute(route models.GroundRoute) bool {
	return route.Price > 0 || !strings.EqualFold(route.Provider, "regiojet")
}

// classifyGroundProvenance is the fail-closed provenance pass of the ground
// merge: Price 0 is unavailable, a priced route without a provenance is
// estimated, and only parsers mark a route live.
func classifyGroundProvenance(routes []models.GroundRoute) {
	for i := range routes {
		r := &routes[i]
		switch {
		case r.Price <= 0:
			r.PriceProvenance = models.PriceUnavailable
		case r.PriceProvenance == "":
			r.PriceProvenance = models.PriceEstimated
		}
	}
}

func filterUnavailableGroundRoutes(routes []models.GroundRoute) []models.GroundRoute {
	filtered := routes[:0]
	for _, route := range routes {
		if shouldKeepGroundRoute(route) {
			filtered = append(filtered, route)
		}
	}
	return filtered
}

// resolveAndSearch is a generic helper that resolves city names via an
// autocomplete function, then delegates to a search function that receives the
// resolved from/to cities. It eliminates the identical resolve-from / resolve-to
// / check-empty / call-search boilerplate shared by FlixBus and RegioJet.
func resolveAndSearch[T any](
	ctx context.Context,
	from, to string,
	providerName string,
	autoComplete func(ctx context.Context, query string) ([]T, error),
	search func(fromCity, toCity T) ([]models.GroundRoute, error),
) ([]models.GroundRoute, error) {
	fromCities, err := autoComplete(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("resolve from city: %w", err)
	}
	if len(fromCities) == 0 {
		return nil, fmt.Errorf("no %s city found for %q: %w", providerName, from, models.ErrNotApplicable)
	}

	toCities, err := autoComplete(ctx, to)
	if err != nil {
		return nil, fmt.Errorf("resolve to city: %w", err)
	}
	if len(toCities) == 0 {
		return nil, fmt.Errorf("no %s city found for %q: %w", providerName, to, models.ErrNotApplicable)
	}

	return search(fromCities[0], toCities[0])
}

// searchFlixBusByName resolves city names and searches FlixBus.
func searchFlixBusByName(ctx context.Context, from, to, date string, opts SearchOptions) ([]models.GroundRoute, error) {
	routes, err := resolveAndSearch(ctx, from, to, "FlixBus",
		FlixBusAutoComplete,
		func(fromCity, toCity FlixBusCity) ([]models.GroundRoute, error) {
			results, err := SearchFlixBus(ctx, fromCity.ID, toCity.ID, date, opts)
			if err != nil {
				return nil, err
			}
			// Enrich city names
			for i := range results {
				if results[i].Departure.City == "" {
					results[i].Departure.City = fromCity.Name
				}
				if results[i].Arrival.City == "" {
					results[i].Arrival.City = toCity.Name
				}
			}
			return results, nil
		},
	)
	return routes, err
}

// searchRegioJetByName resolves city names and searches RegioJet.
func searchRegioJetByName(ctx context.Context, from, to, date string, opts SearchOptions) ([]models.GroundRoute, error) {
	return resolveAndSearch(ctx, from, to, "RegioJet",
		RegioJetAutoComplete,
		func(fromCity, toCity RegioJetCity) ([]models.GroundRoute, error) {
			return SearchRegioJet(ctx, fromCity.ID, toCity.ID, date, opts)
		},
	)
}

// searchTransitousByName geocodes city names to coordinates and searches Transitous.
func searchTransitousByName(ctx context.Context, from, to, date string) ([]models.GroundRoute, error) {
	fromGeo, err := geocodeCity(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("geocode from city: %w", err)
	}
	toGeo, err := geocodeCity(ctx, to)
	if err != nil {
		return nil, fmt.Errorf("geocode to city: %w", err)
	}
	routes, err := SearchTransitous(ctx, fromGeo.lat, fromGeo.lon, toGeo.lat, toGeo.lon, date)
	// MOTIS names coordinate endpoints with placeholders (START/END), so the
	// by-name search labels routes with the cities the caller asked for.
	for i := range routes {
		routes[i].Departure.City, routes[i].Arrival.City = from, to
	}
	return routes, err
}

// geoCoord holds a latitude/longitude pair from geocoding.
type geoCoord struct {
	lat float64
	lon float64
}

// geoCityCache caches city name to coordinate lookups.
var geoCityCache = struct {
	sync.RWMutex
	entries map[string]geoCoord
}{entries: make(map[string]geoCoord)}

// geocodeCity resolves a city name to coordinates using Nominatim.
func geocodeCity(ctx context.Context, city string) (geoCoord, error) {
	key := strings.ToLower(strings.TrimSpace(city))

	geoCityCache.RLock()
	if entry, ok := geoCityCache.entries[key]; ok {
		geoCityCache.RUnlock()
		return entry, nil
	}
	geoCityCache.RUnlock()

	params := url.Values{
		"q":      {city},
		"format": {"json"},
		"limit":  {"1"},
	}
	apiURL := "https://nominatim.openstreetmap.org/search?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return geoCoord{}, err
	}
	req.Header.Set("User-Agent", "trvl/1.0 (travel agent; github.com/MikkoParkkola/trvl)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return geoCoord{}, fmt.Errorf("nominatim: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return geoCoord{}, fmt.Errorf("nominatim: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return geoCoord{}, fmt.Errorf("nominatim read: %w", err)
	}

	var results []struct {
		Lat string `json:"lat"`
		Lon string `json:"lon"`
	}
	if err := json.Unmarshal(body, &results); err != nil {
		return geoCoord{}, fmt.Errorf("nominatim decode: %w", err)
	}
	if len(results) == 0 {
		return geoCoord{}, fmt.Errorf("no geocoding results for %q", city)
	}

	var lat, lon float64
	if _, err := fmt.Sscanf(results[0].Lat, "%f", &lat); err != nil {
		return geoCoord{}, fmt.Errorf("parse lat %q: %w", results[0].Lat, err)
	}
	if _, err := fmt.Sscanf(results[0].Lon, "%f", &lon); err != nil {
		return geoCoord{}, fmt.Errorf("parse lon %q: %w", results[0].Lon, err)
	}

	coord := geoCoord{lat: lat, lon: lon}
	geoCityCache.Lock()
	geoCityCache.entries[key] = coord
	geoCityCache.Unlock()

	return coord, nil
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
			// The top of a range moves with its bottom so a range never mixes
			// two currencies; if it cannot be converted, it is dropped.
			if r.PriceMax > 0 {
				if top, ok := conv(ctx, r.PriceMax, r.Currency, target); ok && top > 0 {
					r.PriceMax = top
				} else {
					r.PriceMax = 0
				}
			}
			r.Price = converted
			r.Currency = target
			r.ComparablePrice = converted
		}
		// else: leave as-is, ComparablePrice=0 => incomparable tail
	}
}

// sortGroundRoutes performs the stable cross-currency comparable-price sort
// (used by search and directly by unit tests for determinism verification).
// The comparable group ranks first, live fares before estimates inside it;
// unconvertible priced routes follow by currency, live first within each
// currency, and routes without a fare sink last.
func sortGroundRoutes(routes []models.GroundRoute) {
	liveTier := func(r models.GroundRoute) int {
		if r.PriceProvenance == models.PriceLive {
			return 0
		}
		return 1
	}
	sort.SliceStable(routes, func(i, j int) bool {
		a, b := routes[i], routes[j]
		ac, bc := a.ComparablePrice > 0, b.ComparablePrice > 0
		if ac != bc {
			return ac
		}
		if ac && bc {
			if ta, tb := liveTier(a), liveTier(b); ta != tb {
				return ta < tb
			}
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
			pa, pb := a.Price, b.Price
			ra, rb := pa > 0, pb > 0
			if ra != rb {
				return ra
			}
			au, bu := strings.ToUpper(strings.TrimSpace(a.Currency)), strings.ToUpper(strings.TrimSpace(b.Currency))
			if au != bu {
				return au < bu
			}
			if ta, tb := liveTier(a), liveTier(b); ra && ta != tb {
				return ta < tb
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
		// A count that is not known is no evidence of a direct train.
		if a.TransfersUnknown != b.TransfersUnknown {
			return !a.TransfersUnknown
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
