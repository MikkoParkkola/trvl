package hotels

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// RoomType represents a specific room category at a hotel.
type RoomType struct {
	Name               string                      `json:"name"`
	Price              float64                     `json:"price"`
	NightlyPrice       float64                     `json:"nightly_price,omitempty"`
	TotalPrice         float64                     `json:"total_price,omitempty"`
	TaxesAndFees       float64                     `json:"taxes_and_fees,omitempty"`
	TaxesFeesIncluded  *bool                       `json:"taxes_fees_included,omitempty"`
	Currency           string                      `json:"currency"`
	Provider           string                      `json:"provider,omitempty"`
	ProviderURL        string                      `json:"provider_url,omitempty"`
	RateID             string                      `json:"rate_id,omitempty"`
	RatePlanName       string                      `json:"rate_plan_name,omitempty"`
	MatchConfidence    string                      `json:"match_confidence,omitempty"`
	MaxGuests          int                         `json:"max_guests,omitempty"`
	BedType            string                      `json:"bed_type,omitempty"`
	SizeM2             float64                     `json:"size_m2,omitempty"`
	Description        string                      `json:"description,omitempty"`
	Amenities          []string                    `json:"amenities,omitempty"`
	CancellationPolicy string                      `json:"cancellation_policy,omitempty"`
	Refundable         *bool                       `json:"refundable,omitempty"`
	FreeCancellation   *bool                       `json:"free_cancellation,omitempty"`
	Board              string                      `json:"board,omitempty"`
	BreakfastIncluded  *bool                       `json:"breakfast_included,omitempty"`
	InventoryOptions   []models.RoomInventoryQuote `json:"inventory_options,omitempty"`
}

// RoomAvailability is the response for a room-type search.
type RoomAvailability struct {
	Success  bool       `json:"success"`
	HotelID  string     `json:"hotel_id"`
	Name     string     `json:"name,omitempty"`
	CheckIn  string     `json:"check_in"`
	CheckOut string     `json:"check_out"`
	Rooms    []RoomType `json:"rooms"`
	Notice   string     `json:"notice,omitempty"`
	Error    string     `json:"error,omitempty"`
}

// RoomSearchOptions configures a room availability search.
type RoomSearchOptions struct {
	HotelID      string // Google Hotels entity ID
	CheckIn      string // YYYY-MM-DD
	CheckOut     string // YYYY-MM-DD
	Currency     string // e.g. "USD", "EUR"
	Guests       int    // searched guest count; defaults to 2
	ChildrenAges []int  // requested child ages
	Rooms        int    // requested room count
	BookingURL   string // optional Booking.com hotel URL for rich room data
	Location     string // optional city/area hint for search-based fallback
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

	// Google's entity-page inline room data is dead, but the yY52ce
	// batchexecute RPC still returns the live booking-partner price matrix for
	// the hotel. Pull it and convert each partner price into a property-level
	// room entry. This surfaces the full OTA matrix — including any Booking.com
	// partner URL, which the downstream exact-room Booking.com fetch needs to
	// produce a booking-ready offer.
	if len(rooms) == 0 {
		rpcRooms, rpcName := tryBatchExecutePrices(ctx, opts)
		if len(rpcRooms) > 0 {
			rooms = rpcRooms
			if hotelName == "" {
				hotelName = rpcName
			}
		}
	}

	// Fallback: search for the hotel on the search page by location extracted
	// from the hotel ID's geocoded area. The search page still has inline
	// AF_initDataCallback data.
	if len(rooms) == 0 {
		rooms, hotelName = trySearchPageFallback(ctx, opts)
	}
	notice := ""
	// SerpAPI is the richest room source we have (named rooms, verified
	// tax-inclusive prices, refundability) but every lookup spends metered
	// quota. Consult it only when the free Google paths could not produce a
	// verified room-level price -- i.e. nothing at all, or only sub-verified
	// lead-ins / "similar" matches. This upgrades a weak Google result (e.g. a
	// nightly-only "similar" price that downgrades to caution) into a
	// booking-ready offer, without burning a search when Google already
	// returned a verified room.
	if len(rooms) == 0 || !hasVerifiedRoom(rooms) {
		serpRooms, serpName, serpNotice := trySerpAPIRoomFallback(ctx, opts)
		if len(serpRooms) > 0 {
			if len(rooms) == 0 {
				rooms = serpRooms
			} else {
				// Keep Google's lead-ins, add SerpAPI's verified rooms.
				rooms = mergeRoomTypes(rooms, serpRooms)
			}
			notice = serpNotice
			if hotelName == "" {
				hotelName = serpName
			}
		}
	}

	// Merge Booking.com rooms with Google rooms if both are available.
	if len(bookingRooms) > 0 {
		rooms = mergeRoomTypes(rooms, bookingRooms)
	}

	// Lead with the rooms that carry a real, bookable price. Sources are merged
	// in fetch order (Google entity, partner matrix, search fallback, SerpAPI,
	// Booking) so a property-level lead-in can otherwise sit above a verified
	// room-level rate. Order by bookability (exact > similar > property-level
	// lead-in), then cheapest-first within a tier, so the most actionable price
	// surfaces first and unpriced lead-ins sink to the bottom.
	sortRoomsByBookability(rooms)

	return &RoomAvailability{
		Success:  true,
		HotelID:  opts.HotelID,
		Name:     hotelName,
		CheckIn:  opts.CheckIn,
		CheckOut: opts.CheckOut,
		Rooms:    rooms,
		Notice:   notice,
	}, nil
}

