package models

import (
	"testing"
	"time"
)

// TestVerifiedSourceLeadsHeadline proves the "verified leads" rule: a verified,
// tax-inclusive room price becomes the headline even when a cheaper unverified
// lead-in teaser exists for the same hotel.
func TestVerifiedSourceLeadsHeadline(t *testing.T) {
	h := &HotelResult{
		Currency: "EUR",
		Sources: []PriceSource{
			{Provider: "google", Price: 98, Currency: "EUR", PriceBasis: PriceBasisLeadIn, PriceConfidence: PriceConfidenceUnverified},
			{Provider: "agoda", Price: 142, Currency: "EUR", PriceBasis: PriceBasisTaxInclusiveTotal, PriceConfidence: PriceConfidenceVerified},
		},
	}
	FinalizeHotelResultPriceTrust(h, "EUR", time.Now())
	if h.PriceConfidence != PriceConfidenceVerified {
		t.Fatalf("headline confidence = %q, want verified (verified must lead over cheaper teaser)", h.PriceConfidence)
	}
	if h.Price != 142 {
		t.Errorf("headline price = %v, want 142 (the verified all-in rate, not the cheaper teaser)", h.Price)
	}
	if h.PriceBasis != PriceBasisTaxInclusiveTotal {
		t.Errorf("headline basis = %q, want tax_inclusive_total", h.PriceBasis)
	}
}

// TestWithinTierCheapestWins proves that among equally-trusted prices the
// cheapest still wins — confidence is the primary key, price the tiebreaker.
func TestWithinTierCheapestWins(t *testing.T) {
	h := &HotelResult{
		Currency: "EUR",
		Sources: []PriceSource{
			{Provider: "a", Price: 120, Currency: "EUR", PriceConfidence: PriceConfidenceVerified},
			{Provider: "b", Price: 99, Currency: "EUR", PriceConfidence: PriceConfidenceVerified},
		},
	}
	FinalizeHotelResultPriceTrust(h, "EUR", time.Now())
	if h.Price != 99 {
		t.Errorf("headline price = %v, want 99 (cheapest within the same confidence tier)", h.Price)
	}
}

// TestRoomLevelBeatsUnverifiedButLosesToVerified proves the full tier ordering
// verified > room_level > unverified for the headline.
func TestRoomLevelBeatsUnverifiedButLosesToVerified(t *testing.T) {
	h := &HotelResult{
		Currency: "EUR",
		Sources: []PriceSource{
			{Provider: "google", Price: 80, Currency: "EUR", PriceConfidence: PriceConfidenceUnverified},
			{Provider: "booking", Price: 110, Currency: "EUR", PriceConfidence: PriceConfidenceRoomLevel},
			{Provider: "agoda", Price: 130, Currency: "EUR", PriceConfidence: PriceConfidenceVerified},
		},
	}
	FinalizeHotelResultPriceTrust(h, "EUR", time.Now())
	if h.Price != 130 || h.PriceConfidence != PriceConfidenceVerified {
		t.Errorf("headline = %v/%q, want 130/verified (top tier leads)", h.Price, h.PriceConfidence)
	}
}
