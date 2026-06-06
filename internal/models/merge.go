package models

import (
	"fmt"
	"math"
	"strings"
)

// HasExternalProviderSource returns true if the hotel has at least one price
// source from an external provider (not google_hotels or trivago). External
// results may lack rating/review data and should not be penalised by quality
// filters designed for Google's well-annotated results.
func HasExternalProviderSource(h HotelResult) bool {
	for _, s := range h.Sources {
		if s.Provider != "google_hotels" && s.Provider != "trivago" && s.Provider != "" {
			return true
		}
	}
	return false
}

// MergeHotelResults deduplicates hotels from multiple sources. When the same
// hotel appears from different providers, sources are merged into a single
// HotelResult with the lowest price as the primary and all provider prices
// preserved in Sources.
//
// Matching uses case-insensitive name normalization. When names match but
// could be ambiguous (e.g. "Hilton" in different cities), normalized address
// equality or geo-proximity within maxDistanceMeters is used as a tiebreaker.
func MergeHotelResults(sources ...[]HotelResult) []HotelResult {
	// Why 100m: a 100 m radius is large enough to catch the same physical
	// hotel listed at slightly different GPS coordinates by different providers
	// (e.g. Google Hotels pins the main entrance; Booking pins the reception
	// desk — typically 10-40 m apart). Beyond 100 m, different buildings in
	// the same block start to appear in range, risking false merges.
	const maxDistanceMeters = 100.0

	type key struct {
		name string
	}

	merged := make(map[key]*HotelResult)
	var order []key // preserve insertion order

	// geoIndex maps each merged entry's key to its coordinates for
	// secondary geo-proximity dedup. When two providers use different name
	// variants for the same physical property (e.g. "Holiday Inn Express
	// Amsterdam Arena Towers by IHG" vs "Holiday Inn Express Amsterdam -
	// Arena Towers"), the primary name-key lookup misses. The geo-index
	// catches these by finding the nearest existing entry within 50m AND
	// requiring name similarity to avoid merging unrelated nearby hotels.
	//
	// Why 50m: stricter than maxDistanceMeters (100m) because the secondary
	// path matches on name similarity, not exact address equality. 50m ensures
	// only the same physical building matches; at 100m+ distinct hotels in
	// dense city-center blocks would collapse into one.
	const geoMergeMeters = 50.0
	type geoEntry struct {
		k        key
		name     string // normalized name for similarity check
		lat, lon float64
	}
	var geoIndex []geoEntry

	for _, batch := range sources {
		for _, h := range batch {
			k := key{name: normalizeName(h.Name)}

			// Secondary geo-proximity lookup: if name-key doesn't match any
			// existing entry, check if a nearby hotel (within 50m) exists
			// with a similar name. Both proximity AND name similarity are
			// required to prevent merging different hotels in dense areas.
			if _, ok := merged[k]; !ok && h.Lat != 0 {
				incomingName := normalizeName(h.Name)
				for _, ge := range geoIndex {
					if haversineMeters(h.Lat, h.Lon, ge.lat, ge.lon) <= geoMergeMeters &&
						NameSimilar(incomingName, ge.name) {
						k = ge.k // remap to the existing entry's key
						break
					}
				}
			}

			if existing, ok := merged[k]; ok {
				if !sameHotelCandidate(*existing, h, maxDistanceMeters) {
					// Same normalized name but different property — use a
					// disambiguated key so source prices don't collapse.
					dk := key{name: hotelDisambiguationKey(h)}
					if _, exists := merged[dk]; !exists {
						clone := h
						clone.Sources = buildSources(clone)
						merged[dk] = &clone
						order = append(order, dk)
						if clone.Lat != 0 {
							geoIndex = append(geoIndex, geoEntry{k: dk, name: normalizeName(clone.Name), lat: clone.Lat, lon: clone.Lon})
						}
					}
					continue
				}

				// Merge: add this provider's price as a source, deduplicating
				// entries with the same (provider, price, currency) tuple.
				existing.Sources = deduplicateSources(append(existing.Sources, buildSources(h)...))

				// Update primary price to the lowest.
				if h.Price > 0 && (existing.Price == 0 || h.Price < existing.Price) {
					existing.Price = h.Price
					existing.Currency = h.Currency
					existing.BookingURL = h.BookingURL
				}

				// Merge fields that the primary might be missing.
				if existing.Rating == 0 && h.Rating > 0 {
					existing.Rating = h.Rating
				}
				if existing.ReviewCount == 0 && h.ReviewCount > 0 {
					existing.ReviewCount = h.ReviewCount
				}
				if existing.Stars == 0 && h.Stars > 0 {
					existing.Stars = h.Stars
				}
				if existing.HotelID == "" && h.HotelID != "" {
					existing.HotelID = h.HotelID
				}
				if existing.Address == "" && h.Address != "" {
					existing.Address = h.Address
				}
				if existing.Lat == 0 && h.Lat != 0 {
					existing.Lat = h.Lat
					existing.Lon = h.Lon
				}
				if existing.BookingURL == "" && h.BookingURL != "" {
					existing.BookingURL = h.BookingURL
				}
				if existing.Description == "" && h.Description != "" {
					existing.Description = h.Description
				}
				if existing.ImageURL == "" && h.ImageURL != "" {
					existing.ImageURL = h.ImageURL
				}
				if existing.Neighborhood == "" && h.Neighborhood != "" {
					existing.Neighborhood = h.Neighborhood
				}
				if len(existing.RoomTypes) == 0 && len(h.RoomTypes) > 0 {
					existing.RoomTypes = h.RoomTypes
				}
			} else {
				clone := h
				clone.Sources = buildSources(clone)
				merged[k] = &clone
				order = append(order, k)
				if clone.Lat != 0 {
					geoIndex = append(geoIndex, geoEntry{k: k, name: normalizeName(clone.Name), lat: clone.Lat, lon: clone.Lon})
				}
			}
		}
	}

	result := make([]HotelResult, 0, len(order))
	for _, k := range order {
		result = append(result, *merged[k])
	}
	ComputeSavings(result)
	return result
}

