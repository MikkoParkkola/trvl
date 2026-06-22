package hotels

import (
	"encoding/json"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// parseRoomsFromPage extracts room-type data from a hotel entity page.
// Returns room types and the hotel name if found.
func parseRoomsFromPage(page, currency string) ([]RoomType, string) {
	callbacks := extractCallbacks(page)
	if len(callbacks) == 0 {
		return nil, ""
	}

	var hotelName string
	var rooms []RoomType
	seen := make(map[string]bool)

	for _, cb := range callbacks {
		// Try to extract hotel name from callbacks.
		if hotelName == "" {
			hotelName = extractHotelNameFromCallback(cb)
		}

		// Search for room data recursively.
		found := findRoomData(cb, currency, 0)
		for _, r := range found {
			key := strings.ToLower(r.Name)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			rooms = append(rooms, r)
		}
	}

	return rooms, hotelName
}

// extractHotelNameFromCallback attempts to find a hotel name string in a callback.
// It looks for the first string that looks like a hotel name (title-case, reasonable length).
func extractHotelNameFromCallback(v any) string {
	arr, ok := v.([]any)
	if !ok {
		return ""
	}
	// Navigate: try [0][0] or [0][0][0] paths often containing hotel name.
	for _, path := range [][]int{{0, 0}, {0, 0, 0}, {0, 0, 0, 0}} {
		cur := any(arr)
		for _, idx := range path {
			a, ok := cur.([]any)
			if !ok || idx >= len(a) {
				cur = nil
				break
			}
			cur = a[idx]
		}
		if s, ok := cur.(string); ok && looksLikeHotelName(s) {
			return s
		}
	}
	return ""
}

// looksLikeHotelName returns true if s appears to be a hotel name:
// non-empty, reasonably short, not a URL, not pure digits.
func looksLikeHotelName(s string) bool {
	if len(s) < 4 || len(s) > 120 {
		return false
	}
	if strings.HasPrefix(s, "http") || strings.Contains(s, "<") || strings.Contains(s, "{") {
		return false
	}
	// Must have at least one letter.
	hasLetter := false
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			hasLetter = true
			break
		}
	}
	return hasLetter
}

// findRoomData recursively searches parsed JSON for room pricing data.
// Room data appears as arrays containing room name strings paired with prices.
func findRoomData(v any, currency string, depth int) []RoomType {
	if depth > 15 {
		return nil
	}

	arr, ok := v.([]any)
	if !ok {
		if m, ok := v.(map[string]any); ok {
			var results []RoomType
			for _, mv := range m {
				results = append(results, findRoomData(mv, currency, depth+1)...)
			}
			return results
		}
		return nil
	}

	// Check if this array looks like a room entry: [name, price, currency, provider, ...]
	if room := tryRoomEntry(arr, currency); room != nil {
		return []RoomType{*room}
	}

	// Check if this looks like a list of room entries.
	if rooms := tryRoomList(arr, currency); len(rooms) > 0 {
		return rooms
	}

	// Recurse.
	var results []RoomType
	for _, item := range arr {
		results = append(results, findRoomData(item, currency, depth+1)...)
	}
	return results
}

// tryRoomEntry checks if arr looks like a single room record:
// [string name, float64 price, string currency, string provider, ...]
// or [string name, float64 price, ...] with at least name + price.
func tryRoomEntry(arr []any, defaultCurrency string) *RoomType {
	if len(arr) < 2 {
		return nil
	}

	name, ok := arr[0].(string)
	if !ok || name == "" || len(name) > 100 {
		return nil
	}
	// Name must look like a room type (not a URL, not a code).
	if !looksLikeRoomName(name) {
		return nil
	}

	// Second element must be a numeric price.
	price, ok := toFloat64(arr[1])
	if !ok || price <= 0 || price > 100000 {
		return nil
	}

	room := &RoomType{
		Name:            name,
		Price:           price,
		Currency:        defaultCurrency,
		MatchConfidence: models.RoomInventoryMatchExact,
	}

	// Optional: currency at [2].
	if len(arr) >= 3 {
		if cur, ok := arr[2].(string); ok && len(cur) == 3 && isUpperAlpha(cur) {
			room.Currency = cur
		}
	}

	// Optional: provider at [3].
	if len(arr) >= 4 {
		if prov, ok := arr[3].(string); ok && looksLikeProvider(prov) {
			room.Provider = prov
		}
	}

	return room
}

// tryRoomList checks if arr is a list where most elements are room entries.
func tryRoomList(arr []any, currency string) []RoomType {
	if len(arr) < 2 {
		return nil
	}

	var rooms []RoomType
	for _, item := range arr {
		sub, ok := item.([]any)
		if !ok {
			continue
		}
		if r := tryRoomEntry(sub, currency); r != nil {
			rooms = append(rooms, *r)
		}
	}

	// Require at least 2 room entries to be confident this is a room list.
	if len(rooms) < 2 {
		return nil
	}
	return rooms
}

// looksLikeRoomName returns true if s appears to be a room type name.
func looksLikeRoomName(s string) bool {
	if len(s) < 3 || len(s) > 100 {
		return false
	}
	if strings.HasPrefix(s, "http") || strings.Contains(s, "<") {
		return false
	}
	lower := strings.ToLower(s)
	roomKeywords := []string{
		"room", "suite", "studio", "apartment", "double", "twin", "single",
		"king", "queen", "deluxe", "standard", "superior", "premium",
		"bed", "bedroom", "penthouse", "villa", "bungalow", "cottage",
		"sea view", "ocean view", "garden view", "pool view", "city view",
		"balcony", "terrace", "floor", "classic", "comfort",
	}
	for _, kw := range roomKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// looksLikeProvider returns true if s looks like a booking provider name.
func looksLikeProvider(s string) bool {
	if len(s) < 3 || len(s) > 60 {
		return false
	}
	if strings.HasPrefix(s, "http") {
		return false
	}
	providers := []string{
		"booking", "expedia", "hotels.com", "agoda", "trip.com",
		"kayak", "trivago", "priceline", "orbitz", "travelocity",
		"marriott", "hilton", "hyatt", "accor", "ihg",
		"direct", "official",
	}
	lower := strings.ToLower(s)
	for _, p := range providers {
		if strings.Contains(lower, p) {
			return true
		}
	}
	// Generic: title-case multi-word string without special chars.
	return len(strings.Fields(s)) >= 1 && !strings.ContainsAny(s, "{}[]<>\\")
}

// toFloat64 attempts to convert v to float64.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// isUpperAlpha returns true if all runes in s are uppercase ASCII letters.
func isUpperAlpha(s string) bool {
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
