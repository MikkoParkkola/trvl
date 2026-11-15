package models

import (
	"math"
	"strings"
	"time"
)

const (
	PriceBasisLeadIn            = "lead_in"
	PriceBasisRoomNightly       = "room_nightly"
	PriceBasisRoomTotal         = "room_total"
	PriceBasisTaxInclusiveTotal = "tax_inclusive_total"

	PriceConfidenceUnverified = "unverified"
	PriceConfidenceRoomLevel  = "room_level"
	PriceConfidenceVerified   = "verified"

	PriceWarningMixedSourceCurrencies = "mixed_source_currencies"
	// PriceWarningCurrencyNotConverted marks a headline left in its source
	// currency because conversion into the requested currency failed.
	PriceWarningCurrencyNotConverted = "currency_not_converted"
)

// FinalizeHotelPriceTrust fills source-level trust metadata, chooses the
// primary source (trust tier, then requested currency, then price), converts
// it into the requested currency for the headline, and mirrors that source's
// trust fields onto the hotel. Search results should call this after all
// providers have merged.
//
// convert turns amount from one currency into another and reports success. It
// is injected because models cannot import the package that holds exchange
// rates; nil means no conversion is available.
func FinalizeHotelPriceTrust(hotels []HotelResult, preferredCurrency string, now time.Time, convert func(amount float64, from, to string) (float64, bool)) {
	if now.IsZero() {
		now = time.Now()
	}
	preferredCurrency = strings.ToUpper(strings.TrimSpace(preferredCurrency))

	for i := range hotels {
		FinalizeHotelResultPriceTrust(&hotels[i], preferredCurrency, now, convert)
	}
}

// FinalizeHotelResultPriceTrust is the single-result form used by tests and
// detail handlers.
func FinalizeHotelResultPriceTrust(h *HotelResult, preferredCurrency string, now time.Time, convert func(amount float64, from, to string) (float64, bool)) {
	if h == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	if len(h.Sources) == 0 && h.Price > 0 {
		h.Sources = []PriceSource{{
			Provider:        "unknown",
			Price:           h.Price,
			Currency:        h.Currency,
			BookingURL:      h.BookingURL,
			PriceBasis:      PriceBasisLeadIn,
			PriceConfidence: PriceConfidenceUnverified,
		}}
	}

	h.Sources = finalizePriceSources(h.Sources, now)
	preferredCurrency = strings.ToUpper(strings.TrimSpace(preferredCurrency))
	if selected, ok := selectPrimaryHotelSource(h.Sources, preferredCurrency, h.Currency, convert); ok {
		// Sources stay provider quotes in the provider's currency; only the
		// headline is converted. A failed conversion keeps the quote's own
		// currency and says so rather than relabelling the number.
		h.Price = selected.Price
		h.Currency = selected.Currency
		h.PriceWarnings = withoutString(h.PriceWarnings, PriceWarningCurrencyNotConverted)
		if preferredCurrency != "" && h.Currency != preferredCurrency {
			if amount, ok := convertPrice(convert, h.Price, h.Currency, preferredCurrency); ok {
				h.Price, h.Currency = amount, preferredCurrency
				// Savings were computed among the quotes in their own
				// currency and are displayed in the headline's; restate them.
				h.Savings, h.CheapestSource = savingsIn(h.Sources, preferredCurrency, convert)
			} else {
				h.PriceWarnings = appendUniqueString(h.PriceWarnings, PriceWarningCurrencyNotConverted)
			}
		}
		if selected.BookingURL != "" {
			h.BookingURL = selected.BookingURL
		}
		h.PriceBasis = selected.PriceBasis
		h.PriceConfidence = selected.PriceConfidence
		h.RetrievedAt = selected.RetrievedAt
		h.Freshness = selected.Freshness
	}
	if h.PropertyType == "" {
		h.PropertyType = InferHotelPropertyType(*h)
	}
	if hasMixedSourceCurrencies(h.Sources) {
		h.PriceWarnings = appendUniqueString(h.PriceWarnings, PriceWarningMixedSourceCurrencies)
	}
}

func finalizePriceSources(sources []PriceSource, now time.Time) []PriceSource {
	if len(sources) == 0 {
		return nil
	}
	out := make([]PriceSource, len(sources))
	for i, s := range sources {
		s.Provider = strings.TrimSpace(s.Provider)
		if s.Provider == "" {
			s.Provider = "unknown"
		}
		s.Currency = strings.ToUpper(strings.TrimSpace(s.Currency))
		if s.PriceBasis == "" {
			s.PriceBasis = PriceBasisLeadIn
		}
		if s.PriceConfidence == "" {
			s.PriceConfidence = PriceConfidenceUnverified
		}
		if s.RetrievedAt.IsZero() {
			s.RetrievedAt = now
		}
		s.Freshness = ClassifyFreshness(s.Provider, s.RetrievedAt, now)
		out[i] = s
	}
	return out
}

