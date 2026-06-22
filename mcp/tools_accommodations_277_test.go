package mcp

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestNormalizeOfferCurrency covers issue #277 defect 1: a provider offer in a
// foreign currency (e.g. HousingAnywhere returning USD when EUR was requested)
// must be FX-normalized to the requested currency before criteria evaluation
// rather than rejected for missing_criteria:["currency"].
func TestNormalizeOfferCurrency(t *testing.T) {
	// Same currency: untouched.
	in := models.AccommodationOffer{Currency: "EUR", NightlyPrice: 100, TotalPrice: 200}
	if out := normalizeOfferCurrency(in, "EUR"); out.Currency != "EUR" || out.NightlyPrice != 100 || out.TotalPrice != 200 {
		t.Fatalf("same currency must be untouched: %+v", out)
	}
	// Empty target: untouched.
	if out := normalizeOfferCurrency(in, ""); out.Currency != "EUR" || out.NightlyPrice != 100 {
		t.Fatalf("empty want must be untouched: %+v", out)
	}
	// USD -> EUR: converted, relabeled, warned. USD/EUR always resolves via
	// fx.go fallback rates, so this is deterministic offline.
	usd := models.AccommodationOffer{Currency: "USD", NightlyPrice: 109, TotalPrice: 218, TaxesAndFees: 20}
	out := normalizeOfferCurrency(usd, "EUR")
	if out.Currency != "EUR" {
		t.Fatalf("currency should be relabeled to EUR, got %q", out.Currency)
	}
	if out.NightlyPrice <= 0 || out.NightlyPrice >= 109 {
		t.Fatalf("USD->EUR nightly should be positive and below the USD nominal, got %v", out.NightlyPrice)
	}
	if out.TotalPrice <= 0 || out.TotalPrice >= 218 {
		t.Fatalf("USD->EUR total should be positive and below the USD nominal, got %v", out.TotalPrice)
	}
	found := false
	for _, w := range out.Warnings {
		if w == "currency_normalized" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected currency_normalized warning, got %v", out.Warnings)
	}
}

// TestSelectAccommodationCandidateHotelsMinStars covers issue #277 defect 3:
// min_stars must be enforced during stage-2 candidate selection. Properties
// with a known star rating below the minimum are dropped; unrated (stars==0)
// properties are kept because we cannot prove they fail the filter.
func TestSelectAccommodationCandidateHotelsMinStars(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Four", Stars: 4},
		{Name: "Five", Stars: 5},
		{Name: "Unrated", Stars: 0},
	}
	got := selectAccommodationCandidateHotels(hotels, 10, models.AccommodationNeed{MinStars: 5})
	names := map[string]bool{}
	for _, h := range got {
		names[h.Name] = true
	}
	if names["Four"] {
		t.Fatalf("4-star property must be excluded for min_stars=5, got %v", names)
	}
	if !names["Five"] {
		t.Fatalf("5-star property must be kept for min_stars=5, got %v", names)
	}
	if !names["Unrated"] {
		t.Fatalf("unrated property must be kept (cannot prove failure), got %v", names)
	}
	// No min_stars: every candidate is kept.
	if all := selectAccommodationCandidateHotels(hotels, 10, models.AccommodationNeed{}); len(all) != 3 {
		t.Fatalf("no min_stars filter should keep all 3, got %d", len(all))
	}
}
