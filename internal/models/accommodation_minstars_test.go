package models

import (
	"testing"
	"time"
)

// TestEvaluateAccommodationOfferMinStarsNotOfferLevel guards issue #277 defect
// 3: min_stars is a property-level filter enforced during candidate selection,
// NOT an offer-level criterion. Offers carry no star rating, so adding MinStars
// to the need must never reject an otherwise-matching offer (that would zero out
// results, the very bug we are fixing). It also confirms the room-level gate
// (defect 2) passes cleanly when room inventory and a room-level price exist.
func TestEvaluateAccommodationOfferMinStarsNotOfferLevel(t *testing.T) {
	need := AccommodationNeed{Adults: 1, Currency: "EUR", MinStars: 5}
	offer := AccommodationOffer{
		Currency:              "EUR",
		NightlyPrice:          100,
		TotalPrice:            200,
		OccupancyAdults:       1,
		PriceBasis:            PriceBasisRoomTotal,
		PriceConfidence:       PriceConfidenceRoomLevel,
		InventoryCompleteness: RoomInventoryCompletenessSingleProvider,
		CheckedAt:             time.Now(),
	}
	got := EvaluateAccommodationOffer(need, offer, time.Now())
	for _, c := range append(append([]string{}, got.MissingCriteria...), got.UnknownCriteria...) {
		if c == "stars" || c == "min_stars" {
			t.Fatalf("stars must not be an offer-level criterion: missing=%v unknown=%v",
				got.MissingCriteria, got.UnknownCriteria)
		}
	}
	if !got.CriteriaMatched {
		t.Fatalf("room-level offer with matching currency/occupancy should match: missing=%v unknown=%v",
			got.MissingCriteria, got.UnknownCriteria)
	}
}
