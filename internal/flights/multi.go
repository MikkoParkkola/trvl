package flights

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

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
	var statuses []models.ProviderStatus
	var errs []error
	attempted, answered := 0, 0
	var wg sync.WaitGroup

	// AFKLM quota protection for multi-airport spread (#471):
	// The find / tripsearch path + SearchMultiAirport fans N origins (home + nearby + rail+fly)
	// to other providers unchanged. For AFKLM (1 QPS + hard 100 req/day quota) we issue
	// at most one query per logical search, using the first (primary) origin/dest pair.
	// Sub-searches in the fanout are suppressed via opts.suppressAFKLM so they
	// treat AFKLM as unconfigured. AFKLM results carry their real departure airport codes
	// and are simply pooled. Non-AFKLM providers are unaffected.
	subOpts := opts
	if opts.ReturnDate != "" && len(origins) > 0 && len(destinations) > 0 {
		primO := origins[0]
		primD := destinations[0]
		afklmFl, afklmSt := searchAFKLMNativeRoundTrip(ctx, primO, primD, date, opts.ReturnDate, opts)
		for _, st := range afklmSt {
			st.Name = routeLabel(st, primO, primD)
			statuses = append(statuses, st)
		}
		if len(afklmFl) > 0 {
			mu.Lock()
			allFlights = append(allFlights, afklmFl...)
			mu.Unlock()
		}
		// Suppress AFKLM in the parallel spread subs (they would otherwise each
		// call NewProvider + search, burning quota). Threaded via copied opts —
		// no shared-global mutation, so concurrent SearchMultiAirport calls are race-free.
		subOpts.suppressAFKLM = true
	}

	for _, orig := range origins {
		for _, dest := range destinations {
			if orig == dest {
				continue
			}
			attempted++
			wg.Add(1)
			go func(o, d string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				result, err := SearchFlightsWithClient(ctx, client, o, d, date, subOpts)
				mu.Lock()
				defer mu.Unlock()
				// Keep every combination's provider statuses, labelled with
				// its route, so a blocked provider is reported rather than
				// silently dropped (MIK-8041).
				if result != nil {
					for _, st := range result.ProviderStatuses {
						st.Name = routeLabel(st, o, d)
						statuses = append(statuses, st)
					}
				}
				if err != nil {
					errs = append(errs, fmt.Errorf("%s-%s: %w", o, d, err))
					if !hasFailedStatus(result) {
						// A pair that failed without provider evidence (a
						// deadline, a nil result) must still count as missing.
						statuses = append(statuses, models.ProviderStatus{
							ID:     "flight_search",
							Name:   "Flight search (" + o + "-" + d + ")",
							Status: models.ClassifyProviderError(err),
							Error:  err.Error(),
						})
					}
					return
				}
				answered++
				if result.Success {
					allFlights = append(allFlights, result.Flights...)
				}
			}(orig, dest)
		}
	}

	wg.Wait()

	sortFlightResults(allFlights, opts.SortBy)

	completeness := models.ComputeCompleteness(statuses)
	// Only a search with no fares at all is a failure: AFKLM fares from the
	// primary pair still count when every fanned-out pair failed.
	if allPairsFailed(len(allFlights), attempted, answered, len(errs)) {
		err := errors.Join(errs...)
		return &models.FlightSearchResult{
			Error:            err.Error(),
			TripType:         tripTypeForSearch(opts),
			ProviderStatuses: statuses,
			Completeness:     completeness,
		}, err
	}
	return &models.FlightSearchResult{
		// A pair that answered with no flights is a definitive empty answer,
		// the same as a single-route search.
		Success:          answered > 0 || len(allFlights) > 0,
		Count:            len(allFlights),
		TripType:         tripTypeForSearch(opts),
		Flights:          allFlights,
		ProviderStatuses: statuses,
		Completeness:     completeness,
	}, nil
}

func routeLabel(st models.ProviderStatus, origin, dest string) string {
	name := st.Name
	if name == "" {
		name = st.ID
	}
	return name + " (" + origin + "-" + dest + ")"
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

// allPairsFailed reports a search that produced nothing usable: no fares,
// and no pair answered.
func allPairsFailed(flights, attempted, answered, errs int) bool {
	return flights == 0 && attempted > 0 && answered == 0 && errs > 0
}

func hasFailedStatus(result *models.FlightSearchResult) bool {
	if result == nil {
		return false
	}
	for _, st := range result.ProviderStatuses {
		if st.Error != "" {
			return true
		}
	}
	return false
}
