package destinations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/jsonutil"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// googleMapsSearchURL is the base URL for Google Maps search pages.
const googleMapsSearchURL = "https://www.google.com/maps/search/"

// googleSearchURL is the base URL for the tbm=map endpoint.
const googleSearchURL = "https://www.google.com/search"

var googleMapsAPIURL = googleMapsSearchURL
var googleSearchAPIURL = googleSearchURL

// mapsCache stores Google Maps rated place results.
var mapsCache = struct {
	sync.RWMutex
	entries map[string]mapsCacheEntry
}{entries: make(map[string]mapsCacheEntry)}

type mapsCacheEntry struct {
	places  []models.RatedPlace
	fetched time.Time
}

const mapsCacheTTL = 30 * time.Minute

// preloadURLPattern matches the preload link containing the pb= search URL.
var preloadURLPattern = regexp.MustCompile(`href="(/search\?tbm=map[^"]+)"`)

// SearchGoogleMapsPlaces searches Google Maps for places near the given coordinates.
// It works without any API key by scraping the Maps search page and extracting
// place data from the embedded JSON response.
func SearchGoogleMapsPlaces(ctx context.Context, lat, lon float64, query string, limit int) ([]models.RatedPlace, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 20 {
		limit = 20
	}

	cacheKey := fmt.Sprintf("%.4f,%.4f,%s,%d", lat, lon, query, limit)

	mapsCache.RLock()
	if entry, ok := mapsCache.entries[cacheKey]; ok && time.Since(entry.fetched) < mapsCacheTTL {
		mapsCache.RUnlock()
		return entry.places, nil
	}
	mapsCache.RUnlock()

	places, err := fetchMapsPlaces(ctx, lat, lon, query, limit)
	if err != nil {
		return nil, err
	}

	mapsCache.Lock()
	mapsCache.entries[cacheKey] = mapsCacheEntry{places: places, fetched: time.Now()}
	mapsCache.Unlock()

	return places, nil
}

