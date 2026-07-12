package hotels

import (
	"context"
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
		// Filter Min/Max using PriceForRanking (the normalized comparable value
		// when available). A numeric threshold is only meaningful against a price
		// expressed in the same currency, so we apply it only when the price is
		// comparable to the requested target currency. An incomparable price
		// (foreign currency we could not convert) bypasses the numeric filter
		// rather than being compared raw against a target-currency threshold —
		// comparing ¥999 against a €100 cap is not a real comparison. Such prices
		// surface in the incomparable tail, flagged, for the user to judge.
		effPrice := h.PriceForRanking()
		if priceComparableToTarget(h, opts.Currency) && effPrice > 0 {
			if opts.MinPrice > 0 && effPrice < opts.MinPrice {
				continue
			}
			if opts.MaxPrice > 0 && effPrice > opts.MaxPrice {
				continue
			}
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

// priceComparableToTarget reports whether a hotel's ranking price can be
// meaningfully compared against a numeric threshold in the target currency.
// It is true when no target currency is requested (the historical single-
// currency behaviour, thresholds interpreted in the price's own currency),
// when the headline was normalized to the target (ComparablePrice > 0), or
// when the price is already denominated in the target currency. A foreign
// price we could not convert is NOT comparable and must bypass Min/Max.
func priceComparableToTarget(h models.HotelResult, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return true
	}
	if h.ComparablePrice > 0 {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(h.Currency), target)
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
		// Sort by rating descending; priced listings always lead unpriced ones.
		sort.Slice(hotels, func(i, j int) bool {
			if lead, decided := pricedLead(hotels[i], hotels[j]); decided {
				return lead
			}
			return hotels[i].Rating > hotels[j].Rating
		})
	case "stars":
		// Sort by star rating descending; priced listings always lead.
		sort.Slice(hotels, func(i, j int) bool {
			if lead, decided := pricedLead(hotels[i], hotels[j]); decided {
				return lead
			}
			return hotels[i].Stars > hotels[j].Stars
		})
	case "distance":
		// Sort by distance from city center ascending; priced listings always lead.
		if centerLat != 0 || centerLon != 0 {
			sort.Slice(hotels, func(i, j int) bool {
				if lead, decided := pricedLead(hotels[i], hotels[j]); decided {
					return lead
				}
				di := Haversine(centerLat, centerLon, hotels[i].Lat, hotels[i].Lon)
				dj := Haversine(centerLat, centerLon, hotels[j].Lat, hotels[j].Lon)
				return di < dj
			})
		}
	}
}

