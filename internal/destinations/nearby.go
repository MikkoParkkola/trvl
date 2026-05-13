package destinations

import (
	"context"
	"os"
	"sync"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// NearbyResult aggregates POI data from multiple sources.
type NearbyResult struct {
	POIs        []models.NearbyPOI  `json:"pois"`
	RatedPlaces []models.RatedPlace `json:"rated_places,omitempty"`
	Attractions []models.Attraction `json:"attractions,omitempty"`
}

// GetNearbyPlaces aggregates POI data from OSM, Foursquare, Geoapify, and OpenTripMap.
// Always uses OSM Overpass (free). Optional sources enrich results if API keys are set.
func GetNearbyPlaces(ctx context.Context, lat, lon float64, radiusMeters int, category string) (*NearbyResult, error) {
	if radiusMeters <= 0 {
		radiusMeters = 500
	}

	result := &NearbyResult{}
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Always: OSM Overpass for basic POIs.
	wg.Add(1)
	go func() {
		defer wg.Done()
		pois, err := GetNearbyPOIs(ctx, lat, lon, radiusMeters, category)
		if err != nil {
			return
		}
		mu.Lock()
		result.POIs = pois
		mu.Unlock()
	}()

	// Rated places: Foursquare if API key is set, otherwise Google Maps (zero keys).
	wg.Add(1)
	go func() {
		defer wg.Done()
		if os.Getenv("FOURSQUARE_API_KEY") != "" {
			places, err := GetRatedPlaces(ctx, lat, lon, category, 10)
			if err != nil || places == nil {
				return
			}
			mu.Lock()
			result.RatedPlaces = places
			mu.Unlock()
		} else {
			query := category
			if query == "all" || query == "" {
				query = "restaurants"
			}
			places, err := SearchGoogleMapsPlaces(ctx, lat, lon, query, 10)
			if err != nil || places == nil {
				return
			}
			mu.Lock()
			result.RatedPlaces = places
			mu.Unlock()
		}
	}()

	// Optional: Geoapify for walkable POIs (enrich OSM results).
	wg.Add(1)
	go func() {
		defer wg.Done()
		pois, err := GetWalkablePOIs(ctx, lat, lon, radiusMeters/80, category) // approx minutes from meters
		if err != nil || pois == nil {
			return
		}
		mu.Lock()
		// Merge Geoapify POIs that aren't duplicates of OSM results.
		result.POIs = deduplicatePOIs(result.POIs, pois)
		mu.Unlock()
	}()

	// Optional: OpenTripMap for tourist attractions.
	if category == "all" || category == "attraction" || category == "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			attractions, err := GetAttractions(ctx, lat, lon, radiusMeters)
			if err != nil || attractions == nil {
				return
			}
			mu.Lock()
			result.Attractions = attractions
			mu.Unlock()
		}()
	}

	wg.Wait()

	return result, nil
}

// deduplicatePOIs merges new POIs into existing ones, skipping duplicates
// within 50 meters of an existing POI.
func deduplicatePOIs(existing, incoming []models.NearbyPOI) []models.NearbyPOI {
	merged := make([]models.NearbyPOI, len(existing))
	copy(merged, existing)

	for _, poi := range incoming {
		isDuplicate := false
		for _, e := range merged {
			dist := haversineMeters(e.Lat, e.Lon, poi.Lat, poi.Lon)
			if dist < 50 {
				isDuplicate = true
				break
			}
		}
		if !isDuplicate {
			merged = append(merged, poi)
		}
	}

	return merged
}

// ProximityMatch checks if two points are within the given distance in meters.
func ProximityMatch(lat1, lon1, lat2, lon2 float64, thresholdMeters int) bool {
	dist := haversineMeters(lat1, lon1, lat2, lon2)
	return dist <= thresholdMeters
}
