package hotels

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// filterHotels applies all post-fetch filters to hotel results.
func filterHotels(hotels []models.HotelResult, opts HotelSearchOptions) []models.HotelResult {
	filtered := hotels[:0]
	for _, h := range hotels {
		// Stars filter: h.Stars==0 means Google didn't annotate this hotel
		// with star data (~92% of hotels). Pass those through rather than
		// treating "unknown" as "zero stars".
		if opts.Stars > 0 && h.Stars > 0 && h.Stars < opts.Stars {
			continue
		}
		if opts.MinPrice > 0 && h.Price > 0 && h.Price < opts.MinPrice {
			continue
		}
		if opts.MaxPrice > 0 && h.Price > 0 && h.Price > opts.MaxPrice {
			continue
		}
		// Rating filter: when MinRating is set, require rating data AND that
		// it meets the minimum. However, external-provider results (Airbnb,
		// Booking.com, Hostelworld) often lack a Google-scale rating — pass
		// those through rather than dropping valuable cross-provider results.
		if opts.MinRating > 0 {
			if h.Rating > 0 && h.Rating < opts.MinRating {
				continue
			}
			if h.Rating == 0 && !models.HasExternalProviderSource(h) {
				continue
			}
		}
		if h.Lat != 0 && h.Lon != 0 && opts.CenterLat != 0 {
			dist := Haversine(opts.CenterLat, opts.CenterLon, h.Lat, h.Lon)
			// Hard geo-outlier ceiling: reject hotels >100km from city
			// center. External providers (Airbnb) sometimes return
			// promoted listings from completely different cities.
			if dist > 100 {
				continue
			}
			if opts.MaxDistanceKm > 0 && dist > opts.MaxDistanceKm {
				continue
			}
		}
		if len(opts.Amenities) > 0 && !hasAllAmenities(h.Amenities, opts.Amenities) {
			continue
		}
		if opts.Brand != "" && !strings.Contains(strings.ToLower(h.Name), strings.ToLower(opts.Brand)) {
			continue
		}
		filtered = append(filtered, h)
	}
	return filtered
}

// filterByStars removes hotels below the requested star rating.
// Hotels with Stars==0 (no star data from Google) are kept, since "unknown"
// should not be treated as "zero stars".
func filterByStars(hotels []models.HotelResult, minStars int) []models.HotelResult {
	return filterHotels(hotels, HotelSearchOptions{Stars: minStars})
}

// hasAllAmenities returns true if the hotel's amenities contain every
// requested amenity (case-insensitive substring match).
func hasAllAmenities(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, a := range have {
		set[strings.ToLower(a)] = true
	}
	for _, req := range want {
		if !set[strings.ToLower(strings.TrimSpace(req))] {
			return false
		}
	}
	return true
}

// sortHotels sorts hotel results in-place by the given criteria.
func sortHotels(hotels []models.HotelResult, sortBy string, centerLat, centerLon float64) {
	switch strings.ToLower(sortBy) {
	case "cheapest", "price", "":
		// Sort by price ascending. Hotels with price=0 go to the end.
		sort.Slice(hotels, func(i, j int) bool {
			return lessPrice(hotels[i], hotels[j])
		})
	case "rating":
		// Sort by rating descending.
		sort.Slice(hotels, func(i, j int) bool {
			return hotels[i].Rating > hotels[j].Rating
		})
	case "stars":
		// Sort by star rating descending.
		sort.Slice(hotels, func(i, j int) bool {
			return hotels[i].Stars > hotels[j].Stars
		})
	case "distance":
		// Sort by distance from city center ascending.
		if centerLat != 0 || centerLon != 0 {
			sort.Slice(hotels, func(i, j int) bool {
				di := Haversine(centerLat, centerLon, hotels[i].Lat, hotels[i].Lon)
				dj := Haversine(centerLat, centerLon, hotels[j].Lat, hotels[j].Lon)
				return di < dj
			})
		}
	}
}

// medianHotelCoords computes the median lat/lon from hotels that have
// coordinates. Used as a fallback center when the geocoder is unavailable.
func medianHotelCoords(hotels []models.HotelResult) (lat, lon float64, ok bool) {
	var lats, lons []float64
	for _, h := range hotels {
		if h.Lat != 0 || h.Lon != 0 {
			lats = append(lats, h.Lat)
			lons = append(lons, h.Lon)
		}
	}
	if len(lats) == 0 {
		return 0, 0, false
	}
	sort.Float64s(lats)
	sort.Float64s(lons)
	mid := len(lats) / 2
	return lats[mid], lons[mid], true
}

// Haversine returns the great-circle distance in kilometers between two
// points specified in decimal degrees.
func Haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	dLat := degreesToRadians(lat2 - lat1)
	dLon := degreesToRadians(lon2 - lon1)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(degreesToRadians(lat1))*math.Cos(degreesToRadians(lat2))*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadiusKm * c
}

