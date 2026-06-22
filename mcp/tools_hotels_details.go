package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

var fetchHotelAmenitiesFunc = hotels.FetchHotelAmenities
var getRoomAvailabilityWithOptsFunc = hotels.GetRoomAvailabilityWithOpts
var searchHotelByNameFunc = hotels.SearchHotelByName

type hotelWithDetails struct {
	models.HotelResult
	VerifiedRate        *hotelVerifiedRate          `json:"verified_rate,omitempty"`
	AccommodationOffers []models.AccommodationOffer `json:"accommodation_offers,omitempty"`
	RoomTypes           []hotels.RoomType           `json:"room_types,omitempty"`
	DetailErrors        []hotelDetailError          `json:"detail_errors,omitempty"`
}

type hotelVerifiedRate struct {
	Provider           string    `json:"provider,omitempty"`
	RoomName           string    `json:"room_name,omitempty"`
	Price              float64   `json:"price"`
	NightlyPrice       float64   `json:"nightly_price,omitempty"`
	TotalPrice         float64   `json:"total_price,omitempty"`
	TaxesAndFees       float64   `json:"taxes_and_fees,omitempty"`
	TaxesFeesIncluded  *bool     `json:"taxes_fees_included,omitempty"`
	Currency           string    `json:"currency"`
	BookingURL         string    `json:"booking_url,omitempty"`
	PriceBasis         string    `json:"price_basis"`
	PriceConfidence    string    `json:"price_confidence"`
	RetrievedAt        time.Time `json:"retrieved_at"`
	Freshness          string    `json:"freshness"`
	CancellationPolicy string    `json:"cancellation_policy,omitempty"`
	Board              string    `json:"board,omitempty"`
	BreakfastIncluded  *bool     `json:"breakfast_included,omitempty"`
	Refundable         *bool     `json:"refundable,omitempty"`
	FreeCancellation   *bool     `json:"free_cancellation,omitempty"`
}

