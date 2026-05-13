package destinations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

const overpassURL = "https://overpass-api.de/api/interpreter"

var overpassAPIURL = overpassURL

// osmCache stores POI results keyed by "lat,lon,radius,category".
var osmCache = struct {
	sync.RWMutex
	entries map[string]osmCacheEntry
}{entries: make(map[string]osmCacheEntry)}

type osmCacheEntry struct {
	pois    []models.NearbyPOI
	fetched time.Time
}

const osmCacheTTL = 1 * time.Hour

// osmRateLimiter enforces 1 req/s to be polite to Overpass.
var osmRateLimiter = struct {
	sync.Mutex
	lastReq time.Time
}{}

func waitForOSMRateLimit(ctx context.Context) error {
	osmRateLimiter.Lock()
	wait := time.Duration(0)
	if elapsed := time.Since(osmRateLimiter.lastReq); elapsed < time.Second {
		wait = time.Second - elapsed
	}
	osmRateLimiter.Unlock()

	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}

	osmRateLimiter.Lock()
	osmRateLimiter.lastReq = time.Now()
	osmRateLimiter.Unlock()
	return nil
}

// osmTourismCategories lists valid tourism= values for the Overpass query.
var osmTourismCategories = map[string]bool{
	"hotel":      true,
	"museum":     true,
	"attraction": true,
	"gallery":    true,
	"viewpoint":  true,
}

// osmAmenityCategories lists valid amenity= values to prevent Overpass QL injection.
// Only whitelisted categories are interpolated into the query string.
var osmAmenityCategories = map[string]bool{
	"restaurant":       true,
	"cafe":             true,
	"bar":              true,
	"pub":              true,
	"fast_food":        true,
	"pharmacy":         true,
	"hospital":         true,
	"bank":             true,
	"atm":              true,
	"fuel":             true,
	"parking":          true,
	"taxi":             true,
	"bus_station":      true,
	"marketplace":      true,
	"place_of_worship": true,
	"theatre":          true,
	"cinema":           true,
	"library":          true,
	"nightclub":        true,
	"ice_cream":        true,
}

// overpassResponse is the JSON shape from the Overpass API.
type overpassResponse struct {
	Elements []overpassElement `json:"elements"`
}

type overpassElement struct {
	Type   string            `json:"type"`
	ID     int64             `json:"id"`
	Lat    float64           `json:"lat"`
	Lon    float64           `json:"lon"`
	Center *overpassCenter   `json:"center,omitempty"` // for way elements
	Tags   map[string]string `json:"tags"`
}

