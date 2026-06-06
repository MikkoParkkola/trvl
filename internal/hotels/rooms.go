package hotels

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// RoomType represents a specific room category at a hotel.
type RoomType struct {
	Name               string   `json:"name"`
	Price              float64  `json:"price"`
	NightlyPrice       float64  `json:"nightly_price,omitempty"`
	TotalPrice         float64  `json:"total_price,omitempty"`
	TaxesAndFees       float64  `json:"taxes_and_fees,omitempty"`
	TaxesFeesIncluded  *bool    `json:"taxes_fees_included,omitempty"`
	Currency           string   `json:"currency"`
	Provider           string   `json:"provider,omitempty"`
	MaxGuests          int      `json:"max_guests,omitempty"`
	BedType            string   `json:"bed_type,omitempty"`
	SizeM2             float64  `json:"size_m2,omitempty"`
	Description        string   `json:"description,omitempty"`
	Amenities          []string `json:"amenities,omitempty"`
	CancellationPolicy string   `json:"cancellation_policy,omitempty"`
	Refundable         *bool    `json:"refundable,omitempty"`
	FreeCancellation   *bool    `json:"free_cancellation,omitempty"`
	Board              string   `json:"board,omitempty"`
	BreakfastIncluded  *bool    `json:"breakfast_included,omitempty"`
}

// RoomAvailability is the response for a room-type search.
type RoomAvailability struct {
	Success  bool       `json:"success"`
	HotelID  string     `json:"hotel_id"`
	Name     string     `json:"name,omitempty"`
	CheckIn  string     `json:"check_in"`
	CheckOut string     `json:"check_out"`
	Rooms    []RoomType `json:"rooms"`
	Error    string     `json:"error,omitempty"`
}

// RoomSearchOptions configures a room availability search.
type RoomSearchOptions struct {
	HotelID    string // Google Hotels entity ID
	CheckIn    string // YYYY-MM-DD
	CheckOut   string // YYYY-MM-DD
	Currency   string // e.g. "USD", "EUR"
	BookingURL string // optional Booking.com hotel URL for rich room data
	Location   string // optional city/area hint for search-based fallback
}

// GetRoomAvailability fetches room-level pricing for a specific hotel.
//
// It fetches the hotel entity page and parses AF_initDataCallback blocks
// to extract room type names, prices, and provider information.
//
// When a BookingURL is provided (via opts or the bookingURL parameter),
// the function also fetches the Booking.com detail page to extract rich
// room data (descriptions, amenities, bed types, sizes) and merges those
// rooms into the result.
func GetRoomAvailability(ctx context.Context, hotelID, checkIn, checkOut, currency string) (*RoomAvailability, error) {
	return GetRoomAvailabilityWithOpts(ctx, RoomSearchOptions{
		HotelID:  hotelID,
		CheckIn:  checkIn,
		CheckOut: checkOut,
		Currency: currency,
	})
}

// GetRoomAvailabilityWithOpts fetches room-level pricing with full options,
// including optional Booking.com room enrichment.
//
// Google's entity page now uses deferred data loading via batchexecute RPCs
// that require browser session context. The inline AF_initDataCallback blocks
// are empty. As a fallback, this function searches for the hotel on the
// Google Hotels search page (which still embeds data inline) and constructs
// room entries from the search result price data.
func GetRoomAvailabilityWithOpts(ctx context.Context, opts RoomSearchOptions) (*RoomAvailability, error) {
	if opts.HotelID == "" {
		return nil, fmt.Errorf("hotel ID is required")
	}
	if opts.CheckIn == "" || opts.CheckOut == "" {
		return nil, fmt.Errorf("check-in and check-out dates are required")
	}
	if opts.Currency == "" {
		opts.Currency = "USD"
	}

	// Try the entity page first (fast path, works when Google serves inline data).
	// Also capture any location hint extracted from the entity page so the
	// search-page fallback can use it when no Location was provided by the caller
	// (e.g. raw hotel ID lookups from the CLI or MCP without a name hint).
	rooms, hotelName, entityLocation := tryEntityPage(ctx, opts)
	if opts.Location == "" && entityLocation != "" {
		opts.Location = entityLocation
	}

	// Fetch Booking.com rooms to provide room-level data alongside Google's.
	// Runs synchronously before the fallback so Booking data is available
	// regardless of whether the Google entity page returns room data.
	var bookingRooms []RoomType
	if opts.BookingURL != "" {
		br, brErr := FetchBookingRooms(ctx, opts.BookingURL, opts.CheckIn, opts.CheckOut, opts.Currency)
		if brErr != nil {
			slog.Debug("booking rooms fetch failed", "error", brErr)
		} else {
			bookingRooms = br
		}
	}

	// Fallback: search for the hotel on the search page by location extracted
	// from the hotel ID's geocoded area. The search page still has inline
	// AF_initDataCallback data.
	if len(rooms) == 0 {
		rooms, hotelName = trySearchPageFallback(ctx, opts)
	}

	// Merge Booking.com rooms with Google rooms if both are available.
	if len(bookingRooms) > 0 {
		rooms = mergeRoomTypes(rooms, bookingRooms)
	}

	return &RoomAvailability{
		Success:  true,
		HotelID:  opts.HotelID,
		Name:     hotelName,
		CheckIn:  opts.CheckIn,
		CheckOut: opts.CheckOut,
		Rooms:    rooms,
	}, nil
}