// fetchMapsPlaces performs the two-step Google Maps scrape:
// 1. GET the Maps search page to extract the pb= URL from a preload link
// 2. GET the pb= URL to get JSON place data
func fetchMapsPlaces(ctx context.Context, lat, lon float64, query string, limit int) ([]models.RatedPlace, error) {
	c := batchexec.NewClient()
	c.SetNoCache(true)

	// Step 1: Load the Maps search page.
	encodedQuery := url.PathEscape(strings.ReplaceAll(query, " ", "+"))
	pageURL := fmt.Sprintf("%s%s+near+%s/@%.4f,%.4f,14z",
		googleMapsAPIURL, encodedQuery, formatCoords(lat, lon), lat, lon)

	status, body, err := c.Get(ctx, pageURL)
	if err != nil {
		return nil, fmt.Errorf("maps page request: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("maps page returned status %d", status)
	}

	// Step 2: Extract the preload pb= URL from HTML.
	pageStr := string(body)
	pbMatch := preloadURLPattern.FindStringSubmatch(pageStr)
	if len(pbMatch) < 2 {
		// Fallback: try tbm=map direct search.
		return fetchMapsPlacesDirect(ctx, c, lat, lon, query, limit)
	}

	// The pb= path is site-relative; resolve it against the search host so a
	// test that points googleSearchAPIURL at a local server reaches it too.
	pbURL := strings.TrimSuffix(googleSearchAPIURL, "/search") + strings.ReplaceAll(pbMatch[1], "&amp;", "&")

	// Step 3: Fetch the pb= URL for JSON data.
	status2, body2, err := c.Get(ctx, pbURL)
	if err != nil {
		return nil, fmt.Errorf("maps pb request: %w", err)
	}
	if status2 != 200 {
		return nil, fmt.Errorf("maps pb returned status %d", status2)
	}

	return parseMapsResponse(body2, lat, lon, limit)
}

// fetchMapsPlacesDirect uses the tbm=map endpoint as a fallback.
func fetchMapsPlacesDirect(ctx context.Context, c *batchexec.Client, lat, lon float64, query string, limit int) ([]models.RatedPlace, error) {
	directURL := fmt.Sprintf("%s?tbm=map&authuser=0&hl=en&gl=us&q=%s+near+%.4f,%.4f",
		googleSearchAPIURL, url.QueryEscape(query), lat, lon)

	status, body, err := c.Get(ctx, directURL)
	if err != nil {
		return nil, fmt.Errorf("maps direct request: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("maps direct returned status %d", status)
	}

	return parseMapsResponse(body, lat, lon, limit)
}

// parseMapsResponse strips the anti-XSSI prefix and extracts place data from
// the Google Maps JSON response.
func parseMapsResponse(body []byte, queryLat, queryLon float64, limit int) ([]models.RatedPlace, error) {
	stripped := strings.TrimSpace(string(body))
	if strings.HasPrefix(stripped, ")]}'") {
		stripped = strings.TrimPrefix(stripped, ")]}'")
		stripped = strings.TrimSpace(stripped)
	}

	var raw any
	if err := json.Unmarshal([]byte(stripped), &raw); err != nil {
		return nil, fmt.Errorf("parse maps JSON: %w", err)
	}

	return extractPlaces(raw, queryLat, queryLon, limit), nil
}

// extractPlaces walks the nested JSON arrays to find place entries.
// Google Maps search results are deeply nested arrays where place data lives
// at predictable indices within each result entry.
func extractPlaces(data any, queryLat, queryLon float64, limit int) []models.RatedPlace {
	var places []models.RatedPlace

	// The response is a deeply nested array. Place listings are typically
	// found by traversing the array structure. We search recursively for
	// arrays that look like place entries (contain a name string, rating number,
	// and coordinate pair).
	findPlaceArrays(data, queryLat, queryLon, &places, limit)

	if len(places) > limit {
		places = places[:limit]
	}

	return places
}

// findPlaceArrays recursively searches the JSON structure for place data.
// A place entry is identified by having a string name, a float rating (1-5),
// and coordinate numbers in a recognizable pattern.
func findPlaceArrays(data any, queryLat, queryLon float64, places *[]models.RatedPlace, limit int) {
	if len(*places) >= limit {
		return
	}

	arr, ok := data.([]any)
	if !ok {
		return
	}

	// Try to extract a place from this array level.
	if place, ok := tryExtractPlace(arr, queryLat, queryLon); ok {
		// Avoid duplicates by name.
		for _, existing := range *places {
			if existing.Name == place.Name {
				return
			}
		}
		*places = append(*places, place)
		return
	}

	// Recurse into sub-arrays.
	for _, item := range arr {
		findPlaceArrays(item, queryLat, queryLon, places, limit)
	}
}

// tryExtractPlace attempts to interpret an array as a Google Maps place entry.
// Place data in the Maps JSON follows patterns where:
// - Index 11: name (string)
// - Index 4: rating (float, 1.0-5.0)
// - Index 9: coordinates array [nil, nil, lat, lon]
// - Index 7: review count
// - Index 14: category/type
// - Index 18: address string
func tryExtractPlace(arr []any, queryLat, queryLon float64) (models.RatedPlace, bool) {
	// Need at least 12 elements to have name and rating.
	if len(arr) < 12 {
		return models.RatedPlace{}, false
	}

	// Index 11: name must be a non-empty string.
	name, ok := arr[11].(string)
	if !ok || name == "" || len(name) > 200 {
		return models.RatedPlace{}, false
	}

	// Index 4: rating must be a number between 1.0 and 5.0.
	rating, ok := jsonutil.ToFloat(arr[4])
	if !ok || rating < 1.0 || rating > 5.0 {
		return models.RatedPlace{}, false
	}

	place := models.RatedPlace{
		Name:   name,
		Rating: rating,
	}

	// Index 9: coordinates [nil, nil, lat, lon]
	if coordArr, ok := arr[9].([]any); ok && len(coordArr) >= 4 {
		if lat, ok := jsonutil.ToFloat(coordArr[2]); ok {
			if lon, ok := jsonutil.ToFloat(coordArr[3]); ok {
				place.Distance = haversineMeters(queryLat, queryLon, lat, lon)
			}
		}
	}

	// Index 14: category string.
	if len(arr) > 14 {
		if cat, ok := arr[14].(string); ok {
			place.Category = cat
		}
	}

	// Index 18: address string.
	if len(arr) > 18 {
		if addr, ok := arr[18].(string); ok {
			place.Address = addr
		}
	}

	return place, true
}

// formatCoords formats lat,lon for URL embedding.
func formatCoords(lat, lon float64) string {
	return fmt.Sprintf("%.4f,%.4f", lat, lon)
}
