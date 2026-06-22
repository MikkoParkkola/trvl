package ground

import (
	"context"
	"encoding/json"
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

// isProviderNotApplicable returns true when the error indicates that a provider
// simply does not serve the requested route (e.g. "no DB station for Helsinki")
// or that the provider was throttled during a burst of calls. Both are expected
// during broad multi-provider searches and should be logged at DEBUG level
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
		})
		r.Confidence = &c
	}
}

// rather than WARN to avoid polluting normal operation output.
func isProviderNotApplicable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, " station for ") ||
		strings.Contains(msg, " city found for ") ||
		strings.Contains(msg, " port for ") ||
		strings.Contains(msg, "no route for ") ||
		strings.Contains(msg, "no Tallink route") ||
		strings.Contains(msg, "no Eurostar route") ||
		strings.Contains(msg, "no DFDS route") ||
		strings.Contains(msg, "no Stena Line route") ||
		strings.Contains(msg, "rate limiter: rate: Wait") ||
		strings.Contains(msg, "would exceed context deadline") ||
		strings.Contains(msg, "context deadline exceeded")
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

		if opts.MaxPrice > 0 && route.Price > opts.MaxPrice {
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

// scheduleOnlyProviders is the set of providers whose results are kept even
// when price is 0 (they provide schedule data without live pricing).
var scheduleOnlyProviders = map[string]bool{
	"distribusion": true, "transitous": true, "db": true, "ns": true,
	"oebb": true, "vr": true, "european_sleeper": true, "snalltaget": true,
	"tallink": true, "stenaline": true,
	"dfds": true, "vikingline": true, "eckeroline": true, "finnlines": true,
	"ferryhopper": true,
}

func shouldKeepGroundRoute(route models.GroundRoute) bool {
	return route.Price > 0 || scheduleOnlyProviders[strings.ToLower(route.Provider)]
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
		return nil, fmt.Errorf("no %s city found for %q", providerName, from)
	}

	toCities, err := autoComplete(ctx, to)
	if err != nil {
		return nil, fmt.Errorf("resolve to city: %w", err)
	}
	if len(toCities) == 0 {
		return nil, fmt.Errorf("no %s city found for %q", providerName, to)
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
	return SearchTransitous(ctx, fromGeo.lat, fromGeo.lon, toGeo.lat, toGeo.lon, date)
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
