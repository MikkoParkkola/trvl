package mcp

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

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
	if stars := argInt(args, "min_stars", argInt(args, "stars", 0)); stars > 0 {
		need.MinStars = stars
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
	candidates := make([]models.HotelResult, 0, len(hotels))
	for _, hotel := range hotels {
		// Enforce min_stars in stage-2 selection. Only drop properties whose
		// star rating is known AND below the minimum; properties with an
		// unrated stars value (0) are kept because we cannot prove they fail
		// the filter — over-pruning would silently hide valid candidates such
		// as apartments that legitimately carry no star rating.
		if need.MinStars > 0 && hotel.Stars > 0 && hotel.Stars < need.MinStars {
			continue
		}
		candidates = append(candidates, hotel)
	}
	if len(candidates) == 0 {
		return nil
	}
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
