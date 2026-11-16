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
	h := HotelResult{Name: "Studio", Price: 0.0000059, Currency: "EUR", ComparablePrice: 0.0000059, Sources: []PriceSource{{Provider: "housinganywhere", Price: 0.01, Currency: "ARS", PriceBasis: "monthly"}}}
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, arsFX)
	if h.PriceProvenance != PriceUnavailable || h.HeadlineProvider != "" {
		t.Errorf("headline = %v %s from %q, provenance %q; want unavailable", h.Price, h.Currency, h.HeadlineProvider, h.PriceProvenance)
	}
	// No number survives to sort first or take the cheapest line.
	if h.Price != 0 || h.ComparablePrice != 0 {
		t.Errorf("unavailable hotel kept price %v (comparable %v), want 0", h.Price, h.ComparablePrice)
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

// TestPriceFloorAddsNoRateLookups: the floor is checked in the requested
// currency, which the search converts into anyway, so a same-currency search
// never asks for an exchange rate (a USD search must not fetch USD to EUR).
func TestPriceFloorAddsNoRateLookups(t *testing.T) {
	noFX := func(amount float64, from, to string) (float64, bool) {
		t.Errorf("unexpected rate lookup %s to %s", from, to)
		return 0, false
	}
	h := HotelResult{Name: "Hotel", Sources: []PriceSource{{Provider: "google_hotels", Price: 150, Currency: "USD"}}}
	FinalizeHotelResultPriceTrust(&h, "USD", trustNow, noFX)
	if h.Price != 150 || h.HeadlineProvider != "google_hotels" {
		t.Errorf("headline = %v %s from %q, want 150 USD from google_hotels", h.Price, h.Currency, h.HeadlineProvider)
	}
}

// TestFinalizeRemovesSubFloorSourcesAndStaleSavings: a sub-floor quote is not
// kept as a source either (it would resurface in sources[] and room data);
// price-0 sources stay, and a hotel with no usable quote keeps no savings.
func TestFinalizeRemovesSubFloorSourcesAndStaleSavings(t *testing.T) {
	providers := func(h HotelResult) []string {
		var out []string
		for _, s := range h.Sources {
			out = append(out, s.Provider)
		}
		return out
	}
	h := HotelResult{Name: "Studio", Savings: 5, CheapestSource: "housinganywhere", Sources: []PriceSource{
		{Provider: "housinganywhere", Price: 0.01, Currency: "ARS"},
		{Provider: "booking", Price: 0, Currency: "EUR"},
	}}
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, arsFX)
	if got := providers(h); len(got) != 1 || got[0] != "booking" {
		t.Errorf("sources = %v, want only the price-0 booking source", got)
	}
	if h.Savings != 0 || h.CheapestSource != "" {
		t.Errorf("savings = %v, cheapest = %q; want none for a hotel with no usable quote", h.Savings, h.CheapestSource)
	}

	h = HotelResult{Name: "Studio", Sources: []PriceSource{
		{Provider: "housinganywhere", Price: 0.01, Currency: "ARS"},
		{Provider: "booking", Price: 80, Currency: "EUR"},
	}}
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, arsFX)
	if got := providers(h); len(got) != 1 || got[0] != "booking" {
		t.Errorf("sources = %v, want the sub-floor quote removed", got)
	}
}
