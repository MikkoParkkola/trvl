package destinations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

const foursquareURL = "https://api.foursquare.com/v3/places/search"

var foursquareAPIURL = foursquareURL

// foursquareCache stores rated place results.
var foursquareCache = struct {
	sync.RWMutex
	entries map[string]foursquareCacheEntry
}{entries: make(map[string]foursquareCacheEntry)}

type foursquareCacheEntry struct {
	places  []models.RatedPlace
	fetched time.Time
}

const foursquareCacheTTL = 6 * time.Hour

// foursquareCategoryIDs maps human-readable categories to Foursquare IDs.
var foursquareCategoryIDs = map[string]string{
	"restaurant": "13065",
	"cafe":       "13032",
	"bar":        "13003",
}

// foursquareResponse is the JSON shape from the Foursquare Places API.
type foursquareResponse struct {
	Results []foursquarePlace `json:"results"`
}

type foursquarePlace struct {
	Name     string  `json:"name"`
	Rating   float64 `json:"rating"`
	Distance int     `json:"distance"`
	Location struct {
		Address          string `json:"address"`
		FormattedAddress string `json:"formatted_address"`
	} `json:"location"`
	Categories []struct {
		Name string `json:"name"`
	} `json:"categories"`
	Hours struct {
		Display string `json:"display"`
	} `json:"hours"`
	Price int `json:"price"`
	Tips  []struct {
		Text string `json:"text"`
	} `json:"tips"`
}

// GetRatedPlaces fetches rated places from Foursquare near a location.
// Returns nil (no error) if FOURSQUARE_API_KEY is not set.
func GetRatedPlaces(ctx context.Context, lat, lon float64, category string, limit int) ([]models.RatedPlace, error) {
	apiKey := os.Getenv("FOURSQUARE_API_KEY")
	if apiKey == "" {
		return nil, nil
	}

	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	catID, ok := foursquareCategoryIDs[category]
	if !ok {
		catID = foursquareCategoryIDs["restaurant"]
	}

	cacheKey := fmt.Sprintf("%.4f,%.4f,%s,%d", lat, lon, category, limit)

	foursquareCache.RLock()
	if entry, ok := foursquareCache.entries[cacheKey]; ok && time.Since(entry.fetched) < foursquareCacheTTL {
		foursquareCache.RUnlock()
		return entry.places, nil
	}
	foursquareCache.RUnlock()

	u, _ := url.Parse(foursquareAPIURL)
	q := u.Query()
	q.Set("ll", fmt.Sprintf("%.6f,%.6f", lat, lon))
	q.Set("radius", "500")
	q.Set("categories", catID)
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("sort", "RATING")
	q.Set("fields", "name,rating,location,categories,hours,price,tips,distance")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create foursquare request: %w", err)
	}
	req.Header.Set("Authorization", apiKey)
	req.Header.Set("User-Agent", "trvl/1.0 (rated places)")

	resp, err := destinationsClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("foursquare request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read foursquare response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("foursquare returned status %d: %s", resp.StatusCode, string(body))
	}

	var fsResp foursquareResponse
	if err := json.Unmarshal(body, &fsResp); err != nil {
		return nil, fmt.Errorf("parse foursquare response: %w", err)
	}

	places := make([]models.RatedPlace, 0, len(fsResp.Results))
	for _, p := range fsResp.Results {
		place := models.RatedPlace{
			Name:       p.Name,
			Rating:     p.Rating,
			PriceLevel: p.Price,
			Distance:   p.Distance,
			Address:    p.Location.FormattedAddress,
		}
		if place.Address == "" {
			place.Address = p.Location.Address
		}
		if len(p.Categories) > 0 {
			place.Category = p.Categories[0].Name
		}
		if len(p.Tips) > 0 {
			place.Tip = p.Tips[0].Text
		}
		places = append(places, place)
	}

	foursquareCache.Lock()
	foursquareCache.entries[cacheKey] = foursquareCacheEntry{places: places, fetched: time.Now()}
	foursquareCache.Unlock()

	return places, nil
}
