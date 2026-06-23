package mcp

import (
	"fmt"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
)

func accommodationOffersFromRooms(hotel models.HotelResult, rooms []hotels.RoomType, need models.AccommodationNeed, checkedAt time.Time) []models.AccommodationOffer {
	if len(rooms) == 0 {
		return nil
	}
	groups := groupRoomsByInventoryIdentity(rooms, need, checkedAt)
	offers := make([]models.AccommodationOffer, 0, len(groups))
	for _, group := range groups {
		room := group.selectedRoom
		selectedQuote := selectedInventoryQuote(group.quotes)
		basis, confidence := roomPriceTrust(room)
		if selectedQuote.PriceBasis != "" {
			basis = selectedQuote.PriceBasis
		}
		if selectedQuote.PriceConfidence != "" {
			confidence = selectedQuote.PriceConfidence
		}
		occupancyAdults, occupancyChildren := roomOccupancyEvidence(room, need)
		offer := models.AccommodationOffer{
			PropertyName:          hotel.Name,
			PropertyID:            hotel.HotelID,
			OfferID:               offerIDForRoomGroup(hotel, room),
			AccommodationType:     offerAccommodationType(hotel, need),
			RoomName:              canonicalRoomName(room),
			Provider:              firstNonEmpty(selectedQuote.Provider, room.Provider),
			ProviderURL:           firstNonEmpty(selectedQuote.ProviderURL, room.ProviderURL, hotel.BookingURL),
			OccupancyAdults:       occupancyAdults,
			OccupancyChildren:     occupancyChildren,
			Rooms:                 need.Rooms,
			Amenities:             append([]string(nil), room.Amenities...),
			NightlyPrice:          selectedQuote.NightlyPrice,
			TotalPrice:            selectedQuote.TotalPrice,
			TaxesAndFees:          selectedQuote.TaxesAndFees,
			TaxesFeesIncluded:     selectedQuote.TaxesFeesIncluded,
			Currency:              selectedQuote.Currency,
			PriceBasis:            basis,
			PriceConfidence:       confidence,
			CheckedAt:             checkedAt,
			Freshness:             firstNonEmpty(selectedQuote.Freshness, models.ClassifyFreshness(firstNonEmpty(selectedQuote.Provider, room.Provider), checkedAt, checkedAt)),
			CancellationPolicy:    selectedQuote.CancellationPolicy,
			Refundable:            selectedQuote.Refundable,
			FreeCancellation:      selectedQuote.FreeCancellation,
			Board:                 selectedQuote.Board,
			BreakfastIncluded:     selectedQuote.BreakfastIncluded,
			InventoryCompleteness: inventoryCompleteness(group.quotes),
			InventoryOptions:      group.quotes,
		}
		offer = normalizeOfferCurrency(offer, need.Currency)
		offers = append(offers, models.EvaluateAccommodationOffer(need, offer, checkedAt))
	}
	return offers
}

// normalizeOfferCurrency converts an offer's price fields into the requested
// currency using live FX rates (ECB via Frankfurter, with hardcoded
// fallbacks). Providers such as HousingAnywhere occasionally return a foreign
// currency (USD) even when the request asked for EUR; without this the
// criteria evaluator rejects the offer for missing_criteria:["currency"] even
// though the price is otherwise usable. Conversion only happens when a rate is
// available — if none is, the offer keeps its original currency and the
// evaluator still flags it, so we never relabel a price we could not convert.
func normalizeOfferCurrency(offer models.AccommodationOffer, want string) models.AccommodationOffer {
	want = strings.ToUpper(strings.TrimSpace(want))
	have := strings.ToUpper(strings.TrimSpace(offer.Currency))
	if want == "" || have == "" || have == want {
		return offer
	}
	rate, ok := providers.ConvertRate(have, want)
	if !ok {
		return offer
	}
	offer.NightlyPrice *= rate
	offer.TotalPrice *= rate
	offer.TaxesAndFees *= rate
	offer.Currency = want
	offer.Warnings = appendUniqueStringMCP(offer.Warnings, "currency_normalized")
	return offer
}

type roomInventoryGroup struct {
	key          string
	selectedRoom hotels.RoomType
	quotes       []models.RoomInventoryQuote
}

