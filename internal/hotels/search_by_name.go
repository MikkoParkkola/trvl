package hotels

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/MikkoParkkola/trvl/internal/models"
)


// SearchHotelsByName searches for hotels matching a specific property name across
// all providers (Google Hotels, Trivago, and any configured external providers).
//
// Strategy: use the full "name + location" string as the search query so that
// Google Hotels and other providers return algorithm-ranked results for that
// specific property, then filter client-side to keep only results whose name
// fuzzy-matches the search name. All providers run in parallel.
//
// Parameters:
//   - name: property name to search for (e.g. "CORU House Prague")
//   - location: city/area context (e.g. "Prague") — appended to name for the
//     area search when not already present in the name
//   - checkIn, checkOut: dates in YYYY-MM-DD format
//   - currency: 3-letter ISO code (e.g. "USD", "EUR"); defaults to "USD"
func SearchHotelsByName(ctx context.Context, name, location, checkIn, checkOut, currency string) ([]models.HotelResult, error) {
	if name == "" {
		return nil, fmt.Errorf("hotel name is required")
	}
	if checkIn == "" || checkOut == "" {
		return nil, fmt.Errorf("check-in and check-out dates are required")
	}
	if currency == "" {
		currency = "USD"
	}

	// Build the search query: use "name, location" so providers treat the name
	// as a disambiguation hint while the location anchors geocoding. If the
	// name already contains the location (case-insensitive), skip appending.
	query := buildNameQuery(name, location)

	opts := HotelSearchOptions{
		CheckIn:  checkIn,
		CheckOut: checkOut,
		Guests:   2,
		Currency: currency,
		// Single page is enough — we only need enough results to find the named
		// property, and using MaxPages=1 avoids unnecessary rate-limit pressure.
		MaxPages: 1,
	}

	// Run Google Hotels + Trivago + external providers in parallel.
	// Google Hotels is always primary; others are non-fatal.
	type providerResult struct {
		hotels []models.HotelResult
		source string
	}

	resultCh := make(chan providerResult, 3)
	var wg sync.WaitGroup

	// Google Hotels — use the full "name + location" query so the algorithm
	// surfaces the specific property.
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err := SearchHotels(ctx, query, opts)
		if err != nil {
			slog.Warn("search_by_name: google hotels failed", "error", err)
			return
		}
		resultCh <- providerResult{hotels: res.Hotels, source: "google_hotels"}
	}()

	// Trivago — location-only query (Trivago's API resolves by area, name
	// search is not supported server-side).
	wg.Add(1)
	go func() {
		defer wg.Done()
		if location == "" {
			return
		}
		res, err := SearchTrivago(ctx, location, opts)
		if err != nil {
			slog.Warn("search_by_name: trivago failed", "error", err)
			return
		}
		resultCh <- providerResult{hotels: res, source: "trivago"}
	}()

	// External providers (Booking, Airbnb, Hostelworld, …).
	if eprt := getExternalProviderRuntime(); eprt != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			searchLoc := location
			if searchLoc == "" {
				searchLoc = name
			}
			lat, lon, err := ResolveLocation(ctx, searchLoc)
			if err != nil {
				slog.Warn("search_by_name: geocode failed", "error", err)
				return
			}
			res, _, err := eprt.SearchHotels(ctx, query, lat, lon, checkIn, checkOut, currency, 2, nil)
			if err != nil {
				slog.Warn("search_by_name: external providers failed", "error", err)
				return
			}
			resultCh <- providerResult{hotels: res, source: "external"}
		}()
	}

	wg.Wait()
	close(resultCh)

	// Collect and merge all provider results.
	var batches [][]models.HotelResult
	for pr := range resultCh {
		batches = append(batches, pr.hotels)
	}
	if len(batches) == 0 {
		return nil, fmt.Errorf("no results from any provider for %q", name)
	}

	merged := models.MergeHotelResults(batches...)

	// Filter to keep only hotels whose name fuzzy-matches the search name.
	matched := filterByNameMatch(merged, name)

	// If the fuzzy filter left nothing (e.g. the property is in results under a
	// slightly different name), fall back to all merged results so the caller
	// at least gets something useful.
	if len(matched) == 0 {
		slog.Warn("search_by_name: no fuzzy match, returning all merged results",
			"name", name, "total", len(merged))
		return merged, nil
	}

	return matched, nil
}

// buildNameQuery constructs the location query string used for the area search.
// If location is non-empty and not already contained in name, it appends
// ", location" so the search anchors to the right city.
func buildNameQuery(name, location string) string {
	if location == "" {
		return name
	}
	// If location words are already in the name, just use the name as-is.
	if strings.Contains(strings.ToLower(name), strings.ToLower(location)) {
		return name
	}
	return name + ", " + location
}

// filterByNameMatch keeps only hotels whose normalised name contains all
// significant words from the search name (length >= 3, case-insensitive,
// punctuation stripped). E.g. "CORU House" matches "CORU House Prague -
// Design Hotel" but not "Prague Hilton".
func filterByNameMatch(hotels []models.HotelResult, searchName string) []models.HotelResult {
	searchWords := normalizeWords(searchName)
	if len(searchWords) == 0 {
		return hotels
	}

	var out []models.HotelResult
	for _, h := range hotels {
		hotelWords := normalizeWordsSet(h.Name)
		if allWordsPresent(searchWords, hotelWords) {
			out = append(out, h)
		}
	}
	return out
}

