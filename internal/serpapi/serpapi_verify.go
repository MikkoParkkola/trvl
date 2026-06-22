package serpapi

import (
	"context"
	"strconv"
	"strings"
	"time"
)

func verifyPropertyDetails(ctx context.Context, result *Response, opts SearchOptions) {
	if result == nil {
		return
	}
	maxDetails := opts.MaxDetails
	if maxDetails <= 0 {
		maxDetails = 8
	}
	checkedAt := time.Now().UTC()
	verified := 0
	for i := range result.Properties {
		if verified >= maxDetails {
			markDetailLimitRemaining(result.Properties[i:], opts.Currency, checkedAt)
			markDetailLimitRemaining(result.Ads, opts.Currency, checkedAt)
			return
		}
		verifyOneProperty(ctx, &result.Properties[i], opts, checkedAt)
		verified++
	}
	for i := range result.Ads {
		if verified >= maxDetails {
			markDetailLimitRemaining(result.Ads[i:], opts.Currency, checkedAt)
			return
		}
		verifyOneProperty(ctx, &result.Ads[i], opts, checkedAt)
		verified++
	}
}

func markDetailLimitRemaining(hotels []Hotel, currency string, checkedAt time.Time) {
	for i := range hotels {
		hotels[i].PriceVerification = &PriceVerification{
			Status:    "detail_not_checked_limit",
			ListTotal: hotels[i].TotalPrice(),
			Currency:  currency,
			CheckedAt: checkedAt,
			Warnings:  []string{"list_price_not_final_quote"},
		}
	}
}

func verifyOneProperty(ctx context.Context, hotel *Hotel, opts SearchOptions, checkedAt time.Time) {
	if hotel == nil {
		return
	}
	if hotel.PropertyToken == "" {
		hotel.PriceVerification = &PriceVerification{
			Status:    "list_only_unverified",
			ListTotal: hotel.TotalPrice(),
			Currency:  opts.Currency,
			CheckedAt: checkedAt,
			Warnings:  []string{"missing_property_token", "list_price_not_final_quote"},
		}
		return
	}
	detail, err := GetPropertyDetails(ctx, opts, hotel.PropertyToken)
	if err != nil {
		hotel.PriceVerification = &PriceVerification{
			Status:    "detail_fetch_failed",
			ListTotal: hotel.TotalPrice(),
			Currency:  opts.Currency,
			CheckedAt: checkedAt,
			Warnings:  []string{"list_price_not_final_quote"},
			Error:     err.Error(),
		}
		return
	}
	mergePropertyDetails(hotel, *detail, opts.Currency, checkedAt)
}

func mergePropertyDetails(hotel *Hotel, detail Hotel, currency string, checkedAt time.Time) {
	if detail.Prices != nil {
		hotel.Prices = detail.Prices
	}
	if detail.FeaturedPrices != nil {
		hotel.FeaturedPrices = detail.FeaturedPrices
	}
	if detail.Link != "" {
		hotel.Link = detail.Link
	}
	if len(detail.Amenities) > 0 {
		hotel.Amenities = detail.Amenities
	}
	if detail.FreeCancellation {
		hotel.FreeCancellation = true
	}

	listRate := hotel.RatePerNight
	listTotal := hotel.TotalRate
	best, ok := hotel.LowestProviderOption()
	if !ok {
		hotel.PriceVerification = &PriceVerification{
			Status:    "detail_prices_missing",
			ListTotal: listTotal.Extracted,
			Currency:  currency,
			CheckedAt: checkedAt,
			Warnings:  []string{"provider_prices_missing", "list_price_not_final_quote"},
		}
		return
	}

	hotel.ListRatePerNight = &listRate
	hotel.ListTotalRate = &listTotal
	hotel.RatePerNight = best.RatePerNight
	hotel.TotalRate = best.TotalRate

	delta := best.TotalRate.Extracted - listTotal.Extracted
	deltaPct := 0.0
	if listTotal.Extracted > 0 {
		deltaPct = delta / listTotal.Extracted * 100
	}
	warnings := []string(nil)
	if absFloat(deltaPct) > 3 {
		warnings = append(warnings, "detail_price_differs_from_list")
	}
	if deltaPct > 10 {
		warnings = append(warnings, "detail_price_higher_than_list")
	}
	hotel.PriceVerification = &PriceVerification{
		Status:        "detail_verified",
		Source:        best.Source,
		ListTotal:     listTotal.Extracted,
		VerifiedTotal: best.TotalRate.Extracted,
		Delta:         delta,
		DeltaPct:      deltaPct,
		Currency:      currency,
		CheckedAt:     checkedAt,
		Warnings:      warnings,
	}
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func (h *Hotel) PricePerNight() float64 {
	if h.RatePerNight.Extracted > 0 {
		return h.RatePerNight.Extracted
	}
	return 0
}

func (h *Hotel) TotalPrice() float64 {
	if h.TotalRate.Extracted > 0 {
		return h.TotalRate.Extracted
	}
	// Total not available — the caller should compute from nights.
	return 0
}

func (h *Hotel) LowestProviderOption() (PriceOption, bool) {
	options := h.ProviderOptions()
	var best PriceOption
	found := false
	for _, option := range options {
		if option.TotalRate.Extracted <= 0 {
			continue
		}
		if !found || option.TotalRate.Extracted < best.TotalRate.Extracted {
			best = option
			found = true
		}
	}
	return best, found
}

func (h *Hotel) ProviderOptions() []PriceOption {
	if h == nil {
		return nil
	}
	options := make([]PriceOption, 0, len(h.FeaturedPrices)+len(h.Prices))
	options = append(options, h.FeaturedPrices...)
	options = append(options, h.Prices...)
	return options
}

func (h *Hotel) ProviderPrices(currency string) string {
	options := h.ProviderOptions()
	if len(options) == 0 {
		return ""
	}
	var b strings.Builder
	for i, p := range options {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Source)
		b.WriteString(" ")
		if currency != "" {
			b.WriteString(currency)
		}
		if p.TotalRate.Extracted > 0 {
			b.WriteString(strconv.FormatFloat(p.TotalRate.Extracted, 'f', 0, 64))
			b.WriteString(" total")
		} else {
			b.WriteString(strconv.FormatFloat(p.RatePerNight.Extracted, 'f', 0, 64))
			b.WriteString("/nt")
		}
	}
	return b.String()
}
