package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/pricefeed"
	"github.com/MikkoParkkola/trvl/internal/pricesignal"
	"github.com/MikkoParkkola/trvl/internal/profile"
)

var searchHotelsFunc = hotels.SearchHotels

type hotelSearchRequest struct {
	Location string
	CheckIn  string
	CheckOut string
	Options  hotels.HotelSearchOptions
	Prefs    *preferences.Preferences
}

type hotelSearchResponse struct {
	*models.HotelSearchResult
	Suggestions []Suggestion `json:"suggestions,omitempty"`
}

// --- Tool definitions ---

func searchHotelsTool() ToolDef {
	return ToolDef{
		Name:        "search_hotels",
		Title:       "Search Hotels",
		Description: "Discover hotel candidates via Google Hotels, Trivago, Booking.com when available, and user-configured external providers. Prices from this broad search are lead-in search prices, not checkout-final quotes; for traveller-facing accommodation decisions use search_accommodations, which verifies room-level offers against the requested criteria. For final trip-cost ranking, verify shortlisted hotels with search_hotels_with_details or hotel_rooms and rank on room-level total_price or tax-inclusive provider totals when present. IMPORTANT: call get_preferences before your first search in a conversation. If the profile is empty, interview the user first - get_preferences returns instructions. Preferences are applied server-side (star/rating filters, hostel exclusion, neighborhood prioritization) but also check the notes field for soft preferences like 'boutique only' or 'no chains' and apply those yourself.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: hotelSearchInputProperties(),
			Required:   []string{"location", "check_in", "check_out"},
		},
		OutputSchema: hotelSearchOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Search Hotels",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

func hotelPricesTool() ToolDef {
	return ToolDef{
		Name:        "hotel_prices",
		Title:       "Hotel Prices Comparison",
		Description: "Get exposed booking-provider prices for a specific Google Hotels property. Use as a provider comparison, not a rate guarantee; for booking decisions prefer room-level totals from hotel_rooms or search_hotels_with_details when available. EXTRA SIGNALS (use them when present): `price_position` shows where this price sits in the property's own history (only trust the verdict when `confident` is true); `booking_readiness` is a composite verdict (ready, caution, unverified) — anything below `ready` means verify before booking, and `booking_readiness_reasons` explains why. Note: from this endpoint readiness usually stays at caution because refundability is not known here; call hotel_rooms for a property where 'ready' can be reached.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"hotel_id":  {Type: "string", Description: "Google Hotels property ID (from search_hotels results)"},
				"check_in":  {Type: "string", Description: "Check-in date in YYYY-MM-DD format"},
				"check_out": {Type: "string", Description: "Check-out date in YYYY-MM-DD format"},
				"currency":  {Type: "string", Description: "Currency code (e.g. USD, EUR). Default: USD"},
			},
			Required: []string{"hotel_id", "check_in", "check_out"},
		},
		OutputSchema: hotelPricesOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Hotel Prices Comparison",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

// --- Tool handlers ---

func buildHotelSearchRequest(args map[string]any) (hotelSearchRequest, error) {
	location := models.ResolveLocationName(argString(args, "location"))
	checkIn := argString(args, "check_in")
	checkOut := argString(args, "check_out")

	if location == "" || checkIn == "" || checkOut == "" {
		return hotelSearchRequest{}, fmt.Errorf("location, check_in, and check_out are required")
	}

	// Validate dates.
	if err := models.ValidateDateRange(checkIn, checkOut); err != nil {
		return hotelSearchRequest{}, err
	}

	// Parse amenities filter: comma-separated or JSON array, trimmed, lowercased.
	var amenities []string
	if raw := argStringSlice(args, "amenities"); len(raw) > 0 {
		for _, a := range raw {
			a = strings.ToLower(strings.TrimSpace(a))
			if a != "" {
				amenities = append(amenities, a)
			}
		}
	}

	// Load preferences early — used for guest count default and filter overrides.
	prefs, _ := preferences.Load()

	currency := strings.ToUpper(argString(args, "currency"))
	if currency == "" && prefs != nil {
		currency = strings.ToUpper(prefs.DisplayCurrency)
	}

	// Determine guest count: use the caller's explicit value, or fall back to
	// DefaultCompanions + 1 (companions + the user), or the tool default (2).
	guests := argInt(args, "guests", 0)
	if guests == 0 {
		// Caller did not provide guests explicitly.
		if prefs != nil && prefs.DefaultCompanions > 0 {
			guests = prefs.DefaultCompanions + 1
		} else {
			guests = 2 // tool default
		}
	}

	opts := hotels.HotelSearchOptions{
		CheckIn:            checkIn,
		CheckOut:           checkOut,
		Guests:             guests,
		ChildrenAges:       argIntSlice(args, "children_ages"),
		Rooms:              argInt(args, "rooms", 0),
		Currency:           currency,
		Stars:              argInt(args, "stars", 0),
		Sort:               argString(args, "sort"),
		MinPrice:           argFloat(args, "min_price", 0),
		MaxPrice:           argFloat(args, "max_price", 0),
		MinRating:          argFloat(args, "min_rating", 0),
		MaxDistanceKm:      argFloat(args, "max_distance", 0),
		Amenities:          amenities,
		EnrichAmenities:    argBool(args, "enrich_amenities", false),
		EnrichRooms:        argBool(args, "enrich_rooms", true),
		FreeCancellation:   argBool(args, "free_cancellation", false),
		RefundableRequired: argBool(args, "refundable_required", false),
		PropertyType:       argString(args, "property_type"),
		Brand:              argString(args, "brand"),
		EcoCertified:       argBool(args, "eco_certified", false),
		MinBedrooms:        argInt(args, "min_bedrooms", 0),
		MinBathrooms:       argInt(args, "min_bathrooms", 0),
		MinBeds:            argInt(args, "min_beds", 0),
		RoomType:           argString(args, "room_type"),
		Superhost:          argBool(args, "superhost", false),
		InstantBook:        argBool(args, "instant_book", false),
		MaxDistanceM:       argInt(args, "max_distance_m", 0),
		Sustainable:        argBool(args, "sustainable", false),
		MealPlan:           argBool(args, "meal_plan", false),
		IncludeSoldOut:     argBool(args, "include_sold_out", false),
		MustHaveKitchen:    argBool(args, "must_have_kitchen", false),
		MustHaveWifi:       argBool(args, "must_have_wifi", false),
		MustHaveWorkspace:  argBool(args, "must_have_workspace", false),
	}

	// Apply user preferences when MCP caller hasn't set these explicitly.
	if prefs != nil {
		if opts.Stars == 0 && prefs.MinHotelStars > 0 {
			opts.Stars = prefs.MinHotelStars
		}
		if opts.MinRating == 0 && prefs.MinHotelRating > 0 {
			opts.MinRating = prefs.MinHotelRating
		}
		if opts.MaxPrice == 0 && prefs.BudgetPerNightMax > 0 {
			opts.MaxPrice = prefs.BudgetPerNightMax
		}
		if opts.MinPrice == 0 && prefs.BudgetPerNightMin > 0 {
			opts.MinPrice = prefs.BudgetPerNightMin
		}
	}

	// Apply profile hints as defaults — lower priority than preferences and
	// explicit caller args. Only fill in fields still at their zero values.
	prof, _ := profile.Load()
	hints := profile.HotelHints(prof, location)
	if _, explicit := args["stars"]; !explicit && opts.Stars == 0 && hints.MinStars > 0 {
		opts.Stars = hints.MinStars
	}
	// NOTE: hints.MaxPrice (profile average nightly rate × budget flex) is
	// deliberately NOT applied here. Seeding a hard price ceiling from a derived
	// profile average silently truncates results the caller never asked to
	// exclude — the exact bug fixed for flights in #453 (search_flights was
	// dropping cheap options below a profile-derived cap). A genuine user
	// preference (prefs.BudgetPerNightMax, applied above) still constrains the
	// search; a profile *hint* must not. Do not re-add a hints.MaxPrice seed.
	if _, explicit := args["property_type"]; !explicit && opts.PropertyType == "" && hints.PropertyType != "" {
		opts.PropertyType = hints.PropertyType
	}
	if _, explicit := args["guests"]; !explicit && opts.Guests == 2 && hints.Guests > 0 {
		// Only override the generic fallback (2), not an explicit value or
		// a preference-derived one.
		if prefs == nil || prefs.DefaultCompanions == 0 {
			opts.Guests = hints.Guests
		}
	}

	return hotelSearchRequest{
		Location: location,
		CheckIn:  checkIn,
		CheckOut: checkOut,
		Options:  opts,
		Prefs:    prefs,
	}, nil
}

func runHotelSearch(ctx context.Context, req hotelSearchRequest) (*models.HotelSearchResult, error) {
	result, err := searchHotelsFunc(ctx, req.Location, req.Options)
	if err != nil {
		return nil, err
	}

	// Post-filter with preference-based filters (dormitories, en-suite, districts).
	if req.Prefs != nil {
		result.Hotels = preferences.FilterHotels(result.Hotels, req.Location, req.Prefs)
		result.Count = len(result.Hotels)
	}

	// Shared CLI+MCP post-search policy: when the party includes children, never
	// surface adults-only properties. Same applicator the CLI uses, so the two
	// surfaces cannot drift. hidden count is ignored here — the structured Count
	// already reflects the exclusion.
	hotels.ApplySharedHotelPolicy(result, len(req.Options.ChildrenAges) > 0)

	return result, nil
}

func handleSearchHotels(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	req, err := buildHotelSearchRequest(args)
	if err != nil {
		return nil, nil, err
	}

	result, err := runHotelSearch(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	// Build suggestions for progressive disclosure.
	suggestions := hotelSuggestions(result, req.Options)

	// The orchestrating LLM receives the full hotel list in structuredContent JSON
	// and can select and rank picks without any server-side sampling round-trip.
	// (curateHotelsViaSampling was removed: sampling is not wired in production.)

	resp := hotelSearchResponse{
		HotelSearchResult: result,
		Suggestions:       suggestions,
	}

	summary := hotelSummary(result, req.Location)

	content, err := buildAnnotatedContentBlocks(summary, resp)
	if err != nil {
		return nil, nil, err
	}

	return content, resp, nil
}

func handleHotelPrices(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	hotelID := argString(args, "hotel_id")
	checkIn := argString(args, "check_in")
	checkOut := argString(args, "check_out")
	currency := argString(args, "currency")
	if currency == "" {
		currency = "USD"
	}

	if hotelID == "" || checkIn == "" || checkOut == "" {
		return nil, nil, fmt.Errorf("hotel_id, check_in, and check_out are required")
	}

	// Validate dates.
	if err := models.ValidateDateRange(checkIn, checkOut); err != nil {
		return nil, nil, err
	}

	result, err := hotels.GetHotelPrices(ctx, hotelID, checkIn, checkOut, currency)
	if err != nil {
		return nil, nil, err
	}

	// MIK-6229/6232: log price history + compute price-position signal and
	// booking-readiness verdict. Best-effort: errors are silently discarded so
	// a store failure never breaks the tool response.
	pricePos, readiness := hotelPriceSignals(hotelID, checkIn, result)

	// Build enriched response with price_position and booking_readiness.
	type enrichedHotelPriceResult struct {
		*models.HotelPriceResult
		PricePosition           *pricesignal.Position `json:"price_position,omitempty"`
		BookingReadiness        string                `json:"booking_readiness,omitempty"`
		BookingReadinessReasons []string              `json:"booking_readiness_reasons,omitempty"`
		// The ceiling is what stops an agent reading a structurally capped
		// verdict as a finding about the hotel. This endpoint carries no
		// cancellation terms, so it can never report ready; without saying so, a
		// caution here is indistinguishable from a caution earned by thin data.
		BookingReadinessCeiling        string   `json:"booking_readiness_ceiling,omitempty"`
		BookingReadinessCeilingReasons []string `json:"booking_readiness_ceiling_reasons,omitempty"`
	}
	enriched := enrichedHotelPriceResult{HotelPriceResult: result, PricePosition: pricePos}
	if readiness != nil {
		enriched.BookingReadiness = string(readiness.Readiness)
		enriched.BookingReadinessReasons = readiness.Reasons
		if readiness.Capped() {
			enriched.BookingReadinessCeiling = string(readiness.Ceiling)
			enriched.BookingReadinessCeilingReasons = readiness.CeilingReasons
		}
	}

	summary := fmt.Sprintf("Found %d booking providers for hotel %s (%s to %s).",
		len(result.Providers), hotelID, checkIn, checkOut)
	if cheapest := pricefeed.CheapestProvider(result.Providers); cheapest.Price > 0 {
		summary += fmt.Sprintf(" Cheapest: %s %.0f via %s.", cheapest.Currency, cheapest.Price, cheapest.Provider)
	}

	content, err := buildAnnotatedContentBlocks(summary, enriched)
	if err != nil {
		return nil, nil, err
	}

	return content, enriched, nil
}

// --- Summary builders ---

func hotelSummary(result *models.HotelSearchResult, location string) string {
	if !result.Success || result.Count == 0 {
		if result.Error != "" {
			return fmt.Sprintf("Hotel search in %s failed: %s", location, result.Error)
		}
		return fmt.Sprintf("No hotels found in %s.", location)
	}

	summary := fmt.Sprintf("Found %d hotels in %s.", result.Count, location)

	// Cheapest headline goes through the currency-cohort guard: a nominally-small
	// unconverted foreign price must not crown the "Lowest lead-in" line ahead of
	// the true comparable minimum.
	cheapest := pricefeed.CheapestHotel(result.Hotels)
	hasCheapest := cheapest.Price > 0

	if note := result.Completeness.IncompleteNote(); note != "" {
		summary = note + " " + summary
	}

	if hasCheapest {
		summary += fmt.Sprintf(" Lowest lead-in: %s%.0f/night (%s).",
			cheapest.Currency, cheapest.Price, cheapest.Name)
	}

	// Find highest rated.
	var bestRated *models.HotelResult
	for i := range result.Hotels {
		if result.Hotels[i].Rating > 0 {
			if bestRated == nil || result.Hotels[i].Rating > bestRated.Rating {
				bestRated = &result.Hotels[i]
			}
		}
	}
	if bestRated != nil && (!hasCheapest || bestRated.Name != cheapest.Name) {
		summary += fmt.Sprintf(" Highest rated: %s (%.1f/10).", bestRated.Name, bestRated.Rating)
	}
	if bookingCount := countHotelsWithProvider(result.Hotels, "booking"); bookingCount > 0 {
		summary += fmt.Sprintf(" Includes %d Booking.com match%s.", bookingCount, pluralSuffix(bookingCount))
	}
	summary += " Treat search prices as lead-in until verified with room/detail totals."

	return summary
}

func countHotelsWithProvider(hotels []models.HotelResult, provider string) int {
	count := 0
	for _, hotel := range hotels {
		for _, source := range hotel.Sources {
			if source.Provider == provider {
				count++
				break
			}
		}
	}
	return count
}

// --- Hotel reviews ---

func hotelReviewsTool() ToolDef {
	return ToolDef{
		Name:        "hotel_reviews",
		Title:       "Hotel Reviews",
		Description: "Get guest reviews for a specific hotel from Google Hotels. Returns review text, ratings, authors, and dates, plus aggregate statistics.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"hotel_id": {Type: "string", Description: "Google Hotels property ID (from search_hotels results)"},
				"limit":    {Type: "integer", Description: "Maximum number of reviews to return (default: 10)"},
				"sort":     {Type: "string", Description: "Sort order: newest, highest, lowest (default: newest)"},
			},
			Required: []string{"hotel_id"},
		},
		OutputSchema: hotelReviewsOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Hotel Reviews",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

func handleHotelReviews(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	hotelID := argString(args, "hotel_id")
	if hotelID == "" {
		return nil, nil, fmt.Errorf("hotel_id is required")
	}

	opts := hotels.ReviewOptions{
		Limit: argInt(args, "limit", 10),
		Sort:  argString(args, "sort"),
	}
	if opts.Sort == "" {
		opts.Sort = "newest"
	}

	result, err := hotels.GetHotelReviews(ctx, hotelID, opts)
	if err != nil {
		return nil, nil, err
	}

	summary := fmt.Sprintf("Found %d reviews for hotel %s.", result.Count, hotelID)
	if result.Name != "" {
		summary = fmt.Sprintf("Found %d reviews for %s.", result.Count, result.Name)
	}
	if result.Summary.AverageRating > 0 {
		summary += fmt.Sprintf(" Average rating: %.1f/5 (%d total).",
			result.Summary.AverageRating, result.Summary.TotalReviews)
	}

	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}

	return content, result, nil
}

// --- Hotel rooms ---

func hotelRoomsTool() ToolDef {
	return ToolDef{
		Name:  "hotel_rooms",
		Title: "Hotel Room Availability",
		Description: "Search room types and per-night pricing for a specific hotel by name. " +
			"Resolves the hotel via Google Hotels entity search, then fetches room-level availability. " +
			"Use this to verify shortlisted hotels before making a final price recommendation. " +
			"When booking_url is provided (from search_hotels results), also fetches rich room data " +
			"from the Booking.com detail page: room descriptions, bed types, sizes, amenities, " +
			"cancellation/refundability, board, and nightly-vs-total price metadata.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"hotel_id":    {Type: "string", Description: "Google Hotels property ID from search_hotels results. When provided, used directly — skips the hotel-name lookup and avoids resolving to the wrong property."},
				"hotel_name":  {Type: "string", Description: "Hotel name and optional city, e.g. 'Beverly Hills Heights, Tenerife'. Required only when hotel_id is absent; also used as a location hint."},
				"check_in":    {Type: "string", Description: "Check-in date in YYYY-MM-DD format"},
				"check_out":   {Type: "string", Description: "Check-out date in YYYY-MM-DD format"},
				"currency":    {Type: "string", Description: "Currency code (e.g. USD, EUR). Default: USD"},
				"booking_url": {Type: "string", Description: "Booking.com hotel URL from search_hotels results (enables rich room data: descriptions, bed types, sizes, amenities, cancellation, board, and price metadata)"},
			},
			Required: []string{"check_in", "check_out"},
		},
		OutputSchema: hotelRoomsOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Hotel Room Availability",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

func handleHotelRooms(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	hotelID := argString(args, "hotel_id")
	hotelName := argString(args, "hotel_name")
	checkIn := argString(args, "check_in")
	checkOut := argString(args, "check_out")
	currency := argString(args, "currency")
	bookingURL := argString(args, "booking_url")
	if currency == "" {
		currency = "USD"
	}

	if (hotelID == "" && hotelName == "") || checkIn == "" || checkOut == "" {
		return nil, nil, fmt.Errorf("hotel_id or hotel_name, plus check_in and check_out, are required")
	}

	if err := models.ValidateDateRange(checkIn, checkOut); err != nil {
		return nil, nil, err
	}

	// Resolved property identity. When the caller supplies a hotel_id (from
	// search_hotels), use it directly — re-running the fuzzy name search can
	// resolve to a different property and fetch room data for the wrong hotel.
	resolvedID := hotelID
	resolvedName := hotelName
	if resolvedID == "" {
		hotel, err := searchHotelByNameFunc(ctx, hotelName, checkIn, checkOut, currency)
		if err != nil {
			return nil, nil, fmt.Errorf("hotel lookup for %q: %w", hotelName, err)
		}
		if hotel.HotelID == "" {
			return nil, nil, fmt.Errorf("hotel %q found (%s) but has no Google ID", hotelName, hotel.Name)
		}
		resolvedID = hotel.HotelID
		resolvedName = hotel.Name
		// Use the booking URL from the search result if the caller didn't provide one.
		if bookingURL == "" && hotel.BookingURL != "" {
			bookingURL = hotel.BookingURL
		}
	}

	// Fetch room availability with optional Booking.com enrichment.
	// Pass the hotel name as a location hint for the search-page fallback
	// (entity pages now use deferred data loading).
	availability, err := getRoomAvailabilityWithOptsFunc(ctx, hotels.RoomSearchOptions{
		HotelID:    resolvedID,
		CheckIn:    checkIn,
		CheckOut:   checkOut,
		Currency:   currency,
		BookingURL: bookingURL,
		Location:   hotelName,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("room availability for %s: %w", resolvedName, err)
	}

	if availability.Name == "" {
		availability.Name = resolvedName
	}

	summary := fmt.Sprintf("Found %d room types at %s (%s to %s).",
		len(availability.Rooms), availability.Name, checkIn, checkOut)
	if len(availability.Rooms) == 0 {
		summary = fmt.Sprintf("No individual room types found for %s. Google Hotels may not expose room-level data for this property.", availability.Name)
	} else {
		// Count rooms with rich Booking.com data.
		bookingRooms := 0
		for _, r := range availability.Rooms {
			if r.Provider == "Booking.com" || r.Description != "" {
				bookingRooms++
			}
		}

		// Find cheapest room.
		cheapest := availability.Rooms[0]
		for _, r := range availability.Rooms[1:] {
			if r.Price > 0 && (cheapest.Price == 0 || r.Price < cheapest.Price) {
				cheapest = r
			}
		}
		if cheapest.Price > 0 {
			summary += fmt.Sprintf(" Cheapest: %s %.0f/night (%s).", cheapest.Currency, cheapest.Price, cheapest.Name)
		}
		if bookingRooms > 0 {
			summary += fmt.Sprintf(" %d rooms include rich Booking.com data (descriptions, amenities, bed types).", bookingRooms)
		}
	}

	content, err := buildAnnotatedContentBlocks(summary, availability)
	if err != nil {
		return nil, nil, err
	}

	// MIK-6232: attach a booking-readiness verdict. Rooms carry refundability +
	// a classifiable link, so "ready" is reachable here (unlike hotel_prices).
	readiness := roomsBookingReadiness(availability)
	enriched := struct {
		*hotels.RoomAvailability
		BookingReadiness        string   `json:"booking_readiness,omitempty"`
		BookingReadinessReasons []string `json:"booking_readiness_reasons,omitempty"`
	}{
		RoomAvailability:        availability,
		BookingReadiness:        string(readiness.Readiness),
		BookingReadinessReasons: readiness.Reasons,
	}

	return content, enriched, nil
}