// sortRoomsByBookability orders rooms so the most actionable, real prices lead.
// Primary key: bookability rank (exact room-level price first, then a real
// "similar" bookable rate, then property-level-only lead-ins). Secondary key:
// effective price ascending so the cheapest real offer in each tier surfaces
// first; rooms with no usable price sink to the bottom of their tier. The sort
// is stable, preserving provider fetch order among otherwise-equal rooms.
func sortRoomsByBookability(rooms []RoomType) {
	sort.SliceStable(rooms, func(i, j int) bool {
		ri, rj := roomBookabilityRank(rooms[i]), roomBookabilityRank(rooms[j])
		if ri != rj {
			return ri < rj
		}
		pi, pj := roomEffectivePrice(rooms[i]), roomEffectivePrice(rooms[j])
		// A usable price (>0) always leads a missing one within the same tier.
		if (pi > 0) != (pj > 0) {
			return pi > 0
		}
		return pi < pj
	})
}

// roomBookabilityRank maps a room's match confidence to a sort rank where a
// lower value leads. An explicit room-level match is the most bookable; a
// "similar" rate is a real bookable price without a nameable room identity;
// everything else (property-level lead-ins, unset) trails.
func roomBookabilityRank(r RoomType) int {
	switch strings.TrimSpace(r.MatchConfidence) {
	case models.RoomInventoryMatchExact:
		return 0
	case models.RoomInventoryMatchSimilar:
		return 1
	default:
		return 2
	}
}

