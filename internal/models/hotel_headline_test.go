package models

import "testing"

// TestFinalizeRecordsHeadlineProvider is St Christopher's, live: the trust
// rule crowns Agoda's room-nightly quote over Google's cheaper lead-in teaser,
// so the headline credits Agoda while cheapest_source still names Google.
func TestFinalizeRecordsHeadlineProvider(t *testing.T) {
	h := HotelResult{Sources: []PriceSource{
		{Provider: "google_hotels", Price: 909, Currency: "CZK", PriceBasis: PriceBasisLeadIn},
		{Provider: "agoda", Price: 66.83, Currency: "EUR", PriceBasis: PriceBasisRoomNightly},
	}}
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, stubFX)
	if h.Price != 66.83 || h.Currency != "EUR" || h.HeadlineProvider != "agoda" {
		t.Fatalf("headline = %v %s from %q, want 66.83 EUR from agoda", h.Price, h.Currency, h.HeadlineProvider)
	}
	if h.CheapestSource != "google_hotels" {
		t.Errorf("cheapest_source = %q, want google_hotels (909 CZK converts lower)", h.CheapestSource)
	}
	if h.PriceProvenance == PriceUnavailable {
		t.Error("a priced hotel is labelled unavailable")
	}
}

// TestFinalizeLabelsUnpricedHotelUnavailable: with no usable quote nothing is
// selected, so the hotel says its price is unavailable, credits no provider,
// and leaves its price-0 sources unstamped.
func TestFinalizeLabelsUnpricedHotelUnavailable(t *testing.T) {
	for name, h := range map[string]HotelResult{
		"price-0 source": {Name: "Pension", Sources: []PriceSource{{Provider: "booking", Price: 0, Currency: "EUR"}}},
		"no sources":     {Name: "Pension"},
	} {
		FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, stubFX)
		if h.PriceProvenance != PriceUnavailable {
			t.Errorf("%s: price_provenance = %q, want %q", name, h.PriceProvenance, PriceUnavailable)
		}
		if h.HeadlineProvider != "" {
			t.Errorf("%s: headline_provider = %q, want none", name, h.HeadlineProvider)
		}
		for _, s := range h.Sources {
			if s.Provenance != "" {
				t.Errorf("%s: price-0 source stamped %q, want it unstamped", name, s.Provenance)
			}
		}
	}
}
