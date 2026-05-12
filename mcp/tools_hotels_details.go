package mcp

import (
	"context"
	"fmt"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

var fetchHotelAmenitiesFunc = hotels.FetchHotelAmenities
var getRoomAvailabilityWithOptsFunc = hotels.GetRoomAvailabilityWithOpts

type hotelWithDetails struct {
	models.HotelResult
	RoomTypes    []hotels.RoomType `json:"room_types,omitempty"`
	DetailErrors []string          `json:"detail_errors,omitempty"`
}

type hotelDetailsSearchResponse struct {
	Success          bool                    `json:"success"`
	Count            int                     `json:"count"`
	TotalAvailable   int                     `json:"total_available,omitempty"`
	Hotels           []hotelWithDetails      `json:"hotels"`
	ProviderStatuses []models.ProviderStatus `json:"provider_statuses,omitempty"`
	Suggestions      []Suggestion            `json:"suggestions,omitempty"`
	Error            string                  `json:"error,omitempty"`
}

func searchHotelsWithDetailsTool() ToolDef {
	props := hotelSearchInputProperties()
	props["max_hotels"] = Property{Type: "integer", Description: "Number of top hotels to enrich with room and amenity details (default: 3, max: 5)"}
	props["include_rooms"] = Property{Type: "boolean", Description: "Fetch room-level availability and rates for each top hotel (default: true)"}
	props["include_amenities"] = Property{Type: "boolean", Description: "Fetch full property amenity details for each top hotel (default: true)"}

	return ToolDef{
		Name:        "search_hotels_with_details",
		Title:       "Search Hotels With Details",
		Description: "Search hotels, then enrich the top matches with room-level availability and property amenities in one call. Use this when comparing a short list of hotels by rooms, rates, Booking.com detail data, and amenities instead of making separate search_hotels and hotel_rooms calls. Detail enrichment is best-effort per hotel: partial failures are reported in detail_errors without failing the full search.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: props,
			Required:   []string{"location", "check_in", "check_out"},
		},
		OutputSchema: hotelDetailsSearchOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Search Hotels With Details",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

func hotelDetailsSearchOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":         schemaBool(),
			"count":           schemaInt(),
			"total_available": schemaInt(),
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
					"booking_url":     schemaString(),
					"amenities":       schemaStringArray(),
					"eco_certified":   schemaBool(),
					"savings":         schemaNumDesc("Price savings vs most expensive source"),
					"cheapest_source": schemaStringDesc("Provider with lowest price"),
					"room_types": schemaArray(map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"name":        schemaString(),
							"price":       schemaNum(),
							"currency":    schemaString(),
							"provider":    schemaString(),
							"max_guests":  schemaInt(),
							"bed_type":    schemaString(),
							"size_m2":     schemaNum(),
							"description": schemaString(),
							"amenities":   schemaStringArray(),
						},
					}),
					"detail_errors": schemaStringArray(),
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
			"provider_statuses": schemaArrayDesc("Per-provider outcome (Google Hotels / Trivago / Booking / Airbnb / Hostelworld / configured providers).", map[string]interface{}{
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

func handleSearchHotelsWithDetails(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	req, err := buildHotelSearchRequest(args)
	if err != nil {
		return nil, nil, err
	}

	result, err := runHotelSearch(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	includeAmenities := argBool(args, "include_amenities", true)
	includeRooms := argBool(args, "include_rooms", true)
	limit := hotelDetailsLimit(argInt(args, "max_hotels", 3), len(result.Hotels))
	hotelsWithDetails := make([]hotelWithDetails, 0, limit)
	for i := 0; i < limit; i++ {
		hotel := result.Hotels[i]
		detailed := hotelWithDetails{HotelResult: hotel}
		if hotel.HotelID == "" {
			detailed.DetailErrors = append(detailed.DetailErrors, "missing hotel_id; cannot fetch hotel details")
			hotelsWithDetails = append(hotelsWithDetails, detailed)
			continue
		}
		if includeAmenities {
			amenities, err := fetchHotelAmenitiesFunc(ctx, hotel.HotelID)
			if err != nil {
				detailed.DetailErrors = append(detailed.DetailErrors, fmt.Sprintf("amenities: %v", err))
			} else if len(amenities) > 0 {
				detailed.Amenities = amenities
			}
		}
		if includeRooms {
			availability, err := getRoomAvailabilityWithOptsFunc(ctx, hotels.RoomSearchOptions{
				HotelID:    hotel.HotelID,
				CheckIn:    req.CheckIn,
				CheckOut:   req.CheckOut,
				Currency:   req.Options.Currency,
				BookingURL: hotel.BookingURL,
				Location:   req.Location,
			})
			if err != nil {
				detailed.DetailErrors = append(detailed.DetailErrors, fmt.Sprintf("rooms: %v", err))
			} else if availability != nil {
				detailed.RoomTypes = availability.Rooms
			}
		}
		hotelsWithDetails = append(hotelsWithDetails, detailed)
	}

	resp := hotelDetailsSearchResponse{
		Success:          result.Success,
		Count:            len(hotelsWithDetails),
		TotalAvailable:   result.Count,
		Hotels:           hotelsWithDetails,
		ProviderStatuses: result.ProviderStatuses,
		Suggestions:      hotelSuggestions(result, req.Options),
		Error:            result.Error,
	}

	summary := hotelDetailsSummary(resp, req.Location)
	content, err := buildAnnotatedContentBlocks(summary, resp)
	if err != nil {
		return nil, nil, err
	}

	return content, resp, nil
}

func hotelDetailsLimit(requested, available int) int {
	if available <= 0 {
		return 0
	}
	if requested <= 0 {
		requested = 3
	}
	if requested > 5 {
		requested = 5
	}
	if requested > available {
		return available
	}
	return requested
}

func hotelDetailsSummary(result hotelDetailsSearchResponse, location string) string {
	if !result.Success || result.TotalAvailable == 0 {
		if result.Error != "" {
			return fmt.Sprintf("Detailed hotel search in %s failed: %s", location, result.Error)
		}
		return fmt.Sprintf("No hotels found in %s.", location)
	}

	summary := fmt.Sprintf("Enriched %d of %d hotels in %s.", result.Count, result.TotalAvailable, location)
	roomCount := 0
	errorCount := 0
	for _, hotel := range result.Hotels {
		roomCount += len(hotel.RoomTypes)
		errorCount += len(hotel.DetailErrors)
	}
	if roomCount > 0 {
		summary += fmt.Sprintf(" Found %d room type%s.", roomCount, pluralSuffix(roomCount))
	}
	if errorCount > 0 {
		summary += fmt.Sprintf(" %d detail lookup%s had partial failures.", errorCount, pluralSuffix(errorCount))
	}
	return summary
}