func groupRoomsByInventoryIdentity(rooms []hotels.RoomType, need models.AccommodationNeed, checkedAt time.Time) []roomInventoryGroup {
	groups := make([]roomInventoryGroup, 0, len(rooms))
	byKey := make(map[string]int, len(rooms))
	for _, room := range rooms {
		if comparableRoomPrice(room) <= 0 || room.Currency == "" {
			continue
		}
		key := canonicalRoomKey(room)
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(room.Provider + "|" + room.Name))
		}
		quotes := roomInventoryQuotes(room, need, checkedAt)
		if len(quotes) == 0 {
			continue
		}
		if idx, ok := byKey[key]; ok {
			group := &groups[idx]
			group.quotes = appendRoomInventoryQuotes(group.quotes, quotes...)
			if roomInventoryRoomRank(room) > roomInventoryRoomRank(group.selectedRoom) ||
				(roomInventoryRoomRank(room) == roomInventoryRoomRank(group.selectedRoom) && comparableRoomPrice(room) < comparableRoomPrice(group.selectedRoom)) {
				group.selectedRoom = room
			}
			continue
		}
		byKey[key] = len(groups)
		groups = append(groups, roomInventoryGroup{
			key:          key,
			selectedRoom: room,
			quotes:       appendRoomInventoryQuotes(nil, quotes...),
		})
	}
	return groups
}

func roomInventoryQuotes(room hotels.RoomType, need models.AccommodationNeed, checkedAt time.Time) []models.RoomInventoryQuote {
	if len(room.InventoryOptions) > 0 {
		out := make([]models.RoomInventoryQuote, 0, len(room.InventoryOptions))
		for _, quote := range room.InventoryOptions {
			out = append(out, completeRoomInventoryQuote(quote, room, need, checkedAt))
		}
		return out
	}
	return []models.RoomInventoryQuote{roomInventoryQuoteFromRoom(room, need, checkedAt)}
}

func roomInventoryQuoteFromRoom(room hotels.RoomType, need models.AccommodationNeed, checkedAt time.Time) models.RoomInventoryQuote {
	basis, confidence := roomPriceTrust(room)
	occupancyAdults, occupancyChildren := roomOccupancyEvidence(room, need)
	return completeRoomInventoryQuote(models.RoomInventoryQuote{
		Provider:           room.Provider,
		ProviderRoomName:   room.Name,
		ProviderRateName:   room.RatePlanName,
		ProviderURL:        room.ProviderURL,
		RateID:             room.RateID,
		MatchConfidence:    room.MatchConfidence,
		NightlyPrice:       roomNightlyPrice(room),
		TotalPrice:         room.TotalPrice,
		TaxesAndFees:       room.TaxesAndFees,
		TaxesFeesIncluded:  room.TaxesFeesIncluded,
		Currency:           room.Currency,
		Refundable:         room.Refundable,
		FreeCancellation:   room.FreeCancellation,
		CancellationPolicy: room.CancellationPolicy,
		Board:              room.Board,
		BreakfastIncluded:  room.BreakfastIncluded,
		OccupancyAdults:    occupancyAdults,
		OccupancyChildren:  occupancyChildren,
		Rooms:              need.Rooms,
		PriceBasis:         basis,
		PriceConfidence:    confidence,
		CheckedAt:          checkedAt,
		Freshness:          models.ClassifyFreshness(room.Provider, checkedAt, checkedAt),
	}, room, need, checkedAt)
}

