package hotels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// bookingRoomOffer represents a single room offer extracted from Booking.com
// JSON-LD structured data (schema.org Offer within a Hotel/LodgingBusiness).
type bookingRoomOffer struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Price       float64  `json:"price"`
	Currency    string   `json:"currency"`
	BedType     string   `json:"bed_type,omitempty"`
	SizeM2      float64  `json:"size_m2,omitempty"`
	MaxGuests   int      `json:"max_guests,omitempty"`
	Amenities   []string `json:"amenities,omitempty"`
}

// FetchBookingRooms fetches a Booking.com hotel detail page and extracts
// rich room data from the JSON-LD structured data. The bookingURL should be
// a full Booking.com hotel URL like:
//
//	https://www.booking.com/hotel/es/beverly-hills-heights.html
//
// Returns room offers with names, descriptions, prices, amenities, and
// physical attributes (size, bed type, max guests) extracted from the
// JSON-LD makesOffer array and room description text.
func FetchBookingRooms(ctx context.Context, bookingURL, checkIn, checkOut, currency string) ([]RoomType, error) {
	if bookingURL == "" {
		return nil, fmt.Errorf("booking URL is required")
	}

	// Append date and currency parameters to the Booking URL so the
	// detail page returns availability-specific room pricing.
	pageURL := buildBookingDetailURL(bookingURL, checkIn, checkOut, currency)

	body, err := fetchBookingPage(ctx, pageURL)
	if err != nil {
		return nil, fmt.Errorf("fetch booking detail page: %w", err)
	}

	offers, err := parseBookingJSONLD(body)
	if err != nil {
		slog.Debug("booking JSON-LD parse failed, trying Apollo cache", "error", err)
		// Fall back to Apollo/SSR cache parsing.
		offers = parseBookingApolloRooms(body)
	}

	if len(offers) == 0 {
		return nil, fmt.Errorf("no room offers found on booking detail page")
	}

	rooms := make([]RoomType, 0, len(offers))
	for _, offer := range offers {
		room := RoomType{
			Name:        offer.Name,
			Price:       offer.Price,
			Currency:    offer.Currency,
			Provider:    "Booking.com",
			MaxGuests:   offer.MaxGuests,
			Description: offer.Description,
			Amenities:   offer.Amenities,
			BedType:     offer.BedType,
			SizeM2:      offer.SizeM2,
		}
		if room.Currency == "" && currency != "" {
			room.Currency = currency
		}
		rooms = append(rooms, room)
	}

	return rooms, nil
}

// buildBookingDetailURL appends check-in/check-out and currency query
// parameters to a Booking.com hotel URL. This makes the detail page
// return date-specific room availability and pricing.
func buildBookingDetailURL(baseURL, checkIn, checkOut, currency string) string {
	// Strip any existing query string for clean parameter injection.
	if idx := strings.Index(baseURL, "?"); idx >= 0 {
		baseURL = baseURL[:idx]
	}

	params := []string{"lang=en-us"}
	if checkIn != "" {
		params = append(params, "checkin="+checkIn)
	}
	if checkOut != "" {
		params = append(params, "checkout="+checkOut)
	}
	if currency != "" {
		params = append(params, "selected_currency="+strings.ToUpper(currency))
	}

	return baseURL + "?" + strings.Join(params, "&")
}

// fetchBookingPage performs an HTTP GET against a Booking.com detail URL
// and returns the response body as a string.
func fetchBookingPage(ctx context.Context, pageURL string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("booking detail page returned status %d", resp.StatusCode)
	}

	// Limit response size to 10 MB.
	limited := io.LimitReader(resp.Body, 10*1024*1024)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("read booking detail page: %w", err)
	}

	return string(data), nil
}

// jsonLDPattern matches <script type="application/ld+json"> blocks.
var jsonLDPattern = regexp.MustCompile(`<script[^>]*type="application/ld\+json"[^>]*>([\s\S]*?)</script>`)

