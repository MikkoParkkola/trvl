package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
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

// --- Output schema builders ---

// hotelSearchOutputSchema returns the JSON Schema for HotelSearchResult.
func hotelSearchOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success": schemaBool(),
			"count":   schemaInt(),
			"hotels": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":            schemaString(),
					"hotel_id":        schemaString(),
					"rating":          schemaNum(),
					"review_count":    schemaInt(),
					"stars":           schemaInt(),
					"price":           schemaNum(),
					"currency":        schemaString(),
					"address":         schemaString(),
					"lat":             schemaNum(),
					"lon":             schemaNum(),
					"booking_url":     schemaString(),
					"amenities":       schemaStringArray(),
					"eco_certified":   schemaBool(),
					"savings":         schemaNumDesc("Price savings vs most expensive source"),
					"cheapest_source": schemaStringDesc("Provider with lowest price"),
					"sources": schemaArray(map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"provider":    schemaString(),
							"price":       schemaNum(),
							"currency":    schemaString(),
							"booking_url": schemaString(),
						},
					}),
				},
			}),
			"suggestions": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action":      schemaString(),
					"description": schemaString(),
					"params":      schemaObject(),
				},
			}),
			"provider_statuses": schemaArrayDesc("Per-provider outcome (Google Hotels / Trivago / Booking / Airbnb / Hostelworld / configured providers). Status: 'ok'|'error'|'skipped'|'circuit_broken'.", map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":            schemaString(),
					"name":          schemaString(),
					"status":        schemaString(),
					"results":       schemaInt(),
					"error":         schemaString(),
					"fix_hint":      schemaString(),
					"fix_hint_code": schemaString(),
				},
			}),
			"error": schemaString(),
		},
		"required": []string{"success", "count"},
	}
}

// hotelPricesOutputSchema returns the JSON Schema for HotelPriceResult.
func hotelPricesOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":   schemaBool(),
			"hotel_id":  schemaString(),
			"name":      schemaString(),
			"check_in":  schemaString(),
			"check_out": schemaString(),
			"providers": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"provider": schemaString(),
					"price":    schemaNum(),
					"currency": schemaString(),
				},
				"required": []string{"provider", "price", "currency"},
			}),
			"error": schemaString(),
		},
		"required": []string{"success"},
	}
}

// --- Tool definitions ---

func hotelSearchInputProperties() map[string]Property {
	return map[string]Property{
		"location":          {Type: "string", Description: "Location name or address (e.g., Helsinki, Tokyo, Manhattan New York)"},
		"check_in":          {Type: "string", Description: "Check-in date in YYYY-MM-DD format"},
		"check_out":         {Type: "string", Description: "Check-out date in YYYY-MM-DD format"},
		"guests":            {Type: "integer", Description: "Number of guests (default: 2)"},
		"currency":          {Type: "string", Description: "Currency code (e.g. USD, EUR). Defaults to display_currency preference, then USD"},
		"stars":             {Type: "integer", Description: "Minimum star rating 1-5 (default: no filter)"},
		"sort":              {Type: "string", Description: "Sort order: price, rating, distance, or stars (default: price)"},
		"min_price":         {Type: "number", Description: "Minimum price per night (default: no filter)"},
		"max_price":         {Type: "number", Description: "Maximum price per night (default: no filter)"},
		"min_rating":        {Type: "number", Description: "Minimum guest rating on 0-10 scale, e.g. 8.0 (default: no filter)"},
		"max_distance":      {Type: "number", Description: "Maximum distance from city center in km (default: no filter)"},
		"amenities":         {Type: "string", Description: "Filter by amenities (comma-separated, e.g. pool,wifi,breakfast)"},
		"enrich_amenities":  {Type: "boolean", Description: "Fetch detail pages for top results to get full amenity lists (slower, default: false)"},
		"free_cancellation": {Type: "boolean", Description: "Only show hotels with free cancellation (default: false)"},
		"property_type":     {Type: "string", Description: "Filter by property type: hotel, apartment, hostel, resort, bnb, or villa (default: no filter)"},
		"brand":             {Type: "string", Description: "Filter by hotel brand/chain name (case-insensitive substring match, e.g. hilton, marriott, ibis)"},
		"eco_certified":     {Type: "boolean", Description: "Only show eco-certified hotels with sustainability certifications (default: false)"},
		"min_bedrooms":      {Type: "integer", Description: "Minimum number of bedrooms (Airbnb, default: no filter)"},
		"min_bathrooms":     {Type: "integer", Description: "Minimum number of bathrooms (Airbnb, default: no filter)"},
		"min_beds":          {Type: "integer", Description: "Minimum number of beds (Airbnb, default: no filter)"},
		"room_type":         {Type: "string", Description: "Room type filter: entire_home, private_room, shared_room, hotel_room (Airbnb, default: no filter)"},
		"superhost":         {Type: "boolean", Description: "Only show Superhost listings (Airbnb, default: false)"},
		"instant_book":      {Type: "boolean", Description: "Only show instant-bookable listings (Airbnb, default: false)"},
		"max_distance_m":    {Type: "integer", Description: "Maximum distance from city center in meters (Booking, default: no filter)"},
		"sustainable":       {Type: "boolean", Description: "Only show eco/sustainable properties (Booking, default: false)"},
		"meal_plan":         {Type: "boolean", Description: "Only show properties with breakfast/meals included (Booking, default: false)"},
		"include_sold_out":  {Type: "boolean", Description: "Include sold-out properties in results (Booking, default: false)"},
	}
}

