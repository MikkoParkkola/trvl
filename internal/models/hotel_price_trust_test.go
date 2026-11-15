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

// TestFinalizeBasisIsPartOfTheTrustTier pins the tier as confidence, then
// price basis (today's within-currency order), ahead of currency preference:
// a verified tax-inclusive total in CZK beats a cheaper verified nightly rate
// already in EUR.
func TestFinalizeBasisIsPartOfTheTrustTier(t *testing.T) {
	h := HotelResult{
		Sources: []PriceSource{
			{Provider: "a", Price: 100, Currency: "EUR", PriceConfidence: PriceConfidenceVerified, PriceBasis: PriceBasisRoomNightly},
			{Provider: "b", Price: 3000, Currency: "CZK", PriceConfidence: PriceConfidenceVerified, PriceBasis: PriceBasisTaxInclusiveTotal},
		},
	}
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, stubFX)
	if h.Price != 120 || h.Currency != "EUR" || h.PriceBasis != PriceBasisTaxInclusiveTotal {
		t.Fatalf("headline = %v %s basis %q, want 120 EUR tax_inclusive_total (3000 CZK converted)", h.Price, h.Currency, h.PriceBasis)
	}
}

// TestFinalizeSkipsUnconvertibleTopTierSource: the converter fails for GBP
// only. The converted comparison runs over the sources it can convert, the
// GBP quote sits out, and the converted headline carries no conversion
// warning.
func TestFinalizeSkipsUnconvertibleTopTierSource(t *testing.T) {
	h := HotelResult{
		Sources: []PriceSource{
			{Provider: "a", Price: 2000, Currency: "CZK", PriceConfidence: PriceConfidenceVerified}, // 80 EUR
			{Provider: "b", Price: 10, Currency: "GBP", PriceConfidence: PriceConfidenceVerified},   // no rate
			{Provider: "c", Price: 100, Currency: "USD", PriceConfidence: PriceConfidenceVerified},  // 90 EUR
		},
	}
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, stubFX)
	if h.Price != 80 || h.Currency != "EUR" {
		t.Fatalf("headline = %v %s, want 80 EUR (cheapest convertible top-tier quote)", h.Price, h.Currency)
	}
	if containsString(h.PriceWarnings, PriceWarningCurrencyNotConverted) {
		t.Fatalf("warnings = %v, the headline was converted", h.PriceWarnings)
	}
}

// TestFinalizeNoWarningWhenWinnerAlreadyInRequestedCurrency: nothing needed
// converting, so a nil or failing converter must not add the warning.
func TestFinalizeNoWarningWhenWinnerAlreadyInRequestedCurrency(t *testing.T) {
	for name, conv := range map[string]func(float64, string, string) (float64, bool){
		"failing converter": failingFX,
		"nil converter":     nil,
	} {
		t.Run(name, func(t *testing.T) {
			h := HotelResult{Sources: []PriceSource{
				{Provider: "a", Price: 100, Currency: "EUR"},
				{Provider: "b", Price: 2000, Currency: "CZK"},
			}}
			FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, conv)
			if h.Price != 100 || h.Currency != "EUR" {
				t.Fatalf("headline = %v %s, want 100 EUR", h.Price, h.Currency)
			}
			if containsString(h.PriceWarnings, PriceWarningCurrencyNotConverted) {
				t.Fatalf("warnings = %v, the winner needed no conversion", h.PriceWarnings)
			}
		})
	}
}

// TestFinalizeClearsStaleConversionWarning: a first pass flags a CZK
// headline; a verified EUR source then arrives (room enrichment) and the
// second pass picks it. The flag described the old headline and must go.
func TestFinalizeClearsStaleConversionWarning(t *testing.T) {
	h := HotelResult{Sources: []PriceSource{{Provider: "google_hotels", Price: 6000, Currency: "CZK"}}}
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, failingFX)
	if !containsString(h.PriceWarnings, PriceWarningCurrencyNotConverted) {
		t.Fatalf("precondition: first pass warnings = %v, want %s", h.PriceWarnings, PriceWarningCurrencyNotConverted)
	}

	h.Sources = append(h.Sources, PriceSource{Provider: "agoda", Price: 150, Currency: "EUR", PriceConfidence: PriceConfidenceVerified})
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, failingFX)
	if h.Price != 150 || h.Currency != "EUR" {
		t.Fatalf("second pass headline = %v %s, want 150 EUR", h.Price, h.Currency)
	}
	if containsString(h.PriceWarnings, PriceWarningCurrencyNotConverted) {
		t.Fatalf("warnings = %v, stale flag from the first pass", h.PriceWarnings)
	}
}

// TestFinalizeTreatsNonPositiveConversionAsFailure: a converter that says ok
// but returns 0 or a negative amount has not produced a price.
func TestFinalizeTreatsNonPositiveConversionAsFailure(t *testing.T) {
	for name, amount := range map[string]float64{"zero": 0, "negative": -5} {
		t.Run(name, func(t *testing.T) {
			conv := func(float64, string, string) (float64, bool) { return amount, true }
			h := HotelResult{Sources: []PriceSource{{Provider: "google_hotels", Price: 6000, Currency: "CZK"}}}
			FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, conv)
			if h.Price != 6000 || h.Currency != "CZK" {
				t.Fatalf("headline = %v %s, want 6000 CZK kept", h.Price, h.Currency)
			}
			if !containsString(h.PriceWarnings, PriceWarningCurrencyNotConverted) {
				t.Fatalf("warnings = %v, want %s", h.PriceWarnings, PriceWarningCurrencyNotConverted)
			}
		})
	}
}

