package hotels

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/serpapi"
)

// #169: a result with bookable prices carries the descriptive tourist-tax note;
// an empty result does not. The note is never numeric.
func TestTouristTaxNote(t *testing.T) {
	withPrices := &models.HotelPriceResult{
		Name:      "Hotel Villa Maria",
		Providers: []models.ProviderPrice{{Provider: "Booking.com", Price: 620, Currency: "EUR"}},
	}
	applyLinkDurability(withPrices)
	if withPrices.TouristTaxNote == "" {
		t.Error("result with providers must carry a tourist-tax note")
	}
	for _, digit := range "0123456789" {
		if containsRune(withPrices.TouristTaxNote, digit) {
			t.Errorf("tourist-tax note must not contain a numeric estimate: %q", withPrices.TouristTaxNote)
		}
	}

	empty := &models.HotelPriceResult{Name: "Hotel Villa Maria"}
	applyLinkDurability(empty)
	if empty.TouristTaxNote != "" {
		t.Errorf("result with no providers must not carry a tourist-tax note, got %q", empty.TouristTaxNote)
	}
}

// #171: TaxAddedAtCheckout is set when a provider's shown total equals its
// pre-tax figure, and unset when the total already includes tax or the pre-tax
// figure is absent.
func TestTaxAddedAtCheckoutFlag(t *testing.T) {
	hotel := &serpapi.Hotel{
		Name: "Hotel Villa Maria",
		Prices: []serpapi.PriceOption{
			{Source: "AddsTaxLater", TotalRate: serpapi.Rate{Extracted: 1000, BeforeFeesExtracted: 1000}},
			{Source: "AllIn", TotalRate: serpapi.Rate{Extracted: 1000, BeforeFeesExtracted: 880}},
			{Source: "NoPreTax", TotalRate: serpapi.Rate{Extracted: 1000}},
		},
	}
	providers := providerPricesFromSerpAPIHotel(hotel, "EUR")
	flagBySource := map[string]bool{}
	for _, p := range providers {
		flagBySource[p.Provider] = p.TaxAddedAtCheckout
	}
	if !flagBySource["AddsTaxLater"] {
		t.Error("total == pre-tax must set TaxAddedAtCheckout")
	}
	if flagBySource["AllIn"] {
		t.Error("total > pre-tax must NOT set TaxAddedAtCheckout")
	}
	if flagBySource["NoPreTax"] {
		t.Error("missing pre-tax figure must NOT set TaxAddedAtCheckout")
	}
}

// trvl#535, TRVL.TRUST.1-3: the official-property-site signal comes from the
// upstream seller row, survives mapping, and does not alter price ordering.
// Missing evidence remains unlabelled rather than becoming "not official".
func TestProviderPricesCarryOfficialSignalWithoutReranking(t *testing.T) {
	hotel := &serpapi.Hotel{
		Prices: []serpapi.PriceOption{
			{Source: "Cheaper OTA", TotalRate: serpapi.Rate{Extracted: 100}},
			{Source: "Property site", Official: true, TotalRate: serpapi.Rate{Extracted: 120}},
		},
	}

	providers := providerPricesFromSerpAPIHotel(hotel, "EUR")
	if len(providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(providers))
	}
	if providers[0].Provider != "Cheaper OTA" {
		t.Fatalf("first provider = %q, want cheaper seller; trust signal must not re-rank", providers[0].Provider)
	}
	if providers[0].Official {
		t.Fatal("seller with no upstream official flag was labelled official")
	}
	if !providers[1].Official {
		t.Fatal("upstream official seller flag was dropped from provider price")
	}
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}