// normalizeWords lowercases, strips punctuation, and returns words of length >= 3.
func normalizeWords(s string) []string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			return unicode.ToLower(r)
		}
		return ' '
	}, s)
	var words []string
	for _, w := range strings.Fields(s) {
		if len(w) >= 3 {
			words = append(words, w)
		}
	}
	return words
}

// normalizeWordsSet returns a set of normalised words from the string.
func normalizeWordsSet(s string) map[string]bool {
	words := normalizeWords(s)
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}

// allWordsPresent returns true if every word in needles appears in haystack.
func allWordsPresent(needles []string, haystack map[string]bool) bool {
	for _, w := range needles {
		if !haystack[w] {
			return false
		}
	}
	return true
}

// word(s)), search that area, then fuzzy-match the hotel name in results. If that
// fails we fall back to searching the full query as the location.
func SearchHotelByName(ctx context.Context, query string, checkIn, checkOut, currency string) (*models.HotelResult, error) {
	if query == "" {
		return nil, fmt.Errorf("hotel name query is required")
	}
	if checkIn == "" || checkOut == "" {
		return nil, fmt.Errorf("check-in and check-out dates are required")
	}
	if currency == "" {
		currency = "USD"
	}

	opts := HotelSearchOptions{
		CheckIn:  checkIn,
		CheckOut: checkOut,
		Guests:   2,
		Currency: currency,
	}

	// Build search location candidates: prefer context after comma, then last word.
	candidates := buildLocationCandidates(query)

	var lastErr error
	for i, loc := range candidates {
		// Cooldown between candidates to avoid Google 429 rate limits.
		// Each SearchHotels call makes multiple page requests across sort
		// orders, so sequential calls without delay trigger rate limiting.
		if i > 0 {
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		result, err := SearchHotels(ctx, loc, opts)
		if err != nil {
			lastErr = err
			continue
		}
		if len(result.Hotels) == 0 {
			continue
		}

		match := findBestNameMatch(result.Hotels, query)
		if match != nil {
			// If the match has no Google ID, do a targeted Google-only search
			// using the hotel name + location as the query. Google Hotels
			// interprets "Summer Shades Hotel, Naoussa" as a specific hotel
			// query and returns it with the proper Google ID, while auxiliary
			// providers (Trivago etc.) may not set HotelID.
			if match.HotelID == "" {
				// Extract just the hotel name part from the query (before comma)
				namePart := query
				if idx := strings.LastIndex(query, ","); idx >= 0 {
					namePart = strings.TrimSpace(query[:idx])
				}
				targetedQuery := namePart + ", " + loc
				targeted, tErr := SearchHotels(ctx, targetedQuery, opts)
				if tErr == nil {
					for i := range targeted.Hotels {
						if targeted.Hotels[i].HotelID != "" {
							if models.NameSimilar(
								strings.ToLower(targeted.Hotels[i].Name),
								strings.ToLower(namePart),
							) {
								return &targeted.Hotels[i], nil
							}
						}
					}
				}
			}
			return match, nil
		}

		// Area search succeeded but no name match — try next location candidate
		// instead of returning a random first result. Returning the wrong
		// hotel causes the caller to fetch room data for a completely
		// different property, which is worse than failing.
		continue
	}

	if lastErr != nil {
		return nil, fmt.Errorf("hotel name search: %w", lastErr)
	}
	return nil, fmt.Errorf("no hotels found for %q", query)
}

// buildLocationCandidates generates location search strings from a hotel name query.
// E.g. "Hotel Lutetia, Paris" -> ["Paris", "Hotel Lutetia Paris", "Hotel Lutetia, Paris"]
// "Summer Shades hotel, Naoussa, Paros" -> ["Paros", "Naoussa, Paros", "Summer Shades hotel, Naoussa Paros", "Summer Shades hotel, Naoussa, Paros"]
func buildLocationCandidates(query string) []string {
	var candidates []string

	// Find all comma-separated parts and generate progressively broader
	// location candidates. For "name, area, city":
	//   ["city", "area, city", "name area city", "name, area, city"]
	parts := strings.Split(query, ",")
	if len(parts) >= 2 {
		// Add location suffixes: start from the rightmost part, build up
		for i := len(parts) - 1; i >= 1; i-- {
			loc := strings.TrimSpace(strings.Join(parts[i:], ","))
			if loc != "" {
				candidates = append(candidates, loc)
			}
		}

		// Also try "before after" as the full query (no comma).
		before := strings.TrimSpace(parts[0])
		after := strings.TrimSpace(parts[len(parts)-1])
		if before != "" && after != "" {
			candidates = append(candidates, before+" "+after)
		}
	}

	// Try the full query as location (works when it contains a city).
	candidates = append(candidates, query)

	return candidates
}

// findBestNameMatch searches hotels for the best fuzzy match to the query.
// The query may include location context after a comma (e.g. "Makis Place, Mykonos").
// Only the part before the comma is used for name matching, so location words
// don't cause false matches with unrelated hotels.
func findBestNameMatch(hotels []models.HotelResult, query string) *models.HotelResult {
	// Strip location context: only match against the part before the last comma.
	namePart := query
	if idx := strings.LastIndex(query, ","); idx >= 0 {
		namePart = strings.TrimSpace(query[:idx])
	}
	queryLower := strings.ToLower(namePart)
	queryWords := strings.Fields(queryLower)
	if len(queryWords) == 0 {
		return nil
	}

	// Filter out short words that commonly indicate location rather than the
	// actual hotel name being searched for (e.g. "hotel", "apartments").
	filtered := queryWords[:0]
	for _, w := range queryWords {
		if len(w) >= 3 {
			filtered = append(filtered, w)
		}
	}
	queryWords = filtered
	if len(queryWords) == 0 {
		return nil
	}

	var best *models.HotelResult
	bestScore := 0

	for i := range hotels {
		h := &hotels[i]
		nameLower := strings.ToLower(h.Name)

		score := 0
		// Exact contains match scores highest.
		if strings.Contains(nameLower, queryLower) {
			score = 100
		} else {
			for _, w := range queryWords {
				if strings.Contains(nameLower, w) {
					score += 10
				}
			}
		}

		if score > bestScore {
			bestScore = score
			best = h
		}
	}

	if bestScore == 0 {
		return nil
	}
	// Require at least a meaningful match (≥10 points from actual hotel name
	// words, not just "hotel" or other generic terms).
	if bestScore < 10 {
		return nil
	}
	return best
}

// enrichHotelAmenities fetches detail pages for the top N hotels to get full
// amenity lists. Runs up to 3 concurrent fetches. Hotels without a HotelID
// are skipped. Failures are silently ignored (search results still have
// partial amenities from the search page).
func enrichHotelAmenities(ctx context.Context, hotels []models.HotelResult, limit int) []models.HotelResult {
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}

	// Collect indices of hotels eligible for enrichment.
	var indices []int
	for i := range hotels {
		if hotels[i].HotelID != "" && len(indices) < limit {
			indices = append(indices, i)
		}
	}
	if len(indices) == 0 {
		return hotels
	}

	// Fetch detail pages in parallel with concurrency limit of 3.
	const concurrency = 3
	type result struct {
		index     int
		amenities []string
	}

	results := make(chan result, len(indices))
	sem := make(chan struct{}, concurrency)

	var wg sync.WaitGroup
	for _, idx := range indices {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			amenities, err := FetchHotelAmenities(ctx, hotels[i].HotelID)
			if err != nil || len(amenities) == 0 {
				return
			}
			results <- result{index: i, amenities: amenities}
		}(idx)
	}

	// Close results channel when all goroutines complete.
	go func() {
		wg.Wait()
		close(results)
	}()

	// Apply enriched amenities back to hotels.
	for r := range results {
		hotels[r.index].Amenities = mergeAmenities(hotels[r.index].Amenities, r.amenities)
	}

	return hotels
}

