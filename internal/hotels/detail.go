package hotels

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/jsonutil"
)

// FetchHotelAmenities fetches the hotel detail page and extracts the full
// amenity list. The hotelID should be a Google place ID as returned in
// search results (e.g. "/g/11b6d4_v_4" or "ChIJ...").
//
// Returns a deduplicated, human-readable slice of amenity names.
func FetchHotelAmenities(ctx context.Context, hotelID string) ([]string, error) {
	if hotelID == "" {
		return nil, fmt.Errorf("hotel ID is required")
	}

	client := DefaultClient()
	entityURL := fmt.Sprintf("https://www.google.com/travel/hotels/entity/%s?hl=en-US&gl=us", hotelID)

	status, body, err := client.Get(ctx, entityURL)
	if err != nil {
		return nil, fmt.Errorf("hotel detail request: %w", err)
	}
	if status == 403 {
		return nil, batchexec.ErrBlocked
	}
	if status != 200 {
		return nil, fmt.Errorf("hotel detail page returned status %d", status)
	}
	if len(body) < 500 {
		return nil, fmt.Errorf("hotel detail page returned empty response")
	}

	return parseDetailAmenities(string(body))
}

// parseDetailAmenities extracts amenities from a hotel detail page's
// AF_initDataCallback blocks.
//
// The detail page contains amenity data in multiple possible locations:
//
//  1. Amenity groups: arrays of [groupName, [[name1], [name2], ...]]
//     found at various nesting depths. Each group (e.g. "Popular",
//     "Parking", "Food & drink") contains individual amenity names.
//
//  2. Flat amenity lists: arrays of strings or single-element arrays
//     containing amenity names.
//
//  3. Amenity code pairs (same format as search results): [[1, code], ...]
func parseDetailAmenities(page string) ([]string, error) {
	callbacks := extractCallbacks(page)
	if len(callbacks) == 0 {
		return nil, fmt.Errorf("no AF_initDataCallback blocks found in detail page")
	}

	seen := make(map[string]bool)
	var amenities []string

	for _, cb := range callbacks {
		found := findDetailAmenities(cb, 0)
		for _, name := range found {
			name = normalizeAmenityName(name)
			if name == "" {
				continue
			}
			if !seen[name] {
				seen[name] = true
				amenities = append(amenities, name)
			}
		}
	}

	if len(amenities) == 0 {
		return nil, fmt.Errorf("no amenities found in detail page")
	}

	return amenities, nil
}

// findDetailAmenities recursively searches parsed JSON for amenity data.
// It looks for:
//   - Amenity group arrays: [groupName, [[amenityName], [amenityName], ...]]
//   - Amenity code pairs: [[1, code], [1, code], ...] (same as search)
//   - Named amenity arrays in detail-page-specific structures
func findDetailAmenities(v any, depth int) []string {
	if depth > 12 {
		return nil
	}

	arr, ok := v.([]any)
	if !ok {
		if m, ok := v.(map[string]any); ok {
			var results []string
			for _, mv := range m {
				results = append(results, findDetailAmenities(mv, depth+1)...)
			}
			return results
		}
		return nil
	}

	// Check if this array looks like amenity code pairs [[1, code], ...].
	if names := tryAmenityCodePairs(arr); len(names) > 0 {
		return names
	}

	// Check if this looks like an amenity group: [string, [[string], ...]]
	if names := tryAmenityGroup(arr); len(names) > 0 {
		return names
	}

	// Check if this is a flat list of amenity-name arrays: [[name], [name], ...]
	if names := tryFlatAmenityList(arr); len(names) > 0 {
		return names
	}

	// Recurse into sub-arrays.
	var results []string
	for _, item := range arr {
		results = append(results, findDetailAmenities(item, depth+1)...)
	}
	return results
}