func degreesToRadians(deg float64) float64 {
	return deg * math.Pi / 180
}

// priceConfidenceRank maps a HotelResult.PriceConfidence value to a sort
// rank, higher rank = more trustworthy headline price. The values mirror the
// constants in internal/models/hotel_price_trust.go:
//
//	verified   (3) — price re-confirmed against a bookable room rate
//	room_level (2) — a real per-night room rate (Agoda/Booking/Trivago path)
//	unverified (1) — headline / lead-in teaser (e.g. Google's single price)
//
// Empty or unrecognised confidence ranks lowest among priced entries (1) so
// listings without a trustworthy basis still sort below real room prices while
// staying ahead of zero-price entries.
func priceConfidenceRank(confidence string) int {
	switch confidence {
	case models.PriceConfidenceVerified:
		return 3
	case models.PriceConfidenceRoomLevel:
		return 2
	default:
		return 1
	}
}

// lessPrice orders hotels for the "cheapest" sort. Zero-price listings always
// sink to the end. Among priced listings, those with a higher price-confidence
// rank lead (a real per-night room price beats a headline teaser even when the
// teaser is cheaper); within the same confidence tier, the cheaper price wins.
func lessPrice(a, b models.HotelResult) bool {
	if a.Price == 0 {
		return false
	}
	if b.Price == 0 {
		return true
	}
	rankA := priceConfidenceRank(a.PriceConfidence)
	rankB := priceConfidenceRank(b.PriceConfidence)
	if rankA != rankB {
		return rankA > rankB
	}
	return a.Price < b.Price
}

func hotelProviderStatusFromResults(id, name string, results int) models.ProviderStatus {
	status := models.StatusCheckedNoHit
	if results > 0 {
		status = models.StatusCheckedHit
	}
	return models.ProviderStatus{
		ID:      id,
		Name:    name,
		Status:  status,
		Results: results,
	}
}

func hotelProviderStatusFromError(id, name string, err error) models.ProviderStatus {
	status := models.ClassifyProviderError(err)
	return models.ProviderStatus{
		ID:     id,
		Name:   name,
		Status: status,
		Error:  err.Error(),
	}
}

func coalesceProviderStatuses(statuses []models.ProviderStatus) []models.ProviderStatus {
	if len(statuses) < 2 {
		return statuses
	}
	out := make([]models.ProviderStatus, 0, len(statuses))
	indexByKey := make(map[string]int, len(statuses))
	for _, status := range statuses {
		key := providerStatusKey(status)
		if key == "" {
			out = append(out, status)
			continue
		}
		if idx, ok := indexByKey[key]; ok {
			if providerStatusRank(status) > providerStatusRank(out[idx]) {
				out[idx] = status
			}
			continue
		}
		indexByKey[key] = len(out)
		out = append(out, status)
	}
	return out
}

func providerStatusKey(status models.ProviderStatus) string {
	key := strings.TrimSpace(status.ID)
	if key == "" {
		key = strings.TrimSpace(status.Name)
	}
	return strings.ToLower(key)
}

func providerStatusRank(status models.ProviderStatus) int {
	switch strings.ToLower(strings.TrimSpace(status.Status)) {
	case models.StatusCheckedHit:
		return 6000 + status.Results
	case models.StatusOK:
		return 5000 + status.Results
	case models.StatusCheckedNoHit:
		return 4000
	case models.StatusStale:
		return 3000 + status.Results
	case models.StatusFailed, models.StatusError, models.StatusTimeout:
		return 2000
	case models.StatusCircuitBroken:
		return 1500
	case models.StatusSkipped, models.StatusDisabled, models.StatusNotConfigured, models.StatusNotAuthorized:
		return 1000
	default:
		return status.Results
	}
}

func countHotelBatchResults(batches [][]models.HotelResult) int {
	count := 0
	for _, batch := range batches {
		count += len(batch)
	}
	return count
}

func joinInts(values []int, separator string) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, separator)
}

func countBySource(hotels []models.HotelResult, provider string) int {
	count := 0
	for _, h := range hotels {
		for _, s := range h.Sources {
			if s.Provider == provider {
				count++
				break
			}
		}
	}
	return count
}

func countExternalSources(hotels []models.HotelResult) int {
	count := 0
	for _, h := range hotels {
		if models.HasExternalProviderSource(h) {
			count++
		}
	}
	return count
}

// parseDateArray converts "YYYY-MM-DD" to [year, month, day].
func parseDateArray(s string) ([3]int, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return [3]int{}, fmt.Errorf("invalid date %q: expected YYYY-MM-DD", s)
	}
	return [3]int{t.Year(), int(t.Month()), t.Day()}, nil
}

// SearchHotelByName searches for a specific hotel by name and returns its details.
// Unlike SearchHotels (which searches by area), this uses Google's entity
// resolution to find a specific property via name matching within search results.
//
// Strategy: Google Hotels returns listings when searching by city/area, not hotel
// names. We extract a location context from the query (text after comma, or last
