package hotels

import "strings"

// extractLocationFromSearchData recursively searches parsed callback data
// for location triplets [null, "CityName", "place_id"], the format
// Google Hotels uses for location references in search-page data.
// Returns the city name from the first matching triplet, or "" if none found.
func extractLocationFromSearchData(v any, depth int) string {
	if depth > 10 {
		return ""
	}
	arr, ok := v.([]any)
	if !ok {
		return ""
	}
	// Location triplet: [null, "CityName", "place_id_hex"]
	if len(arr) == 3 && arr[0] == nil {
		city, cityOK := arr[1].(string)
		pid, pidOK := arr[2].(string)
		if cityOK && pidOK && len(city) >= 2 && len(city) <= 80 {
			if strings.HasPrefix(pid, "0x") {
				return city
			}
		}
	}
	// Recurse into sub-arrays.
	for _, item := range arr {
		if loc := extractLocationFromSearchData(item, depth+1); loc != "" {
			return loc
		}
	}
	return ""
}

// extractLocationFromPage extracts the city name from the AF_initDataCallback
// data on a Google Hotels page. The location is at data[6][1][18][1] in
// organic hotel entries, stored as a triplet [null, "CityName", "placeID"].
//
// On search pages this returns the city (e.g. "Paris"). On entity pages
// with deferred data loading, the callbacks are empty and this returns "".
//
// The search-wide params at [6][1][18] contain [null, "CityName", "placeID"].
// We recursively search all callbacks for this triplet pattern.
func extractLocationFromPage(page string) string {
	callbacks := extractCallbacks(page)
	if len(callbacks) == 0 {
		return ""
	}

	// Search each callback for a location triplet.
	for _, cb := range callbacks {
		if loc := extractLocationFromCallback(cb); loc != "" {
			return loc
		}
	}

	return ""
}

// extractLocationFromCallback searches a parsed callback for location data
// at the path used by the search-wide price parameters: [6][1][18][1].
// Delegates to the generic extractLocationFromSearchData recursive scanner.
func extractLocationFromCallback(v any) string {
	return findLocationTriplet(v, 0)
}

// findLocationTriplet recursively searches for arrays matching
// [null, "city_name", "place_id_hex"] which is how Google embeds location
// references in hotel data.
// Delegates to the generic extractLocationFromSearchData recursive scanner.
func findLocationTriplet(v any, depth int) string {
	return extractLocationFromSearchData(v, depth)
}
