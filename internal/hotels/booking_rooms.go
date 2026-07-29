package hotels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	cookiesconsent "github.com/MikkoParkkola/trvl/internal/cookies"
	"github.com/MikkoParkkola/trvl/internal/logredact"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// bookingRoomOffer represents a single room offer extracted from Booking.com
// JSON-LD structured data (schema.org Offer within a Hotel/LodgingBusiness).
type bookingRoomOffer struct {
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	URL                string   `json:"url,omitempty"`
	Price              float64  `json:"price"`
	NightlyPrice       float64  `json:"nightly_price,omitempty"`
	TotalPrice         float64  `json:"total_price,omitempty"`
	TaxesAndFees       float64  `json:"taxes_and_fees,omitempty"`
	TaxesFeesIncluded  *bool    `json:"taxes_fees_included,omitempty"`
	Currency           string   `json:"currency"`
	BedType            string   `json:"bed_type,omitempty"`
	SizeM2             float64  `json:"size_m2,omitempty"`
	MaxGuests          int      `json:"max_guests,omitempty"`
	Amenities          []string `json:"amenities,omitempty"`
	CancellationPolicy string   `json:"cancellation_policy,omitempty"`
	Refundable         *bool    `json:"refundable,omitempty"`
	FreeCancellation   *bool    `json:"free_cancellation,omitempty"`
	Board              string   `json:"board,omitempty"`
	BreakfastIncluded  *bool    `json:"breakfast_included,omitempty"`
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
var FetchBookingRooms = defaultFetchBookingRooms

func defaultFetchBookingRooms(ctx context.Context, bookingURL, checkIn, checkOut, currency string) ([]RoomType, error) {
	if bookingURL == "" {
		return nil, fmt.Errorf("booking URL is required")
	}
	// The URL arrives from outside: an MCP booking_url argument, or a link
	// carried on a search result. Pin it to Booking.com before the first
	// request, not merely before the cookies. hotel_rooms is advertised as a
	// read-only tool, so without this any client holding a read token could
	// aim trvl's HTTP client at localhost, a private network, or a cloud
	// metadata endpoint and use the response as an oracle.
	if !bookingHostAllowed(bookingURL, bookingCookieSite) {
		return nil, ErrNotBookingURL
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
		slog.Debug("booking JSON-LD parse failed, trying Apollo cache", "error", logredact.Err(err))
		// Fall back to Apollo/SSR cache parsing.
		offers = parseBookingApolloRooms(body)
	}

	if len(offers) == 0 {
		return nil, fmt.Errorf("no room offers found on booking detail page")
	}

	rooms := make([]RoomType, 0, len(offers))
	for _, offer := range offers {
		room := RoomType{
			Name:               offer.Name,
			Price:              offer.Price,
			NightlyPrice:       offer.NightlyPrice,
			TotalPrice:         offer.TotalPrice,
			TaxesAndFees:       offer.TaxesAndFees,
			TaxesFeesIncluded:  offer.TaxesFeesIncluded,
			Currency:           offer.Currency,
			Provider:           "Booking.com",
			ProviderURL:        firstNonEmptyBookingString(offer.URL, pageURL),
			MatchConfidence:    models.RoomInventoryMatchExact,
			MaxGuests:          offer.MaxGuests,
			Description:        offer.Description,
			Amenities:          offer.Amenities,
			BedType:            offer.BedType,
			SizeM2:             offer.SizeM2,
			CancellationPolicy: offer.CancellationPolicy,
			Refundable:         offer.Refundable,
			FreeCancellation:   offer.FreeCancellation,
			Board:              offer.Board,
			BreakfastIncluded:  offer.BreakfastIncluded,
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

// bookingCookieSite is the site the Booking.com session cookies belong to.
// It is both the domain they are read for and the only domain they may be
// sent to; see the origin check in fetchBookingPage.
const bookingCookieSite = "booking.com"

// bookingHostAllowed is the destination pin. It is a var only so the parser
// tests can point the lookup at a local fixture server; production code must
// never reassign it, and the guard tests exercise the default.
var bookingHostAllowed = cookiesconsent.IsHTTPSOnSite

// ErrNotBookingURL is returned when the room lookup is handed a URL that is not
// an https Booking.com address. It is a distinct error rather than an empty
// room list so a caller can tell "refused" from "found nothing".
var ErrNotBookingURL = errors.New("room lookup refused: not an https booking.com URL")

// browserCookies is overridable in tests; defaults to providers.BrowserCookiesForURL.
var browserCookies = defaultBrowserCookies

func defaultBrowserCookies(url string) []*http.Cookie {
	return nil
}

// fetchBookingPage performs an HTTP GET against a Booking.com URL
// and returns the response body as a string. Uses the batchexec client
// with Chrome TLS fingerprint impersonation.
//
// When Booking.com returns a WAF challenge (202), the function tries to
// read the user's Booking.com session cookies from their installed browser.
// This bypasses Booking's WAF without requiring a headless browser. If no
// browser cookie is found, the returned error gives the user the recovery step.
func fetchBookingPage(ctx context.Context, pageURL string) (string, error) {
	client := DefaultClient()
	status, body, err := client.Get(ctx, pageURL)
	if err != nil {
		return "", err
	}
	if status == 200 {
		return string(body), nil
	}

	// Booking.com returns 202/403/503 for WAF challenge pages. Try reading the
	// bkng cookie from the user's browser via kooky.
	//
	// The read itself is gated inside providers (permittedAfterRead), which is
	// where the guarantee lives. The HeaderIfPermittedForURL wraps below are the
	// second layer: they sit on the last line before transmission, so a decline
	// arriving even later than the read still stops the credential. Round 11 of
	// review found this path sending live Booking.com credentials with neither.
	//
	// They also carry the origin check. pageURL is derived from a caller-supplied
	// booking_url (an MCP argument, or a link carried on a search result), and
	// buildBookingDetailURL concatenates rather than validates, so the host here
	// is not trustworthy. Without the check, pointing booking_url at any host
	// that answers 202/403/503 would hand it the user's live Booking.com session.
	if status == 202 || status == 403 || status == 503 {
		cookies := browserCookies("https://www." + bookingCookieSite)
		var cookieStr string
		for _, c := range cookies {
			if c.Name == "bkng" && c.Value != "" {
				cookieStr = "bkng=" + c.Value
				break
			}
		}
		if cookieStr != "" {
			slog.Debug("booking.com challenge, retrying with browser cookie", "status", status)
			status, body, err = client.GetWithCookie(ctx, pageURL, cookiesconsent.HeaderIfPermittedForURL(cookieStr, pageURL, bookingCookieSite))
			if err == nil && status == 200 {
				return string(body), nil
			}
		}

		// No valid browser cookie found. Try with a generic header approach.
		cookieStr = ""
		for _, c := range cookies {
			if c.Name == "bkng" || c.Name == "session" || strings.HasPrefix(c.Name, "bkng_") {
				if cookieStr != "" {
					cookieStr += "; "
				}
				cookieStr += c.Name + "=" + c.Value
			}
		}
		if cookieStr != "" {
			slog.Debug("booking.com challenge, retrying with all browser cookies", "status", status)
			status, body, err = client.GetWithCookie(ctx, pageURL, cookiesconsent.HeaderIfPermittedForURL(cookieStr, pageURL, bookingCookieSite))
			if err == nil && status == 200 {
				return string(body), nil
			}
		}

		return "", fmt.Errorf("booking.com WAF challenge (status %d). "+
			"To fix: open booking.com in your browser once, then retry. "+
			"trvl auto-detects your browser cookies via kooky", status)
	}

	return "", fmt.Errorf("booking detail page returned status %d", status)
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
	room.URL, _ = offer["url"].(string)
	var priceSpec map[string]any

	// Extract price from priceSpecification or direct price field.
	if ps, ok := offer["priceSpecification"].(map[string]any); ok {
		priceSpec = ps
		room.Price = ldFloat(ps, "price")
		room.NightlyPrice = firstLDFloat(ps, "nightlyPrice", "nightly_price", "pricePerNight", "price_per_night", "unitPrice", "unit_price")
		room.TotalPrice = firstLDFloat(ps, "totalPrice", "total_price", "total", "grossPrice", "gross_price")
		room.TaxesAndFees = firstLDFloat(ps, "taxesAndFees", "taxes_and_fees", "taxes", "fees")
		room.TaxesFeesIncluded = firstLDBoolPtr(ps, "taxesFeesIncluded", "taxes_fees_included", "taxIncluded", "tax_included")
		room.Currency, _ = ps["priceCurrency"].(string)
		if room.Currency == "" {
			room.Currency, _ = ps["currency"].(string)
		}
	}
	if room.Price == 0 {
		room.Price = ldFloat(offer, "price")
	}
	if room.NightlyPrice == 0 {
		room.NightlyPrice = firstLDFloat(offer, "nightlyPrice", "nightly_price", "pricePerNight", "price_per_night", "unitPrice", "unit_price")
	}
	if room.TotalPrice == 0 {
		room.TotalPrice = firstLDFloat(offer, "totalPrice", "total_price", "total", "grossPrice", "gross_price")
	}
	if room.TaxesAndFees == 0 {
		room.TaxesAndFees = firstLDFloat(offer, "taxesAndFees", "taxes_and_fees", "taxes", "fees")
	}
	if room.TaxesFeesIncluded == nil {
		room.TaxesFeesIncluded = firstLDBoolPtr(offer, "taxesFeesIncluded", "taxes_fees_included", "taxIncluded", "tax_included")
	}
	if room.Currency == "" {
		room.Currency, _ = offer["priceCurrency"].(string)
	}
	if room.Price == 0 {
		if room.NightlyPrice > 0 {
			room.Price = room.NightlyPrice
		} else if room.TotalPrice > 0 {
			room.Price = room.TotalPrice
		}
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

	detailText := roomOfferDecisionText(room, offer, priceSpec)
	room.CancellationPolicy, room.Refundable, room.FreeCancellation = extractCancellationMetadata(detailText)
	room.Board, room.BreakfastIncluded = extractBoardMetadata(detailText)
	if room.TaxesFeesIncluded == nil {
		room.TaxesFeesIncluded = extractTaxesFeesIncluded(detailText)
	}

	return room
}

func roomOfferDecisionText(room bookingRoomOffer, offer, priceSpec map[string]any) string {
	parts := []string{room.Name, room.Description}
	for _, key := range []string{"cancellationPolicy", "cancellation_policy", "availability", "mealPlan", "meal_plan", "board"} {
		if v, ok := offer[key].(string); ok {
			parts = append(parts, v)
		}
	}
	if priceSpec != nil {
		for _, key := range []string{"description", "name", "valueAddedTaxIncluded"} {
			if v, ok := priceSpec[key].(string); ok {
				parts = append(parts, v)
			}
		}
	}
	return strings.Join(parts, " ")
}

func firstNonEmptyBookingString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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

func firstLDFloat(obj map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if f := ldFloat(obj, key); f > 0 {
			return f
		}
	}
	return 0
}

func firstLDBoolPtr(obj map[string]any, keys ...string) *bool {
	for _, key := range keys {
		if v, ok := ldBoolPtr(obj, key); ok {
			return v
		}
	}
	return nil
}

func ldBoolPtr(obj map[string]any, key string) (*bool, bool) {
	v, ok := obj[key]
	if !ok {
		return nil, false
	}
	switch b := v.(type) {
	case bool:
		return &b, true
	case string:
		switch strings.ToLower(strings.TrimSpace(b)) {
		case "true", "yes", "included", "1":
			val := true
			return &val, true
		case "false", "no", "excluded", "not included", "0":
			val := false
			return &val, true
		}
	}
	return nil, false
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