// parseBookingJSONLD extracts room offers from JSON-LD structured data on
// a Booking.com hotel detail page. The JSON-LD typically contains a Hotel
// or LodgingBusiness entity with a makesOffer array of room Offers.
func parseBookingJSONLD(page string) ([]bookingRoomOffer, error) {
	matches := jsonLDPattern.FindAllStringSubmatch(page, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no JSON-LD blocks found")
	}

	var allOffers []bookingRoomOffer

	for _, m := range matches {
		if len(m) < 2 {
			continue
		}

		raw := m[1]

		// Try as a single object first.
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err == nil {
			offers := extractOffersFromLDObject(obj)
			allOffers = append(allOffers, offers...)
			continue
		}

		// Try as an array (some pages wrap in an array).
		var arr []map[string]any
		if err := json.Unmarshal([]byte(raw), &arr); err == nil {
			for _, item := range arr {
				offers := extractOffersFromLDObject(item)
				allOffers = append(allOffers, offers...)
			}
		}
	}

	if len(allOffers) == 0 {
		return nil, fmt.Errorf("no room offers in JSON-LD")
	}

	return deduplicateOffers(allOffers), nil
}

// extractOffersFromLDObject extracts room offers from a JSON-LD object.
// It handles both top-level Hotel objects and @graph arrays.
func extractOffersFromLDObject(obj map[string]any) []bookingRoomOffer {
	var offers []bookingRoomOffer

	// Check if this object directly has makesOffer.
	if isHotelType(obj) {
		offers = append(offers, extractMakesOffer(obj)...)
	}

	// Check @graph array for Hotel entities.
	if graph, ok := obj["@graph"].([]any); ok {
		for _, item := range graph {
			if node, ok := item.(map[string]any); ok && isHotelType(node) {
				offers = append(offers, extractMakesOffer(node)...)
			}
		}
	}

	return offers
}

// isHotelType checks if a JSON-LD object is a Hotel, LodgingBusiness,
// or related accommodation type.
func isHotelType(obj map[string]any) bool {
	t, _ := obj["@type"].(string)
	switch strings.ToLower(t) {
	case "hotel", "lodgingbusiness", "motel", "hostel", "resort",
		"bedandbreakfast", "campingpitch", "apartment":
		return true
	}
	return false
}

// extractMakesOffer parses the makesOffer array from a Hotel JSON-LD object.
func extractMakesOffer(hotel map[string]any) []bookingRoomOffer {
	makesOffer, ok := hotel["makesOffer"]
	if !ok {
		return nil
	}

	var offerList []any
	switch v := makesOffer.(type) {
	case []any:
		offerList = v
	case map[string]any:
		offerList = []any{v}
	default:
		return nil
	}

	var offers []bookingRoomOffer
	for _, item := range offerList {
		offer, ok := item.(map[string]any)
		if !ok {
			continue
		}

		room := parseOfferObject(offer)
		if room.Name != "" {
			offers = append(offers, room)
		}
	}

	return offers
}

// parseOfferObject converts a single JSON-LD Offer object into a
// bookingRoomOffer, extracting name, description, price, and amenities.
func parseOfferObject(offer map[string]any) bookingRoomOffer {
	room := bookingRoomOffer{}

	room.Name, _ = offer["name"].(string)
	room.Description, _ = offer["description"].(string)

	// Extract price from priceSpecification or direct price field.
	if ps, ok := offer["priceSpecification"].(map[string]any); ok {
		room.Price = ldFloat(ps, "price")
		room.Currency, _ = ps["priceCurrency"].(string)
		if room.Currency == "" {
			room.Currency, _ = ps["currency"].(string)
		}
	}
	if room.Price == 0 {
		room.Price = ldFloat(offer, "price")
	}
	if room.Currency == "" {
		room.Currency, _ = offer["priceCurrency"].(string)
	}

	// Extract bed type and room details from description text.
	if room.Description != "" {
		room.BedType = extractBedType(room.Description)
		room.SizeM2 = extractSizeM2(room.Description)
		room.MaxGuests = extractMaxGuests(room.Description)
		room.Amenities = extractRoomAmenities(room.Description)
	}

	// Also extract from the offer name (e.g., "Deluxe Double Room with Sea View").
	if room.BedType == "" {
		room.BedType = extractBedType(room.Name)
	}
	nameAmenities := extractRoomAmenities(room.Name)
	room.Amenities = mergeStringSlices(room.Amenities, nameAmenities)

	return room
}

