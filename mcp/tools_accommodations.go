package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
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
	Destination             *models.DestinationInfo        `json:"destination,omitempty"`
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
	// accommodationRoomLookupTimeout bounds one candidate's room-level fetch
	// during stage-2 selection. The Booking.com path is the only source of
	// exact_room_match prices, and behind Booking's WAF it needs >=2 HTTP
	// round-trips (initial 202 challenge + cookie re-harvest retry). The old
	// 20s budget equalled a single batchexec request timeout, so it could
	// never complete the WAF dance — every WAF-gated hotel degraded to
	// property_level_only and booking_ready_count stayed 0 (issue #277
	// defect 2). Match the reverify budget so the first pass can also reach a
	// room-level price. Per-candidate lookups run concurrently (worker pool),
	// so this lifts wall-clock by ~one timeout, not N, and the outer tool
	// deadline (toolTimeout / caller ctx) still clips it when the budget is
	// already spent on search.
	accommodationRoomLookupTimeout         = 45 * time.Second
	accommodationReverifyRoomLookupTimeout = 45 * time.Second
)

// accommodationVerifyResult holds one candidate hotel's room-level verification
// output, collected by a worker so the shortlist can be verified concurrently
// while the final merge preserves deterministic candidate order.
type accommodationVerifyResult struct {
	candidate accommodationCandidate
	offers    []models.AccommodationOffer
	rejected  []models.AccommodationOffer
	evidence  []models.AccommodationEvidence
}

// accommodationVerifyConcurrency bounds the parallel candidate-verification
// worker pool. Room lookups are network-bound, so a small fixed cap keeps peak
// goroutines/sockets predictable without re-introducing serial latency.
func accommodationVerifyConcurrency(n int) int {
	const maxWorkers = 6
	if n < maxWorkers {
		return n
	}
	return maxWorkers
}

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
	perHotel := make([]accommodationVerifyResult, len(candidateHotels))
	if total := len(candidateHotels); total > 0 {
		work := make(chan int)
		var wg sync.WaitGroup
		go func() {
			defer close(work)
			for i := range candidateHotels {
				select {
				case work <- i:
				case <-ctx.Done():
					return
				}
			}
		}()
		workers := accommodationVerifyConcurrency(total)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range work {
					hotel := candidateHotels[i]
					var res accommodationVerifyResult
					res.candidate = accommodationCandidateFromHotel(hotel)
					checkedAt := time.Now()
					searchInventoryRooms := roomTypesFromHotelSearchInventory(hotel, need, checkedAt)
					if hotel.HotelID == "" {
						if len(searchInventoryRooms) > 0 {
							roomOffers := accommodationOffersFromRooms(hotel, searchInventoryRooms, need, checkedAt)
							res.candidate.OfferCount = len(roomOffers)
							for _, offer := range roomOffers {
								offer = withAccommodationPriceShockWarning(hotel, offer)
								if offer.CriteriaMatched {
									res.candidate.MatchingOfferCount++
									res.offers = append(res.offers, offer)
									if offer.BookingReadyStatus {
										res.candidate.BookingReadyOfferCount++
									}
									res.evidence = append(res.evidence, accommodationOfferEvidence(need, hotel, offer, "criteria_matched"))
								} else if includeUnmatched {
									res.rejected = append(res.rejected, offer)
									res.evidence = append(res.evidence, accommodationOfferEvidence(need, hotel, offer, "criteria_rejected"))
								}
							}
						} else {
							res.candidate.DetailErrors = append(res.candidate.DetailErrors, hotelDetailError{
								Scope:   "hotel",
								Code:    "missing_hotel_id",
								Message: "missing hotel_id; cannot verify room-level accommodation offers",
							})
							res.evidence = append(res.evidence, accommodationCandidateEvidence(need, hotel, res.candidate, "missing_hotel_id", checkedAt))
						}
						perHotel[i] = res
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
						BookingURL:   accommodationRoomLookupBookingURL(hotel),
						Location:     req.Location,
					})
					cancelRoomLookup()
					if err != nil {
						res.candidate.DetailErrors = append(res.candidate.DetailErrors, newHotelDetailError("rooms", "rooms_fetch_failed", err))
						res.evidence = append(res.evidence, accommodationCandidateEvidence(need, hotel, res.candidate, "rooms_fetch_failed", checkedAt))
						if len(searchInventoryRooms) == 0 {
							perHotel[i] = res
							continue
						}
					}
					rooms := searchInventoryRooms
					if availability != nil {
						rooms = mergeDetailAndSearchInventoryRooms(availability.Rooms, searchInventoryRooms)
					}
					if len(rooms) > 0 {
						roomOffers := accommodationOffersFromRooms(hotel, rooms, need, checkedAt)
						res.candidate.OfferCount = len(roomOffers)
						for _, offer := range roomOffers {
							offer = withAccommodationPriceShockWarning(hotel, offer)
							if offer.CriteriaMatched {
								res.candidate.MatchingOfferCount++
								res.offers = append(res.offers, offer)
								if offer.BookingReadyStatus {
									res.candidate.BookingReadyOfferCount++
								}
								res.evidence = append(res.evidence, accommodationOfferEvidence(need, hotel, offer, "criteria_matched"))
							} else if includeUnmatched {
								res.rejected = append(res.rejected, offer)
								res.evidence = append(res.evidence, accommodationOfferEvidence(need, hotel, offer, "criteria_rejected"))
							}
						}
						if len(roomOffers) == 0 {
							res.evidence = append(res.evidence, accommodationCandidateEvidence(need, hotel, res.candidate, "no_room_offers", checkedAt))
						}
					}
					perHotel[i] = res
				}
			}()
		}
		wg.Wait()
	}
	for _, res := range perHotel {
		offers = append(offers, res.offers...)
		rejected = append(rejected, res.rejected...)
		evidence = append(evidence, res.evidence...)
		if includeCandidates {
			candidates = append(candidates, res.candidate)
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
	// Best-effort destination intelligence on the default search path: a plain
	// hotel search returns weather, safety, holidays, currency, and country
	// facts inline, with no extra switch. Silent degrade — never blocks search.
	resp.Destination = enrichDestination(ctx, req.Location, models.DateRange{CheckIn: req.CheckIn, CheckOut: req.CheckOut})
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
		BookingURL:   accommodationRoomLookupBookingURL(hotel),
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
