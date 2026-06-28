package models

import (
	"testing"
	"time"
)

func TestEvaluateAccommodationOfferRejectsLeadInAndUnknownRefundability(t *testing.T) {
	need := AccommodationNeed{
		CheckIn:            "2026-08-10",
		CheckOut:           "2026-08-17",
		Adults:             2,
		ChildrenAges:       []int{7},
		Rooms:              1,
		AccommodationType:  AccommodationTypeEntireApartment,
		RequiredAmenities:  []string{"kitchen", "washing machine"},
		RefundableRequired: true,
		Currency:           "EUR",
	}
	checkedAt := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	offer := EvaluateAccommodationOffer(need, AccommodationOffer{
		PropertyName:      "Central Flat",
		AccommodationType: AccommodationTypeEntireApartment,
		RoomName:          "Two-bedroom apartment",
		OccupancyAdults:   2,
		OccupancyChildren: []int{7},
		Rooms:             1,
		Amenities:         []string{"Kitchen", "Washing machine", "WiFi"},
		TotalPrice:        700,
		Currency:          "EUR",
		PriceBasis:        PriceBasisLeadIn,
		PriceConfidence:   PriceConfidenceUnverified,
		CheckedAt:         checkedAt,
	}, checkedAt)

	if offer.CriteriaMatched {
		t.Fatalf("CriteriaMatched = true, want false because refundability is unknown")
	}
	if offer.BookingReady() {
		t.Fatal("BookingReady = true, want false for lead-in price")
	}
	if offer.FinalTripCostReady() {
		t.Fatal("FinalTripCostReady = true, want false for lead-in price")
	}
	if offer.BookingOrderHint != BookingOrderNeedsRefundabilityCheck {
		t.Fatalf("BookingOrderHint = %q, want %q", offer.BookingOrderHint, BookingOrderNeedsRefundabilityCheck)
	}
	if !containsString(offer.UnknownCriteria, "refundability") {
		t.Fatalf("UnknownCriteria = %v, want refundability", offer.UnknownCriteria)
	}
	if !containsString(offer.UnknownCriteria, "room_inventory") {
		t.Fatalf("UnknownCriteria = %v, want room_inventory", offer.UnknownCriteria)
	}
	if !containsString(offer.Warnings, AccommodationWarningLeadInOnly) {
		t.Fatalf("Warnings = %v, want lead-in warning", offer.Warnings)
	}
}