// propertyTypeCode converts a human-readable property type string to the
// Google Hotels &ptype= parameter value. Returns "" if the type is unknown
// or empty (meaning: no filter applied).
//
// Known Google Hotels ptype values (reverse-engineered):
//
//	2 = hotel, 3 = apartment, 4 = hostel, 5 = resort, 7 = bnb, 8 = villa
func propertyTypeCode(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "hotel":
		return "2"
	case "apartment":
		return "3"
	case "hostel":
		return "4"
	case "resort":
		return "5"
	case "bnb", "bed_and_breakfast", "bed and breakfast":
		return "7"
	case "villa":
		return "8"
	default:
		return ""
	}
}

// mergeAmenities combines two amenity lists, deduplicating by lowercase name.
// The first list's items take priority in ordering.
// tagHotelSource stamps each hotel with a PriceSource for the given provider
// so that MergeHotelResults can track per-provider prices. Hotels that already
// carry Sources (e.g. from a previous enrichment pass) are left unchanged.
func tagHotelSource(hotels []models.HotelResult, provider string) []models.HotelResult {
	tagged := make([]models.HotelResult, len(hotels))
	copy(tagged, hotels)
	for i := range tagged {
		if len(tagged[i].Sources) == 0 {
			tagged[i].Sources = []models.PriceSource{{
				Provider:   provider,
				Price:      tagged[i].Price,
				Currency:   tagged[i].Currency,
				BookingURL: tagged[i].BookingURL,
			}}
		}
	}
	return tagged
}

func mergeAmenities(existing, additional []string) []string {
	seen := make(map[string]bool, len(existing)+len(additional))
	var merged []string

	for _, a := range existing {
		lower := strings.ToLower(a)
		if !seen[lower] {
			seen[lower] = true
			merged = append(merged, a)
		}
	}
	for _, a := range additional {
		lower := strings.ToLower(a)
		if !seen[lower] {
			seen[lower] = true
			merged = append(merged, a)
		}
	}

	return merged
}