// priceConfidenceRank orders price-confidence tiers so the highest-trust price
// wins the headline: verified > room_level > unverified. It mirrors the
// sort-side ranking in internal/hotels/search_filter.go so headline selection
// and result ordering agree — a verified, all-in room price leads even when a
// cheaper unverified lead-in teaser exists for the same hotel.
func priceConfidenceRank(confidence string) int {
	switch confidence {
	case PriceConfidenceVerified:
		return 3
	case PriceConfidenceRoomLevel:
		return 2
	default:
		return 1
	}
}

// priceBasisRank maps basis to a sort rank (higher = more complete). Mirrors
// the definition in search_filter.go for determinism in source selection.
func priceBasisRank(basis string) int {
	switch basis {
	case PriceBasisTaxInclusiveTotal:
		return 3
	case PriceBasisRoomTotal:
		return 2
	case PriceBasisRoomNightly:
		return 1
	case PriceBasisLeadIn, "":
		return 0
	default:
		return 0
	}
}

func selectPrimaryHotelSource(sources []PriceSource, preferredCurrency, currentCurrency string, convert func(amount float64, from, to string) (float64, bool)) (PriceSource, bool) {
	preferredCurrency = strings.ToUpper(strings.TrimSpace(preferredCurrency))
	currentCurrency = strings.ToUpper(strings.TrimSpace(currentCurrency))
	tier := topTrustTier(sources)
	if selected, ok := bestSourceInCurrency(tier, preferredCurrency); ok {
		return selected, true
	}
	if selected, ok := cheapestConverted(tier, preferredCurrency, convert); ok {
		return selected, true
	}
	if selected, ok := bestSourceInCurrency(tier, currentCurrency); ok {
		return selected, true
	}
	if selected, ok := bestSourceInCurrency(tier, mostRepresentedSourceCurrency(sources)); ok {
		return selected, true
	}
	if selected, ok := bestSourceInCurrency(tier, mostRepresentedSourceCurrency(tier)); ok {
		return selected, true
	}
	return PriceSource{}, false
}

// topTrustTier keeps the priced sources at the highest trust tier: price
// confidence first, then price basis. Trust is decided before currency so a
// verified total in a foreign currency beats an unverified teaser in the
// requested one.
func topTrustTier(sources []PriceSource) []PriceSource {
	var tier []PriceSource
	bestConf, bestBasis := 0, -1
	for _, s := range sources {
		if s.Price <= 0 || strings.TrimSpace(s.Currency) == "" {
			continue
		}
		conf, basis := priceConfidenceRank(s.PriceConfidence), priceBasisRank(s.PriceBasis)
		switch {
		case conf > bestConf || (conf == bestConf && basis > bestBasis):
			tier = []PriceSource{s}
			bestConf, bestBasis = conf, basis
		case conf == bestConf && basis == bestBasis:
			tier = append(tier, s)
		}
	}
	return tier
}

// cheapestConverted compares sources by their price converted into currency,
// skipping any the converter cannot handle. Ties break on provider name.
func cheapestConverted(sources []PriceSource, currency string, convert func(amount float64, from, to string) (float64, bool)) (PriceSource, bool) {
	if currency == "" || convert == nil {
		return PriceSource{}, false
	}
	var selected PriceSource
	selectedAmount := 0.0
	found := false
	for _, s := range sources {
		amount, ok := convertPrice(convert, s.Price, s.Currency, currency)
		if !ok {
			continue
		}
		if !found || amount < selectedAmount || (amount == selectedAmount && s.Provider < selected.Provider) {
			selected, selectedAmount, found = s, amount, true
		}
	}
	return selected, found
}

// savingsIn is the spread between the cheapest and dearest quotes once
// converted into currency, and the cheapest provider. Quotes the converter
// cannot handle are left out; fewer than two comparable quotes means none.
func savingsIn(sources []PriceSource, currency string, convert func(amount float64, from, to string) (float64, bool)) (float64, string) {
	minPrice, maxPrice := math.MaxFloat64, 0.0
	cheapest, n := "", 0
	for _, s := range sources {
		if s.Price <= 0 {
			continue
		}
		amount, ok := convertPrice(convert, s.Price, s.Currency, currency)
		if !ok {
			continue
		}
		n++
		if amount < minPrice || (amount == minPrice && s.Provider < cheapest) {
			minPrice, cheapest = amount, s.Provider
		}
		if amount > maxPrice {
			maxPrice = amount
		}
	}
	if n < 2 || minPrice >= maxPrice {
		return 0, ""
	}
	return math.Round(maxPrice - minPrice), cheapest
}