func completeRoomInventoryQuote(quote models.RoomInventoryQuote, room hotels.RoomType, need models.AccommodationNeed, checkedAt time.Time) models.RoomInventoryQuote {
	if quote.Provider == "" {
		quote.Provider = room.Provider
	}
	if quote.ProviderRoomName == "" {
		quote.ProviderRoomName = room.Name
	}
	if quote.ProviderURL == "" {
		quote.ProviderURL = room.ProviderURL
	}
	if quote.MatchConfidence == "" {
		quote.MatchConfidence = roomMatchConfidence(room)
	}
	if quote.NightlyPrice == 0 {
		quote.NightlyPrice = roomNightlyPrice(room)
	}
	if quote.TotalPrice == 0 {
		quote.TotalPrice = room.TotalPrice
	}
	// Property-level rooms expose only a lead-in figure in room.Price, which is
	// not a nightly rate (roomNightlyPrice returns 0 for them). Preserve that
	// figure as a property-level total so the quote retains a price without
	// claiming a fabricated nightly rate; PriceBasis stays lead_in/unverified.
	if quote.TotalPrice == 0 && quote.NightlyPrice == 0 && room.Price > 0 &&
		roomMatchConfidence(room) == models.RoomInventoryMatchPropertyLevelOnly {
		quote.TotalPrice = room.Price
	}
	if quote.TaxesAndFees == 0 {
		quote.TaxesAndFees = room.TaxesAndFees
	}
	if quote.TaxesFeesIncluded == nil {
		quote.TaxesFeesIncluded = room.TaxesFeesIncluded
	}
	if quote.Currency == "" {
		quote.Currency = room.Currency
	}
	if quote.Refundable == nil {
		quote.Refundable = room.Refundable
	}
	if quote.FreeCancellation == nil {
		quote.FreeCancellation = room.FreeCancellation
	}
	if quote.CancellationPolicy == "" {
		quote.CancellationPolicy = room.CancellationPolicy
	}
	if quote.Board == "" {
		quote.Board = room.Board
	}
	if quote.BreakfastIncluded == nil {
		quote.BreakfastIncluded = room.BreakfastIncluded
	}
	if quote.OccupancyAdults == 0 && len(quote.OccupancyChildren) == 0 {
		quote.OccupancyAdults, quote.OccupancyChildren = roomOccupancyEvidence(room, need)
	}
	if quote.Rooms == 0 {
		quote.Rooms = need.Rooms
	}
	if quote.PriceBasis == "" || quote.PriceConfidence == "" {
		quote.PriceBasis, quote.PriceConfidence = roomPriceTrust(room)
	}
	if quote.CheckedAt.IsZero() {
		quote.CheckedAt = checkedAt
	}
	if quote.Freshness == "" && !quote.CheckedAt.IsZero() {
		quote.Freshness = models.ClassifyFreshness(quote.Provider, quote.CheckedAt, checkedAt)
	}
	return quote
}

func appendRoomInventoryQuotes(existing []models.RoomInventoryQuote, quotes ...models.RoomInventoryQuote) []models.RoomInventoryQuote {
	for _, quote := range quotes {
		if roomInventoryQuotePrice(quote) <= 0 || quote.Currency == "" {
			continue
		}
		key := roomInventoryQuoteKey(quote)
		duplicate := false
		for _, current := range existing {
			if roomInventoryQuoteKey(current) == key {
				duplicate = true
				break
			}
		}
		if !duplicate {
			existing = append(existing, quote)
		}
	}
	return existing
}

func selectedInventoryQuote(quotes []models.RoomInventoryQuote) models.RoomInventoryQuote {
	if len(quotes) == 0 {
		return models.RoomInventoryQuote{}
	}
	selected := quotes[0]
	for _, quote := range quotes[1:] {
		if roomInventoryQuoteRank(quote) > roomInventoryQuoteRank(selected) {
			selected = quote
			continue
		}
		if roomInventoryQuoteRank(quote) == roomInventoryQuoteRank(selected) {
			qp, sp := roomInventoryQuotePrice(quote), roomInventoryQuotePrice(selected)
			if qp > 0 && (sp == 0 || qp < sp) {
				selected = quote
			}
		}
	}
	return selected
}

func inventoryCompleteness(quotes []models.RoomInventoryQuote) string {
	if len(quotes) == 0 {
		return models.RoomInventoryCompletenessNoProviderInventory
	}
	propertyLevel := 0
	exact := 0
	for _, quote := range quotes {
		switch quote.MatchConfidence {
		case models.RoomInventoryMatchPropertyLevelOnly:
			propertyLevel++
		case models.RoomInventoryMatchExact:
			exact++
		}
	}
	if propertyLevel == len(quotes) {
		return models.RoomInventoryCompletenessPropertyLevelOnly
	}
	if len(quotes) == 1 {
		return models.RoomInventoryCompletenessSingleProvider
	}
	if exact == len(quotes) {
		return models.RoomInventoryCompletenessMultiProviderExact
	}
	return models.RoomInventoryCompletenessMultiProviderMixed
}

func canonicalRoomKey(room hotels.RoomType) string {
	name := strings.ToLower(strings.Join(strings.Fields(room.Name), " "))
	if name == "" {
		return ""
	}
	if room.MatchConfidence == models.RoomInventoryMatchPropertyLevelOnly {
		return "property|" + name
	}
	parts := []string{"room", name}
	if bed := strings.ToLower(strings.Join(strings.Fields(room.BedType), " ")); bed != "" {
		parts = append(parts, "bed:"+bed)
	}
	if room.MaxGuests > 0 {
		parts = append(parts, fmt.Sprintf("guests:%d", room.MaxGuests))
	}
	return strings.Join(parts, "|")
}

