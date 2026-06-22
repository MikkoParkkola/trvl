package providers

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// substituteVars replaces all ${var} placeholders in s with values from vars.
func substituteVars(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}

// stripUnresolvedPlaceholders removes any remaining ${...} substrings from
// a URL, along with the &key= or ?key= prefix that leads to them. This
// handles optional filter parameters that weren't set (e.g. "&nflt=${nflt}"
// when no filters are active).
func stripUnresolvedPlaceholders(u string) string {
	for {
		idx := strings.Index(u, "${")
		if idx < 0 {
			break
		}
		end := strings.Index(u[idx:], "}")
		if end < 0 {
			break
		}
		end += idx + 1 // position after closing }
		// Walk backwards to remove the &key= or ?key= prefix.
		start := idx
		for start > 0 && u[start-1] != '&' && u[start-1] != '?' {
			start--
		}
		// Include the & separator itself (but keep ? since it starts the query).
		if start > 0 && u[start-1] == '&' {
			start--
		}
		u = u[:start] + u[end:]
	}
	return u
}

// substituteEnvVars replaces ${env.VAR_NAME} placeholders with values from
// the process environment. This allows provider configs to reference API keys
// stored in environment variables without hardcoding them.
func substituteEnvVars(s string) string {
	if !strings.Contains(s, "${env.") {
		return s
	}
	// Find all ${env.XXX} patterns and replace.
	for {
		start := strings.Index(s, "${env.")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "}")
		if end < 0 {
			break
		}
		varName := s[start+6 : start+end] // skip "${env." prefix
		envVal := os.Getenv(varName)
		s = s[:start] + envVal + s[start+end+1:]
	}
	return s
}

// jsonPath walks a parsed JSON value using dot-notation.
// Supports nested objects and traversal through arrays by iterating
// elements until it finds a non-empty match.
//
// When a path segment is applied to an array (e.g. Airbnb's
// explore_tabs.sections.listings where sections is an array of
// objects), the function iterates the array and returns the first
// element whose value for that segment is non-empty. Empty arrays
// and nil values are skipped so that metadata/ad sections (e.g.
// Airbnb "inserts" sections with listings:[]) don't shadow the real
// results in the next section.
func jsonPath(data any, path string) any {
	if path == "" {
		return data
	}
	parts := strings.Split(path, ".")
	current := data
	for _, part := range parts {
		switch v := current.(type) {
		case map[string]any:
			// Wildcard segment: "prefix*" matches the first map key whose
			// name starts with `prefix`. Enables paths like
			// `searchQueries.search*.results` against Apollo normalized
			// caches where the real key is `search({"input":...})`.
			if strings.HasSuffix(part, "*") {
				prefix := part[:len(part)-1]
				var matched any
				found := false
				for k, val := range v {
					if strings.HasPrefix(k, prefix) {
						matched = val
						found = true
						break
					}
				}
				if !found {
					return nil
				}
				current = matched
				continue
			}
			current = v[part]
		case []any:
			// Iterate the array and prefer the first element with a
			// non-empty value for this path segment. Falls back to
			// the first element with any value (including empty).
			var firstAny any
			foundAny := false
			var chosen any
			chose := false
			for _, elem := range v {
				m, ok := elem.(map[string]any)
				if !ok {
					continue
				}
				val, exists := m[part]
				if !exists {
					continue
				}
				if !foundAny {
					firstAny = val
					foundAny = true
				}
				if !isEmptyValue(val) {
					chosen = val
					chose = true
					break
				}
			}
			if chose {
				current = chosen
			} else if foundAny {
				current = firstAny
			} else {
				return nil
			}
		default:
			return nil
		}
	}
	return current
}