// roomEffectivePrice returns the room's most representative bookable price,
// preferring the headline Price, then nightly, then total. Returns 0 when the
// room carries no usable price.
func roomEffectivePrice(r RoomType) float64 {
	if r.Price > 0 {
		return r.Price
	}
	if r.NightlyPrice > 0 {
		return r.NightlyPrice
	}
	return r.TotalPrice
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

// tryBatchExecutePrices fetches Google's live booking-partner price matrix via
// the yY52ce batchexecute RPC (the entity page's inline data is dead since
// mid-2026) and converts each partner price into a room entry. The matrix is a
// dated, occupancy-specific quote, so the cheapest partner price is promoted to
// room-level "similar" (a real bookable rate without a nameable room identity);
// the remaining prices keep their basis-derived confidence and expose the
// partner URLs (notably Booking.com) so the downstream exact-room fetch can run.
func tryBatchExecutePrices(ctx context.Context, opts RoomSearchOptions) ([]RoomType, string) {
	res, err := GetHotelPricesWithOpts(ctx, HotelPriceOpts{
		HotelID:      opts.HotelID,
		CheckIn:      opts.CheckIn,
		CheckOut:     opts.CheckOut,
		Currency:     opts.Currency,
		Guests:       opts.Guests,
		ChildrenAges: opts.ChildrenAges,
		Location:     opts.Location,
	})
	if err != nil || res == nil || len(res.Providers) == 0 {
		return nil, ""
	}

	rooms := make([]RoomType, 0, len(res.Providers))
	// The yY52ce partner matrix is a dated, occupancy-specific quote, so the
	// cheapest partner price is a real bookable rate for the stay, not a
	// property lead-in. Promote it to room-level "similar" (no nameable room
	// identity) so it reaches the booking-ready gate — mirroring the Agoda fix
	// (PR #289) and the search-page fallback. Only the cheapest is promoted:
	// the rest keep their basis-derived confidence (an explicit room basis
	// still earns "exact"; everything else stays property-level), because
	// promoting the whole identity-less ladder trips the multi-provider-mixed
	// completeness flag and re-blocks the offer.
	cheapestIdx, cheapestPrice := -1, 0.0
	for i, p := range res.Providers {
		price := p.Price
		if price <= 0 {
			price = p.NightlyPrice
		}
		if price <= 0 {
			continue
		}
		if cheapestIdx == -1 || price < cheapestPrice {
			cheapestIdx, cheapestPrice = i, price
		}
	}
	for i, p := range res.Providers {
		price := p.Price
		if price <= 0 {
			price = p.NightlyPrice
		}
		if price <= 0 {
			continue
		}
		currency := p.Currency
		if currency == "" {
			currency = opts.Currency
		}
		match := roomMatchFromPriceBasis(p.PriceBasis)
		nightly := p.NightlyPrice
		if i == cheapestIdx && match == models.RoomInventoryMatchPropertyLevelOnly {
			match = models.RoomInventoryMatchSimilar
			if nightly == 0 && p.TotalPrice == 0 {
				nightly = price
			}
		}
		rooms = append(rooms, RoomType{
			Name:            "Standard Room",
			Price:           price,
			NightlyPrice:    nightly,
			TotalPrice:      p.TotalPrice,
			Currency:        currency,
			Provider:        p.Provider,
			ProviderURL:     p.ProviderURL,
			MatchConfidence: match,
		})
	}
	return rooms, res.Name
}

// roomMatchFromPriceBasis maps a provider PriceBasis to a room match
// confidence. Only an explicit room-total / tax-inclusive / room-nightly basis
// earns an exact match; every other basis is treated as property_level_only so
// the honesty model never promotes a property lead-in to booking-ready.
func roomMatchFromPriceBasis(basis string) string {
	switch basis {
	case models.PriceBasisRoomTotal, models.PriceBasisTaxInclusiveTotal, models.PriceBasisRoomNightly:
		return models.RoomInventoryMatchExact
	default:
		return models.RoomInventoryMatchPropertyLevelOnly
	}
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

	searchOpts := roomFallbackSearchOptions(opts)

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
	if hotel == nil {
		return nil, ""
	}

	var rooms []RoomType
	// Exactly one bookable price is promoted to room-level: the dated,
	// occupancy-specific search price (roomFallbackSearchOptions carries
	// CheckIn/CheckOut/Guests) is the real nightly rate for the requested
	// stay, not a property lead-in. Tagged "similar" (not "exact") because the
	// search response gives a rate without a nameable room identity — mirroring
	// the Agoda fix (PR #289). Only one entry is promoted: emitting the whole
	// identity-less price ladder as room-level would trip the multi-provider-
	// mixed completeness flag and re-block the offer. The headline price is
	// preferred when present; otherwise the cheapest partner price (which is
	// where the real search-page price usually lives) is promoted instead.
	roomLevelEmitted := false
	if hotel.Price > 0 {
		currency := hotel.Currency
		if currency == "" {
			currency = opts.Currency
		}
		rooms = append(rooms, RoomType{
			Name:            "Standard Room",
			Price:           hotel.Price,
			NightlyPrice:    hotel.Price,
			Currency:        currency,
			Provider:        providerFromSources(hotel),
			ProviderURL:     hotel.BookingURL,
			MatchConfidence: models.RoomInventoryMatchSimilar,
		})
		roomLevelEmitted = true
	}

	// Find the cheapest priced partner source to promote when there is no
	// headline price, so the real Google search-page rate still reaches the
	// booking-ready gate.
	cheapestIdx := -1
	if !roomLevelEmitted {
		for i, src := range hotel.Sources {
			if src.Price > 0 && (cheapestIdx == -1 || src.Price < hotel.Sources[cheapestIdx].Price) {
				cheapestIdx = i
			}
		}
	}

	// Add provider prices as separate "room" entries. The promoted entry is
	// room-level "similar"; the rest stay property-level (their value is the
	// partner URL, and promoting more than one identity-less price trips the
	// completeness re-block).
	for i, src := range hotel.Sources {
		if src.Price > 0 && src.Price != hotel.Price {
			currency := src.Currency
			if currency == "" {
				currency = opts.Currency
			}
			match := models.RoomInventoryMatchPropertyLevelOnly
			nightly := 0.0
			if i == cheapestIdx {
				match = models.RoomInventoryMatchSimilar
				nightly = src.Price
			}
			rooms = append(rooms, RoomType{
				Name:            "Standard Room",
				Price:           src.Price,
				NightlyPrice:    nightly,
				Currency:        currency,
				Provider:        src.Provider,
				ProviderURL:     src.BookingURL,
				MatchConfidence: match,
			})
		}
	}

	return rooms, hotel.Name
}

func roomFallbackSearchOptions(opts RoomSearchOptions) HotelSearchOptions {
	guests := opts.Guests
	if guests <= 0 {
		guests = 2
	}
	return HotelSearchOptions{
		CheckIn:      opts.CheckIn,
		CheckOut:     opts.CheckOut,
		Guests:       guests,
		ChildrenAges: append([]int(nil), opts.ChildrenAges...),
		Rooms:        opts.Rooms,
		Currency:     opts.Currency,
		MaxPages:     1, // Single page — just need to find the target hotel.
	}
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
			enriched.InventoryOptions = appendRoomInventoryOption(enriched.InventoryOptions, roomInventoryQuote(enriched))
			enriched.InventoryOptions = appendRoomInventoryOption(enriched.InventoryOptions, roomInventoryQuote(br))
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
				enriched.ProviderURL = br.ProviderURL
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

// hasVerifiedRoom reports whether any room already carries a verified,
// tax-inclusive room-level price -- the bar SerpAPI would otherwise be
// consulted to reach. Used to spend SerpAPI quota only when it can upgrade an
// otherwise sub-verified result.
func hasVerifiedRoom(rooms []RoomType) bool {
	for _, r := range rooms {
		if roomInventoryQuote(r).PriceConfidence == models.PriceConfidenceVerified {
			return true
		}
	}
	return false
}

func roomInventoryQuote(room RoomType) models.RoomInventoryQuote {
	provider := strings.TrimSpace(room.Provider)
	if provider == "" {
		provider = "Google Hotels"
	}
	confidence := strings.TrimSpace(room.MatchConfidence)
	if confidence == "" {
		confidence = models.RoomInventoryMatchExact
	}
	priceBasis := models.PriceBasisRoomNightly
	priceConfidence := models.PriceConfidenceRoomLevel
	if confidence == models.RoomInventoryMatchPropertyLevelOnly {
		priceBasis = models.PriceBasisLeadIn
		priceConfidence = models.PriceConfidenceUnverified
	} else if room.TotalPrice > 0 {
		priceBasis = models.PriceBasisRoomTotal
		if room.TaxesFeesIncluded != nil && *room.TaxesFeesIncluded {
			priceBasis = models.PriceBasisTaxInclusiveTotal
			priceConfidence = models.PriceConfidenceVerified
		}
	}
	nightly := room.NightlyPrice
	total := room.TotalPrice
	if nightly == 0 && total == 0 {
		nightly = room.Price
	}
	return models.RoomInventoryQuote{
		Provider:           provider,
		ProviderRoomName:   room.Name,
		ProviderRateName:   room.RatePlanName,
		ProviderURL:        room.ProviderURL,
		RateID:             room.RateID,
		MatchConfidence:    confidence,
		NightlyPrice:       nightly,
		TotalPrice:         total,
		TaxesAndFees:       room.TaxesAndFees,
		TaxesFeesIncluded:  room.TaxesFeesIncluded,
		Currency:           room.Currency,
		Refundable:         room.Refundable,
		FreeCancellation:   room.FreeCancellation,
		CancellationPolicy: room.CancellationPolicy,
		Board:              room.Board,
		BreakfastIncluded:  room.BreakfastIncluded,
		PriceBasis:         priceBasis,
		PriceConfidence:    priceConfidence,
	}
}

func appendRoomInventoryOption(options []models.RoomInventoryQuote, quote models.RoomInventoryQuote) []models.RoomInventoryQuote {
	if quote.Provider == "" && quote.ProviderRoomName == "" && quote.NightlyPrice == 0 && quote.TotalPrice == 0 {
		return options
	}
	key := strings.ToLower(strings.Join([]string{
		quote.Provider,
		quote.ProviderRoomName,
		quote.ProviderRateName,
		quote.Currency,
		strings.TrimRight(strings.TrimRight(strconv.FormatFloat(quote.NightlyPrice, 'f', 2, 64), "0"), "."),
		strings.TrimRight(strings.TrimRight(strconv.FormatFloat(quote.TotalPrice, 'f', 2, 64), "0"), "."),
		quote.CancellationPolicy,
	}, "|"))
	for _, existing := range options {
		existingKey := strings.ToLower(strings.Join([]string{
			existing.Provider,
			existing.ProviderRoomName,
			existing.ProviderRateName,
			existing.Currency,
			strings.TrimRight(strings.TrimRight(strconv.FormatFloat(existing.NightlyPrice, 'f', 2, 64), "0"), "."),
			strings.TrimRight(strings.TrimRight(strconv.FormatFloat(existing.TotalPrice, 'f', 2, 64), "0"), "."),
			existing.CancellationPolicy,
		}, "|"))
		if existingKey == key {
			return options
		}
	}
	return append(options, quote)
}