func canonicalRoomName(room hotels.RoomType) string {
	if strings.TrimSpace(room.Name) != "" {
		return strings.TrimSpace(room.Name)
	}
	return "Accommodation option"
}

func roomMatchConfidence(room hotels.RoomType) string {
	if strings.TrimSpace(room.MatchConfidence) != "" {
		return room.MatchConfidence
	}
	return models.RoomInventoryMatchExact
}

func roomInventoryRoomRank(room hotels.RoomType) int {
	basis, confidence := roomPriceTrust(room)
	return roomInventoryPriceRank(basis, confidence, roomMatchConfidence(room))
}

func roomInventoryQuoteRank(quote models.RoomInventoryQuote) int {
	return roomInventoryPriceRank(quote.PriceBasis, quote.PriceConfidence, quote.MatchConfidence)
}

func roomInventoryPriceRank(basis, confidence, matchConfidence string) int {
	rank := 0
	switch matchConfidence {
	case models.RoomInventoryMatchExact:
		rank += 30
	case models.RoomInventoryMatchSimilar:
		rank += 20
	case models.RoomInventoryMatchPropertyLevelOnly:
		rank += 0
	default:
		rank += 10
	}
	switch confidence {
	case models.PriceConfidenceVerified:
		rank += 12
	case models.PriceConfidenceRoomLevel:
		rank += 8
	case models.PriceConfidenceUnverified:
		rank += 0
	}
	switch basis {
	case models.PriceBasisTaxInclusiveTotal:
		rank += 4
	case models.PriceBasisRoomTotal:
		rank += 3
	case models.PriceBasisRoomNightly:
		rank += 2
	}
	return rank
}

func roomInventoryQuotePrice(quote models.RoomInventoryQuote) float64 {
	if quote.TotalPrice > 0 {
		return quote.TotalPrice
	}
	return quote.NightlyPrice
}

func roomInventoryQuoteKey(quote models.RoomInventoryQuote) string {
	return strings.ToLower(strings.Join([]string{
		quote.Provider,
		quote.ProviderRoomName,
		quote.ProviderRateName,
		quote.Currency,
		fmt.Sprintf("%.2f", quote.NightlyPrice),
		fmt.Sprintf("%.2f", quote.TotalPrice),
		quote.CancellationPolicy,
	}, "|"))
}

func adultsFromGuests(guests int, childrenAges []int) int {
	if guests <= 0 {
		return 0
	}
	adults := guests - len(childrenAges)
	if adults <= 0 {
		return guests
	}
	return adults
}

func accommodationTypeFromHotelOptions(opts hotels.HotelSearchOptions) string {
	if opts.RoomType != "" {
		return accommodationTypeFromString(opts.RoomType)
	}
	if opts.PropertyType != "" {
		return accommodationTypeFromString(opts.PropertyType)
	}
	return ""
}

func accommodationTypeFromString(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "hotel", "hotel_room", "hotel room":
		return models.AccommodationTypeHotelRoom
	case "apartment", "entire_home", "entire home", "entire_apartment", "vacation_rental":
		return models.AccommodationTypeEntireApartment
	case "private", "private_room", "private room":
		return models.AccommodationTypePrivateRoom
	case "shared", "shared_room", "shared room":
		return models.AccommodationTypeSharedRoom
	case "hostel", "hostel_bed", "hostel bed":
		return models.AccommodationTypeHostelBed
	case "villa":
		return models.AccommodationTypeVilla
	default:
		// Honest default: an unrecognized or missing type string maps to ""
		// (not evidenced), never a guessed "hotel_room". Callers decide what an
		// empty type means; none of them fabricate a match from it.
		return ""
	}
}

func offerAccommodationType(hotel models.HotelResult, need models.AccommodationNeed) string {
	if evidenceType := hotelAccommodationEvidenceType(hotel); evidenceType != "" {
		return evidenceType
	}
	if need.AccommodationType != "" {
		return need.AccommodationType
	}
	// No property-type evidence and the caller stated no preference: leave the
	// type empty rather than guessing "hotel_room". An unspecified field means
	// all types are allowed, and a missing value must read as missing.
	// EvaluateAccommodationOffer reports "" honestly as a not-evidenced field
	// instead of a fabricated match.
	return ""
}