type overpassCenter struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// GetNearbyPOIs fetches points of interest near a location using OSM Overpass.
func GetNearbyPOIs(ctx context.Context, lat, lon float64, radiusMeters int, category string) ([]models.NearbyPOI, error) {
	if radiusMeters <= 0 {
		radiusMeters = 500
	}
	if radiusMeters > 5000 {
		radiusMeters = 5000
	}

	cacheKey := fmt.Sprintf("%.4f,%.4f,%d,%s", lat, lon, radiusMeters, category)

	osmCache.RLock()
	if entry, ok := osmCache.entries[cacheKey]; ok && time.Since(entry.fetched) < osmCacheTTL {
		osmCache.RUnlock()
		return entry.pois, nil
	}
	osmCache.RUnlock()

	query := buildOverpassQuery(lat, lon, radiusMeters, category)

	if err := waitForOSMRateLimit(ctx); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, overpassAPIURL, strings.NewReader("data="+query))
	if err != nil {
		return nil, fmt.Errorf("create overpass request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "trvl/1.0 (nearby POI search)")

	resp, err := destinationsSlowClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("overpass request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read overpass response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("overpass returned status %d: %s", resp.StatusCode, string(body))
	}

	var ovResp overpassResponse
	if err := json.Unmarshal(body, &ovResp); err != nil {
		return nil, fmt.Errorf("parse overpass response: %w", err)
	}

	pois := parseOverpassElements(ovResp.Elements, lat, lon, category)

	// Sort by distance.
	sort.Slice(pois, func(i, j int) bool {
		return pois[i].Distance < pois[j].Distance
	})

	// Cap at 20 results.
	if len(pois) > 20 {
		pois = pois[:20]
	}

	osmCache.Lock()
	osmCache.entries[cacheKey] = osmCacheEntry{pois: pois, fetched: time.Now()}
	osmCache.Unlock()

	return pois, nil
}

// buildOverpassQuery constructs an Overpass QL query.
func buildOverpassQuery(lat, lon float64, radius int, category string) string {
	if category == "all" || category == "" {
		// Query all amenity types we support plus tourism types.
		return fmt.Sprintf(`[out:json][timeout:10];
(
  node(around:%d,%.6f,%.6f)[amenity~"restaurant|cafe|bar|pharmacy|atm|bank|supermarket|hospital|police|museum"];
  way(around:%d,%.6f,%.6f)[amenity~"restaurant|cafe|bar|pharmacy|atm|bank|supermarket|hospital|police|museum"];
  node(around:%d,%.6f,%.6f)[tourism~"hotel|museum|attraction|gallery|viewpoint"];
  way(around:%d,%.6f,%.6f)[tourism~"hotel|museum|attraction|gallery|viewpoint"];
);
out center 20;`, radius, lat, lon, radius, lat, lon, radius, lat, lon, radius, lat, lon)
	}

	// Check if it's a tourism category.
	if osmTourismCategories[category] {
		return fmt.Sprintf(`[out:json][timeout:10];
(
  node(around:%d,%.6f,%.6f)[tourism=%s];
  way(around:%d,%.6f,%.6f)[tourism=%s];
);
out center 20;`, radius, lat, lon, category, radius, lat, lon, category)
	}

	// Default: amenity category — validate against whitelist to prevent Overpass QL injection.
	if !osmAmenityCategories[category] {
		// Unknown category: fall back to a safe generic query instead of interpolating user input.
		return fmt.Sprintf(`[out:json][timeout:10];
(
  node(around:%d,%.6f,%.6f)[amenity=restaurant];
  way(around:%d,%.6f,%.6f)[amenity=restaurant];
);
out center 20;`, radius, lat, lon, radius, lat, lon)
	}
	return fmt.Sprintf(`[out:json][timeout:10];
(
  node(around:%d,%.6f,%.6f)[amenity=%s];
  way(around:%d,%.6f,%.6f)[amenity=%s];
);
out center 20;`, radius, lat, lon, category, radius, lat, lon, category)
}

// parseOverpassElements converts Overpass elements to NearbyPOI models.
func parseOverpassElements(elements []overpassElement, queryLat, queryLon float64, category string) []models.NearbyPOI {
	var pois []models.NearbyPOI

	for _, elem := range elements {
		name := elem.Tags["name"]
		if name == "" {
			continue // Skip unnamed POIs.
		}

		poiLat, poiLon := elem.Lat, elem.Lon
		if elem.Center != nil {
			poiLat = elem.Center.Lat
			poiLon = elem.Center.Lon
		}

		if poiLat == 0 && poiLon == 0 {
			continue
		}

		// Determine type from tags.
		poiType := elem.Tags["amenity"]
		if poiType == "" {
			poiType = elem.Tags["tourism"]
		}
		if poiType == "" {
			poiType = category
		}

		poi := models.NearbyPOI{
			Name:     name,
			Type:     poiType,
			Lat:      poiLat,
			Lon:      poiLon,
			Distance: haversineMeters(queryLat, queryLon, poiLat, poiLon),
			Cuisine:  elem.Tags["cuisine"],
			Hours:    elem.Tags["opening_hours"],
			Phone:    elem.Tags["phone"],
			Website:  elem.Tags["website"],
		}

		pois = append(pois, poi)
	}

	return pois
}

// haversineMeters computes the distance in meters between two lat/lon points.
func haversineMeters(lat1, lon1, lat2, lon2 float64) int {
	const earthRadiusM = 6371000.0

	dLat := (lat2 - lat1) * math.Pi / 180.0
	dLon := (lon2 - lon1) * math.Pi / 180.0

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180.0)*math.Cos(lat2*math.Pi/180.0)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return int(math.Round(earthRadiusM * c))
}