type hotelDetailError struct {
	Scope   string `json:"scope"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type hotelDetailsSearchResponse struct {
	Success          bool                    `json:"success"`
	Count            int                     `json:"count"`
	TotalAvailable   int                     `json:"total_available,omitempty"`
	Hotels           []hotelWithDetails      `json:"hotels"`
	ProviderStatuses []models.ProviderStatus `json:"provider_statuses,omitempty"`
	Completeness     models.Completeness     `json:"completeness,omitempty"`
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
		Description: "Search hotels, then enrich the top matches with room-level availability and property amenities in one call. Use this to verify shortlisted hotels before final recommendations, because raw search_hotels prices can be lead-in rates rather than checkout-final quotes. Compare rooms, rates, Booking.com detail data, amenities, cancellation, board, taxes/fees, and total_price when exposed instead of making separate search_hotels and hotel_rooms calls. Detail enrichment is best-effort per hotel: partial failures are reported in detail_errors without failing the full search.",
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
					"name":                 schemaString(),
					"hotel_id":             schemaString(),
					"rating":               schemaNum(),
					"review_count":         schemaInt(),
					"stars":                schemaInt(),
					"price":                schemaNum(),
					"currency":             schemaString(),
					"address":              schemaString(),
					"property_type":        schemaStringDesc("Inferred lodging type: hotel, hostel, apartment, vacation_rental, resort, bnb, villa, or unknown."),
					"booking_url":          schemaString(),
					"amenities":            schemaStringArray(),
					"eco_certified":        schemaBool(),
					"price_basis":          schemaStringDesc("Basis for primary search price: lead_in, room_nightly, room_total, or tax_inclusive_total."),
					"price_confidence":     schemaStringDesc("Confidence for primary search price: unverified, room_level, or verified."),
					"retrieved_at":         schemaStringDesc("Time trvl retrieved the primary search price."),
					"freshness":            schemaStringDesc("Freshness class for primary search price: live, recent, or stale."),
					"price_warnings":       schemaStringArray(),
					"savings":              schemaNumDesc("Price savings vs most expensive source"),
					"cheapest_source":      schemaStringDesc("Provider with lowest price"),
					"verified_rate":        hotelVerifiedRateSchema(),
					"accommodation_offers": schemaArray(accommodationOfferSchema()),
					"room_types":           schemaArray(hotelRoomTypeSchema()),
					"detail_errors":        hotelDetailErrorsSchema(),
					"sources": schemaArray(map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"provider":         schemaString(),
							"price":            schemaNum(),
							"max_price":        schemaNum(),
							"currency":         schemaString(),
							"room_count":       schemaInt(),
							"booking_url":      schemaString(),
							"price_basis":      schemaString(),
							"price_confidence": schemaString(),
							"retrieved_at":     schemaString(),
							"freshness":        schemaString(),
						},
					}),
				},
			}),
			"completeness": schemaObject(),
			"suggestions": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action":      schemaString(),
					"description": schemaString(),
					"params":      schemaObject(),
				},
			}),
			"provider_statuses": schemaArrayDesc("Per-provider outcome (Google Hotels / Trivago / Booking / Airbnb / Hostelworld / configured providers). Status may be checked_hit, checked_no_hit, timeout, failed, skipped, disabled, or circuit_broken.", map[string]interface{}{
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

func hotelRoomTypeSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name":                schemaString(),
			"price":               schemaNumDesc("Primary display price for backward-compatible clients."),
			"nightly_price":       schemaNumDesc("Nightly room price when the provider exposes it separately."),
			"total_price":         schemaNumDesc("Total stay price when the provider exposes it separately."),
			"taxes_and_fees":      schemaNumDesc("Tax and fee amount when exposed separately by the provider."),
			"taxes_fees_included": schemaBoolDesc("Whether taxes and fees are included in the exposed price when known."),
			"currency":            schemaString(),
			"provider":            schemaString(),
			"provider_url":        schemaString(),
			"rate_id":             schemaString(),
			"rate_plan_name":      schemaString(),
			"match_confidence":    schemaStringDesc("exact_room_match, similar_room_match, or property_level_only."),
			"max_guests":          schemaInt(),
			"bed_type":            schemaString(),
			"size_m2":             schemaNum(),
			"description":         schemaString(),
			"amenities":           schemaStringArray(),
			"cancellation_policy": schemaStringDesc("Normalized cancellation label such as free_cancellation, refundable, or non_refundable."),
			"refundable":          schemaBoolDesc("Whether the room rate is refundable when known."),
			"free_cancellation":   schemaBoolDesc("Whether the room rate has free cancellation when known."),
			"board":               schemaStringDesc("Normalized meal plan such as room_only, breakfast_included, half_board, full_board, or all_inclusive."),
			"breakfast_included":  schemaBoolDesc("Whether breakfast is included when known."),
			"inventory_options":   schemaArray(roomInventoryQuoteSchema()),
		},
		"required": []string{"name", "price", "currency"},
	}
}

func hotelVerifiedRateSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"provider":            schemaString(),
			"room_name":           schemaString(),
			"price":               schemaNumDesc("Comparable price used for ranking; total_price when available, otherwise room nightly/display price."),
			"nightly_price":       schemaNumDesc("Nightly room price when the provider exposes it separately."),
			"total_price":         schemaNumDesc("Total stay price when the provider exposes it separately."),
			"taxes_and_fees":      schemaNumDesc("Tax and fee amount when exposed separately by the provider."),
			"taxes_fees_included": schemaBoolDesc("Whether taxes and fees are included in the exposed price when known."),
			"currency":            schemaString(),
			"booking_url":         schemaString(),
			"price_basis":         schemaStringDesc("room_nightly, room_total, or tax_inclusive_total."),
			"price_confidence":    schemaStringDesc("room_level or verified."),
			"retrieved_at":        schemaStringDesc("Time trvl retrieved this room-level rate."),
			"freshness":           schemaStringDesc("Freshness class for this room-level rate."),
			"cancellation_policy": schemaString(),
			"board":               schemaString(),
			"breakfast_included":  schemaBoolDesc("Whether breakfast is included when known."),
			"refundable":          schemaBoolDesc("Whether the selected room rate is refundable when known."),
			"free_cancellation":   schemaBoolDesc("Whether free cancellation is available when known."),
		},
		"required": []string{"price", "currency", "price_basis", "price_confidence", "retrieved_at", "freshness"},
	}
}

func accommodationOfferSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"property_name":          schemaString(),
			"property_id":            schemaString(),
			"offer_id":               schemaString(),
			"accommodation_type":     schemaStringDesc("Matched lodging type such as hotel_room, entire_apartment, private_room, shared_room, hostel_bed, or villa."),
			"room_name":              schemaString(),
			"provider":               schemaString(),
			"provider_url":           schemaString(),
			"occupancy_adults":       schemaInt(),
			"occupancy_children":     schemaArray(map[string]interface{}{"type": "integer"}),
			"rooms":                  schemaInt(),
			"bedrooms":               schemaInt(),
			"bathrooms":              schemaInt(),
			"beds":                   schemaInt(),
			"amenities":              schemaStringArray(),
			"occupancy_matched":      schemaBoolDesc("Whether this offer can host the requested adults/children/rooms when known."),
			"criteria_matched":       schemaBoolDesc("Whether all required user criteria are present and known."),
			"booking_ready":          schemaBoolDesc("Whether this is a real room-level offer suitable for booking decisions."),
			"final_trip_cost_ready":  schemaBoolDesc("Whether total price and tax/fee status are known enough for final trip totals."),
			"missing_criteria":       schemaStringArrayDesc("Criteria known to be absent from this offer."),
			"unknown_criteria":       schemaStringArrayDesc("Criteria the provider did not expose, so the offer should not be treated as matching."),
			"nightly_price":          schemaNum(),
			"total_price":            schemaNum(),
			"taxes_and_fees":         schemaNum(),
			"taxes_fees_included":    schemaBoolDesc("Whether taxes and fees are included in total_price when known."),
			"currency":               schemaString(),
			"price_basis":            schemaStringDesc("room_nightly, room_total, tax_inclusive_total, or lead_in."),
			"price_confidence":       schemaStringDesc("room_level, verified, or unverified."),
			"checked_at":             schemaStringDesc("Time trvl checked this room-level rate."),
			"expires_at":             schemaString(),
			"freshness":              schemaStringDesc("Freshness class: live, recent, or stale."),
			"cancellation_policy":    schemaString(),
			"refundable":             schemaBoolDesc("Whether the rate is refundable when known."),
			"free_cancellation":      schemaBoolDesc("Whether the rate has free cancellation when known."),
			"booking_order_hint":     schemaStringDesc("flights_first_ok, accommodation_first, needs_refundability_check, or needs_price_verification."),
			"board":                  schemaString(),
			"breakfast_included":     schemaBoolDesc("Whether breakfast is included when known."),
			"inventory_completeness": schemaStringDesc("single_provider, multi_provider_exact, multi_provider_mixed, property_level_only, or no_provider_inventory."),
			"inventory_options":      schemaArray(roomInventoryQuoteSchema()),
			"warnings":               schemaStringArray(),
		},
	}
}

func roomInventoryQuoteSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"provider":            schemaString(),
			"provider_room_name":  schemaString(),
			"provider_rate_name":  schemaString(),
			"provider_url":        schemaString(),
			"rate_id":             schemaString(),
			"match_confidence":    schemaStringDesc("exact_room_match, similar_room_match, or property_level_only."),
			"nightly_price":       schemaNum(),
			"total_price":         schemaNum(),
			"taxes_and_fees":      schemaNum(),
			"taxes_fees_included": schemaBoolDesc("Whether taxes and fees are included in total_price when known."),
			"currency":            schemaString(),
			"refundable":          schemaBoolDesc("Whether the provider rate is refundable when known."),
			"free_cancellation":   schemaBoolDesc("Whether the provider rate has free cancellation when known."),
			"cancellation_policy": schemaString(),
			"board":               schemaString(),
			"breakfast_included":  schemaBoolDesc("Whether breakfast is included when known."),
			"occupancy_adults":    schemaInt(),
			"occupancy_children":  schemaArray(map[string]interface{}{"type": "integer"}),
			"rooms":               schemaInt(),
			"price_basis":         schemaString(),
			"price_confidence":    schemaString(),
			"checked_at":          schemaString(),
			"expires_at":          schemaString(),
			"freshness":           schemaString(),
			"warnings":            schemaStringArray(),
		},
	}
}

func hotelDetailErrorsSchema() map[string]interface{} {
	return schemaArray(map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"scope":   schemaStringDesc("Detail fetch area: hotel, amenities, or rooms."),
			"code":    schemaStringDesc("Stable machine-readable error code."),
			"message": schemaStringDesc("Human-readable error summary."),
		},
		"required": []string{"scope", "code", "message"},
	})
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
	need := accommodationNeedFromHotelSearchRequest(req)
	hotelsWithDetails := make([]hotelWithDetails, 0, limit)
	for i := 0; i < limit; i++ {
		hotel := result.Hotels[i]
		detailed := hotelWithDetails{HotelResult: hotel}
		checkedAt := time.Now()
		searchInventoryRooms := roomTypesFromHotelSearchInventory(hotel, need, checkedAt)
		if hotel.HotelID == "" {
			if includeRooms && len(searchInventoryRooms) > 0 {
				detailed.RoomTypes = searchInventoryRooms
				detailed.VerifiedRate = verifiedRateFromRooms(hotel, searchInventoryRooms, checkedAt)
				detailed.AccommodationOffers = accommodationOffersFromRooms(hotel, searchInventoryRooms, need, checkedAt)
			} else {
				detailed.DetailErrors = append(detailed.DetailErrors, hotelDetailError{
					Scope:   "hotel",
					Code:    "missing_hotel_id",
					Message: "missing hotel_id; cannot fetch hotel details",
				})
			}
			hotelsWithDetails = append(hotelsWithDetails, detailed)
			continue
		}
		if includeAmenities {
			amenities, err := fetchHotelAmenitiesFunc(ctx, hotel.HotelID)
			if err != nil {
				detailed.DetailErrors = append(detailed.DetailErrors, newHotelDetailError("amenities", "amenities_fetch_failed", err))
			} else if len(amenities) > 0 {
				detailed.Amenities = amenities
			}
		}
		if includeRooms {
			availability, err := getRoomAvailabilityWithOptsFunc(ctx, hotels.RoomSearchOptions{
				HotelID:      hotel.HotelID,
				CheckIn:      req.CheckIn,
				CheckOut:     req.CheckOut,
				Currency:     req.Options.Currency,
				Guests:       req.Options.Guests,
				ChildrenAges: req.Options.ChildrenAges,
				Rooms:        req.Options.Rooms,
				BookingURL:   hotel.BookingURL,
				Location:     req.Location,
			})
			if err != nil {
				detailed.DetailErrors = append(detailed.DetailErrors, newHotelDetailError("rooms", "rooms_fetch_failed", err))
			} else if availability != nil {
				searchInventoryRooms = mergeDetailAndSearchInventoryRooms(availability.Rooms, searchInventoryRooms)
			}
			if len(searchInventoryRooms) > 0 {
				detailed.RoomTypes = searchInventoryRooms
				detailed.VerifiedRate = verifiedRateFromRooms(hotel, searchInventoryRooms, checkedAt)
				detailed.AccommodationOffers = accommodationOffersFromRooms(hotel, searchInventoryRooms, need, checkedAt)
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
		Completeness:     result.Completeness,
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

func accommodationNeedFromHotelSearchRequest(req hotelSearchRequest) models.AccommodationNeed {
	opts := req.Options
	return models.AccommodationNeed{
		Location:                 req.Location,
		CheckIn:                  req.CheckIn,
		CheckOut:                 req.CheckOut,
		Adults:                   adultsFromGuests(opts.Guests, opts.ChildrenAges),
		ChildrenAges:             append([]int(nil), opts.ChildrenAges...),
		Rooms:                    opts.Rooms,
		AccommodationType:        accommodationTypeFromHotelOptions(opts),
		MinBedrooms:              opts.MinBedrooms,
		MinBathrooms:             opts.MinBathrooms,
		MinBeds:                  opts.MinBeds,
		MinStars:                 opts.Stars,
		RequiredAmenities:        append([]string(nil), opts.Amenities...),
		MaxDistanceKm:            opts.MaxDistanceKm,
		MustHaveKitchen:          opts.MustHaveKitchen,
		MustHaveWifi:             opts.MustHaveWifi,
		MustHaveWorkspace:        opts.MustHaveWorkspace,
		BreakfastRequired:        opts.MealPlan,
		RefundableRequired:       opts.RefundableRequired,
		FreeCancellationRequired: opts.FreeCancellation,
		Currency:                 opts.Currency,
	}
}

func roomTypesFromHotelSearchInventory(hotel models.HotelResult, need models.AccommodationNeed, checkedAt time.Time) []hotels.RoomType {
	rooms := make([]hotels.RoomType, 0, len(hotel.RoomTypes))
	for _, room := range hotel.RoomTypes {
		converted, ok := roomTypeFromSearchRoom(hotel, room, need, checkedAt)
		if ok {
			rooms = append(rooms, converted)
		}
	}
	if len(rooms) > 0 {
		return rooms
	}
	return propertyLevelRoomsFromHotelSources(hotel, need, checkedAt)
}

func roomTypeFromSearchRoom(hotel models.HotelResult, room models.Room, need models.AccommodationNeed, checkedAt time.Time) (hotels.RoomType, bool) {
	price := firstPositiveFloat(room.Price, room.TotalPrice, room.NightlyPrice)
	total := room.TotalPrice
	nightly := room.NightlyPrice
	if total == 0 && nightly == 0 && price > 0 {
		total = price
	}
	if price <= 0 || strings.TrimSpace(room.Currency) == "" {
		return hotels.RoomType{}, false
	}
	provider, providerURL := searchRoomProviderAndURL(hotel, room)
	freeCancellation := truthyRoomBool(room.FreeCancellation)
	breakfastIncluded := truthyRoomBool(room.BreakfastIncluded)
	refundable := room.Refundable
	if refundable == nil && freeCancellation != nil && *freeCancellation {
		refundable = mcpBoolPtr(true)
	}
	board := room.Board
	if board == "" && breakfastIncluded != nil && *breakfastIncluded {
		board = "breakfast included"
	}
	matchConfidence := firstNonEmpty(room.MatchConfidence, models.RoomInventoryMatchExact)
	priceBasis := firstNonEmpty(room.PriceBasis, models.PriceBasisRoomTotal)
	priceConfidence := firstNonEmpty(room.PriceConfidence, models.PriceConfidenceRoomLevel)
	occupancyAdults, occupancyChildren := modelRoomOccupancy(room, need)
	return hotels.RoomType{
		Name:               firstNonEmpty(room.Name, "Accommodation option"),
		Price:              price,
		NightlyPrice:       nightly,
		TotalPrice:         total,
		TaxesAndFees:       room.TaxesAndFees,
		TaxesFeesIncluded:  room.TaxesFeesIncluded,
		Currency:           strings.ToUpper(strings.TrimSpace(room.Currency)),
		Provider:           provider,
		ProviderURL:        providerURL,
		RateID:             room.RateID,
		RatePlanName:       room.RatePlanName,
		MatchConfidence:    matchConfidence,
		MaxGuests:          room.MaxGuests,
		BedType:            room.BedType,
		SizeM2:             room.SizeM2,
		Description:        room.Description,
		Amenities:          append([]string(nil), room.Amenities...),
		CancellationPolicy: room.CancellationPolicy,
		Refundable:         refundable,
		FreeCancellation:   freeCancellation,
		Board:              board,
		BreakfastIncluded:  breakfastIncluded,
		InventoryOptions: []models.RoomInventoryQuote{{
			Provider:           provider,
			ProviderRoomName:   firstNonEmpty(room.Name, "Accommodation option"),
			ProviderRateName:   room.RatePlanName,
			ProviderURL:        providerURL,
			RateID:             room.RateID,
			MatchConfidence:    matchConfidence,
			NightlyPrice:       nightly,
			TotalPrice:         total,
			TaxesAndFees:       room.TaxesAndFees,
			TaxesFeesIncluded:  room.TaxesFeesIncluded,
			Currency:           strings.ToUpper(strings.TrimSpace(room.Currency)),
			Refundable:         refundable,
			FreeCancellation:   freeCancellation,
			CancellationPolicy: room.CancellationPolicy,
			Board:              board,
			BreakfastIncluded:  breakfastIncluded,
			OccupancyAdults:    occupancyAdults,
			OccupancyChildren:  occupancyChildren,
			Rooms:              need.Rooms,
			PriceBasis:         priceBasis,
			PriceConfidence:    priceConfidence,
			CheckedAt:          checkedAt,
			Freshness:          models.ClassifyFreshness(provider, checkedAt, checkedAt),
		}},
	}, true
}

func propertyLevelRoomsFromHotelSources(hotel models.HotelResult, need models.AccommodationNeed, checkedAt time.Time) []hotels.RoomType {
	rooms := make([]hotels.RoomType, 0, len(hotel.Sources))
	for _, source := range hotel.Sources {
		if source.Price <= 0 {
			continue
		}
		currency := strings.ToUpper(strings.TrimSpace(firstNonEmpty(source.Currency, hotel.Currency)))
		if currency == "" {
			continue
		}
		provider := displayInventoryProvider(source.Provider)
		providerURL := firstNonEmpty(source.BookingURL, hotel.BookingURL)
		priceBasis := firstNonEmpty(source.PriceBasis, models.PriceBasisLeadIn)
		priceConfidence := firstNonEmpty(source.PriceConfidence, models.PriceConfidenceUnverified)
		sourceCheckedAt := source.RetrievedAt
		if sourceCheckedAt.IsZero() {
			sourceCheckedAt = checkedAt
		}
		rooms = append(rooms, hotels.RoomType{
			Name:            "Property lead-in rate",
			Price:           source.Price,
			TotalPrice:      source.Price,
			Currency:        currency,
			Provider:        provider,
			ProviderURL:     providerURL,
			RatePlanName:    "search result lead-in",
			MatchConfidence: models.RoomInventoryMatchPropertyLevelOnly,
			InventoryOptions: []models.RoomInventoryQuote{{
				Provider:         provider,
				ProviderRoomName: "Property lead-in rate",
				ProviderRateName: "search result lead-in",
				ProviderURL:      providerURL,
				MatchConfidence:  models.RoomInventoryMatchPropertyLevelOnly,
				TotalPrice:       source.Price,
				Currency:         currency,
				OccupancyAdults:  need.Adults,
				Rooms:            need.Rooms,
				PriceBasis:       priceBasis,
				PriceConfidence:  priceConfidence,
				CheckedAt:        sourceCheckedAt,
				Freshness:        models.ClassifyFreshness(provider, sourceCheckedAt, checkedAt),
			}},
		})
	}
	return rooms
}

func mergeDetailAndSearchInventoryRooms(detailRooms, searchRooms []hotels.RoomType) []hotels.RoomType {
	if len(searchRooms) == 0 {
		return detailRooms
	}
	out := make([]hotels.RoomType, 0, len(detailRooms)+len(searchRooms))
	seen := make(map[string]struct{}, len(detailRooms)+len(searchRooms))
	for _, room := range append(detailRooms, searchRooms...) {
		key := strings.ToLower(strings.Join([]string{
			room.Provider,
			room.Name,
			room.RatePlanName,
			room.Currency,
			fmt.Sprintf("%.2f", comparableRoomPrice(room)),
			room.MatchConfidence,
		}, "|"))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, room)
	}
	return out
}

func searchRoomProviderAndURL(hotel models.HotelResult, room models.Room) (string, string) {
	provider := strings.TrimSpace(room.Provider)
	providerURL := strings.TrimSpace(room.ProviderURL)
	if provider != "" {
		return displayInventoryProvider(provider), firstNonEmpty(providerURL, hotel.BookingURL)
	}
	for _, source := range hotel.Sources {
		if source.RoomCount > 0 || floatPricesEqual(source.Price, firstPositiveFloat(room.Price, room.TotalPrice, room.NightlyPrice)) {
			return displayInventoryProvider(source.Provider), firstNonEmpty(providerURL, source.BookingURL, hotel.BookingURL)
		}
	}
	if len(hotel.Sources) > 0 {
		source := hotel.Sources[0]
		return displayInventoryProvider(source.Provider), firstNonEmpty(providerURL, source.BookingURL, hotel.BookingURL)
	}
	return "Provider", firstNonEmpty(providerURL, hotel.BookingURL)
}

func modelRoomOccupancy(room models.Room, need models.AccommodationNeed) (int, []int) {
	adults := need.Adults
	children := append([]int(nil), need.ChildrenAges...)
	if room.MaxGuests <= 0 {
		return adults, children
	}
	total := adults + len(children)
	if total <= room.MaxGuests {
		return adults, children
	}
	if room.MaxGuests <= adults {
		return room.MaxGuests, nil
	}
	childSlots := room.MaxGuests - adults
	if childSlots < len(children) {
		children = children[:childSlots]
	}
	return adults, children
}

func truthyRoomBool(value bool) *bool {
	if !value {
		return nil
	}
	return mcpBoolPtr(true)
}

func mcpBoolPtr(value bool) *bool {
	return &value
}

func firstPositiveFloat(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func floatPricesEqual(a, b float64) bool {
	if a == 0 || b == 0 {
		return false
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 0.01
}

func displayInventoryProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "google_hotels", "google hotels":
		return "Google Hotels"
	case "booking", "booking.com":
		return "Booking.com"
	case "hometogo", "home to go":
		return "HomeToGo"
	case "trivago":
		return "Trivago"
	case "airbnb":
		return "Airbnb"
	case "hostelworld":
		return "Hostelworld"
	case "":
		return "Provider"
	default:
		return strings.TrimSpace(provider)
	}
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
	freeCancelCount := 0
	breakfastCount := 0
	verifiedRateCount := 0
	for _, hotel := range result.Hotels {
		roomCount += len(hotel.RoomTypes)
		errorCount += len(hotel.DetailErrors)
		if hotel.VerifiedRate != nil &&
			hotel.VerifiedRate.PriceBasis != models.PriceBasisLeadIn &&
			hotel.VerifiedRate.PriceConfidence != models.PriceConfidenceUnverified {
			verifiedRateCount++
		}
		for _, room := range hotel.RoomTypes {
			if roomHasFreeCancellation(room) {
				freeCancelCount++
			}
			if room.BreakfastIncluded != nil && *room.BreakfastIncluded {
				breakfastCount++
			}
		}
	}
	if roomCount > 0 {
		summary += fmt.Sprintf(" Found %d room type%s.", roomCount, pluralSuffix(roomCount))
	}
	if verifiedRateCount > 0 {
		verb := "have"
		if verifiedRateCount == 1 {
			verb = "has"
		}
		summary += fmt.Sprintf(" %d hotel%s %s room-level verified_rate.", verifiedRateCount, pluralSuffix(verifiedRateCount), verb)
	}
	if freeCancelCount > 0 {
		summary += fmt.Sprintf(" %d with free cancellation.", freeCancelCount)
	}
	if breakfastCount > 0 {
		summary += fmt.Sprintf(" %d with breakfast included.", breakfastCount)
	}
	if errorCount > 0 {
		summary += fmt.Sprintf(" %d detail lookup%s had partial failures.", errorCount, pluralSuffix(errorCount))
	}
	if note := result.Completeness.IncompleteNote(); note != "" {
		summary = note + " " + summary
	}
	return summary
}

// roomHasFreeCancellation reports whether a room's parsed metadata indicates
// free cancellation. It only returns true when the provider surfaced an
// affirmative signal (explicit flag or normalized policy); absent data
// (nil pointer, empty policy) returns false rather than fabricating a claim.
func roomHasFreeCancellation(room hotels.RoomType) bool {
	if room.FreeCancellation != nil && *room.FreeCancellation {
		return true
	}
	return room.CancellationPolicy == "free_cancellation"
}
