// Package hotels provides hotel search and price lookup via Google's internal
// batchexecute API (rpcids AtySUc and yY52ce).
package hotels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// nominatimURL is the OpenStreetMap Nominatim geocoding API endpoint.
const nominatimURL = "https://nominatim.openstreetmap.org/search"

// nominatimResult represents a single result from the Nominatim API.
type nominatimResult struct {
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
	DisplayName string `json:"display_name"`
}

// geoCache stores resolved coordinates to avoid repeated Nominatim calls.
var geoCache = struct {
	sync.RWMutex
	entries map[string]geoEntry
}{entries: make(map[string]geoEntry)}

type geoEntry struct {
	lat, lon float64
}

// ResolveLocation converts a location name (e.g. "Helsinki") to lat/lon
// coordinates using the Nominatim (OpenStreetMap) geocoding API.
//
// Results are cached in memory so repeated queries for the same location
// do not hit the external API.
func ResolveLocation(ctx context.Context, query string) (lat, lon float64, err error) {
	// Check cache first.
	geoCache.RLock()
	if entry, ok := geoCache.entries[query]; ok {
		geoCache.RUnlock()
		return entry.lat, entry.lon, nil
	}
	geoCache.RUnlock()

	lat, lon, err = nominatimLookup(ctx, query)
	if err != nil {
		return 0, 0, err
	}

	// Cache the result.
	geoCache.Lock()
	geoCache.entries[query] = geoEntry{lat: lat, lon: lon}
	geoCache.Unlock()

	return lat, lon, nil
}

// nominatimLookup performs the actual HTTP call to Nominatim.
//
// Coverage exclusion: HTTP call to external Nominatim API.
// Tested indirectly via destinations/info_test.go with URL override and
// hotels/search_extra_test.go with cache-based mock.
func nominatimLookup(ctx context.Context, query string) (float64, float64, error) {
	u, _ := url.Parse(nominatimURL)
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	q.Set("limit", "1")
	u.RawQuery = q.Encode()

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, 0, fmt.Errorf("create nominatim request: %w", err)
	}
	// Nominatim requires a meaningful User-Agent per their usage policy.
	req.Header.Set("User-Agent", "trvl/1.0 (hotel search tool)")

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("nominatim request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, 0, fmt.Errorf("read nominatim response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("nominatim returned status %d", resp.StatusCode)
	}

	var results []nominatimResult
	if err := json.Unmarshal(body, &results); err != nil {
		return 0, 0, fmt.Errorf("parse nominatim response: %w", err)
	}

	if len(results) == 0 {
		return 0, 0, fmt.Errorf("no geocoding results for %q", query)
	}

	var lat, lon float64
	if _, err := fmt.Sscanf(results[0].Lat, "%f", &lat); err != nil {
		return 0, 0, fmt.Errorf("parse latitude %q: %w", results[0].Lat, err)
	}
	if _, err := fmt.Sscanf(results[0].Lon, "%f", &lon); err != nil {
		return 0, 0, fmt.Errorf("parse longitude %q: %w", results[0].Lon, err)
	}

	return lat, lon, nil
}