// convertPrice treats a missing converter, a reported failure, and a
// non-positive result alike: there is no converted price.
func convertPrice(convert func(amount float64, from, to string) (float64, bool), amount float64, from, to string) (float64, bool) {
	from = strings.ToUpper(strings.TrimSpace(from))
	if from == to {
		return amount, true
	}
	if convert == nil {
		return 0, false
	}
	converted, ok := convert(amount, from, to)
	return converted, ok && converted > 0
}

// bestSourceInCurrency picks the headline source within a single currency by
// highest price-confidence tier first, then cheapest within that tier. This is
// the "verified leads" rule: an honest, tax-inclusive verified price beats a
// cheaper-but-unverified teaser, but among equally-trusted prices the cheapest
// still wins.
func bestSourceInCurrency(sources []PriceSource, currency string) (PriceSource, bool) {
	if currency == "" {
		return PriceSource{}, false
	}
	cU := strings.ToUpper(strings.TrimSpace(currency))
	var selected PriceSource
	selectedConfRank := 0
	selectedBasisRank := -1
	found := false
	for _, s := range sources {
		if s.Price <= 0 || strings.ToUpper(strings.TrimSpace(s.Currency)) != cU {
			continue
		}
		confRank := priceConfidenceRank(s.PriceConfidence)
		basisR := priceBasisRank(s.PriceBasis)
		prov := strings.ToLower(strings.TrimSpace(s.Provider))
		if !found ||
			confRank > selectedConfRank ||
			(confRank == selectedConfRank && basisR > selectedBasisRank) ||
			(confRank == selectedConfRank && basisR == selectedBasisRank && s.Price < selected.Price) ||
			(confRank == selectedConfRank && basisR == selectedBasisRank && s.Price == selected.Price && (prov < strings.ToLower(strings.TrimSpace(selected.Provider)) || selected.Provider == "")) {
			selected = s
			selectedConfRank = confRank
			selectedBasisRank = basisR
			found = true
		}
	}
	return selected, found
}

func mostRepresentedSourceCurrency(sources []PriceSource) string {
	// Delegate to the order-independent lex-tiebreak implementation in resolve.go
	// (dominantCurrency) so equal-sized groups produce a stable, lex-smallest pick.
	return dominantCurrency(sources)
}

func hasMixedSourceCurrencies(sources []PriceSource) bool {
	seen := ""
	for _, s := range sources {
		if s.Price <= 0 || s.Currency == "" {
			continue
		}
		currency := strings.ToUpper(s.Currency)
		if seen == "" {
			seen = currency
			continue
		}
		if currency != seen {
			return true
		}
	}
	return false
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// withoutString returns values minus value, never writing into values: hotel
// copies made during merges can share the same backing array.
func withoutString(values []string, value string) []string {
	var out []string
	for _, v := range values {
		if v != value {
			out = append(out, v)
		}
	}
	return out
}

// InferHotelPropertyType gives callers a conservative machine-readable type
// for filtering and UI labeling. It intentionally returns "unknown" when the
// evidence is weak instead of pretending every lodging-like Google result is a
// hotel.
func InferHotelPropertyType(h HotelResult) string {
	text := strings.ToLower(h.Name + " " + h.Description + " " + h.Address)
	for _, s := range h.Sources {
		provider := strings.ToLower(strings.TrimSpace(s.Provider))
		switch {
		case strings.Contains(provider, "hostelworld"):
			return "hostel"
		case strings.Contains(provider, "airbnb"), strings.Contains(provider, "hometogo"), strings.Contains(provider, "vrbo"):
			return "vacation_rental"
		}
	}
	switch {
	case strings.Contains(text, "hostel"):
		return "hostel"
	case strings.Contains(text, "apartment"), strings.Contains(text, "apartments"), strings.Contains(text, "aparthotel"), strings.Contains(text, "residence"):
		return "apartment"
	case strings.Contains(text, "villa"):
		return "villa"
	case strings.Contains(text, "resort"):
		return "resort"
	case strings.Contains(text, "bed and breakfast"), strings.Contains(text, "b&b"):
		return "bnb"
	case strings.Contains(text, "hotel"), strings.Contains(text, "inn"), strings.Contains(text, "motel"):
		return "hotel"
	default:
		return "unknown"
	}
}
