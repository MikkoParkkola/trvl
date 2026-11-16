package models

import "testing"

// arsFX extends stubFX with the Argentine peso (about 1,700 ARS to the euro).
func arsFX(amount float64, from, to string) (float64, bool) {
	if from == "ARS" && to == "EUR" {
		return amount / 1700, true
	}
	return stubFX(amount, from, to)
}

// TestFinalizeTreatsSubEuroPricesAsUnavailable is the live 0.01 ARS row: a
// quote worth less than EUR 1 once converted is not a price, so it never
// becomes the headline and the hotel reads as unavailable.
func TestFinalizeTreatsSubEuroPricesAsUnavailable(t *testing.T) {
	h := HotelResult{Name: "Studio", Sources: []PriceSource{{Provider: "housinganywhere", Price: 0.01, Currency: "ARS", PriceBasis: "monthly"}}}
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, arsFX)
	if h.PriceProvenance != PriceUnavailable || h.HeadlineProvider != "" {
		t.Errorf("headline = %v %s from %q, provenance %q; want unavailable", h.Price, h.Currency, h.HeadlineProvider, h.PriceProvenance)
	}

	// A real quote next to the sub-euro one still wins.
	h = HotelResult{Name: "Studio", Sources: []PriceSource{
		{Provider: "housinganywhere", Price: 0.01, Currency: "ARS"},
		{Provider: "booking", Price: 80, Currency: "EUR"},
	}}
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, arsFX)
	if h.HeadlineProvider != "booking" || h.Price != 80 {
		t.Errorf("headline = %v %s from %q, want 80 EUR from booking", h.Price, h.Currency, h.HeadlineProvider)
	}

	// The floor is EUR 1: EUR 0.50 is out, EUR 1 is in.
	for price, wantUnavailable := range map[float64]bool{0.5: true, 1: false} {
		h = HotelResult{Name: "Hostel", Sources: []PriceSource{{Provider: "hostelworld", Price: price, Currency: "EUR"}}}
		FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, arsFX)
		if got := h.PriceProvenance == PriceUnavailable; got != wantUnavailable {
			t.Errorf("EUR %v: unavailable = %v, want %v", price, got, wantUnavailable)
		}
	}
}