// tryEntityPage attempts to extract room data from the Google Hotels entity
// page. Returns nil rooms if the page uses deferred loading (common since
// mid-2026). The third return value is a location hint extracted from the page
// (e.g. "Paris") which callers can use as a fallback when no Location was
// provided in opts.
func tryEntityPage(ctx context.Context, opts RoomSearchOptions) ([]RoomType, string, string) {
	client := DefaultClient()
	entityURL := fmt.Sprintf(
		"https://www.google.com/travel/hotels/entity/%s?q=&dates=%s,%s&hl=en&currency=%s",
		opts.HotelID, opts.CheckIn, opts.CheckOut, opts.Currency,
	)

	status, body, err := client.Get(ctx, entityURL)
	if err != nil || status != 200 || len(body) < 500 {
		return nil, "", ""
	}

	page := string(body)
	rooms, hotelName := parseRoomsFromPage(page, opts.Currency)

	// Extract location from the entity page so the caller can pass it to the
	// search-page fallback when opts.Location is empty.
	location := extractLocationFromPage(page)

	return rooms, hotelName, location
}

// trySearchPageFallback searches for the hotel on the Google Hotels search
// page to extract its price data. The search page embeds hotel data in
// AF_initDataCallback blocks (unlike the entity page which now defers them).
//
// This function searches by the location associated with the hotel ID,
// finds the specific hotel by matching its entity ID, and returns a room
// entry with the hotel's price from the search results.
func trySearchPageFallback(ctx context.Context, opts RoomSearchOptions) ([]RoomType, string) {
	if opts.Location == "" {
		return nil, ""
	}

	searchOpts := HotelSearchOptions{
		CheckIn:  opts.CheckIn,
		CheckOut: opts.CheckOut,
		Guests:   2,
		Currency: opts.Currency,
		MaxPages: 1, // Single page — just need to find the target hotel.
	}

	// Try multiple location candidates extracted from the hint (e.g.
	// "Hotel Lutetia, Paris" yields ["Paris", "Hotel Lutetia Paris"]).
	client := DefaultClient()
	candidates := buildLocationCandidates(opts.Location)
	var result *models.HotelSearchResult
	for _, loc := range candidates {
		r, err := SearchHotelsWithClient(ctx, client, loc, searchOpts)
		if err == nil && len(r.Hotels) > 0 {
			result = r
			break
		}
	}
	if result == nil || len(result.Hotels) == 0 {
		return nil, ""
	}

	// Find target hotel by ID first, then by name as fallback.
	// Google Hotels uses different ID formats on the search page vs
	// the entity page, so strict ID matching often fails for raw IDs
	// passed from the CLI or MCP tools.
	var hotel *models.HotelResult
	for i := range result.Hotels {
		if result.Hotels[i].HotelID == opts.HotelID {
			hotel = &result.Hotels[i]
			break
		}
	}
	if hotel == nil {
		// ID matching failed — try name matching using the location hint.
		// The location often contains the hotel name (e.g. "Hotel Lutetia Paris")
		// which can be used as a fuzzy match query.
		hotel = findBestNameMatch(result.Hotels, opts.Location)
	}

	var rooms []RoomType
	if hotel.Price > 0 {
		rooms = append(rooms, RoomType{
			Name:     "Standard Room",
			Price:    hotel.Price,
			Currency: opts.Currency,
			Provider: providerFromSources(hotel),
		})
	}

	// Add additional provider prices as separate "room" entries.
	for _, src := range hotel.Sources {
		if src.Price > 0 && src.Price != hotel.Price {
			rooms = append(rooms, RoomType{
				Name:     "Standard Room",
				Price:    src.Price,
				Currency: opts.Currency,
				Provider: src.Provider,
			})
		}
	}

	return rooms, hotel.Name
}

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