func hotelAccommodationEvidenceType(hotel models.HotelResult) string {
	if hotel.PropertyType != "" {
		return accommodationTypeFromString(hotel.PropertyType)
	}
	if inferred := accommodationTypeFromString(models.InferHotelPropertyType(hotel)); inferred != "" && inferred != "unknown" {
		return inferred
	}
	return ""
}

func roomPriceTrust(room hotels.RoomType) (string, string) {
	basis := models.PriceBasisRoomNightly
	confidence := models.PriceConfidenceRoomLevel
	if roomMatchConfidence(room) == models.RoomInventoryMatchPropertyLevelOnly {
		return models.PriceBasisLeadIn, models.PriceConfidenceUnverified
	}
	if room.TotalPrice > 0 {
		basis = models.PriceBasisRoomTotal
		if room.TaxesFeesIncluded != nil && *room.TaxesFeesIncluded {
			basis = models.PriceBasisTaxInclusiveTotal
			confidence = models.PriceConfidenceVerified
		}
	}
	return basis, confidence
}

func roomNightlyPrice(room hotels.RoomType) float64 {
	if room.NightlyPrice > 0 {
		return room.NightlyPrice
	}
	// Property-level lead-in prices are NOT nightly rates — e.g. a HousingAnywhere
	// mid-term rental's room.Price is monthly rent (issue #277). Presenting it as a
	// per-night figure fabricated a ~30x inflated nightly rate. Return 0 (no nightly
	// claim) for property-level rooms; the lead-in figure stays in TotalPrice.
	if roomMatchConfidence(room) == models.RoomInventoryMatchPropertyLevelOnly {
		return 0
	}
	return room.Price
}

func roomOccupancyEvidence(room hotels.RoomType, need models.AccommodationNeed) (int, []int) {
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

func offerIDForRoomGroup(hotel models.HotelResult, room hotels.RoomType) string {
	id := strings.TrimSpace(hotel.HotelID)
	if id == "" {
		id = strings.TrimSpace(hotel.Name)
	}
	return strings.ToLower(strings.Join([]string{id, canonicalRoomKey(room)}, "|"))
}

func newHotelDetailError(scope, code string, err error) hotelDetailError {
	return hotelDetailError{
		Scope:   scope,
		Code:    code,
		Message: fmt.Sprintf("%s: %v", scope, err),
	}
}

func verifiedRateFromRooms(hotel models.HotelResult, rooms []hotels.RoomType, checkedAt time.Time) *hotelVerifiedRate {
	if checkedAt.IsZero() {
		checkedAt = time.Now()
	}
	var selected *hotels.RoomType
	selectedComparable := 0.0
	for i := range rooms {
		room := &rooms[i]
		comparable := comparableRoomPrice(*room)
		if comparable <= 0 || room.Currency == "" {
			continue
		}
		if selected == nil || comparable < selectedComparable {
			selected = room
			selectedComparable = comparable
		}
	}
	if selected == nil {
		return nil
	}

	basis, confidence := roomPriceTrust(*selected)
	nightly := selected.NightlyPrice
	if nightly == 0 {
		nightly = selected.Price
	}
	return &hotelVerifiedRate{
		Provider:           selected.Provider,
		RoomName:           selected.Name,
		Price:              selectedComparable,
		NightlyPrice:       nightly,
		TotalPrice:         selected.TotalPrice,
		TaxesAndFees:       selected.TaxesAndFees,
		TaxesFeesIncluded:  selected.TaxesFeesIncluded,
		Currency:           selected.Currency,
		BookingURL:         firstNonEmpty(selected.ProviderURL, hotel.BookingURL),
		PriceBasis:         basis,
		PriceConfidence:    confidence,
		RetrievedAt:        checkedAt,
		Freshness:          models.ClassifyFreshness(selected.Provider, checkedAt, checkedAt),
		CancellationPolicy: selected.CancellationPolicy,
		Board:              selected.Board,
		BreakfastIncluded:  selected.BreakfastIncluded,
		Refundable:         selected.Refundable,
		FreeCancellation:   selected.FreeCancellation,
	}
}

func comparableRoomPrice(room hotels.RoomType) float64 {
	if room.TotalPrice > 0 {
		return room.TotalPrice
	}
	if room.NightlyPrice > 0 {
		return room.NightlyPrice
	}
	return room.Price
}