// ComputeSavings populates Savings and CheapestSource for each hotel that has
// multiple price sources. Savings = max(sources.price) - min(sources.price).
// Only set when there are 2+ sources with different prices.
func ComputeSavings(hotels []HotelResult) {
	for i := range hotels {
		h := &hotels[i]
		if len(h.Sources) < 2 {
			continue
		}

		// Group sources by currency to avoid cross-currency comparison.
		// Only compute savings within each currency group.
		byCurrency := make(map[string][]PriceSource)
		for _, s := range h.Sources {
			if s.Price <= 0 || s.Currency == "" {
				continue
			}
			byCurrency[strings.ToUpper(s.Currency)] = append(byCurrency[strings.ToUpper(s.Currency)], s)
		}

		// Find the currency group with the most sources (best comparison).
		var bestCurrency string
		var bestSources []PriceSource
		for currency, sources := range byCurrency {
			if len(sources) > len(bestSources) {
				bestCurrency = currency
				bestSources = sources
			}
		}
		_ = bestCurrency

		if len(bestSources) < 2 {
			continue
		}

		minPrice := math.MaxFloat64
		maxPrice := 0.0
		cheapest := ""
		for _, s := range bestSources {
			if s.Price < minPrice {
				minPrice = s.Price
				cheapest = s.Provider
			}
			if s.Price > maxPrice {
				maxPrice = s.Price
			}
		}
		if minPrice < maxPrice && cheapest != "" {
			h.Savings = math.Round(maxPrice - minPrice)
			h.CheapestSource = cheapest
		}
	}
}

func sameHotelCandidate(existing, incoming HotelResult, maxDistanceMeters float64) bool {
	existingAddress := normalizeAddress(existing.Address)
	incomingAddress := normalizeAddress(incoming.Address)
	if existingAddress != "" && incomingAddress != "" {
		if existingAddress == incomingAddress {
			return true
		}
		// Different addresses — these are different hotels even if nearby.
		return false
	}
	if existing.Lat != 0 && incoming.Lat != 0 {
		return haversineMeters(existing.Lat, existing.Lon, incoming.Lat, incoming.Lon) <= maxDistanceMeters
	}
	// No address or coordinates to disambiguate. Only merge if the
	// normalized name is long enough to be specific (e.g. "Hilton" alone
	// is too short, "Hilton Paris Opera" is specific enough).
	existingName := normalizeName(existing.Name)
	return len(existingName) >= 15
}

func hotelDisambiguationKey(h HotelResult) string {
	base := normalizeName(h.Name)
	if address := normalizeAddress(h.Address); address != "" {
		return base + "|" + address
	}
	if h.Lat != 0 || h.Lon != 0 {
		return fmt.Sprintf("%s|%.5f,%.5f", base, h.Lat, h.Lon)
	}
	return base + "|unknown"
}