// providerFromSources returns the provider display name from the first source
// entry, or "Google Hotels" as the default.
func providerFromSources(h *models.HotelResult) string {
	if len(h.Sources) > 0 && h.Sources[0].Provider != "" {
		return displayProvider(h.Sources[0].Provider)
	}
	return "Google Hotels"
}

// displayProvider converts internal provider identifiers (e.g. "google_hotels")
// to human-readable names (e.g. "Google Hotels").
func displayProvider(p string) string {
	switch p {
	case "google_hotels":
		return "Google Hotels"
	default:
		return p
	}
}

// mergeRoomTypes combines Google and Booking room lists. Booking rooms with
// richer data (descriptions, amenities) are preferred when a room name matches.
// Non-matching Booking rooms are appended to the result.
func mergeRoomTypes(google, booking []RoomType) []RoomType {
	if len(booking) == 0 {
		return google
	}
	if len(google) == 0 {
		return booking
	}

	// Index Google rooms by lowercase name for matching.
	type indexedRoom struct {
		index int
		room  RoomType
	}
	googleByName := make(map[string]indexedRoom, len(google))
	for i, r := range google {
		key := strings.ToLower(strings.TrimSpace(r.Name))
		googleByName[key] = indexedRoom{index: i, room: r}
	}

	merged := make([]RoomType, len(google))
	copy(merged, google)

	matched := make(map[string]bool)

	for _, br := range booking {
		bKey := strings.ToLower(strings.TrimSpace(br.Name))
		if gr, found := googleByName[bKey]; found {
			// Merge: enrich Google room with Booking data.
			enriched := gr.room
			if br.Description != "" && enriched.Description == "" {
				enriched.Description = br.Description
			}
			if br.BedType != "" && enriched.BedType == "" {
				enriched.BedType = br.BedType
			}
			if br.SizeM2 > 0 && enriched.SizeM2 == 0 {
				enriched.SizeM2 = br.SizeM2
			}
			if br.MaxGuests > 0 && enriched.MaxGuests == 0 {
				enriched.MaxGuests = br.MaxGuests
			}
			if len(br.Amenities) > 0 {
				enriched.Amenities = mergeStringSlices(enriched.Amenities, br.Amenities)
			}
			if br.NightlyPrice > 0 && enriched.NightlyPrice == 0 {
				enriched.NightlyPrice = br.NightlyPrice
			}
			if br.TotalPrice > 0 && enriched.TotalPrice == 0 {
				enriched.TotalPrice = br.TotalPrice
			}
			if br.TaxesAndFees > 0 && enriched.TaxesAndFees == 0 {
				enriched.TaxesAndFees = br.TaxesAndFees
			}
			if br.TaxesFeesIncluded != nil && enriched.TaxesFeesIncluded == nil {
				enriched.TaxesFeesIncluded = br.TaxesFeesIncluded
			}
			if br.CancellationPolicy != "" && enriched.CancellationPolicy == "" {
				enriched.CancellationPolicy = br.CancellationPolicy
			}
			if br.Refundable != nil && enriched.Refundable == nil {
				enriched.Refundable = br.Refundable
			}
			if br.FreeCancellation != nil && enriched.FreeCancellation == nil {
				enriched.FreeCancellation = br.FreeCancellation
			}
			if br.Board != "" && enriched.Board == "" {
				enriched.Board = br.Board
			}
			if br.BreakfastIncluded != nil && enriched.BreakfastIncluded == nil {
				enriched.BreakfastIncluded = br.BreakfastIncluded
			}
			// Keep Google price if available; add Booking as secondary.
			if enriched.Price == 0 && br.Price > 0 {
				enriched.Price = br.Price
				enriched.Provider = br.Provider
			}
			merged[gr.index] = enriched
			matched[bKey] = true
		}
	}

	// Append unmatched Booking rooms (rooms only on Booking).
	for _, br := range booking {
		bKey := strings.ToLower(strings.TrimSpace(br.Name))
		if !matched[bKey] {
			merged = append(merged, br)
		}
	}

	return merged
}

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
		Name:     name,
		Price:    price,
		Currency: defaultCurrency,
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
