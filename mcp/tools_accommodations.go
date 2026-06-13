package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

type accommodationSearchResponse struct {
	Success                 bool                           `json:"success"`
	Need                    models.AccommodationNeed       `json:"need"`
	Count                   int                            `json:"count"`
	MatchingCount           int                            `json:"matching_count"`
	BookingReadyCount       int                            `json:"booking_ready_count"`
	FinalTripCostReadyCount int                            `json:"final_trip_cost_ready_count"`
	CandidateCount          int                            `json:"candidate_count"`
	TotalAvailable          int                            `json:"total_available,omitempty"`
	Offers                  []models.AccommodationOffer    `json:"offers"`
	RejectedOffers          []models.AccommodationOffer    `json:"rejected_offers,omitempty"`
	Candidates              []accommodationCandidate       `json:"candidates,omitempty"`
	Evidence                []models.AccommodationEvidence `json:"evidence,omitempty"`
	ProviderStatuses        []models.ProviderStatus        `json:"provider_statuses,omitempty"`
	Completeness            models.Completeness            `json:"completeness,omitempty"`
	Suggestions             []Suggestion                   `json:"suggestions,omitempty"`
	Warnings                []string                       `json:"warnings,omitempty"`
	Error                   string                         `json:"error,omitempty"`
}

type accommodationCandidate struct {
	Name                   string             `json:"name"`
	HotelID                string             `json:"hotel_id,omitempty"`
	Rating                 float64            `json:"rating,omitempty"`
	ReviewCount            int                `json:"review_count,omitempty"`
	Stars                  int                `json:"stars,omitempty"`
	LeadInPrice            float64            `json:"lead_in_price,omitempty"`
	Currency               string             `json:"currency,omitempty"`
	Address                string             `json:"address,omitempty"`
	PropertyType           string             `json:"property_type,omitempty"`
	BookingURL             string             `json:"booking_url,omitempty"`
	PriceBasis             string             `json:"price_basis,omitempty"`
	PriceConfidence        string             `json:"price_confidence,omitempty"`
	Freshness              string             `json:"freshness,omitempty"`
	PriceWarnings          []string           `json:"price_warnings,omitempty"`
	OfferCount             int                `json:"offer_count"`
	MatchingOfferCount     int                `json:"matching_offer_count"`
	BookingReadyOfferCount int                `json:"booking_ready_offer_count"`
	DetailErrors           []hotelDetailError `json:"detail_errors,omitempty"`
}

var (
	accommodationRoomLookupTimeout         = 20 * time.Second
	accommodationReverifyRoomLookupTimeout = 45 * time.Second
)

func searchAccommodationsTool() ToolDef {
	props := hotelSearchInputProperties()
	props["adults"] = Property{Type: "integer", Description: "Number of adult travellers. If guests is omitted, guests becomes adults + len(children_ages)."}
	props["accommodation_type"] = Property{Type: "string", Description: "Requested accommodation product: hotel_room, entire_apartment, private_room, shared_room, hostel_bed, or villa."}
	props["preferred_amenities"] = Property{Type: "string", Description: "Nice-to-have amenities, comma-separated. They are echoed for ranking/explanation but do not hard-block offers."}
	props["neighborhoods"] = Property{Type: "string", Description: "Preferred neighborhoods or areas, comma-separated."}
	props["max_total_price"] = Property{Type: "number", Description: "Maximum total stay price for the requested dates and party. Use this for final offer matching; max_price remains per-night discovery filtering."}
	props["free_cancellation_required"] = Property{Type: "boolean", Description: "Only treat room/apartment rates with explicit free cancellation as matching the requested need. This also asks providers to filter for free-cancellation options when they support it."}
	props["must_have_washing_machine"] = Property{Type: "boolean", Description: "Require washer/washing-machine evidence in the returned room or apartment offer."}
	props["max_candidates"] = Property{Type: "integer", Description: "Number of candidate properties to verify with room-level availability (default: 5, max: 8)."}
	props["max_offers"] = Property{Type: "integer", Description: "Maximum criteria-matched offers to return (default: 12, max: 40)."}
	props["include_unmatched"] = Property{Type: "boolean", Description: "Include room-level offers that were rejected with missing/unknown criteria in rejected_offers (default: true)."}
	props["include_candidates"] = Property{Type: "boolean", Description: "Include discovery candidates and per-candidate detail errors (default: true)."}
	props["reverify_only"] = Property{Type: "boolean", Description: "Skip property discovery and refresh room-level availability for a known hotel_id before presenting booking options."}
	props["hotel_id"] = Property{Type: "string", Description: "Known Google Hotels entity ID to refresh when reverify_only is true."}
	props["property_name"] = Property{Type: "string", Description: "Known property name to display when reverify_only is true."}
	props["room_name"] = Property{Type: "string", Description: "Optional room name to reverify and filter before handoff."}
	props["offer_id"] = Property{Type: "string", Description: "Optional trvl offer_id to reverify and filter before handoff."}
	props["booking_url"] = Property{Type: "string", Description: "Optional booking/deep-link URL used for richer room-level verification."}
	props["expected_total_price"] = Property{Type: "number", Description: "Previously shown total stay price. Reverification reports price deltas against it when currency matches."}
	props["expected_currency"] = Property{Type: "string", Description: "Currency for expected_total_price when it differs from currency."}

	return ToolDef{
		Name:        "search_accommodations",
		Title:       "Search Accommodations",
		Description: "Criteria-first accommodation search for traveller decisions. Use this before recommending where to stay: it captures the requested room/apartment need, searches candidate properties, verifies room-level rates for the shortlist, and returns only criteria-matched room/apartment offers in offers. Raw hotel lead-in prices stay in candidates and must not be used as final booking advice. Rejected room-level offers include missing_criteria and unknown_criteria so the user can see why they were not ranked. Set reverify_only=true with hotel_id before booking handoff to refresh the exact room quote and evidence ledger.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: props,
			Required:   []string{"location", "check_in", "check_out"},
		},
		OutputSchema: accommodationSearchOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Search Accommodations",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

func accommodationSearchOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":                     schemaBool(),
			"need":                        schemaObject(),
			"count":                       schemaInt(),
			"matching_count":              schemaInt(),
			"booking_ready_count":         schemaInt(),
			"final_trip_cost_ready_count": schemaInt(),
			"candidate_count":             schemaInt(),
			"total_available":             schemaInt(),
			"offers":                      schemaArray(accommodationOfferSchema()),
			"rejected_offers":             schemaArray(accommodationOfferSchema()),
			"evidence":                    schemaArray(accommodationEvidenceSchema()),
			"candidates": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":                      schemaString(),
					"hotel_id":                  schemaString(),
					"rating":                    schemaNum(),
					"review_count":              schemaInt(),
					"stars":                     schemaInt(),
					"lead_in_price":             schemaNumDesc("Discovery-only property-level price. Do not rank as a final booking quote."),
					"currency":                  schemaString(),
					"address":                   schemaString(),
					"property_type":             schemaString(),
					"booking_url":               schemaString(),
					"price_basis":               schemaString(),
					"price_confidence":          schemaString(),
					"freshness":                 schemaString(),
					"price_warnings":            schemaStringArray(),
					"offer_count":               schemaInt(),
					"matching_offer_count":      schemaInt(),
					"booking_ready_offer_count": schemaInt(),
					"detail_errors":             hotelDetailErrorsSchema(),
				},
			}),
			"provider_statuses": schemaArrayDesc("Per-provider discovery outcome.", map[string]interface{}{
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
			"completeness": schemaObject(),
			"suggestions": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action":      schemaString(),
					"description": schemaString(),
					"params":      schemaObject(),
				},
			}),
			"warnings": schemaStringArray(),
			"error":    schemaString(),
		},
		"required": []string{"success", "count", "offers"},
	}
}

func accommodationEvidenceSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"evidence_id":            schemaString(),
			"provider":               schemaString(),
			"status":                 schemaStringDesc("provider_status, missing_hotel_id, rooms_fetch_failed, no_room_offers, criteria_matched, criteria_rejected, reverified_match, or reverified_rejected."),
			"parser_version":         schemaString(),
			"checked_at":             schemaStringDesc("Time trvl checked the provider or room quote."),
			"expires_at":             schemaStringDesc("Time after which this quote should be refreshed before booking advice."),
			"ttl_seconds":            schemaIntDesc("Seconds until the quote should be refreshed."),
			"criteria":               schemaObject(),
			"property_name":          schemaString(),
			"property_id":            schemaString(),
			"offer_id":               schemaString(),
			"room_name":              schemaString(),
			"source_url":             schemaString(),
			"lead_in_price":          schemaNumDesc("Discovery-only property-level price used only for comparison."),
			"lead_in_currency":       schemaString(),
			"verified_nightly_price": schemaNumDesc("Room-level nightly price when exposed."),
			"verified_total_price":   schemaNumDesc("Room-level total stay price when exposed."),
			"taxes_and_fees":         schemaNum(),
			"taxes_fees_included":    schemaBoolDesc("Whether taxes and fees are included in verified_total_price when known."),
			"currency":               schemaString(),
			"price_basis":            schemaString(),
			"price_confidence":       schemaString(),
			"price_delta":            schemaNumDesc("Difference between comparable verified room price and the discovery/expected price."),
			"price_delta_pct":        schemaNumDesc("Percent difference between comparable verified room price and the discovery/expected price."),
			"criteria_matched":       schemaBool(),
			"occupancy_matched":      schemaBool(),
			"booking_ready":          schemaBool(),
			"final_trip_cost_ready":  schemaBool(),
			"missing_criteria":       schemaStringArray(),
			"unknown_criteria":       schemaStringArray(),
			"warnings":               schemaStringArray(),
			"detail_errors":          schemaStringArray(),
		},
	}
}

