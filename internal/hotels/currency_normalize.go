package hotels

import (
	"context"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// hotelCurrencyConverter returns the shared converter used by the hotel search
// paths. Success convention mirrors ground: the conversion is trusted only when
// the returned status echoes the requested to-currency.
func hotelCurrencyConverter() currencyConverter {
	return func(ctx context.Context, amount float64, from, to string) (float64, bool) {
		amt, status := destinations.ConvertCurrency(ctx, amount, from, to)
		if amt > 0 && strings.EqualFold(strings.TrimSpace(status), strings.TrimSpace(to)) {
			return amt, true
		}
		return amt, false
	}
}

// normalizeBatchesToTarget converts every batch to a common target currency
// before merge, so the merge headline compares within one currency and Min/Max
// filters see comparable numbers. Target derivation mirrors ground: the opts
// currency if set, else the first observed currency across the raw batches.
// Nothing is hardcoded; with no derivable target the batches are left as-is.
func normalizeBatchesToTarget(ctx context.Context, batches [][]models.HotelResult, optsCurrency string) {
	target := strings.TrimSpace(optsCurrency)
	if target == "" {
		for _, batch := range batches {
			for _, h := range batch {
				if c := strings.TrimSpace(h.Currency); c != "" {
					target = c
					break
				}
			}
			if target != "" {
				break
			}
		}
	}
	if target == "" {
		return
	}
	conv := hotelCurrencyConverter()
	for _, batch := range batches {
		normalizeHotelCurrencies(ctx, batch, target, conv)
	}
}

// ensureComparableInTargetCurrency keeps ComparablePrice populated for any
// headline already expressed in the requested target currency. Merge sets it
// pre-normalization, but FinalizeHotelPriceTrust and room enrichment can refresh
// Price/Currency from a source; this re-asserts the invariant so PriceForRanking,
// sort and filters stay cross-currency honest instead of falling back to a raw
// foreign-currency number.
func ensureComparableInTargetCurrency(hotels []models.HotelResult, optsCurrency string) {
	tc := strings.TrimSpace(optsCurrency)
	if tc == "" {
		return
	}
	tU := strings.ToUpper(tc)
	for i := range hotels {
		if hotels[i].Price > 0 && hotels[i].ComparablePrice == 0 &&
			strings.ToUpper(strings.TrimSpace(hotels[i].Currency)) == tU {
			hotels[i].ComparablePrice = hotels[i].Price
		}
	}
}