// deduplicateSources removes duplicate entries from a Sources slice.
// Two sources are considered duplicates if they share the same provider,
// price, and currency. This prevents the same Google Hotels entry from
// appearing 7-9 times when results arrive from multiple sort orders or
// pagination pages.
func deduplicateSources(sources []PriceSource) []PriceSource {
	type sourceKey struct {
		provider string
		price    float64
		currency string
	}
	seen := make(map[sourceKey]struct{}, len(sources))
	out := make([]PriceSource, 0, len(sources))
	for _, s := range sources {
		k := sourceKey{provider: s.Provider, price: s.Price, currency: s.Currency}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, s)
	}
	return out
}

// buildSources creates a Sources slice from a HotelResult's own price.
func buildSources(h HotelResult) []PriceSource {
	if h.Price == 0 {
		return nil
	}
	provider := "unknown"
	for _, s := range h.Sources {
		provider = s.Provider
		break
	}
	if len(h.Sources) > 0 {
		return h.Sources
	}
	return []PriceSource{{
		Provider:   provider,
		Price:      h.Price,
		Currency:   h.Currency,
		BookingURL: h.BookingURL,
	}}
}

// brandSuffixes are hotel chain brand suffixes that different providers
// append inconsistently. Stripping them improves cross-provider dedup
// hit rate (e.g. "Holiday Inn Express Arena Towers by IHG" vs
// "Holiday Inn Express Arena Towers").
var brandSuffixes = []string{
	" by ihg", " powered by radisson hotels", " powered by radisson",
	" an ihg hotel", " a marriott hotel", " by marriott",
	" by hilton", " by hyatt", " by accor", " by wyndham",
	" by choice hotels", " by best western",
	" autograph collection", " tribute portfolio",
	" curio collection", " tapestry collection",
}

// normalizeName lowercases, trims whitespace, strips brand suffixes,
// removes punctuation, and collapses internal spaces. This maximizes
// cross-provider dedup hits where providers use different name variants
// for the same physical property.
func normalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	// Strip brand suffixes.
	for _, suffix := range brandSuffixes {
		if strings.HasSuffix(name, suffix) {
			name = strings.TrimSuffix(name, suffix)
			break
		}
	}
	// Remove common punctuation that varies across providers.
	name = strings.NewReplacer(
		",", "", ".", "", "-", " ", "'", "", "'", "", "\"", "",
		"(", "", ")", "", "&", "and",
	).Replace(name)
	// Collapse multiple spaces.
	for strings.Contains(name, "  ") {
		name = strings.ReplaceAll(name, "  ", " ")
	}
	return strings.TrimSpace(name)
}

func normalizeAddress(address string) string {
	address = strings.ToLower(strings.TrimSpace(address))
	replacer := strings.NewReplacer(",", " ", ".", " ", ";", " ", ":", " ", "-", " ", "/", " ")
	address = replacer.Replace(address)
	for strings.Contains(address, "  ") {
		address = strings.ReplaceAll(address, "  ", " ")
	}
	return address
}

// NameSimilar returns true if two normalized hotel names are similar enough to
// consider them the same property. This is used as a guard for geo-proximity
// merging to prevent unrelated nearby hotels from collapsing.
//
// The algorithm uses word-level Jaccard similarity: the intersection of words
// divided by the union of words. A threshold of 0.5 means at least half the
// words must overlap. Both names must also have at least 2 words each to
// prevent trivially short names from matching.
func NameSimilar(a, b string) bool {
	wordsA := strings.Fields(a)
	wordsB := strings.Fields(b)
	if len(wordsA) < 2 || len(wordsB) < 2 {
		return a == b // very short names must match exactly
	}

	setA := make(map[string]struct{}, len(wordsA))
	for _, w := range wordsA {
		setA[w] = struct{}{}
	}
	setB := make(map[string]struct{}, len(wordsB))
	for _, w := range wordsB {
		setB[w] = struct{}{}
	}

	intersection := 0
	for w := range setA {
		if _, ok := setB[w]; ok {
			intersection++
		}
	}

	union := len(setA)
	for w := range setB {
		if _, ok := setA[w]; !ok {
			union++
		}
	}

	if union == 0 {
		return false
	}
	return float64(intersection)/float64(union) >= 0.5
}

// haversineMeters returns the distance in meters between two lat/lon points.
func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusMeters = 6_371_000.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusMeters * c
}