// pricedLead enforces the operator rule "lead with the ones that have proper
// price available" for the non-price sort modes (rating/stars/distance).
// It uses the same comparable-first + conf>basis>price logic as lessPrice
// so that a same-currency lead_in never falsely leads a better-basis result
// when deciding the "priced" head for other sorts. decided=true only when
// one is clearly the better priced entry under the honest ranking.
func pricedLead(a, b models.HotelResult) (lead, decided bool) {
	// Priced listings lead unpriced ones; when both (or neither) are priced,
	// decided=false so the caller's sort key (rating/stars/distance) decides.
	// Cross-currency/basis ordering lives in lessPrice (the "cheapest" sort)
	// ONLY — applying it here would let price silently override the chosen
	// non-price sort key, which is a regression, not the operator rule.
	aHas := a.Price > 0
	bHas := b.Price > 0
	if aHas != bHas {
		return aHas, true
	}
	return false, false
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

// priceBasisRank maps a price basis to a rank (higher = more complete for
// trip costing and honesty). Used inside comparators after confidence.
// tax_inclusive_total > room_total > room_nightly > lead_in (0).
func priceBasisRank(basis string) int {
	switch basis {
	case models.PriceBasisTaxInclusiveTotal:
		return 3
	case models.PriceBasisRoomTotal:
		return 2
	case models.PriceBasisRoomNightly:
		return 1
	case models.PriceBasisLeadIn, "":
		return 0
	default:
		return 0
	}
}

// lessPrice orders hotels for the "cheapest" sort. It implements a stable,
// cross-currency honest partition:
//  1. Hotels with ComparablePrice >0 (successfully normalized) come before the
//     incomparable tail (ComparablePrice==0).
//  2. Within comparables: confidence rank desc, then basis rank desc, then
//     PriceForRanking() asc.
//  3. Incomparable tail: group by upper currency (lex asc), then conf, basis,
//     raw Price asc. Never numeric cross-curr compare.
//  4. Deterministic final tiebreakers: Name, then Provider/first source.
//
// Zero/neg prices sink within their partition.
func lessPrice(a, b models.HotelResult) bool {
	// Zero/negative prices ALWAYS sink (preserve pre-existing contract).
	if a.Price <= 0 {
		return false
	}
	if b.Price <= 0 {
		return true
	}

	// Partition: comparable (have ComparablePrice) first.
	ac := a.ComparablePrice > 0
	bc := b.ComparablePrice > 0
	if ac != bc {
		return ac
	}

	if ac && bc {
		// Both comparable: conf desc > basis desc > PriceForRanking asc
		ra := priceConfidenceRank(a.PriceConfidence)
		rb := priceConfidenceRank(b.PriceConfidence)
		if ra != rb {
			return ra > rb
		}
		ba := priceBasisRank(a.PriceBasis)
		bb := priceBasisRank(b.PriceBasis)
		if ba != bb {
			return ba > bb
		}
		pa := a.PriceForRanking()
		pb := b.PriceForRanking()
		if pa != pb && pa > 0 && pb > 0 {
			return pa < pb
		}
	} else {
		// Incomparable tail: group by currency lex asc, then conf/basis/price
		au := strings.ToUpper(strings.TrimSpace(a.Currency))
		bu := strings.ToUpper(strings.TrimSpace(b.Currency))
		if au != bu {
			return au < bu
		}
		ra := priceConfidenceRank(a.PriceConfidence)
		rb := priceConfidenceRank(b.PriceConfidence)
		if ra != rb {
			return ra > rb
		}
		ba := priceBasisRank(a.PriceBasis)
		bb := priceBasisRank(b.PriceBasis)
		if ba != bb {
			return ba > bb
		}
		pa, pb := a.Price, b.Price
		if pa != pb {
			return pa < pb
		}
	}

	// Common deterministic tiebreakers (total order, independent of input order)
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	// First source provider for determinism
	ap := ""
	if len(a.Sources) > 0 {
		ap = strings.ToLower(strings.TrimSpace(a.Sources[0].Provider))
	}
	bp := ""
	if len(b.Sources) > 0 {
		bp = strings.ToLower(strings.TrimSpace(b.Sources[0].Provider))
	}
	if ap != bp {
		return ap < bp
	}
	return false // equal
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

// currencyConverter converts amount from->to, returning the converted amount
// and ok. Injected so unit tests never hit the network. (Mirror ground/search.go:147-149.)
type currencyConverter func(ctx context.Context, amount float64, from, to string) (converted float64, ok bool)

// normalizeHotelCurrencies best-effort converts every hotel's headline price into
// the target currency for comparable ranking. On success it mutates Price+Currency
// to the target AND sets ComparablePrice. On failure it leaves the hotel in its
// original currency and ComparablePrice stays 0 (=> incomparable tail).
func normalizeHotelCurrencies(ctx context.Context, hotels []models.HotelResult, target string, conv currencyConverter) {
	tU := strings.ToUpper(strings.TrimSpace(target))
	if tU == "" || conv == nil {
		return
	}
	for i := range hotels {
		h := &hotels[i]
		if h.Price <= 0 {
			continue
		}
		cU := strings.ToUpper(strings.TrimSpace(h.Currency))
		if cU == tU {
			h.ComparablePrice = h.Price // already in target
			continue
		}
		if converted, ok := conv(ctx, h.Price, h.Currency, target); ok && converted > 0 {
			h.Price = converted
			h.Currency = target
			h.ComparablePrice = converted
		}
		// else: leave as-is, ComparablePrice=0 => incomparable
	}
}

// SearchHotelByName searches for a specific hotel by name and returns its details.
// Unlike SearchHotels (which searches by area), this uses Google's entity
// resolution to find a specific property via name matching within search results.
//
// Strategy: Google Hotels returns listings when searching by city/area, not hotel
// names. We extract a location context from the query (text after comma, or last
