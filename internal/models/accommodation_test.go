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