// denormalizeApollo recursively resolves __ref pointers in an Apollo
// normalized-cache JSON object. Every object of the form {"__ref": "Key:123"}
// is replaced by the actual entity stored at cache["Key:123"], with nested
// refs resolved recursively. This enables jsonPath to traverse SSR-hydrated
// Apollo data as if it were a plain denormalized JSON tree.
//
// The seen map guards against circular references (unlikely in Apollo, but
// defensive). Returns the resolved value — may be the original if no refs
// are present, so the operation is safe on non-Apollo JSON.
func denormalizeApollo(v any, cache map[string]any, seen map[string]bool) any {
	if seen == nil {
		seen = make(map[string]bool)
	}
	switch obj := v.(type) {
	case map[string]any:
		// Check for __ref pointer.
		if ref, ok := obj["__ref"].(string); ok && len(obj) <= 2 {
			if seen[ref] {
				return obj // circular — stop
			}
			seen[ref] = true
			if entity, exists := cache[ref]; exists {
				return denormalizeApollo(entity, cache, seen)
			}
			return obj // dangling ref
		}
		// Recursively resolve children.
		resolved := make(map[string]any, len(obj))
		for k, child := range obj {
			if k == "__typename" {
				resolved[k] = child
				continue
			}
			resolved[k] = denormalizeApollo(child, cache, seen)
		}
		return resolved
	case []any:
		resolved := make([]any, len(obj))
		for i, elem := range obj {
			resolved[i] = denormalizeApollo(elem, cache, seen)
		}
		return resolved
	default:
		return v
	}
}

// unwrapNiobe detects Airbnb's "Niobe" SSR cache format and returns the
// inner data object so that jsonPath can traverse it with the standard
// results_path (e.g. "data.presentation.staysSearch.results.searchResults").
//
// The Niobe cache structure is:
//
//	{"niobeClientData": [["CacheKey:...", {"data": {...}, "variables": {...}}]]}
//
// Each element of niobeClientData is a 2-element array: [cacheKey, payload].
// This function iterates all entries and returns the first payload whose
// "data" key is a non-empty map. If the input is not Niobe-shaped, it is
// returned unchanged.
func unwrapNiobe(v any) any {
	top, ok := v.(map[string]any)
	if !ok {
		return v
	}
	niobeRaw, hasNiobe := top["niobeClientData"]
	if !hasNiobe {
		return v
	}
	entries, ok := niobeRaw.([]any)
	if !ok || len(entries) == 0 {
		return v
	}
	for _, entry := range entries {
		pair, ok := entry.([]any)
		if !ok || len(pair) < 2 {
			continue
		}
		payload, ok := pair[1].(map[string]any)
		if !ok {
			continue
		}
		if dataObj, hasData := payload["data"].(map[string]any); hasData && len(dataObj) > 0 {
			return payload
		}
	}
	return v
}

// isEmptyValue reports whether v is nil, an empty slice, map, or string.
// Used by jsonPath to skip metadata/placeholder entries when traversing
// arrays of heterogeneous objects.
func isEmptyValue(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	case string:
		return x == ""
	}
	return false
}

// resolveCityID finds the best-matching provider-specific city ID for a
// location string. Matches are case-insensitive and support partial matching
// (e.g. "Prague" → "praha"). Returns the empty string when no mapping exists
// or when the location is blank.
func resolveCityID(lookup map[string]string, location string) string {
	if len(lookup) == 0 {
		return ""
	}
	loc := strings.ToLower(strings.TrimSpace(location))
	if loc == "" {
		return ""
	}
	if id, ok := lookup[loc]; ok {
		return id
	}
	// Partial match: location contains a key, or key contains location.
	for city, id := range lookup {
		c := strings.ToLower(city)
		if c == "" {
			continue
		}
		if strings.Contains(loc, c) || strings.Contains(c, loc) {
			return id
		}
	}
	return ""
}

// resolvePropertyType maps a normalized property type name (e.g. "apartment",
// "hostel", "hotel") to a provider-specific identifier using the lookup table.
// Like resolveCityID, matching is case-insensitive. Returns the empty string
// when no mapping exists or when the property type is blank.
func resolvePropertyType(lookup map[string]string, propertyType string) string {
	if len(lookup) == 0 {
		return ""
	}
	pt := strings.ToLower(strings.TrimSpace(propertyType))
	if pt == "" {
		return ""
	}
	if id, ok := lookup[pt]; ok {
		return id
	}
	// Case-insensitive scan over keys.
	for key, id := range lookup {
		if strings.ToLower(key) == pt {
			return id
		}
	}
	return ""
}