func searchHotelsTool() ToolDef {
	return ToolDef{
		Name:        "search_hotels",
		Title:       "Search Hotels",
		Description: "Search hotels via Google Hotels, Trivago, optionally Booking.com when BOOKING_API_KEY is configured, and any user-configured external providers. Returns real-time pricing, ratings, star levels, and amenities for a given location and dates. Results are merged and deduplicated across providers so the cheapest price wins. IMPORTANT: call get_preferences before your first search in a conversation. If the profile is empty, interview the user first — get_preferences returns instructions. Preferences are applied server-side (star/rating filters, hostel exclusion, neighborhood prioritization) but also check the notes field for soft preferences like 'boutique only' or 'no chains' and apply those yourself.",
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
		Description: "Get prices from multiple booking providers for a specific hotel. Compares prices across providers like Booking.com, Hotels.com, Expedia, etc.",
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

	// Parse amenities filter: comma-separated, trimmed, lowercased.
	var amenities []string
	if raw := argString(args, "amenities"); raw != "" {
		for _, a := range strings.Split(raw, ",") {
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
		CheckIn:          checkIn,
		CheckOut:         checkOut,
		Guests:           guests,
		Currency:         currency,
		Stars:            argInt(args, "stars", 0),
		Sort:             argString(args, "sort"),
		MinPrice:         argFloat(args, "min_price", 0),
		MaxPrice:         argFloat(args, "max_price", 0),
		MinRating:        argFloat(args, "min_rating", 0),
		MaxDistanceKm:    argFloat(args, "max_distance", 0),
		Amenities:        amenities,
		EnrichAmenities:  argBool(args, "enrich_amenities", false),
		FreeCancellation: argBool(args, "free_cancellation", false),
		PropertyType:     argString(args, "property_type"),
		Brand:            argString(args, "brand"),
		EcoCertified:     argBool(args, "eco_certified", false),
		MinBedrooms:      argInt(args, "min_bedrooms", 0),
		MinBathrooms:     argInt(args, "min_bathrooms", 0),
		MinBeds:          argInt(args, "min_beds", 0),
		RoomType:         argString(args, "room_type"),
		Superhost:        argBool(args, "superhost", false),
		InstantBook:      argBool(args, "instant_book", false),
		MaxDistanceM:     argInt(args, "max_distance_m", 0),
		Sustainable:      argBool(args, "sustainable", false),
		MealPlan:         argBool(args, "meal_plan", false),
		IncludeSoldOut:   argBool(args, "include_sold_out", false),
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
	if _, explicit := args["max_price"]; !explicit && opts.MaxPrice == 0 && hints.MaxPrice > 0 {
		opts.MaxPrice = hints.MaxPrice
	}
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

	summary := fmt.Sprintf("Found %d booking providers for hotel %s (%s to %s).",
		len(result.Providers), hotelID, checkIn, checkOut)
	if len(result.Providers) > 0 {
		cheapest := result.Providers[0]
		for _, p := range result.Providers[1:] {
			if p.Price > 0 && p.Price < cheapest.Price {
				cheapest = p
			}
		}
		summary += fmt.Sprintf(" Cheapest: %s %.0f via %s.", cheapest.Currency, cheapest.Price, cheapest.Provider)
	}

	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}

	return content, result, nil
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

	// Find cheapest.
	var cheapest *models.HotelResult
	for i := range result.Hotels {
		if result.Hotels[i].Price > 0 {
			if cheapest == nil || result.Hotels[i].Price < cheapest.Price {
				cheapest = &result.Hotels[i]
			}
		}
	}
	if cheapest != nil {
		summary += fmt.Sprintf(" Cheapest: %s%.0f/night (%s).",
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
	if bestRated != nil && (cheapest == nil || bestRated.Name != cheapest.Name) {
		summary += fmt.Sprintf(" Highest rated: %s (%.1f/10).", bestRated.Name, bestRated.Rating)
	}
	if bookingCount := countHotelsWithProvider(result.Hotels, "booking"); bookingCount > 0 {
		summary += fmt.Sprintf(" Includes %d Booking.com match%s.", bookingCount, pluralSuffix(bookingCount))
	}

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

func hotelReviewsOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":  schemaBool(),
			"hotel_id": schemaString(),
			"name":     schemaString(),
			"summary": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"average_rating":   schemaNum(),
					"total_reviews":    schemaInt(),
					"rating_breakdown": schemaObject(),
				},
			},
			"reviews": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"rating": schemaNum(),
					"text":   schemaString(),
					"author": schemaString(),
					"date":   schemaString(),
				},
			}),
			"count": schemaInt(),
			"error": schemaString(),
		},
		"required": []string{"success"},
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
			"When booking_url is provided (from search_hotels results), also fetches rich room data " +
			"from the Booking.com detail page: room descriptions, bed types, sizes, amenities, " +
			"cancellation/refundability, board, and nightly-vs-total price metadata.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"hotel_name":  {Type: "string", Description: "Hotel name and optional city, e.g. 'Beverly Hills Heights, Tenerife'"},
				"check_in":    {Type: "string", Description: "Check-in date in YYYY-MM-DD format"},
				"check_out":   {Type: "string", Description: "Check-out date in YYYY-MM-DD format"},
				"currency":    {Type: "string", Description: "Currency code (e.g. USD, EUR). Default: USD"},
				"booking_url": {Type: "string", Description: "Booking.com hotel URL from search_hotels results (enables rich room data: descriptions, bed types, sizes, amenities, cancellation, board, and price metadata)"},
			},
			Required: []string{"hotel_name", "check_in", "check_out"},
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

func hotelRoomsOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":   schemaBool(),
			"hotel_id":  schemaString(),
			"name":      schemaString(),
			"check_in":  schemaString(),
			"check_out": schemaString(),
			"rooms":     schemaArray(hotelRoomTypeSchema()),
			"error":     schemaString(),
		},
		"required": []string{"success"},
	}
}

func handleHotelRooms(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	hotelName := argString(args, "hotel_name")
	checkIn := argString(args, "check_in")
	checkOut := argString(args, "check_out")
	currency := argString(args, "currency")
	bookingURL := argString(args, "booking_url")
	if currency == "" {
		currency = "USD"
	}

	if hotelName == "" || checkIn == "" || checkOut == "" {
		return nil, nil, fmt.Errorf("hotel_name, check_in, and check_out are required")
	}

	if err := models.ValidateDateRange(checkIn, checkOut); err != nil {
		return nil, nil, err
	}

	// Resolve hotel name to a Google ID.
	hotel, err := hotels.SearchHotelByName(ctx, hotelName, checkIn, checkOut)
	if err != nil {
		return nil, nil, fmt.Errorf("hotel lookup for %q: %w", hotelName, err)
	}

	if hotel.HotelID == "" {
		return nil, nil, fmt.Errorf("hotel %q found (%s) but has no Google ID", hotelName, hotel.Name)
	}

	// Use the booking URL from the search result if the caller didn't provide one.
	if bookingURL == "" && hotel.BookingURL != "" {
		bookingURL = hotel.BookingURL
	}

	// Fetch room availability with optional Booking.com enrichment.
	// Pass the hotel name as a location hint for the search-page fallback
	// (entity pages now use deferred data loading).
	availability, err := hotels.GetRoomAvailabilityWithOpts(ctx, hotels.RoomSearchOptions{
		HotelID:    hotel.HotelID,
		CheckIn:    checkIn,
		CheckOut:   checkOut,
		Currency:   currency,
		BookingURL: bookingURL,
		Location:   hotelName,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("room availability for %s: %w", hotel.Name, err)
	}

	if availability.Name == "" {
		availability.Name = hotel.Name
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

	return content, availability, nil
}
