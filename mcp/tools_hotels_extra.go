package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// --- Room availability watch ---

func watchRoomAvailabilityTool() ToolDef {
	return ToolDef{
		Name:  "watch_room_availability",
		Title: "Watch Room Availability",
		Description: "Monitor a specific hotel for room availability matching criteria keywords. " +
			"Creates a persistent watch that periodically checks hotel_rooms and alerts when a " +
			"matching room becomes available. Keywords are matched case-insensitively against " +
			"room names and descriptions; all keywords must match. Normalize synonyms before " +
			"setting up the watch (e.g. use 'sea view' not 'ocean view' if the hotel uses that term).",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"hotel_name": {Type: "string", Description: "Hotel name (and optional city), e.g. 'Beverly Hills Heights, Tenerife'"},
				"check_in":   {Type: "string", Description: "Check-in date in YYYY-MM-DD format"},
				"check_out":  {Type: "string", Description: "Check-out date in YYYY-MM-DD format"},
				"keywords":   {Type: "string", Description: "Comma-separated keywords that must all match room name/description (e.g. '2 bedroom,balcony,sea view')"},
				"below":      {Type: "number", Description: "Optional: alert only when matching room price is below this amount"},
				"currency":   {Type: "string", Description: "Currency code (e.g. USD, EUR). Default: USD"},
			},
			Required: []string{"hotel_name", "check_in", "check_out", "keywords"},
		},
		OutputSchema: watchRoomOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Watch Room Availability",
			ReadOnlyHint:   false,
			IdempotentHint: false,
			OpenWorldHint:  false,
		},
	}
}

func watchRoomOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":   schemaBool(),
			"watch_id":  schemaString(),
			"hotel":     schemaString(),
			"check_in":  schemaString(),
			"check_out": schemaString(),
			"keywords":  schemaStringArray(),
			"below":     schemaNum(),
			"currency":  schemaString(),
			"error":     schemaString(),
		},
		"required": []string{"success"},
	}
}

func handleWatchRoomAvailability(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	hotelName := argString(args, "hotel_name")
	checkIn := argString(args, "check_in")
	checkOut := argString(args, "check_out")
	keywordsRaw := argString(args, "keywords")
	below := argFloat(args, "below", 0)
	currency := argString(args, "currency")
	if currency == "" {
		currency = "USD"
	}

	if hotelName == "" || checkIn == "" || checkOut == "" || keywordsRaw == "" {
		return nil, nil, fmt.Errorf("hotel_name, check_in, check_out, and keywords are required")
	}

	if err := models.ValidateDateRange(checkIn, checkOut); err != nil {
		return nil, nil, err
	}

	// Parse keywords.
	var keywords []string
	for _, k := range strings.Split(keywordsRaw, ",") {
		k = strings.TrimSpace(k)
		if k != "" {
			keywords = append(keywords, k)
		}
	}
	if len(keywords) == 0 {
		return nil, nil, fmt.Errorf("at least one non-empty keyword is required")
	}

	store, err := watch.DefaultStore()
	if err != nil {
		return nil, nil, fmt.Errorf("open watch store: %w", err)
	}
	if err := store.Load(); err != nil {
		return nil, nil, fmt.Errorf("load watch store: %w", err)
	}

	w := watch.Watch{
		Type:         "room",
		HotelName:    hotelName,
		Destination:  hotelName,
		DepartDate:   checkIn,
		ReturnDate:   checkOut,
		RoomKeywords: keywords,
		BelowPrice:   below,
		Currency:     currency,
	}

	id, created, err := store.Add(w)
	if err != nil {
		return nil, nil, fmt.Errorf("add room watch: %w", err)
	}

	type watchRoomResponse struct {
		Success  bool     `json:"success"`
		WatchID  string   `json:"watch_id"`
		Hotel    string   `json:"hotel"`
		CheckIn  string   `json:"check_in"`
		CheckOut string   `json:"check_out"`
		Keywords []string `json:"keywords"`
		Below    float64  `json:"below,omitempty"`
		Currency string   `json:"currency"`
		// False when an existing watch for the same target was updated instead.
		// Add is idempotent, so re-watching returns the ORIGINAL id; reporting it
		// as newly created would be a falsehood the agent then repeats to the user.
		Created bool `json:"created"`
	}

	resp := watchRoomResponse{
		Success:  true,
		Created:  created,
		WatchID:  id,
		Hotel:    hotelName,
		CheckIn:  checkIn,
		CheckOut: checkOut,
		Keywords: keywords,
		Below:    below,
		Currency: currency,
	}

	summary := fmt.Sprintf("Room watch %s created for %s (%s to %s). Keywords: %s.",
		id, hotelName, checkIn, checkOut, strings.Join(keywords, ", "))
	if below > 0 {
		summary += fmt.Sprintf(" Alert when below %.0f %s.", below, currency)
	}
	summary += " The daemon will check periodically and notify when a matching room is available."

	content, err := buildAnnotatedContentBlocks(summary, resp)
	if err != nil {
		return nil, nil, err
	}

	return content, resp, nil
}