func handleSearchAccommodations(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	normalizedArgs := normalizeAccommodationSearchArgs(args)
	req, err := buildHotelSearchRequest(normalizedArgs)
	if err != nil {
		return nil, nil, err
	}

	need := accommodationNeedFromHotelSearchRequest(req)
	need = enrichAccommodationNeedFromArgs(need, normalizedArgs)
	req = relaxAccommodationDiscoveryFilters(req)
	if argBool(normalizedArgs, "reverify_only", false) {
		return handleReverifyAccommodation(ctx, req, need, normalizedArgs)
	}

	result, err := runHotelSearch(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	candidateLimit := accommodationCandidateLimit(argInt(normalizedArgs, "max_candidates", 5), len(result.Hotels))
	offerLimit := accommodationOfferLimit(argInt(normalizedArgs, "max_offers", 12))
	includeUnmatched := argBool(normalizedArgs, "include_unmatched", true)
	includeCandidates := argBool(normalizedArgs, "include_candidates", true)
	candidateHotels := selectAccommodationCandidateHotels(result.Hotels, candidateLimit, need)

	offers := make([]models.AccommodationOffer, 0)
	rejected := make([]models.AccommodationOffer, 0)
	candidates := make([]accommodationCandidate, 0, len(candidateHotels))
	evidence := accommodationEvidenceFromProviderStatuses(need, result.ProviderStatuses, time.Now())
	for _, hotel := range candidateHotels {
		candidate := accommodationCandidateFromHotel(hotel)
		checkedAt := time.Now()
		searchInventoryRooms := roomTypesFromHotelSearchInventory(hotel, need, checkedAt)
		if hotel.HotelID == "" {
			if len(searchInventoryRooms) > 0 {
				roomOffers := accommodationOffersFromRooms(hotel, searchInventoryRooms, need, checkedAt)
				candidate.OfferCount = len(roomOffers)
				for _, offer := range roomOffers {
					offer = withAccommodationPriceShockWarning(hotel, offer)
					if offer.CriteriaMatched {
						candidate.MatchingOfferCount++
						offers = append(offers, offer)
						if offer.BookingReadyStatus {
							candidate.BookingReadyOfferCount++
						}
						evidence = append(evidence, accommodationOfferEvidence(need, hotel, offer, "criteria_matched"))
					} else if includeUnmatched {
						rejected = append(rejected, offer)
						evidence = append(evidence, accommodationOfferEvidence(need, hotel, offer, "criteria_rejected"))
					}
				}
			} else {
				candidate.DetailErrors = append(candidate.DetailErrors, hotelDetailError{
					Scope:   "hotel",
					Code:    "missing_hotel_id",
					Message: "missing hotel_id; cannot verify room-level accommodation offers",
				})
				evidence = append(evidence, accommodationCandidateEvidence(need, hotel, candidate, "missing_hotel_id", checkedAt))
			}
			if includeCandidates {
				candidates = append(candidates, candidate)
			}
			continue
		}

		roomCtx, cancelRoomLookup := context.WithTimeout(ctx, accommodationRoomLookupTimeout)
		availability, err := getRoomAvailabilityWithOptsFunc(roomCtx, hotels.RoomSearchOptions{
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
		cancelRoomLookup()
		if err != nil {
			candidate.DetailErrors = append(candidate.DetailErrors, newHotelDetailError("rooms", "rooms_fetch_failed", err))
			evidence = append(evidence, accommodationCandidateEvidence(need, hotel, candidate, "rooms_fetch_failed", checkedAt))
			if len(searchInventoryRooms) == 0 {
				if includeCandidates {
					candidates = append(candidates, candidate)
				}
				continue
			}
		}
		rooms := searchInventoryRooms
		if availability != nil {
			rooms = mergeDetailAndSearchInventoryRooms(availability.Rooms, searchInventoryRooms)
		}
		if len(rooms) > 0 {
			roomOffers := accommodationOffersFromRooms(hotel, rooms, need, checkedAt)
			candidate.OfferCount = len(roomOffers)
			for _, offer := range roomOffers {
				offer = withAccommodationPriceShockWarning(hotel, offer)
				if offer.CriteriaMatched {
					candidate.MatchingOfferCount++
					offers = append(offers, offer)
					if offer.BookingReadyStatus {
						candidate.BookingReadyOfferCount++
					}
					evidence = append(evidence, accommodationOfferEvidence(need, hotel, offer, "criteria_matched"))
				} else if includeUnmatched {
					rejected = append(rejected, offer)
					evidence = append(evidence, accommodationOfferEvidence(need, hotel, offer, "criteria_rejected"))
				}
			}
			if len(roomOffers) == 0 {
				evidence = append(evidence, accommodationCandidateEvidence(need, hotel, candidate, "no_room_offers", checkedAt))
			}
		}
		if includeCandidates {
			candidates = append(candidates, candidate)
		}
	}

	sortAccommodationOffers(offers)
	sortAccommodationOffers(rejected)
	if len(offers) > offerLimit {
		offers = offers[:offerLimit]
	}
	if len(rejected) > offerLimit {
		rejected = rejected[:offerLimit]
	}

	resp := accommodationSearchResponse{
		Success:          result.Success,
		Need:             need,
		Count:            len(offers),
		MatchingCount:    len(offers),
		CandidateCount:   len(candidateHotels),
		TotalAvailable:   result.Count,
		Offers:           offers,
		RejectedOffers:   rejected,
		Candidates:       candidates,
		Evidence:         evidence,
		ProviderStatuses: result.ProviderStatuses,
		Completeness:     result.Completeness,
		Suggestions:      hotelSuggestions(result, req.Options),
		Warnings:         accommodationSearchWarnings(result, offers, rejected, evidence, len(candidateHotels)),
		Error:            result.Error,
	}
	for _, offer := range offers {
		if offer.BookingReadyStatus {
			resp.BookingReadyCount++
		}
		if offer.FinalTripCostReadyStatus {
			resp.FinalTripCostReadyCount++
		}
	}

	summary := accommodationSearchSummary(resp)
	content, err := buildAnnotatedContentBlocks(summary, resp)
	if err != nil {
		return nil, nil, err
	}
	return content, resp, nil
}

func handleReverifyAccommodation(ctx context.Context, req hotelSearchRequest, need models.AccommodationNeed, args map[string]any) ([]ContentBlock, interface{}, error) {
	hotelID := strings.TrimSpace(argString(args, "hotel_id"))
	if hotelID == "" {
		return nil, nil, fmt.Errorf("hotel_id is required when reverify_only is true")
	}

	propertyName := strings.TrimSpace(argString(args, "property_name"))
	if propertyName == "" {
		propertyName = hotelID
	}
	expectedCurrency := strings.ToUpper(strings.TrimSpace(argString(args, "expected_currency")))
	if expectedCurrency == "" {
		expectedCurrency = need.Currency
	}
	hotel := models.HotelResult{
		Name:            propertyName,
		HotelID:         hotelID,
		BookingURL:      strings.TrimSpace(argString(args, "booking_url")),
		Price:           argFloat(args, "expected_total_price", 0),
		Currency:        expectedCurrency,
		PriceBasis:      models.PriceBasisRoomTotal,
		PriceConfidence: models.PriceConfidenceRoomLevel,
	}
	if hotel.Currency == "" {
		hotel.Currency = req.Options.Currency
	}

	checkedAt := time.Now()
	candidate := accommodationCandidateFromHotel(hotel)
	roomCtx, cancelRoomLookup := context.WithTimeout(ctx, accommodationReverifyRoomLookupTimeout)
	availability, err := getRoomAvailabilityWithOptsFunc(roomCtx, hotels.RoomSearchOptions{
		HotelID:      hotelID,
		CheckIn:      req.CheckIn,
		CheckOut:     req.CheckOut,
		Currency:     req.Options.Currency,
		Guests:       req.Options.Guests,
		ChildrenAges: req.Options.ChildrenAges,
		Rooms:        req.Options.Rooms,
		BookingURL:   hotel.BookingURL,
		Location:     req.Location,
	})
	cancelRoomLookup()
	if err != nil {
		candidate.DetailErrors = append(candidate.DetailErrors, newHotelDetailError("rooms", "rooms_fetch_failed", err))
		resp := accommodationSearchResponse{
			Success:        false,
			Need:           need,
			CandidateCount: 1,
			Candidates:     []accommodationCandidate{candidate},
			Evidence:       []models.AccommodationEvidence{accommodationCandidateEvidence(need, hotel, candidate, "rooms_fetch_failed", checkedAt)},
			Warnings:       []string{"reverified_before_booking_handoff", "room_level_reverify_failed"},
			Error:          err.Error(),
		}
		content, buildErr := buildAnnotatedContentBlocks(accommodationSearchSummary(resp), resp)
		if buildErr != nil {
			return nil, nil, buildErr
		}
		return content, resp, nil
	}

	var rooms []hotels.RoomType
	if availability != nil {
		rooms = availability.Rooms
	}
	roomOffers := accommodationOffersFromRooms(hotel, rooms, need, checkedAt)
	candidate.OfferCount = len(roomOffers)
	offerID := strings.TrimSpace(argString(args, "offer_id"))
	roomName := strings.ToLower(strings.TrimSpace(argString(args, "room_name")))
	selectorSet := offerID != "" || roomName != ""

	offers := make([]models.AccommodationOffer, 0, len(roomOffers))
	rejected := make([]models.AccommodationOffer, 0, len(roomOffers))
	evidence := make([]models.AccommodationEvidence, 0, len(roomOffers)+1)
	for _, offer := range roomOffers {
		if selectorSet && !accommodationOfferMatchesSelector(offer, offerID, roomName) {
			continue
		}
		offer = withAccommodationPriceShockWarning(hotel, offer)
		status := "reverified_rejected"
		if offer.CriteriaMatched {
			status = "reverified_match"
			candidate.MatchingOfferCount++
			if offer.BookingReadyStatus {
				candidate.BookingReadyOfferCount++
			}
			offers = append(offers, offer)
		} else {
			rejected = append(rejected, offer)
		}
		evidence = append(evidence, accommodationOfferEvidence(need, hotel, offer, status))
	}
	if len(roomOffers) == 0 {
		candidate.DetailErrors = append(candidate.DetailErrors, hotelDetailError{
			Scope:   "rooms",
			Code:    "no_room_offers",
			Message: "no room-level accommodation offers were returned during reverify",
		})
		evidence = append(evidence, accommodationCandidateEvidence(need, hotel, candidate, "no_room_offers", checkedAt))
	} else if selectorSet && len(offers)+len(rejected) == 0 {
		candidate.DetailErrors = append(candidate.DetailErrors, hotelDetailError{
			Scope:   "rooms",
			Code:    "reverify_offer_not_found",
			Message: "requested offer_id or room_name was not present in refreshed room availability",
		})
		evidence = append(evidence, accommodationCandidateEvidence(need, hotel, candidate, "reverify_offer_not_found", checkedAt))
	}

	sortAccommodationOffers(offers)
	sortAccommodationOffers(rejected)
	resp := accommodationSearchResponse{
		Success:        availability == nil || availability.Success,
		Need:           need,
		Count:          len(offers),
		MatchingCount:  len(offers),
		CandidateCount: 1,
		TotalAvailable: 1,
		Offers:         offers,
		RejectedOffers: rejected,
		Candidates:     []accommodationCandidate{candidate},
		Evidence:       evidence,
		Warnings:       reverifiedAccommodationWarnings(offers, rejected, evidence, selectorSet),
	}
	for _, offer := range offers {
		if offer.BookingReadyStatus {
			resp.BookingReadyCount++
		}
		if offer.FinalTripCostReadyStatus {
			resp.FinalTripCostReadyCount++
		}
	}

	summary := accommodationSearchSummary(resp)
	content, buildErr := buildAnnotatedContentBlocks(summary, resp)
	if buildErr != nil {
		return nil, nil, buildErr
	}
	return content, resp, nil
}

func accommodationOfferMatchesSelector(offer models.AccommodationOffer, offerID, roomName string) bool {
	if offerID != "" && strings.EqualFold(strings.TrimSpace(offer.OfferID), offerID) {
		return true
	}
	if roomName != "" && strings.Contains(strings.ToLower(strings.TrimSpace(offer.RoomName)), roomName) {
		return true
	}
	return offerID == "" && roomName == ""
}

func accommodationEvidenceFromProviderStatuses(need models.AccommodationNeed, statuses []models.ProviderStatus, checkedAt time.Time) []models.AccommodationEvidence {
	if len(statuses) == 0 {
		return nil
	}
	evidence := make([]models.AccommodationEvidence, 0, len(statuses))
	for _, status := range statuses {
		provider := firstNonEmpty(status.ID, status.Name)
		expiresAt, ttlSeconds := accommodationEvidenceTTL(provider, checkedAt)
		detailErrors := []string(nil)
		if status.Error != "" {
			detailErrors = append(detailErrors, status.Error)
		}
		evidence = append(evidence, models.AccommodationEvidence{
			EvidenceID:    accommodationEvidenceID("provider", provider, status.Status),
			Provider:      provider,
			Status:        "provider_status",
			ParserVersion: models.AccommodationEvidenceParserVersion,
			CheckedAt:     checkedAt,
			ExpiresAt:     expiresAt,
			TTLSeconds:    ttlSeconds,
			Criteria:      need,
			DetailErrors:  detailErrors,
			Warnings:      providerStatusEvidenceWarnings(status),
		})
	}
	return evidence
}

func providerStatusEvidenceWarnings(status models.ProviderStatus) []string {
	switch strings.ToLower(strings.TrimSpace(status.Status)) {
	case "", "ok", "checked_hit":
		return nil
	default:
		return []string{"provider_status_" + strings.ToLower(strings.TrimSpace(status.Status))}
	}
}

func accommodationCandidateEvidence(need models.AccommodationNeed, hotel models.HotelResult, candidate accommodationCandidate, status string, checkedAt time.Time) models.AccommodationEvidence {
	provider := accommodationHotelProvider(hotel)
	expiresAt, ttlSeconds := accommodationEvidenceTTL(provider, checkedAt)
	return models.AccommodationEvidence{
		EvidenceID:      accommodationEvidenceID("candidate", candidate.HotelID, candidate.Name, status),
		Provider:        provider,
		Status:          status,
		ParserVersion:   models.AccommodationEvidenceParserVersion,
		CheckedAt:       checkedAt,
		ExpiresAt:       expiresAt,
		TTLSeconds:      ttlSeconds,
		Criteria:        need,
		PropertyName:    candidate.Name,
		PropertyID:      candidate.HotelID,
		SourceURL:       candidate.BookingURL,
		LeadInPrice:     candidate.LeadInPrice,
		LeadInCurrency:  candidate.Currency,
		PriceBasis:      candidate.PriceBasis,
		PriceConfidence: candidate.PriceConfidence,
		Warnings:        append([]string(nil), candidate.PriceWarnings...),
		DetailErrors:    accommodationDetailErrorStrings(candidate.DetailErrors),
	}
}

func accommodationOfferEvidence(need models.AccommodationNeed, hotel models.HotelResult, offer models.AccommodationOffer, status string) models.AccommodationEvidence {
	provider := accommodationEvidenceProvider(hotel, offer)
	expiresAt, ttlSeconds := accommodationEvidenceTTL(provider, offer.CheckedAt)
	priceDelta, priceDeltaPct, _ := accommodationPriceDelta(hotel, offer)
	return models.AccommodationEvidence{
		EvidenceID:           accommodationEvidenceID("offer", offer.PropertyID, offer.OfferID, status),
		Provider:             provider,
		Status:               status,
		ParserVersion:        models.AccommodationEvidenceParserVersion,
		CheckedAt:            offer.CheckedAt,
		ExpiresAt:            expiresAt,
		TTLSeconds:           ttlSeconds,
		Criteria:             need,
		PropertyName:         offer.PropertyName,
		PropertyID:           offer.PropertyID,
		OfferID:              offer.OfferID,
		RoomName:             offer.RoomName,
		SourceURL:            offer.ProviderURL,
		LeadInPrice:          hotel.Price,
		LeadInCurrency:       hotel.Currency,
		VerifiedNightlyPrice: offer.NightlyPrice,
		VerifiedTotalPrice:   offer.TotalPrice,
		TaxesAndFees:         offer.TaxesAndFees,
		TaxesFeesIncluded:    offer.TaxesFeesIncluded,
		Currency:             offer.Currency,
		PriceBasis:           offer.PriceBasis,
		PriceConfidence:      offer.PriceConfidence,
		PriceDelta:           priceDelta,
		PriceDeltaPct:        priceDeltaPct,
		CriteriaMatched:      offer.CriteriaMatched,
		OccupancyMatched:     offer.OccupancyMatched,
		BookingReady:         offer.BookingReadyStatus,
		FinalTripCostReady:   offer.FinalTripCostReadyStatus,
		MissingCriteria:      append([]string(nil), offer.MissingCriteria...),
		UnknownCriteria:      append([]string(nil), offer.UnknownCriteria...),
		Warnings:             append([]string(nil), offer.Warnings...),
	}
}

func accommodationEvidenceProvider(hotel models.HotelResult, offer models.AccommodationOffer) string {
	return firstNonEmpty(offer.Provider, hotel.CheapestSource, accommodationHotelProvider(hotel))
}

func accommodationHotelProvider(hotel models.HotelResult) string {
	if hotel.CheapestSource != "" {
		return hotel.CheapestSource
	}
	if len(hotel.Sources) > 0 && hotel.Sources[0].Provider != "" {
		return hotel.Sources[0].Provider
	}
	return "google_hotels"
}

func accommodationEvidenceTTL(provider string, checkedAt time.Time) (time.Time, int) {
	if checkedAt.IsZero() {
		checkedAt = time.Now()
	}
	profile := models.SourceProfileFor(provider)
	minutes := profile.LiveMinutes
	if minutes <= 0 {
		minutes = 60
	}
	ttlSeconds := minutes * 60
	return checkedAt.Add(time.Duration(ttlSeconds) * time.Second), ttlSeconds
}

func withAccommodationPriceShockWarning(hotel models.HotelResult, offer models.AccommodationOffer) models.AccommodationOffer {
	_, pct, ok := accommodationPriceDelta(hotel, offer)
	if !ok {
		return offer
	}
	if pct > 30 {
		offer.Warnings = appendUniqueStringMCP(offer.Warnings, models.AccommodationWarningPriceShock)
	}
	return offer
}

func accommodationPriceDelta(hotel models.HotelResult, offer models.AccommodationOffer) (float64, float64, bool) {
	if hotel.Price <= 0 {
		return 0, 0, false
	}
	if hotel.Currency != "" && offer.Currency != "" && !strings.EqualFold(hotel.Currency, offer.Currency) {
		return 0, 0, false
	}
	verified := offer.TotalPrice
	switch hotel.PriceBasis {
	case "", models.PriceBasisLeadIn, models.PriceBasisRoomNightly:
		if offer.NightlyPrice > 0 {
			verified = offer.NightlyPrice
		}
	}
	if verified <= 0 {
		return 0, 0, false
	}
	delta := verified - hotel.Price
	return delta, delta / hotel.Price * 100, true
}

func accommodationDetailErrorStrings(errors []hotelDetailError) []string {
	if len(errors) == 0 {
		return nil
	}
	out := make([]string, 0, len(errors))
	for _, err := range errors {
		if err.Code != "" && err.Message != "" {
			out = append(out, err.Code+": "+err.Message)
		} else if err.Message != "" {
			out = append(out, err.Message)
		} else if err.Code != "" {
			out = append(out, err.Code)
		}
	}
	return out
}

func accommodationEvidenceID(parts ...string) string {
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		values = append(values, strings.Join(strings.Fields(part), "_"))
	}
	return strings.Join(values, "|")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func appendUniqueStringMCP(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func normalizeAccommodationSearchArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args)+3)
	for k, v := range args {
		out[k] = v
	}
	if argBool(out, "free_cancellation_required", false) {
		if _, ok := out["free_cancellation"]; !ok {
			out["free_cancellation"] = true
		}
	}
	if argBool(out, "must_have_washing_machine", false) {
		out["amenities"] = appendAccommodationSearchAmenity(out["amenities"], "washing machine")
	}
	children := argIntSlice(out, "children_ages")
	adults := argInt(out, "adults", 0)
	if _, ok := out["guests"]; !ok && adults > 0 {
		out["guests"] = adults + len(children)
	}
	accommodationType := strings.TrimSpace(argString(out, "accommodation_type"))
	if accommodationType == "" {
		return out
	}
	normalizedType := accommodationTypeFromString(accommodationType)
	if _, ok := out["room_type"]; !ok {
		switch normalizedType {
		case models.AccommodationTypeEntireApartment:
			out["room_type"] = "entire_home"
		case models.AccommodationTypePrivateRoom:
			out["room_type"] = "private_room"
		case models.AccommodationTypeSharedRoom:
			out["room_type"] = "shared_room"
		case models.AccommodationTypeHotelRoom:
			out["room_type"] = "hotel_room"
		}
	}
	if _, ok := out["property_type"]; !ok {
		switch normalizedType {
		case models.AccommodationTypeEntireApartment:
			out["property_type"] = "apartment"
		case models.AccommodationTypeHostelBed:
			out["property_type"] = "hostel"
		case models.AccommodationTypeVilla:
			out["property_type"] = "villa"
		case models.AccommodationTypeHotelRoom:
			out["property_type"] = "hotel"
		}
	}
	return out
}

func enrichAccommodationNeedFromArgs(need models.AccommodationNeed, args map[string]any) models.AccommodationNeed {
	if adults := argInt(args, "adults", 0); adults > 0 {
		need.Adults = adults
	}
	if value := strings.TrimSpace(argString(args, "accommodation_type")); value != "" {
		need.AccommodationType = accommodationTypeFromString(value)
	}
	if preferred := argStringSlice(args, "preferred_amenities"); len(preferred) > 0 {
		need.PreferredAmenities = preferred
	}
	if neighborhoods := argStringSlice(args, "neighborhoods"); len(neighborhoods) > 0 {
		need.Neighborhoods = neighborhoods
	}
	if maxTotal := argFloat(args, "max_total_price", 0); maxTotal > 0 {
		need.MaxTotalPrice = maxTotal
	}
	if argBool(args, "must_have_washing_machine", false) {
		need.RequiredAmenities = appendUniqueStringMCP(need.RequiredAmenities, "washing machine")
	}
	return need
}

func appendAccommodationSearchAmenity(value any, amenity string) any {
	amenity = strings.TrimSpace(amenity)
	if amenity == "" {
		return value
	}
	switch current := value.(type) {
	case string:
		if strings.TrimSpace(current) == "" {
			return amenity
		}
		for _, part := range strings.Split(current, ",") {
			if strings.EqualFold(strings.TrimSpace(part), amenity) {
				return current
			}
		}
		return current + "," + amenity
	case []string:
		for _, part := range current {
			if strings.EqualFold(strings.TrimSpace(part), amenity) {
				return current
			}
		}
		return append(current, amenity)
	case []any:
		for _, part := range current {
			if s, ok := part.(string); ok && strings.EqualFold(strings.TrimSpace(s), amenity) {
				return current
			}
		}
		return append(current, amenity)
	default:
		return amenity
	}
}

func relaxAccommodationDiscoveryFilters(req hotelSearchRequest) hotelSearchRequest {
	// For need-first accommodation search, amenities must be judged against the
	// returned room/apartment evidence. Keeping them as broad hotel-search
	// post-filters drops providers that lack amenity metadata before we can
	// return a criteria_rejected offer with explicit missing/unknown fields.
	req.Options.Amenities = nil
	return req
}

func accommodationCandidateFromHotel(hotel models.HotelResult) accommodationCandidate {
	return accommodationCandidate{
		Name:            hotel.Name,
		HotelID:         hotel.HotelID,
		Rating:          hotel.Rating,
		ReviewCount:     hotel.ReviewCount,
		Stars:           hotel.Stars,
		LeadInPrice:     hotel.Price,
		Currency:        hotel.Currency,
		Address:         hotel.Address,
		PropertyType:    hotel.PropertyType,
		BookingURL:      hotel.BookingURL,
		PriceBasis:      hotel.PriceBasis,
		PriceConfidence: hotel.PriceConfidence,
		Freshness:       hotel.Freshness,
		PriceWarnings:   append([]string(nil), hotel.PriceWarnings...),
	}
}

func accommodationCandidateLimit(requested, available int) int {
	if available <= 0 {
		return 0
	}
	if requested <= 0 {
		requested = 5
	}
	if requested > 8 {
		requested = 8
	}
	if requested > available {
		return available
	}
	return requested
}

func selectAccommodationCandidateHotels(hotels []models.HotelResult, limit int, need models.AccommodationNeed) []models.HotelResult {
	if limit <= 0 || len(hotels) == 0 {
		return nil
	}
	candidates := append([]models.HotelResult(nil), hotels...)
	sort.SliceStable(candidates, func(i, j int) bool {
		leftScore := accommodationVerificationCandidateScore(candidates[i], need)
		rightScore := accommodationVerificationCandidateScore(candidates[j], need)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		leftPrice, rightPrice := candidates[i].Price, candidates[j].Price
		if leftPrice > 0 && rightPrice > 0 && leftPrice != rightPrice {
			return leftPrice < rightPrice
		}
		return false
	})
	if limit > len(candidates) {
		limit = len(candidates)
	}
	return candidates[:limit]
}

func accommodationVerificationCandidateScore(hotel models.HotelResult, need models.AccommodationNeed) int {
	score := 0
	if need.AccommodationType != "" {
		if evidenceType := hotelAccommodationEvidenceType(hotel); evidenceType != "" {
			if evidenceType == need.AccommodationType {
				score += 100
			} else {
				score -= 100
			}
		}
	}
	if accommodationHotelIDSupportsRoomLookup(hotel) {
		score += 80
	} else if strings.TrimSpace(hotel.HotelID) != "" {
		score += 10
	}
	if accommodationBookingURLSupportsRoomLookup(hotel.BookingURL) {
		score += 60
	} else if strings.TrimSpace(hotel.BookingURL) != "" {
		score += 15
	}
	bestRoomScore := 0
	for _, room := range hotel.RoomTypes {
		if roomScore := accommodationSearchRoomTrustScore(room); roomScore > bestRoomScore {
			bestRoomScore = roomScore
		}
	}
	score += bestRoomScore
	switch hotel.PriceConfidence {
	case models.PriceConfidenceVerified:
		score += 30
	case models.PriceConfidenceRoomLevel:
		score += 20
	case models.PriceConfidenceUnverified:
		score -= 20
	}
	switch hotel.PriceBasis {
	case models.PriceBasisTaxInclusiveTotal:
		score += 20
	case models.PriceBasisRoomTotal:
		score += 15
	case models.PriceBasisRoomNightly:
		score += 10
	case models.PriceBasisLeadIn:
		score -= 15
	}
	return score
}

func accommodationHotelIDSupportsRoomLookup(hotel models.HotelResult) bool {
	if strings.TrimSpace(hotel.HotelID) == "" {
		return false
	}
	provider := strings.ToLower(accommodationHotelProvider(hotel))
	if provider == "" || provider == "google_hotels" || provider == "google hotels" {
		return true
	}
	return len(hotel.Sources) == 0 && hotel.CheapestSource == ""
}

func accommodationBookingURLSupportsRoomLookup(rawURL string) bool {
	value := strings.ToLower(strings.TrimSpace(rawURL))
	return strings.Contains(value, "booking.com/")
}

func accommodationSearchRoomTrustScore(room models.Room) int {
	score := 0
	if room.Price > 0 || room.NightlyPrice > 0 || room.TotalPrice > 0 {
		score += 5
	}
	if room.Currency != "" {
		score += 3
	}
	if room.ProviderURL != "" {
		score += 5
	}
	switch room.MatchConfidence {
	case models.RoomInventoryMatchExact:
		score += 40
	case models.RoomInventoryMatchSimilar:
		score += 25
	case models.RoomInventoryMatchPropertyLevelOnly:
		score -= 35
	}
	switch room.PriceConfidence {
	case models.PriceConfidenceVerified:
		score += 30
	case models.PriceConfidenceRoomLevel:
		score += 20
	case models.PriceConfidenceUnverified:
		score -= 20
	}
	switch room.PriceBasis {
	case models.PriceBasisTaxInclusiveTotal:
		score += 20
	case models.PriceBasisRoomTotal:
		score += 15
	case models.PriceBasisRoomNightly:
		score += 10
	case models.PriceBasisLeadIn:
		score -= 15
	}
	return score
}

func accommodationOfferLimit(requested int) int {
	if requested <= 0 {
		return 12
	}
	if requested > 40 {
		return 40
	}
	return requested
}

func sortAccommodationOffers(offers []models.AccommodationOffer) {
	sort.SliceStable(offers, func(i, j int) bool {
		a, b := offers[i], offers[j]
		if a.FinalTripCostReadyStatus != b.FinalTripCostReadyStatus {
			return a.FinalTripCostReadyStatus
		}
		if a.BookingReadyStatus != b.BookingReadyStatus {
			return a.BookingReadyStatus
		}
		if a.CriteriaMatched != b.CriteriaMatched {
			return a.CriteriaMatched
		}
		if accommodationConfidenceRank(a.PriceConfidence) != accommodationConfidenceRank(b.PriceConfidence) {
			return accommodationConfidenceRank(a.PriceConfidence) > accommodationConfidenceRank(b.PriceConfidence)
		}
		if accommodationBasisRank(a.PriceBasis) != accommodationBasisRank(b.PriceBasis) {
			return accommodationBasisRank(a.PriceBasis) > accommodationBasisRank(b.PriceBasis)
		}
		ap, bp := accommodationComparablePrice(a), accommodationComparablePrice(b)
		if ap > 0 && bp > 0 && ap != bp {
			return ap < bp
		}
		return strings.ToLower(a.PropertyName+" "+a.RoomName) < strings.ToLower(b.PropertyName+" "+b.RoomName)
	})
}

func accommodationConfidenceRank(value string) int {
	switch value {
	case models.PriceConfidenceVerified:
		return 3
	case models.PriceConfidenceRoomLevel:
		return 2
	default:
		return 1
	}
}

func accommodationBasisRank(value string) int {
	switch value {
	case models.PriceBasisTaxInclusiveTotal:
		return 4
	case models.PriceBasisRoomTotal:
		return 3
	case models.PriceBasisRoomNightly:
		return 2
	default:
		return 1
	}
}

func accommodationComparablePrice(offer models.AccommodationOffer) float64 {
	if offer.TotalPrice > 0 {
		return offer.TotalPrice
	}
	return offer.NightlyPrice
}

func accommodationSearchWarnings(result *models.HotelSearchResult, offers, rejected []models.AccommodationOffer, evidence []models.AccommodationEvidence, candidateCount int) []string {
	warnings := []string{"lead_in_prices_are_candidates_only"}
	if note := result.Completeness.IncompleteNote(); note != "" {
		warnings = append(warnings, note)
	}
	if candidateCount > 0 && len(offers) == 0 {
		warnings = append(warnings, "no_criteria_matched_room_level_offers")
	}
	if len(rejected) > 0 {
		warnings = append(warnings, "some_room_level_offers_rejected_by_criteria")
	}
	if accommodationEvidenceHasWarning(evidence, models.AccommodationWarningPriceShock) {
		warnings = appendUniqueStringMCP(warnings, models.AccommodationWarningPriceShock)
	}
	return warnings
}

func reverifiedAccommodationWarnings(offers, rejected []models.AccommodationOffer, evidence []models.AccommodationEvidence, selectorSet bool) []string {
	warnings := []string{"reverified_before_booking_handoff"}
	if selectorSet && len(offers)+len(rejected) == 0 {
		warnings = append(warnings, "reverify_offer_not_found")
	}
	if len(offers) == 0 {
		warnings = append(warnings, "no_criteria_matched_room_level_offers")
	}
	if len(rejected) > 0 {
		warnings = append(warnings, "some_room_level_offers_rejected_by_criteria")
	}
	if accommodationEvidenceHasWarning(evidence, models.AccommodationWarningPriceShock) {
		warnings = appendUniqueStringMCP(warnings, models.AccommodationWarningPriceShock)
	}
	return warnings
}

func accommodationEvidenceHasWarning(evidence []models.AccommodationEvidence, want string) bool {
	for _, item := range evidence {
		for _, warning := range item.Warnings {
			if warning == want {
				return true
			}
		}
	}
	return false
}

func accommodationSearchSummary(resp accommodationSearchResponse) string {
	location := resp.Need.Location
	if location == "" {
		location = "requested location"
	}
	if !resp.Success || resp.TotalAvailable == 0 {
		if resp.Error != "" {
			return fmt.Sprintf("Accommodation search in %s failed: %s", location, resp.Error)
		}
		return fmt.Sprintf("No accommodation candidates found in %s.", location)
	}
	if resp.Count == 0 {
		return fmt.Sprintf("No criteria-matched accommodation offers found in %s after checking %d candidate properties. Discovery prices remain candidate-only; inspect rejected_offers and candidates for missing or unknown criteria.", location, resp.CandidateCount)
	}
	summary := fmt.Sprintf("Found %d criteria-matched accommodation offer%s in %s after checking %d candidate properties.",
		resp.Count, pluralSuffix(resp.Count), location, resp.CandidateCount)
	if resp.BookingReadyCount > 0 {
		summary += fmt.Sprintf(" %d booking-ready.", resp.BookingReadyCount)
	}
	if resp.FinalTripCostReadyCount > 0 {
		summary += fmt.Sprintf(" %d final-trip-cost-ready.", resp.FinalTripCostReadyCount)
	}
	best := resp.Offers[0]
	if price := accommodationComparablePrice(best); price > 0 {
		summary += fmt.Sprintf(" Best verified match: %s %.0f at %s (%s).", best.Currency, price, best.PropertyName, best.RoomName)
	}
	return summary
}