// ldFloat extracts a float64 from a JSON-LD object, handling both
// numeric and string representations of prices.
func ldFloat(obj map[string]any, key string) float64 {
	v, ok := obj[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0
		}
		return f
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

// --- Description text extractors ---

// bedTypePatterns maps keywords in room names/descriptions to standardized
// bed type strings.
var bedTypeKeywords = []struct {
	keyword string
	bedType string
}{
	// Explicit bed descriptions (most specific first).
	{"king bed", "1 king bed"},
	{"queen bed", "1 queen bed"},
	{"double bed", "1 double bed"},
	{"twin bed", "2 twin beds"},
	{"single bed", "1 single bed"},
	{"bunk bed", "bunk beds"},
	{"sofa bed", "sofa bed"},
	{"king-size", "1 king bed"},
	{"queen-size", "1 queen bed"},
	{"2 single", "2 single beds"},
	{"2 twin", "2 twin beds"},
	{"1 double", "1 double bed"},
	{"1 king", "1 king bed"},
	{"1 queen", "1 queen bed"},
	// Room type names that imply bed type.
	{"double room", "1 double bed"},
	{"twin room", "2 twin beds"},
	{"single room", "1 single bed"},
	{"king suite", "1 king bed"},
	{"king room", "1 king bed"},
	{"queen suite", "1 queen bed"},
	{"queen room", "1 queen bed"},
}

// extractBedType identifies bed type from a room name or description string.
func extractBedType(text string) string {
	lower := strings.ToLower(text)
	for _, bt := range bedTypeKeywords {
		if strings.Contains(lower, bt.keyword) {
			return bt.bedType
		}
	}
	return ""
}

// sizePattern matches room size in square meters, e.g. "35 m²", "28m2", "40 sqm".
var sizePattern = regexp.MustCompile(`(\d+)\s*(?:m²|m2|sqm|sq\.?\s*m)`)

// extractSizeM2 extracts room size in square meters from a description.
func extractSizeM2(text string) float64 {
	m := sizePattern.FindStringSubmatch(text)
	if len(m) < 2 {
		return 0
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	return f
}

// guestPattern matches max guest counts, e.g. "max 4 guests", "sleeps 6",
// "for 2 adults", "accommodates 3".
var guestPattern = regexp.MustCompile(`(?i)(?:max(?:imum)?|sleeps|for|accommodates|up to)\s+(\d+)\s*(?:guests?|adults?|people|persons?)`)

// extractMaxGuests extracts the maximum guest count from a description.
func extractMaxGuests(text string) int {
	m := guestPattern.FindStringSubmatch(text)
	if len(m) < 2 {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 || n > 20 {
		return 0
	}
	return n
}

// roomAmenityKeywords are amenities that can be detected from room
// names and descriptions (not property-level amenities).
var roomAmenityKeywords = []string{
	"balcony", "terrace", "sea view", "ocean view", "mountain view",
	"garden view", "pool view", "city view", "lake view", "river view",
	"minibar", "kitchenette", "kitchen", "air conditioning",
	"bathtub", "jacuzzi", "hot tub", "sauna", "private pool",
	"fireplace", "washing machine", "dishwasher", "oven",
	"coffee machine", "espresso", "microwave", "refrigerator",
	"soundproofing", "blackout curtains", "safe", "desk",
	"private bathroom", "shared bathroom", "en-suite",
	"free wifi", "flat-screen tv", "satellite tv",
	"breakfast included", "all inclusive",
	"parking", "rooftop", "patio", "courtyard",
}

// extractRoomAmenities detects room-level amenities from a text string.
func extractRoomAmenities(text string) []string {
	if text == "" {
		return nil
	}
	lower := strings.ToLower(text)
	var amenities []string
	for _, kw := range roomAmenityKeywords {
		if strings.Contains(lower, kw) {
			amenities = append(amenities, titleCase(kw))
		}
	}
	return amenities
}

// titleCase capitalizes the first letter of each word in s.
func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// mergeStringSlices combines two string slices, deduplicating by lowercase.
func mergeStringSlices(a, b []string) []string {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[strings.ToLower(s)] = true
	}
	merged := make([]string, len(a))
	copy(merged, a)
	for _, s := range b {
		if !seen[strings.ToLower(s)] {
			seen[strings.ToLower(s)] = true
			merged = append(merged, s)
		}
	}
	return merged
}

// deduplicateOffers removes duplicate room offers by name (case-insensitive).
// When duplicates exist, the one with more detail (description, price) wins.
func deduplicateOffers(offers []bookingRoomOffer) []bookingRoomOffer {
	seen := make(map[string]int, len(offers)) // name -> index in result
	var result []bookingRoomOffer

	for _, offer := range offers {
		key := strings.ToLower(strings.TrimSpace(offer.Name))
		if key == "" {
			continue
		}
		if idx, exists := seen[key]; exists {
			// Keep the version with more data.
			existing := result[idx]
			if offer.Description != "" && existing.Description == "" {
				result[idx] = offer
			} else if offer.Price > 0 && existing.Price == 0 {
				result[idx] = offer
			}
		} else {
			seen[key] = len(result)
			result = append(result, offer)
		}
	}
	return result
}

// --- Apollo/SSR cache fallback parsing ---

// parseBookingApolloRooms attempts to extract room data from Booking.com's
// Apollo client state or server-side rendered room blocks. This is a fallback
// when JSON-LD parsing fails (some Booking pages use different markup).
func parseBookingApolloRooms(page string) []bookingRoomOffer {
	// Look for room name patterns in the Apollo cache or b_blocks data.
	// Booking.com SSR pages have room names in patterns like:
	// "room_name":"Deluxe Double Room with Sea View"
	return extractRoomNamesFromSSR(page)
}

// roomNameSSRPattern matches room name fields in Booking.com's SSR/Apollo JSON.
var roomNameSSRPattern = regexp.MustCompile(`"room_name"\s*:\s*"([^"]{5,100})"`)

// roomPriceSSRPattern matches price fields near room data in SSR.
var roomPriceSSRPattern = regexp.MustCompile(`"price_breakdown"[^}]*"gross_amount"[^}]*"value"\s*:\s*([\d.]+)`)

// extractRoomNamesFromSSR extracts room names from Booking.com SSR HTML.
func extractRoomNamesFromSSR(page string) []bookingRoomOffer {
	nameMatches := roomNameSSRPattern.FindAllStringSubmatch(page, 50)
	if len(nameMatches) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var offers []bookingRoomOffer

	for _, m := range nameMatches {
		if len(m) < 2 {
			continue
		}
		name := m[1]
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true

		offer := bookingRoomOffer{
			Name:      name,
			BedType:   extractBedType(name),
			Amenities: extractRoomAmenities(name),
		}
		offers = append(offers, offer)
	}

	// Try to match prices to rooms (best-effort).
	priceMatches := roomPriceSSRPattern.FindAllStringSubmatch(page, 50)
	for i, pm := range priceMatches {
		if i >= len(offers) {
			break
		}
		if len(pm) >= 2 {
			if p, err := strconv.ParseFloat(pm[1], 64); err == nil && p > 0 {
				offers[i].Price = p
			}
		}
	}

	return offers
}
