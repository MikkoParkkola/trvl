package flights

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/MikkoParkkola/trvl/internal/flights/afklm"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// SearchMultiAirport searches flights across multiple origin and destination airports.
// Runs all origin×destination combinations in parallel (max 5 concurrent) and merges
// results sorted by price. Each flight already contains departure/arrival airport codes.
func SearchMultiAirport(ctx context.Context, origins, destinations []string, date string, opts SearchOptions) (*models.FlightSearchResult, error) {
	client := DefaultClient()
	opts.defaults()

	if len(origins) == 0 || len(destinations) == 0 || date == "" {
		return nil, fmt.Errorf("origins, destinations, and date are required")
	}

	sem := make(chan struct{}, 5) // max 5 concurrent searches
	var mu sync.Mutex
	var allFlights []models.FlightResult
	var wg sync.WaitGroup

	// AFKLM quota protection for multi-airport spread (#471):
	// The find / tripsearch path + SearchMultiAirport fans N origins (home + nearby + rail+fly)
	// to other providers unchanged. For AFKLM (1 QPS + hard 100 req/day quota) we issue
	// at most one query per logical search, using the first (primary) origin/dest pair.
	// Sub-searches in the fanout are suppressed via the afklmNewProvider seam so they
	// treat AFKLM as unconfigured. AFKLM results carry their real departure airport codes
	// and are simply pooled. Non-AFKLM providers are unaffected.
	if opts.ReturnDate != "" && len(origins) > 0 && len(destinations) > 0 {
		primO := origins[0]
		primD := destinations[0]
		afklmFl, _ := searchAFKLMNativeRoundTrip(ctx, primO, primD, date, opts.ReturnDate, opts)
		if len(afklmFl) > 0 {
			mu.Lock()
			allFlights = append(allFlights, afklmFl...)
			mu.Unlock()
		}
		// Suppress AFKLM in the parallel spread subs (they would otherwise each
		// call NewProvider + search, burning quota). Restore after.
		origAF := afklmNewProvider
		afklmNewProvider = func() (*afklm.AFKLMProvider, error) { return nil, afklm.ErrNoCredential }
		defer func() { afklmNewProvider = origAF }()
	}

	for _, orig := range origins {
		for _, dest := range destinations {
			if orig == dest {
				continue
			}
			wg.Add(1)
			go func(o, d string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				result, err := SearchFlightsWithClient(ctx, client, o, d, date, opts)
				if err != nil || !result.Success {
					return // skip failed combos silently
				}

				mu.Lock()
				allFlights = append(allFlights, result.Flights...)
				mu.Unlock()
			}(orig, dest)
		}
	}

	wg.Wait()

	sortFlightResults(allFlights, opts.SortBy)

	return &models.FlightSearchResult{
		Success:  len(allFlights) > 0,
		Count:    len(allFlights),
		TripType: tripTypeForSearch(opts),
		Flights:  allFlights,
	}, nil
}

// ParseAirports splits a comma-separated airport string into a slice.
// Trims whitespace and uppercases each code.
func ParseAirports(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToUpper(p))
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// ParseFlightLocations extends ParseAirports with city-name resolution.
// Each comma-separated token is treated as:
//   - An IATA code (exactly 3 uppercase ASCII letters) → kept as-is
//   - A known city name → expanded to all airports serving that city
//   - Anything else → kept as-is (unknown code passthrough)
//
// Returned slice contains no duplicates and preserves encounter order.
func ParseFlightLocations(s string) []string {
	tokens := ParseAirports(s)
	if len(tokens) == 0 {
		return tokens
	}
	seen := make(map[string]struct{}, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if models.IsIATACode(token) {
			if _, ok := seen[token]; !ok {
				seen[token] = struct{}{}
				out = append(out, token)
			}
			continue
		}
		airports := models.ResolveCityToAirports(token)
		if len(airports) == 0 {
			// Our static map is incomplete — pass unknown tokens through so
			// the search layer can reject them with a clear error.
			if _, ok := seen[token]; !ok {
				seen[token] = struct{}{}
				out = append(out, token)
			}
			continue
		}
		for _, code := range airports {
			if _, ok := seen[code]; !ok {
				seen[code] = struct{}{}
				out = append(out, code)
			}
		}
	}
	return out
}
