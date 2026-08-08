// Package deals aggregates travel deals from free RSS feeds.
package deals

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Deal represents a single travel deal parsed from an RSS feed.
type Deal struct {
	Title       string    `json:"title"`
	Price       float64   `json:"price,omitempty"`
	Currency    string    `json:"currency,omitempty"`
	Origin      string    `json:"origin,omitempty"`
	Destination string    `json:"destination,omitempty"`
	Airline     string    `json:"airline,omitempty"`
	Stops       string    `json:"stops,omitempty"`
	CabinClass  string    `json:"cabin_class,omitempty"`
	DateRange   string    `json:"date_range,omitempty"`
	Type        string    `json:"type"`
	Source      string    `json:"source"`
	URL         string    `json:"url"`
	Published   time.Time `json:"published"`
	Summary     string    `json:"summary,omitempty"`
}

// DealsResult is the structured response from a deals search.
type DealsResult struct {
	Success bool   `json:"success"`
	Count   int    `json:"count"`
	Deals   []Deal `json:"deals"`
	Error   string `json:"error,omitempty"`
}

// DealFilter controls which deals are returned.
type DealFilter struct {
	Origins  []string // filter by departure city/airport
	MaxPrice float64  // max price filter (0 = no limit)
	Type     string   // "error_fare", "deal", "flash_sale", "package"
	HoursAgo int      // only deals from last N hours (default: 48)
}

// AllSources lists all supported deal source keys.
// "google" fetches cheapest destinations from Google Flights Explore.
var AllSources = []string{"google", "secretflying", "fly4free", "holidaypirates", "thepointsguy"}

// SourceFeeds maps source keys to their RSS feed URLs.
// #nosec G101 -- "secretflying" is a publication name; values are public feeds.
var SourceFeeds = map[string]string{
	"secretflying":   "https://www.secretflying.com/feed/",
	"fly4free":       "https://www.fly4free.com/feed/",
	"holidaypirates": "https://www.holidaypirates.com/feed",
	"thepointsguy":   "https://thepointsguy.com/feed/",
}

// SourceNames maps source keys to display names.
var SourceNames = map[string]string{
	"google":         "Google Flights",
	"secretflying":   "Secret Flying",
	"fly4free":       "Fly4Free",
	"holidaypirates": "Holiday Pirates",
	"thepointsguy":   "The Points Guy",
}

// --- Cached deal matching for integration into search results ---

var dealCache struct {
	sync.RWMutex
	deals     []Deal
	fetchedAt time.Time
}

const dealCacheTTL = 30 * time.Minute

// MatchDeals returns any current deals matching the given route.
// Fetches and caches RSS feeds (30-min TTL) so repeated calls are fast.
// Case-insensitive matching on origin/destination (city name or IATA code).
// Returns nil if no deals match or if feeds can't be fetched.
func MatchDeals(ctx context.Context, origin, destination string) []Deal {
	allDeals := getCachedDeals(ctx)
	if len(allDeals) == 0 {
		return nil
	}

	origin = strings.ToLower(strings.TrimSpace(origin))
	destination = strings.ToLower(strings.TrimSpace(destination))

	var matches []Deal
	for _, d := range allDeals {
		dOrigin := strings.ToLower(d.Origin)
		dDest := strings.ToLower(d.Destination)

		// Only match if the deal has BOTH a specific origin and destination.
		// Deals without route info are too noisy to show inline.
		if dOrigin == "" || dDest == "" {
			continue
		}

		originMatch := dOrigin == origin ||
			strings.Contains(dOrigin, origin) ||
			strings.Contains(origin, dOrigin)

		destMatch := dDest == destination ||
			strings.Contains(dDest, destination) ||
			strings.Contains(destination, dDest)

		if originMatch && destMatch {
			matches = append(matches, d)
		}
	}
	return matches
}

// getCachedDeals returns the cached deal list, refreshing if stale.
func getCachedDeals(ctx context.Context) []Deal {
	dealCache.RLock()
	if time.Since(dealCache.fetchedAt) < dealCacheTTL && len(dealCache.deals) > 0 {
		deals := dealCache.deals
		dealCache.RUnlock()
		return deals
	}
	dealCache.RUnlock()

	// Fetch fresh — use a short timeout to avoid slowing down searches.
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	result, err := FetchDeals(fetchCtx, AllSources, DealFilter{HoursAgo: 72})
	if err != nil || !result.Success {
		return nil
	}

	dealCache.Lock()
	dealCache.deals = result.Deals
	dealCache.fetchedAt = time.Now()
	dealCache.Unlock()

	return result.Deals
}