func TestEvaluateAccommodationOfferAllowsFlightsFirstForRefundableMatchedOffer(t *testing.T) {
	refundable := true
	taxesIncluded := true
	need := AccommodationNeed{
		CheckIn:            "2026-08-10",
		CheckOut:           "2026-08-17",
		Adults:             2,
		AccommodationType:  AccommodationTypeHotelRoom,
		RequiredAmenities:  []string{"wifi"},
		RefundableRequired: true,
		Currency:           "EUR",
	}
	checkedAt := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	offer := EvaluateAccommodationOffer(need, AccommodationOffer{
		PropertyName:      "Hotel One",
		AccommodationType: AccommodationTypeHotelRoom,
		RoomName:          "Flexible double room",
		OccupancyAdults:   2,
		Amenities:         []string{"Free WiFi", "Desk"},
		TotalPrice:        840,
		TaxesFeesIncluded: &taxesIncluded,
		Currency:          "EUR",
		PriceBasis:        PriceBasisTaxInclusiveTotal,
		PriceConfidence:   PriceConfidenceVerified,
		CheckedAt:         checkedAt,
		Refundable:        &refundable,
	}, checkedAt)

	if !offer.CriteriaMatched {
		t.Fatalf("CriteriaMatched = false, missing=%v unknown=%v", offer.MissingCriteria, offer.UnknownCriteria)
	}
	if !offer.BookingReady() {
		t.Fatalf("BookingReady = false, warnings=%v", offer.Warnings)
	}
	if !offer.FinalTripCostReady() {
		t.Fatal("FinalTripCostReady = false, want true for verified tax-inclusive matched offer")
	}
	if offer.BookingOrderHint != BookingOrderFlightsFirstOK {
		t.Fatalf("BookingOrderHint = %q, want %q", offer.BookingOrderHint, BookingOrderFlightsFirstOK)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestEvaluateAccommodationOfferFlagsMultiProviderMixedInventory(t *testing.T) {
	need := AccommodationNeed{Adults: 2, Currency: "EUR"}
	checkedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	offer := EvaluateAccommodationOffer(need, AccommodationOffer{
		PropertyName:          "Mixed Inn",
		OccupancyAdults:       2,
		TotalPrice:            420,
		Currency:              "EUR",
		PriceBasis:            PriceBasisRoomTotal,
		PriceConfidence:       PriceConfidenceRoomLevel,
		InventoryCompleteness: RoomInventoryCompletenessMultiProviderMixed,
		CheckedAt:             checkedAt,
	}, checkedAt)
	if !containsString(offer.UnknownCriteria, "room_inventory") {
		t.Fatalf("UnknownCriteria = %v, want room_inventory for multi_provider_mixed", offer.UnknownCriteria)
	}
}

func TestEvaluateAccommodationOfferDoesNotFabricateFreshnessForUnverified(t *testing.T) {
	need := AccommodationNeed{Adults: 2, Currency: "EUR"}
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	offer := EvaluateAccommodationOffer(need, AccommodationOffer{
		PropertyName:    "Lead-In Lodge",
		OccupancyAdults: 2,
		TotalPrice:      300,
		Currency:        "EUR",
		PriceBasis:      PriceBasisLeadIn,
		PriceConfidence: PriceConfidenceUnverified,
	}, now)
	if !offer.CheckedAt.IsZero() {
		t.Fatalf("CheckedAt = %v, want zero (no fabricated freshness for unverified price)", offer.CheckedAt)
	}
	if offer.Freshness != "" {
		t.Fatalf("Freshness = %q, want empty for an unverified price with no check time", offer.Freshness)
	}
	if offer.BookingReady() {
		t.Fatal("BookingReady = true, want false for unverified price")
	}
}

func TestEvaluateAccommodationOfferStampsVerifiedPrice(t *testing.T) {
	need := AccommodationNeed{Adults: 2, Currency: "EUR"}
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	offer := EvaluateAccommodationOffer(need, AccommodationOffer{
		PropertyName:    "Verified Villa",
		OccupancyAdults: 2,
		TotalPrice:      300,
		Currency:        "EUR",
		PriceBasis:      PriceBasisRoomTotal,
		PriceConfidence: PriceConfidenceVerified,
	}, now)
	if offer.CheckedAt.IsZero() {
		t.Fatal("CheckedAt = zero, want stamped for a verified price")
	}
}

func TestAccommodationPriceTrustHelpers(t *testing.T) {
	if HotelPriceEligibleForFinalTripCost(HotelResult{Price: 0}) {
		t.Fatal("zero hotel price should not be final-trip eligible")
	}
	if !HotelPriceEligibleForFinalTripCost(HotelResult{Price: 120}) {
		t.Fatal("legacy hotel price with no trust fields should remain eligible")
	}
	if HotelPriceEligibleForFinalTripCost(HotelResult{Price: 120, PriceBasis: PriceBasisLeadIn}) {
		t.Fatal("lead-in hotel price should not be final-trip eligible")
	}
	if HotelPriceEligibleForFinalTripCost(HotelResult{Price: 120, PriceBasis: PriceBasisRoomTotal, PriceConfidence: PriceConfidenceUnverified}) {
		t.Fatal("unverified hotel price should not be final-trip eligible")
	}
	if !HotelPriceEligibleForFinalTripCost(HotelResult{Price: 120, PriceBasis: PriceBasisTaxInclusiveTotal, PriceConfidence: PriceConfidenceVerified}) {
		t.Fatal("verified tax-inclusive hotel price should be final-trip eligible")
	}
	if HotelPriceEligibleForFinalTripCost(HotelResult{Price: 120, PriceBasis: "unknown", PriceConfidence: PriceConfidenceVerified}) {
		t.Fatal("unknown hotel price basis should not be final-trip eligible")
	}

	if comparableAccommodationPrice(AccommodationOffer{TotalPrice: 300, NightlyPrice: 100}) != 300 {
		t.Fatal("total accommodation price should win")
	}
	if comparableAccommodationPrice(AccommodationOffer{NightlyPrice: 100}) != 100 {
		t.Fatal("nightly accommodation price should be fallback")
	}
	if !needsWholeUnitEvidence(AccommodationTypeEntireApartment) || !needsWholeUnitEvidence(AccommodationTypeVilla) {
		t.Fatal("whole-unit accommodation types should need whole-unit evidence")
	}
	if needsWholeUnitEvidence(AccommodationTypeHotelRoom) {
		t.Fatal("hotel rooms should not need whole-unit evidence")
	}
}

func TestHotelPriceTrustFallsBackToMostRepresentedCurrency(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	hotel := HotelResult{
		Name: "Currency Mix",
		Sources: []PriceSource{
			{Provider: "a", Price: 140, Currency: "USD", PriceBasis: PriceBasisRoomTotal, PriceConfidence: PriceConfidenceRoomLevel},
			{Provider: "b", Price: 110, Currency: "EUR", PriceBasis: PriceBasisRoomTotal, PriceConfidence: PriceConfidenceVerified},
			{Provider: "c", Price: 120, Currency: "USD", PriceBasis: PriceBasisRoomTotal, PriceConfidence: PriceConfidenceVerified},
			{Provider: "ignored", Price: 0, Currency: "GBP"},
		},
	}

	FinalizeHotelResultPriceTrust(&hotel, "JPY", now)
	if hotel.Currency != "USD" || hotel.Price != 120 || hotel.PriceConfidence != PriceConfidenceVerified {
		t.Fatalf("selected headline = %.0f %s/%s", hotel.Price, hotel.Currency, hotel.PriceConfidence)
	}
	if !containsString(hotel.PriceWarnings, PriceWarningMixedSourceCurrencies) {
		t.Fatalf("PriceWarnings = %v, want mixed-currency warning", hotel.PriceWarnings)
	}
}

func TestModelPureRankingAndConfidenceHelpers(t *testing.T) {
	c := Confidence{Rated: true, Score: 1.2}
	if c.Percent() != 100 {
		t.Fatalf("Percent high clamp = %d", c.Percent())
	}
	c.Score = -0.2
	if c.Percent() != 0 {
		t.Fatalf("Percent low clamp = %d", c.Percent())
	}
	if got := (Confidence{}).Percent(); got != 0 {
		t.Fatalf("unrated Percent = %d", got)
	}
	if unrated := UnratedConfidence("no signal"); unrated.Rated || unrated.Label != ConfidenceUnrated || unrated.Basis != "no signal" {
		t.Fatalf("UnratedConfidence = %#v", unrated)
	}

	if got := (FlightResult{Price: 200, ComparablePrice: 180}).PriceForRanking(); got != 180 {
		t.Fatalf("PriceForRanking comparable = %.0f", got)
	}
	if got := (FlightResult{Price: 200}).PriceForRanking(); got != 200 {
		t.Fatalf("PriceForRanking base = %.0f", got)
	}
	if !(FlightResult{Sources: []PriceSource{{Freshness: FreshnessStale}}}).HasStalePrice() {
		t.Fatal("stale source should mark flight stale")
	}
	if (FlightResult{Sources: []PriceSource{{Freshness: FreshnessLive}}}).HasStalePrice() {
		t.Fatal("live source should not mark flight stale")
	}

	flights := []FlightResult{{Price: 0}, {Price: 300}, {Price: 120}}
	SortFlightsByPrice(flights)
	if flights[0].Price != 120 || flights[1].Price != 300 || flights[2].Price != 0 {
		t.Fatalf("SortFlightsByPrice = %#v", flights)
	}

	parsed, err := ParseDate("2026-07-01")
	if err != nil || parsed.Format("2006-01-02") != "2026-07-01" {
		t.Fatalf("ParseDate = %v/%v", parsed, err)
	}
}