// TestFinalizeSavingsFollowHeadlineCurrency: merge computed savings among
// the CZK quotes (500). Once the headline is converted to EUR, savings are
// shown as "saves EUR <savings>", so they must be in EUR too: 500 CZK = 20 EUR.
func TestFinalizeSavingsFollowHeadlineCurrency(t *testing.T) {
	h := HotelResult{
		Savings: 500, CheapestSource: "google_hotels",
		Sources: []PriceSource{
			{Provider: "google_hotels", Price: 2500, Currency: "CZK"},
			{Provider: "booking", Price: 3000, Currency: "CZK"},
		},
	}
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, stubFX)
	if h.Price != 100 || h.Currency != "EUR" {
		t.Fatalf("headline = %v %s, want 100 EUR", h.Price, h.Currency)
	}
	if h.Savings != 20 || h.CheapestSource != "google_hotels" {
		t.Fatalf("savings = %v via %q, want 20 (EUR) via google_hotels", h.Savings, h.CheapestSource)
	}
}

// TestFinalizeSavingsKeptInSourceCurrencyWhenNotConverted: with no exchange
// rate the headline stays CZK, so the CZK savings remain truthful and stay.
func TestFinalizeSavingsKeptInSourceCurrencyWhenNotConverted(t *testing.T) {
	h := HotelResult{
		Savings: 500, CheapestSource: "google_hotels",
		Sources: []PriceSource{
			{Provider: "google_hotels", Price: 2500, Currency: "CZK"},
			{Provider: "booking", Price: 3000, Currency: "CZK"},
		},
	}
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, failingFX)
	if h.Currency != "CZK" || h.Savings != 500 {
		t.Fatalf("headline %s savings %v, want CZK headline with 500 CZK savings", h.Currency, h.Savings)
	}
}

// TestFinalizeSavingsAlwaysInHeadlineCurrency: merge-era savings may come
// from another currency group. Whatever path picks the headline, savings are
// restated in its currency, or omitted when they cannot be.
func TestFinalizeSavingsAlwaysInHeadlineCurrency(t *testing.T) {
	t.Run("headline already requested, no converter: omitted", func(t *testing.T) {
		h := HotelResult{Savings: 500, CheapestSource: "booking", Sources: []PriceSource{
			{Provider: "google_hotels", Price: 100, Currency: "EUR"},
			{Provider: "booking", Price: 2500, Currency: "CZK"},
			{Provider: "agoda", Price: 3000, Currency: "CZK"},
		}}
		FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, nil)
		if h.Currency != "EUR" || h.Savings != 0 || h.CheapestSource != "" {
			t.Fatalf("headline %s, savings %v via %q; want EUR with no savings (one EUR quote)", h.Currency, h.Savings, h.CheapestSource)
		}
	})
	t.Run("headline already requested, converter works", func(t *testing.T) {
		h := HotelResult{Savings: 500, CheapestSource: "booking", Sources: []PriceSource{
			{Provider: "google_hotels", Price: 100, Currency: "EUR"},
			{Provider: "booking", Price: 2000, Currency: "CZK"}, // 80 EUR
			{Provider: "agoda", Price: 3000, Currency: "CZK"},   // 120 EUR
		}}
		FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, stubFX)
		if h.Price != 100 || h.Savings != 40 || h.CheapestSource != "booking" {
			t.Fatalf("headline %v, savings %v via %q; want 100 EUR, 40 EUR via booking", h.Price, h.Savings, h.CheapestSource)
		}
	})
	t.Run("conversion failed, headline stays CZK", func(t *testing.T) {
		h := HotelResult{Currency: "CZK", Savings: 30, CheapestSource: "x", Sources: []PriceSource{
			{Provider: "google_hotels", Price: 2500, Currency: "CZK"},
			{Provider: "booking", Price: 3000, Currency: "CZK"},
			{Provider: "x", Price: 100, Currency: "USD"},
			{Provider: "y", Price: 130, Currency: "USD"},
			{Provider: "z", Price: 120, Currency: "USD"},
		}}
		FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, failingFX)
		if h.Currency != "CZK" || h.Savings != 500 || h.CheapestSource != "google_hotels" {
			t.Fatalf("headline %s, savings %v via %q; want CZK, 500 CZK via google_hotels", h.Currency, h.Savings, h.CheapestSource)
		}
	})
}

// TestFinalizeNoCheapestSourceWithoutSavings: quotes that differ by less than
// one unit round to zero savings, so no provider is named as cheapest.
func TestFinalizeNoCheapestSourceWithoutSavings(t *testing.T) {
	h := HotelResult{Sources: []PriceSource{
		{Provider: "a", Price: 100.2, Currency: "EUR"},
		{Provider: "b", Price: 100.4, Currency: "EUR"},
	}}
	FinalizeHotelResultPriceTrust(&h, "EUR", trustNow, nil)
	if h.Savings != 0 || h.CheapestSource != "" {
		t.Fatalf("savings %v via %q, want none", h.Savings, h.CheapestSource)
	}
}