// mapHotelResult maps a raw JSON object to a HotelResult using field mappings.
func mapHotelResult(raw any, fields map[string]string) models.HotelResult {
	var h models.HotelResult
	var priceStr string // raw price string for currency extraction fallback
	for modelField, jsonField := range fields {
		val := jsonPath(raw, jsonField)
		if val == nil {
			continue
		}
		switch modelField {
		case "name":
			h.Name, _ = val.(string)
		case "hotel_id":
			// Format IDs without scientific notation. JSON numbers deserialize
			// as float64 in Go; a hotel_id of 1042748 becomes 1.042748e+06
			// which is useless as an identifier. Detect whole-number floats
			// and format as integers.
			switch id := val.(type) {
			case float64:
				if id == float64(int64(id)) {
					h.HotelID = strconv.FormatInt(int64(id), 10)
				} else {
					h.HotelID = strconv.FormatFloat(id, 'f', -1, 64)
				}
			default:
				h.HotelID = fmt.Sprintf("%v", val)
			}
		case "rating":
			h.Rating = toFloat64(val)
		case "review_count":
			h.ReviewCount = toInt(val)
		case "stars":
			h.Stars = toInt(val)
		case "price":
			h.Price = toFloat64(val)
			if s, ok := val.(string); ok {
				priceStr = s
			}
		case "currency":
			h.Currency, _ = val.(string)
		case "address":
			h.Address, _ = val.(string)
		case "lat":
			h.Lat = toFloat64(val)
		case "lon":
			h.Lon = toFloat64(val)
		case "booking_url":
			h.BookingURL, _ = val.(string)
		case "eco_certified":
			h.EcoCertified, _ = val.(bool)
		case "description":
			h.Description, _ = val.(string)
		case "image_url":
			h.ImageURL, _ = val.(string)
		case "neighborhood":
			h.Neighborhood, _ = val.(string)
		}
	}

	// When no explicit currency field was mapped (or it resolved to empty),
	// try to extract a currency code from the raw price string. Providers
	// like Airbnb embed the currency in the display price ("EUR 204", "€175")
	// without exposing a separate currency field.
	if h.Currency == "" && priceStr != "" {
		h.Currency = extractCurrencyCode(priceStr)
	}

	return h
}

// extractNeighborhood extracts the neighborhood/district name from a Booking.com
// SSR hotel result. Booking stores this at basicPropertyData.location.neighbourhood.name
// or basicPropertyData.neighbourhood.name. Returns empty string when unavailable.
func extractNeighborhood(raw any) string {
	// Primary: basicPropertyData.location.neighbourhood.name
	if name, ok := jsonPath(raw, "basicPropertyData.location.neighbourhood.name").(string); ok && name != "" {
		return name
	}
	// Fallback: basicPropertyData.neighbourhood.name
	if name, ok := jsonPath(raw, "basicPropertyData.neighbourhood.name").(string); ok && name != "" {
		return name
	}
	return ""
}

// extractBlocksPriceSpread scans the "blocks" array on a raw hotel result
// (Booking.com SSR structure) and returns the maximum block price and the
// number of distinct room types. This gives the LLM a price spread signal
// ("cheapest room €120, most expensive €280, 4 room types") without
// requiring a separate hotel_rooms drill-down call.
func extractBlocksPriceSpread(raw any) (maxPrice float64, roomCount int) {
	blocksRaw := jsonPath(raw, "blocks")
	blocks, ok := blocksRaw.([]any)
	if !ok || len(blocks) == 0 {
		return 0, 0
	}
	seen := make(map[string]bool)
	for _, b := range blocks {
		price := toFloat64(jsonPath(b, "finalPrice.amount"))
		if price > maxPrice {
			maxPrice = price
		}
		// Count distinct room types by blockId.roomId.
		if roomID := fmt.Sprintf("%v", jsonPath(b, "blockId.roomId")); roomID != "<nil>" {
			seen[roomID] = true
		}
	}
	return maxPrice, len(seen)
}

