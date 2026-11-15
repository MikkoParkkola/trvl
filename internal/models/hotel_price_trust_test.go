package models

import (
	"testing"
	"time"
)

// stubFX converts into EUR at fixed test rates (25 CZK = 1 EUR, 10 USD =
// 9 EUR), using exact integer factors so results compare with ==.
func stubFX(amount float64, from, to string) (float64, bool) {
	if to != "EUR" {
		return 0, false
	}
	switch from {
	case "EUR":
		return amount, true
	case "CZK":
		return amount / 25, true
	case "USD":
		return amount * 9 / 10, true
	}
	return 0, false
}

func failingFX(float64, string, string) (float64, bool) { return 0, false }

var trustNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// TestFinalizeConvertsSelectedSourceToRequestedCurrency is the D12 flip-back
// case: a CZK provider quote stays a CZK source, and the headline is that
// quote converted into the requested EUR.
func TestFinalizeConvertsSelectedSourceToRequestedCurrency(t *testing.T) {
	h := HotelResult{
		Name: "Prague Stay", Price: 6000, Currency: "CZK",
		Sources: []PriceSource{{Provider: "google_hotels", Price: 6000, Currency: "CZK"}},
	}
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, stubFX)

	if h.Price != 240 || h.Currency != "EUR" {
		t.Fatalf("headline = %v %s, want 240 EUR (6000 CZK converted)", h.Price, h.Currency)
	}
	if len(h.Sources) != 1 || h.Sources[0].Price != 6000 || h.Sources[0].Currency != "CZK" {
		t.Fatalf("sources = %+v, want the provider quote left at 6000 CZK", h.Sources)
	}
	if containsString(h.PriceWarnings, PriceWarningCurrencyNotConverted) {
		t.Fatalf("warnings = %v, conversion succeeded so no %s", h.PriceWarnings, PriceWarningCurrencyNotConverted)
	}
}

// TestFinalizeHotelPriceTrustPassesConverter covers the slice form the search
// paths call.
func TestFinalizeHotelPriceTrustPassesConverter(t *testing.T) {
	hotels := []HotelResult{{
		Name: "Prague Stay", Price: 6000, Currency: "CZK",
		Sources: []PriceSource{{Provider: "google_hotels", Price: 6000, Currency: "CZK"}},
	}}
	FinalizeHotelPriceTrust(hotels, "EUR", trustNow, stubFX)
	if hotels[0].Price != 240 || hotels[0].Currency != "EUR" {
		t.Fatalf("headline = %v %s, want 240 EUR", hotels[0].Price, hotels[0].Currency)
	}
}

// TestFinalizeTrustTierBeatsRequestedCurrency: trust tier is the first key.
// A verified CZK total outranks an unverified EUR teaser, and the headline is
// the verified quote converted to EUR.
func TestFinalizeTrustTierBeatsRequestedCurrency(t *testing.T) {
	h := HotelResult{
		Currency: "EUR",
		Sources: []PriceSource{
			{Provider: "google_hotels", Price: 100, Currency: "EUR", BookingURL: "https://google.example/h"},
			{Provider: "agoda", Price: 3000, Currency: "CZK", BookingURL: "https://agoda.example/h",
				PriceBasis: PriceBasisTaxInclusiveTotal, PriceConfidence: PriceConfidenceVerified},
		},
	}
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, stubFX)

	if h.PriceConfidence != PriceConfidenceVerified || h.BookingURL != "https://agoda.example/h" {
		t.Fatalf("headline from %q at confidence %q, want the verified agoda source", h.BookingURL, h.PriceConfidence)
	}
	if h.Price != 120 || h.Currency != "EUR" {
		t.Fatalf("headline = %v %s, want 120 EUR (3000 CZK converted)", h.Price, h.Currency)
	}
}

// TestFinalizeTopTierComparesConvertedPrices: when no top-tier source is in
// the requested currency and the converter works, the cheapest top-tier
// quote after conversion wins. The winner has the larger raw number (2000 vs
// 100), so comparing raw amounts across currencies picks the wrong one.
func TestFinalizeTopTierComparesConvertedPrices(t *testing.T) {
	h := HotelResult{
		Sources: []PriceSource{
			{Provider: "a", Price: 2000, Currency: "CZK", PriceConfidence: PriceConfidenceVerified}, // 80 EUR
			{Provider: "b", Price: 100, Currency: "USD", PriceConfidence: PriceConfidenceVerified},  // 90 EUR
			{Provider: "c", Price: 50, Currency: "EUR"},                                             // unverified
		},
	}
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, stubFX)

	if h.Price != 80 || h.Currency != "EUR" || h.PriceConfidence != PriceConfidenceVerified {
		t.Fatalf("headline = %v %s/%s, want 80 EUR verified (2000 CZK is the cheaper verified quote once converted)", h.Price, h.Currency, h.PriceConfidence)
	}
}

// TestFinalizeRequestedCurrencyWinsWithinTier guards the second key: inside
// one trust tier a requested-currency quote beats a foreign one, even when
// the foreign one converts cheaper.
func TestFinalizeRequestedCurrencyWinsWithinTier(t *testing.T) {
	h := HotelResult{
		Sources: []PriceSource{
			{Provider: "a", Price: 2500, Currency: "CZK"}, // 100 EUR
			{Provider: "b", Price: 120, Currency: "EUR"},
		},
	}
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, stubFX)
	if h.Price != 120 || h.Currency != "EUR" {
		t.Fatalf("headline = %v %s, want 120 EUR from the requested-currency source", h.Price, h.Currency)
	}
}

// TestFinalizeConversionFailureKeepsSourceCurrencyAndSaysSo: with no usable
// rate the headline stays in the source currency, never relabelled, and the
// hotel carries currency_not_converted. A nil converter behaves the same.
func TestFinalizeConversionFailureKeepsSourceCurrencyAndSaysSo(t *testing.T) {
	for name, conv := range map[string]func(float64, string, string) (float64, bool){
		"failing converter": failingFX,
		"nil converter":     nil,
	} {
		t.Run(name, func(t *testing.T) {
			h := HotelResult{
				Price: 6000, Currency: "CZK",
				Sources: []PriceSource{{Provider: "google_hotels", Price: 6000, Currency: "CZK"}},
			}
			FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, conv)
			if h.Price != 6000 || h.Currency != "CZK" {
				t.Fatalf("headline = %v %s, want 6000 CZK unchanged", h.Price, h.Currency)
			}
			if !containsString(h.PriceWarnings, PriceWarningCurrencyNotConverted) {
				t.Fatalf("warnings = %v, want %s", h.PriceWarnings, PriceWarningCurrencyNotConverted)
			}
		})
	}
}

// TestFinalizeNoRequestedCurrencyNeedsNoConversion: the merge step finalizes
// with no requested currency; nothing is converted and nothing is flagged.
func TestFinalizeNoRequestedCurrencyNeedsNoConversion(t *testing.T) {
	h := HotelResult{
		Price: 6000, Currency: "CZK",
		Sources: []PriceSource{{Provider: "google_hotels", Price: 6000, Currency: "CZK"}},
	}
	FinalizeHotelResultPriceTrust(&h, "", trustNow, nil)
	if h.Price != 6000 || h.Currency != "CZK" || containsString(h.PriceWarnings, PriceWarningCurrencyNotConverted) {
		t.Fatalf("headline = %v %s warnings %v, want 6000 CZK with no conversion warning", h.Price, h.Currency, h.PriceWarnings)
	}
}