// --- search_hotel_by_name tool ---

func searchHotelByNameTool() ToolDef {
	return ToolDef{
		Name:  "search_hotel_by_name",
		Title: "Search Hotel by Name",
		Description: "Search for a specific hotel or property by name across all providers " +
			"(Google Hotels, Trivago, and any configured external providers like Booking.com, " +
			"Airbnb, Hostelworld). Unlike search_hotels which returns area-ranked results, this " +
			"tool uses the property name as the search query so providers surface the named " +
			"property, then filters results to only those whose name matches. Ideal for finding " +
			"a specific property when you already know its name (e.g. 'CORU House Prague').",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"name":      {Type: "string", Description: "Property name to search for (e.g. 'CORU House Prague', 'Hotel Kamp Helsinki')"},
				"location":  {Type: "string", Description: "City or area context to anchor the search (e.g. 'Prague', 'Helsinki'). Recommended when the name alone is ambiguous."},
				"check_in":  {Type: "string", Description: "Check-in date in YYYY-MM-DD format"},
				"check_out": {Type: "string", Description: "Check-out date in YYYY-MM-DD format"},
				"currency":  {Type: "string", Description: "Currency code (e.g. USD, EUR). Default: USD"},
			},
			Required: []string{"name", "check_in", "check_out"},
		},
		OutputSchema: hotelSearchOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Search Hotel by Name",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

func handleSearchHotelByName(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	name := argString(args, "name")
	location := models.ResolveLocationName(argString(args, "location"))
	checkIn := argString(args, "check_in")
	checkOut := argString(args, "check_out")
	currency := argString(args, "currency")
	if currency == "" {
		currency = "USD"
	}

	if name == "" || checkIn == "" || checkOut == "" {
		return nil, nil, fmt.Errorf("name, check_in, and check_out are required")
	}

	if err := models.ValidateDateRange(checkIn, checkOut); err != nil {
		return nil, nil, err
	}

	results, err := hotels.SearchHotelsByName(ctx, name, location, checkIn, checkOut, currency)
	if err != nil {
		return nil, nil, err
	}

	result := &models.HotelSearchResult{
		Success: true,
		Count:   len(results),
		Hotels:  results,
	}

	displayLoc := name
	if location != "" {
		displayLoc = name + " in " + location
	}
	summary := hotelSummary(result, displayLoc)

	type byNameResponse struct {
		*models.HotelSearchResult
		SearchName string `json:"search_name"`
	}
	resp := byNameResponse{
		HotelSearchResult: result,
		SearchName:        name,
	}

	content, err := buildAnnotatedContentBlocks(summary, resp)
	if err != nil {
		return nil, nil, err
	}

	return content, resp, nil
}

// --- Suggestion builders ---

func hotelSuggestions(result *models.HotelSearchResult, opts hotels.HotelSearchOptions) []Suggestion {
	var suggestions []Suggestion

	if !result.Success || result.Count == 0 {
		return nil
	}

	// If no star filter, suggest filtering.
	if opts.Stars == 0 {
		suggestions = append(suggestions, Suggestion{
			Action:      "search_hotels",
			Description: "Filter to 4+ star hotels only",
			Params:      map[string]any{"stars": 4},
		})
	}

	// If a great-rated hotel is found, suggest getting detailed pricing.
	for _, h := range result.Hotels {
		if h.Rating >= 9.0 && h.HotelID != "" {
			suggestions = append(suggestions, Suggestion{
				Action:      "hotel_prices",
				Description: fmt.Sprintf("Get detailed pricing for %s (%.1f rating)", h.Name, h.Rating),
				Params:      map[string]any{"hotel_id": h.HotelID, "check_in": opts.CheckIn, "check_out": opts.CheckOut},
			})
			break // Only suggest the top one.
		}
	}

	// If a hotel has many reviews, suggest reading them.
	for _, h := range result.Hotels {
		if h.ReviewCount >= 100 && h.HotelID != "" {
			suggestions = append(suggestions, Suggestion{
				Action:      "hotel_reviews",
				Description: fmt.Sprintf("Read guest reviews for %s (%d reviews)", h.Name, h.ReviewCount),
				Params:      map[string]any{"hotel_id": h.HotelID},
			})
			break
		}
	}

	// If results are expensive, suggest expanding search.
	expensiveCount := 0
	for _, h := range result.Hotels {
		if h.Price > 300 {
			expensiveCount++
		}
	}
	if expensiveCount > result.Count/2 {
		suggestions = append(suggestions, Suggestion{
			Action:      "search_hotels",
			Description: "Try expanding the area or checking different dates for more affordable options",
		})
	}

	return suggestions
}
