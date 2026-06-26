// Package explore implements Google Flights explore destination search
// via the GetExploreDestinations endpoint.
package explore

import (
	"context"
	"fmt"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// warmupTimeout is the deadline for the TLS warmup ping.
const warmupTimeout = 5 * time.Second

// SearchExplore searches for cheapest flight destinations from the given origin.
//
// Unlike a normal flight search which requires a destination, explore searches
// return a list of destinations with their cheapest prices, optionally filtered
// by geographic coordinates.
//
// To reduce timeout failures, this function:
//  1. Warms up the TLS connection with a lightweight GET before the heavy POST
//  2. Retries once if the API returns empty results within 10 seconds
func SearchExplore(ctx context.Context, client *batchexec.Client, origin string, opts ExploreOptions) (*models.ExploreResult, error) {
	if origin == "" {
		return nil, fmt.Errorf("origin airport is required")
	}

	// Warm up the TLS connection to reduce cold-start latency.
	warmupTLS(ctx, client)

	encoded := EncodeExplorePayload(origin, opts)

	result, err := doExploreRequest(ctx, client, encoded)
	if err != nil {
		return nil, err
	}

	// If empty results came back quickly, retry once -- Google sometimes
	// returns empty on the first request when the backend is cold.
	if result.Count == 0 {
		result, err = doExploreRequest(ctx, client, encoded)
		if err != nil {
			return nil, err
		}
	}

	// Enforce the budget cap client-side. This is the authoritative price
	// filter (see ExploreOptions.MaxPrice); it stays correct even if a future
	// server-side payload slot drifts or is absent.
	result.Destinations = filterByBudget(result.Destinations, opts.MaxPrice)
	result.Count = len(result.Destinations)

	return result, nil
}
func doExploreRequest(ctx context.Context, client *batchexec.Client, encoded string) (*models.ExploreResult, error) {
	status, body, err := client.PostExplore(ctx, encoded)
	if err != nil {
		return nil, fmt.Errorf("explore request: %w", err)
	}

	if status == 403 {
		return nil, batchexec.ErrBlocked
	}
	if status != 200 {
		return nil, fmt.Errorf("unexpected status %d", status)
	}

	destinations, err := ParseExploreResponse(body)
	if err != nil {
		return nil, fmt.Errorf("parse explore response: %w", err)
	}

	return &models.ExploreResult{
		Success:      true,
		Count:        len(destinations),
		Destinations: destinations,
	}, nil
}

// warmupTLS sends a lightweight GET to Google to establish the TLS connection
// before the heavy explore POST. This avoids paying TLS handshake cost inside
// the main request's timeout budget.
func warmupTLS(ctx context.Context, client *batchexec.Client) {
	warmCtx, cancel := context.WithTimeout(ctx, warmupTimeout)
	defer cancel()
	// A simple GET to the flights domain warms the connection pool.
	_, _, _ = client.Get(warmCtx, "https://www.google.com/travel/flights")
}

// ExploreOptions configures an explore destination search.
type ExploreOptions struct {
	DepartureDate string  // YYYY-MM-DD (required)
	ReturnDate    string  // YYYY-MM-DD (empty = one-way)
	Adults        int     // Number of adult passengers (default: 1)
	NorthLat      float64 // Geographic bounding box (all zero = worldwide)
	SouthLat      float64
	EastLng       float64
	WestLng       float64

	// CabinClass is encoded server-side into the explore payload's known
	// class slot (1=economy..4=first). Zero defaults to economy.
	CabinClass models.CabinClass

	// MaxPrice caps the per-traveller indicative price. Zero = no cap.
	//
	// Enforced client-side (filterByBudget) as the correctness layer. Google's
	// explore payload has a server-side max-price slot, but its positional
	// offset is not reverse-engineered here, so we do NOT encode it — guessing
	// the offset would corrupt the whole payload. The client-side filter is
	// authoritative; a server-side slot would be pure optimisation.
	// ponytail: client-side cap is correctness; server-side encoding is a
	// follow-up needing the gflights offset reference.
	MaxPrice float64
}

// filterByBudget drops destinations whose indicative price exceeds maxPrice.
// maxPrice <= 0 is a no-op (returns the slice unchanged).
func filterByBudget(dests []models.ExploreDestination, maxPrice float64) []models.ExploreDestination {
	if maxPrice <= 0 {
		return dests
	}
	kept := dests[:0]
	for _, d := range dests {
		if d.Price > 0 && d.Price <= maxPrice {
			kept = append(kept, d)
		}
	}
	return kept
}