// tryAmenityCodePairs checks if arr contains amenity code pairs [[1, N], ...]
// and returns the corresponding amenity names. Unlike extractAmenityCodes
// (which searches for pairs nested inside a container), this function treats
// arr itself as the pairs array.
func tryAmenityCodePairs(arr []any) []string {
	if len(arr) < 3 {
		return nil
	}

	// All elements must be 2-element arrays with [float64, float64].
	for _, item := range arr {
		pair, ok := item.([]any)
		if !ok || len(pair) != 2 {
			return nil
		}
		if _, ok := pair[0].(float64); !ok {
			return nil
		}
		if _, ok := pair[1].(float64); !ok {
			return nil
		}
	}

	// Decode pairs directly.
	seen := make(map[string]bool)
	var names []string
	for _, item := range arr {
		pair := item.([]any)
		available := pair[0].(float64)
		if available != 1 {
			continue
		}
		code := int(pair[1].(float64))
		name, known := amenityCodeMap[code]
		if !known {
			continue
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// tryAmenityGroup checks if arr looks like [groupName, [[amenityName], ...]].
// Google Hotels detail pages organize amenities into groups like "Popular",
// "Parking", "Food & drink", "Activities", etc.
func tryAmenityGroup(arr []any) []string {
	if len(arr) < 2 {
		return nil
	}

	// First element should be a group name string.
	groupName, ok := arr[0].(string)
	if !ok || groupName == "" {
		return nil
	}

	// Known amenity group names on Google Hotels detail pages.
	if !isAmenityGroupName(groupName) {
		return nil
	}

	// Second element should be an array of amenity entries.
	entries, ok := arr[1].([]any)
	if !ok || len(entries) == 0 {
		return nil
	}

	var names []string
	for _, entry := range entries {
		entryArr, ok := entry.([]any)
		if !ok || len(entryArr) == 0 {
			continue
		}
		// Each entry is [amenityName, ...] or just [amenityName].
		if name := jsonutil.StringValue(entryArr[0]); name != "" {
			names = append(names, name)
		}
	}

	return names
}

// tryFlatAmenityList checks if arr is a list of single-element arrays each
// containing an amenity name string, e.g. [["Pool"], ["Spa"], ["Free WiFi"]].
func tryFlatAmenityList(arr []any) []string {
	if len(arr) < 3 {
		return nil
	}

	// At least 60% of elements must be single-string arrays for this to be
	// an amenity list (allow some nulls/metadata).
	stringCount := 0
	for _, item := range arr {
		sub, ok := item.([]any)
		if ok && len(sub) >= 1 {
			if _, ok := sub[0].(string); ok {
				stringCount++
			}
		}
	}

	if stringCount < 3 || float64(stringCount)/float64(len(arr)) < 0.5 {
		return nil
	}

	// Also verify these look like amenity names (not random strings).
	var names []string
	amenityLike := 0
	for _, item := range arr {
		sub, ok := item.([]any)
		if !ok || len(sub) == 0 {
			continue
		}
		name, ok := sub[0].(string)
		if !ok || name == "" {
			continue
		}
		if looksLikeAmenity(name) {
			amenityLike++
		}
		names = append(names, name)
	}

	// Require at least 2 amenity-like names to avoid false positives.
	if amenityLike < 2 {
		return nil
	}

	return names
}

// amenityGroupNames are the known amenity category headers on Google Hotels
// detail pages (case-insensitive matching).
var amenityGroupNames = map[string]bool{
	"popular":                true,
	"parking":                true,
	"food & drink":           true,
	"food and drink":         true,
	"internet":               true,
	"activities":             true,
	"accessibility":          true,
	"property amenities":     true,
	"room amenities":         true,
	"bathroom":               true,
	"bedroom":                true,
	"entertainment":          true,
	"outdoor":                true,
	"health & wellness":      true,
	"health and wellness":    true,
	"services":               true,
	"business":               true,
	"transportation":         true,
	"family":                 true,
	"general":                true,
	"front desk services":    true,
	"cleaning services":      true,
	"safety & security":      true,
	"safety and security":    true,
	"pets":                   true,
	"media & technology":     true,
	"media and technology":   true,
	"pool":                   true,
	"spa":                    true,
	"fitness":                true,
	"nearby":                 true,
	"highlights":             true,
	"top amenities":          true,
	"most popular":           true,
	"guest favorite":         true,
	"what this place offers": true,
}

// isAmenityGroupName returns true if the name matches a known amenity group header.
func isAmenityGroupName(name string) bool {
	return amenityGroupNames[strings.ToLower(strings.TrimSpace(name))]
}

// amenityKeywords are substrings that indicate a string is likely an amenity name.
var amenityKeywords = []string{
	"wifi", "wi-fi", "internet", "pool", "spa", "gym", "fitness",
	"parking", "breakfast", "restaurant", "bar", "lounge",
	"kitchen", "laundry", "air conditioning", "room service",
	"pet", "shuttle", "ev charger", "accessible", "concierge",
	"minibar", "safe", "balcony", "terrace", "garden",
	"beach", "sauna", "hot tub", "jacuzzi", "tennis",
	"golf", "bicycle", "coffee", "tea", "refrigerator",
	"microwave", "dishwasher", "washer", "dryer", "iron",
	"hair dryer", "bathtub", "shower", "tv", "cable",
	"free", "complimentary", "24-hour", "heated",
}

// looksLikeAmenity returns true if the string resembles an amenity name.
func looksLikeAmenity(s string) bool {
	lower := strings.ToLower(s)
	for _, kw := range amenityKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// normalizeAmenityName cleans up an amenity name string.
func normalizeAmenityName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 100 {
		return ""
	}
	// Skip strings that look like URLs or HTML.
	if strings.HasPrefix(s, "http") || strings.Contains(s, "<") {
		return ""
	}
	return s
}
