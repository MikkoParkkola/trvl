package destinations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

const geoapifyPlacesURL = "https://api.geoapify.com/v2/places"

var geoapifyPlacesAPIURL = geoapifyPlacesURL

// geoapifyCache stores walkable POI results.
var geoapifyCache = struct {
	sync.RWMutex
	entries map[string]geoapifyCacheEntry
}{entries: make(map[string]geoapifyCacheEntry)}

type geoapifyCacheEntry struct {
	pois    []models.NearbyPOI
	fetched time.Time
}

const geoapifyCacheTTL = 1 * time.Hour

// geoapifyCategoryMap maps user-facing categories to Geoapify categories.
var geoapifyCategoryMap = map[string]string{
	"restaurant":  "catering.restaurant",
	"cafe":        "catering.cafe",
	"bar":         "catering.bar",
	"pharmacy":    "healthcare.pharmacy",
	"atm":         "service.financial.atm",
	"bank":        "service.financial.bank",
	"supermarket": "commercial.supermarket",
	"hospital":    "healthcare.hospital",
	"museum":      "entertainment.museum",
	"attraction":  "tourism.attraction",
}

// geoapifyPlacesResponse is the JSON shape from the Geoapify Places API.
type geoapifyPlacesResponse struct {
	Features []struct {
		Properties struct {
			Name     string  `json:"name"`
			Category string  `json:"category"`
			Lat      float64 `json:"lat"`
			Lon      float64 `json:"lon"`
			Distance int     `json:"distance"`
			Phone    string  `json:"contact:phone"`
			Website  string  `json:"website"`
			Opening  string  `json:"opening_hours"`
		} `json:"properties"`
	} `json:"features"`
}

// GetWalkablePOIs fetches POIs within walking distance using Geoapify.
// Returns nil (no error) if GEOAPIFY_API_KEY is not set.
func GetWalkablePOIs(ctx context.Context, lat, lon float64, walkMinutes int, category string) ([]models.NearbyPOI, error) {
	apiKey := os.Getenv("GEOAPIFY_API_KEY")
	if apiKey == "" {
		return nil, nil
	}

	if walkMinutes <= 0 {
		walkMinutes = 10
	}

	// Convert walking minutes to approximate radius in meters (avg 80m/min).
	radiusM := walkMinutes * 80

	geoapifyCat := geoapifyCategoryMap[category]
	if geoapifyCat == "" {
		geoapifyCat = "catering.restaurant"
	}

	cacheKey := fmt.Sprintf("%.4f,%.4f,%d,%s", lat, lon, walkMinutes, category)

	geoapifyCache.RLock()
	if entry, ok := geoapifyCache.entries[cacheKey]; ok && time.Since(entry.fetched) < geoapifyCacheTTL {
		geoapifyCache.RUnlock()
		return entry.pois, nil
	}
	geoapifyCache.RUnlock()

	u, _ := url.Parse(geoapifyPlacesAPIURL)
	q := u.Query()
	q.Set("categories", geoapifyCat)
	q.Set("filter", fmt.Sprintf("circle:%f,%f,%d", lon, lat, radiusM))
	q.Set("limit", "20")
	q.Set("apiKey", apiKey)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create geoapify request: %w", err)
	}
	req.Header.Set("User-Agent", "trvl/1.0 (walkable POIs)")

	resp, err := destinationsClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("geoapify request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read geoapify response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("geoapify returned status %d: %s", resp.StatusCode, string(body))
	}

	var geoResp geoapifyPlacesResponse
	if err := json.Unmarshal(body, &geoResp); err != nil {
		return nil, fmt.Errorf("parse geoapify response: %w", err)
	}

	pois := make([]models.NearbyPOI, 0, len(geoResp.Features))
	for _, f := range geoResp.Features {
		if f.Properties.Name == "" {
			continue
		}
		poi := models.NearbyPOI{
			Name:     f.Properties.Name,
			Type:     category,
			Lat:      f.Properties.Lat,
			Lon:      f.Properties.Lon,
			Distance: haversineMeters(lat, lon, f.Properties.Lat, f.Properties.Lon),
			Hours:    f.Properties.Opening,
			Phone:    f.Properties.Phone,
			Website:  f.Properties.Website,
		}
		pois = append(pois, poi)
	}

	sort.Slice(pois, func(i, j int) bool {
		return pois[i].Distance < pois[j].Distance
	})

	geoapifyCache.Lock()
	geoapifyCache.entries[cacheKey] = geoapifyCacheEntry{pois: pois, fetched: time.Now()}
	geoapifyCache.Unlock()

	return pois, nil
}