// extractRoomTypes extracts distinct room types from a Booking.com SSR hotel
// result. It checks two sources:
//
//  1. matchingUnitConfigurations.unitConfigurations — an array of room type
//     objects with a "name" field (e.g. "Standard Double Room", "Superior Suite").
//
//  2. blocks — the per-room pricing array. Each block has a "roomName" (or
//     "room_name") field and a "finalPrice.amount" for per-room pricing.
//
// Returns deduplicated rooms. Empty when the raw data has neither structure.
func extractRoomTypes(raw any) []models.Room {
	seen := make(map[string]bool)
	var rooms []models.Room

	// Build a lookup from unitId → unit config for cross-referencing with blocks.
	type unitInfo struct {
		name           string
		bedDescription string
	}
	unitByID := make(map[string]unitInfo)

	// Source 1: matchingUnitConfigurations.unitConfigurations
	unitsRaw := jsonPath(raw, "matchingUnitConfigurations.unitConfigurations")
	if units, ok := unitsRaw.([]any); ok {
		for _, u := range units {
			name, _ := jsonPath(u, "name").(string)
			if name == "" {
				continue
			}
			unitID := fmt.Sprintf("%v", jsonPath(u, "unitId"))

			// Extract bed type from bedConfigurations.
			var bedDesc string
			if beds := jsonPath(u, "bedConfigurations"); beds != nil {
				if bedArr, ok := beds.([]any); ok && len(bedArr) > 0 {
					if desc, ok := jsonPath(bedArr[0], "description").(string); ok {
						bedDesc = desc
					} else {
						// Build from beds array: "1 double bed"
						if innerBeds := jsonPath(bedArr[0], "beds"); innerBeds != nil {
							if ba, ok := innerBeds.([]any); ok {
								for _, bed := range ba {
									count := toInt(jsonPath(bed, "count"))
									bedType := toInt(jsonPath(bed, "type"))
									typeName := bookingBedType(bedType)
									if count > 0 && typeName != "" {
										bedDesc = fmt.Sprintf("%d %s", count, typeName)
									}
								}
							}
						}
					}
				}
			}

			unitByID[unitID] = unitInfo{name: name, bedDescription: bedDesc}
		}
	}

	// Source 2: blocks array — has per-room pricing + rich metadata.
	blocksRaw := jsonPath(raw, "blocks")
	blocks, ok := blocksRaw.([]any)
	if !ok || len(blocks) == 0 {
		return rooms
	}

	for _, b := range blocks {
		// Cross-reference: get room name from unitConfigurations via blockId.roomId.
		roomID := fmt.Sprintf("%v", jsonPath(b, "blockId.roomId"))
		name := ""
		var bedDesc string
		if info, ok := unitByID[roomID]; ok {
			name = info.name
			bedDesc = info.bedDescription
		}
		// Fallback: try roomName / room_name directly on the block.
		if name == "" {
			name, _ = jsonPath(b, "roomName").(string)
		}
		if name == "" {
			name, _ = jsonPath(b, "room_name").(string)
		}
		if name == "" {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		price := toFloat64(jsonPath(b, "finalPrice.amount"))
		currency, _ := jsonPath(b, "finalPrice.currency").(string)

		room := models.Room{
			Name:            name,
			Price:           price,
			TotalPrice:      price,
			Currency:        currency,
			BedType:         bedDesc,
			MatchConfidence: models.RoomInventoryMatchExact,
			PriceBasis:      models.PriceBasisRoomTotal,
			PriceConfidence: models.PriceConfidenceRoomLevel,
		}

		// Room size (m²).
		if size := toFloat64(jsonPath(b, "roomSize.value")); size > 0 {
			room.SizeM2 = size
		}

		// Max guests / occupancy.
		if maxG := toInt(jsonPath(b, "maxOccupancy")); maxG > 0 {
			room.MaxGuests = maxG
		} else if maxG := toInt(jsonPath(b, "nbAdults")); maxG > 0 {
			room.MaxGuests = maxG
		}

		// Bed type from bedConfigurations or bedType.
		if bedDesc, ok := jsonPath(b, "bedType").(string); ok && bedDesc != "" {
			room.BedType = bedDesc
		} else if beds := jsonPath(b, "bedConfigurations"); beds != nil {
			if bedArr, ok := beds.([]any); ok && len(bedArr) > 0 {
				if desc, ok := jsonPath(bedArr[0], "description").(string); ok {
					room.BedType = desc
				}
			}
		}

		// Free cancellation.
		if fc, ok := jsonPath(b, "freeCancellationUntil").(string); ok && fc != "" {
			room.FreeCancellation = true
		}
		if fc, _ := jsonPath(b, "policies.showFreeCancellation").(bool); fc {
			room.FreeCancellation = true
		}

		// Breakfast included (mealPlan or breakfast fields).
		if mp, _ := jsonPath(b, "mealPlanIncluded").(bool); mp {
			room.BreakfastIncluded = true
		}
		if bk, ok := jsonPath(b, "breakfast").(string); ok && bk != "" {
			room.BreakfastIncluded = true
		}

		// Room-level amenities from roomFacilities or facilities.
		if facRaw := jsonPath(b, "roomFacilities"); facRaw != nil {
			if facArr, ok := facRaw.([]any); ok {
				for _, f := range facArr {
					if fname, ok := jsonPath(f, "name").(string); ok && fname != "" {
						room.Amenities = append(room.Amenities, fname)
					} else if fname, ok := f.(string); ok {
						room.Amenities = append(room.Amenities, fname)
					}
				}
			}
		}

		rooms = append(rooms, room)
	}

	// Add any unitConfigurations rooms that weren't matched to blocks
	// (rooms with no pricing in this search, e.g. sold out).
	for unitID, info := range unitByID {
		if !seen[info.name] {
			seen[info.name] = true
			rooms = append(rooms, models.Room{
				Name:            info.name,
				BedType:         info.bedDescription,
				MatchConfidence: models.RoomInventoryMatchExact,
			})
			_ = unitID // used for cross-reference only
		}
	}

	return rooms
}

// bookingBedType maps Booking's numeric bed type codes to human-readable names.
func bookingBedType(typeCode int) string {
	switch typeCode {
	case 1:
		return "single bed"
	case 2:
		return "double bed"
	case 3:
		return "bunk bed"
	case 4:
		return "futon"
	case 5:
		return "sofa bed"
	case 6:
		return "king bed"
	case 7:
		return "queen bed"
	default:
		return "bed"
	}
}

// extractImageURL extracts the main property image from a Booking.com SSR
// hotel result. Booking stores images at basicPropertyData.photos.main with
// highResUrl and lowResUrl variants. Airbnb and Hostelworld use field mappings
// instead (image_url in the provider config).
func extractImageURL(raw any) string {
	// Booking: basicPropertyData.photos.main.highResUrl
	if url, ok := jsonPath(raw, "basicPropertyData.photos.main.highResUrl").(map[string]any); ok {
		if relURL, ok := url["relativeUrl"].(string); ok && relURL != "" {
			return "https://cf.bstatic.com" + relURL
		}
	}
	if url, _ := jsonPath(raw, "basicPropertyData.photos.main.highResUrl").(string); url != "" {
		return url
	}
	// Fallback: lowResUrl
	if url, _ := jsonPath(raw, "basicPropertyData.photos.main.lowResUrl").(string); url != "" {
		return url
	}
	return ""
}

// extractDescription extracts a property description/tagline from a Booking.com
// SSR hotel result. Booking stores this at basicPropertyData.tagline or
// in the propertyDescription field.
func extractDescription(raw any) string {
	// Try propertyDescription first (full text).
	if desc, ok := jsonPath(raw, "propertyDescription").(string); ok && desc != "" {
		return desc
	}
	// Booking tagline.
	if desc, ok := jsonPath(raw, "basicPropertyData.tagline").(string); ok && desc != "" {
		return desc
	}
	return ""
}
