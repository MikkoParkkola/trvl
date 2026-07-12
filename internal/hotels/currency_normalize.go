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

// ensureComparableInTargetCurrency enforces the hotel ComparablePrice invariant:
// a hotel is comparable iff it is expressed in the requested target currency, and
// then its comparable value just is its Price. Merge sets ComparablePrice
// pre-normalization, but FinalizeHotelPriceTrust re-derives Price AND Currency
// from the raw provider sources afterwards (an EUR80 lead-in becomes an EUR200
// verified room; a hotel normalized to EUR can even be flipped back to a verified
// USD source) without touching ComparablePrice. Filling only zero values would
// leave a stale ComparablePrice that makes PriceForRanking, sort, filters and the
// summary headline select on a phantom number while displaying a different one.
// So we re-assert the invariant both ways: sync ComparablePrice to Price for
// target-currency hotels, and zero it for anything now in a foreign currency
// (its target-currency comparable no longer exists — it stays in results with its
// real foreign price but cannot win a cross-currency "cheapest" headline).
func ensureComparableInTargetCurrency(hotels []models.HotelResult, optsCurrency string) {
	tc := strings.TrimSpace(optsCurrency)
	if tc == "" {
		return
	}
	tU := strings.ToUpper(tc)
	for i := range hotels {
		if hotels[i].Price > 0 &&
			strings.ToUpper(strings.TrimSpace(hotels[i].Currency)) == tU {
			hotels[i].ComparablePrice = hotels[i].Price
		} else {
			hotels[i].ComparablePrice = 0
		}
	}
}
