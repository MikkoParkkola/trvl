package models

import (
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
)

// FinalizeHotelPriceTrust fills source-level trust metadata, chooses a primary
// price from comparable currencies, and mirrors that source's trust fields onto
// the hotel. Search results should call this after all providers have merged.
func FinalizeHotelPriceTrust(hotels []HotelResult, preferredCurrency string, now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	preferredCurrency = strings.ToUpper(strings.TrimSpace(preferredCurrency))

	for i := range hotels {
		FinalizeHotelResultPriceTrust(&hotels[i], preferredCurrency, now)
	}
}

// FinalizeHotelResultPriceTrust is the single-result form used by tests and
// detail handlers.
func FinalizeHotelResultPriceTrust(h *HotelResult, preferredCurrency string, now time.Time) {
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
	if selected, ok := selectPrimaryHotelSource(h.Sources, preferredCurrency, h.Currency); ok {
		h.Price = selected.Price
		h.Currency = selected.Currency
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

func selectPrimaryHotelSource(sources []PriceSource, preferredCurrency, currentCurrency string) (PriceSource, bool) {
	preferredCurrency = strings.ToUpper(strings.TrimSpace(preferredCurrency))
	currentCurrency = strings.ToUpper(strings.TrimSpace(currentCurrency))
	if selected, ok := bestSourceInCurrency(sources, preferredCurrency); ok {
		return selected, true
	}
	if selected, ok := bestSourceInCurrency(sources, currentCurrency); ok {
		return selected, true
	}
	bestCurrency := mostRepresentedSourceCurrency(sources)
	if selected, ok := bestSourceInCurrency(sources, bestCurrency); ok {
		return selected, true
	}
	return PriceSource{}, false
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
