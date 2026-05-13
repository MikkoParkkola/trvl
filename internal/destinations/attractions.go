package destinations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

const openTripMapURL = "https://api.opentripmap.com/0.1/en/places/radius"

var openTripMapAPIURL = openTripMapURL

// attractionsCache stores attraction results keyed by "lat,lon,radius".
var attractionsCache = struct {
	sync.RWMutex
	entries map[string]attractionsCacheEntry
}{entries: make(map[string]attractionsCacheEntry)}

type attractionsCacheEntry struct {
	attractions []models.Attraction
	fetched     time.Time
}

const attractionsCacheTTL = 6 * time.Hour

// openTripMapResponse element.
type openTripMapPlace struct {
	XID   string  `json:"xid"`
	Name  string  `json:"name"`
	Kinds string  `json:"kinds"`
	Dist  float64 `json:"dist"`
	Point struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	} `json:"point"`
	Wikipedia string `json:"wikipedia"`
}

// GetAttractions fetches tourist attractions from OpenTripMap near a location.
// Returns nil (no error) if OPENTRIPMAP_API_KEY is not set.
func GetAttractions(ctx context.Context, lat, lon float64, radiusMeters int) ([]models.Attraction, error) {
	apiKey := os.Getenv("OPENTRIPMAP_API_KEY")
	if apiKey == "" {
		return nil, nil
	}

	if radiusMeters <= 0 {
		radiusMeters = 1000
	}
	if radiusMeters > 10000 {
		radiusMeters = 10000
	}

	cacheKey := fmt.Sprintf("%.4f,%.4f,%d", lat, lon, radiusMeters)

	attractionsCache.RLock()
	if entry, ok := attractionsCache.entries[cacheKey]; ok && time.Since(entry.fetched) < attractionsCacheTTL {
		attractionsCache.RUnlock()
		return entry.attractions, nil
	}
	attractionsCache.RUnlock()

	apiURL := fmt.Sprintf("%s?radius=%d&lon=%.6f&lat=%.6f&kinds=interesting_places&rate=3&format=json&apikey=%s",
		openTripMapAPIURL, radiusMeters, lon, lat, apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create opentripmap request: %w", err)
	}
	req.Header.Set("User-Agent", "trvl/1.0 (tourist attractions)")

	resp, err := destinationsClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opentripmap request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read opentripmap response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opentripmap returned status %d: %s", resp.StatusCode, string(body))
	}

	var places []openTripMapPlace
	if err := json.Unmarshal(body, &places); err != nil {
		return nil, fmt.Errorf("parse opentripmap response: %w", err)
	}

	attractions := make([]models.Attraction, 0, len(places))
	for _, p := range places {
		if p.Name == "" {
			continue
		}

		// Extract the primary kind (first comma-separated value).
		kind := p.Kinds
		if idx := strings.Index(kind, ","); idx > 0 {
			kind = kind[:idx]
		}

		attraction := models.Attraction{
			Name:         p.Name,
			Kind:         kind,
			Distance:     int(p.Dist),
			WikipediaURL: p.Wikipedia,
		}
		attractions = append(attractions, attraction)
	}

	sort.Slice(attractions, func(i, j int) bool {
		return attractions[i].Distance < attractions[j].Distance
	})

	// Cap at 20.
	if len(attractions) > 20 {
		attractions = attractions[:20]
	}

	attractionsCache.Lock()
	attractionsCache.entries[cacheKey] = attractionsCacheEntry{attractions: attractions, fetched: time.Now()}
	attractionsCache.Unlock()

	return attractions, nil
}
