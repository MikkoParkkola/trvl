package hacks

import (
	"context"
	"sync/atomic"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// currencyConverter is the currency-conversion seam every detector must go
// through instead of calling destinations.ConvertCurrency directly.
type currencyConverter func(ctx context.Context, amount float64, from, to string) (float64, string)

// convertCurrencySeam holds the active converter. Production wires it to
// destinations.ConvertCurrency; tests swap in a deterministic fake.
//
// It is atomic rather than a plain var because detector goroutines can outlive
// the call that started them. DetectAll returns at its caller's deadline and
// leaves stragglers finishing, so a detector from one test can still be reading
// this while the next test writes it — a real data race that CI caught with
// -race and a plain var could not prevent. Sequencing test writes cannot fix it;
// the reader is not under the test's control.
var convertCurrencySeam atomic.Pointer[currencyConverter]

func init() {
	setCurrencyConverter(destinations.ConvertCurrency)
}

// setCurrencyConverter installs a converter. Tests use swapCurrencyConverter.
func setCurrencyConverter(fn currencyConverter) {
	convertCurrencySeam.Store(&fn)
}

// currentCurrencyConverter returns the active converter.
func currentCurrencyConverter() currencyConverter {
	return *convertCurrencySeam.Load()
}

// convertCurrency converts amount from `from` into `target` through the
// package's currency seam and reports whether the conversion actually landed
// in target. This is the single gate every hack detector must pass a price
// through before combining it with, or displaying it alongside, a price
// already expressed in a different currency: a non-ok result means the
// caller must drop the candidate rather than mix denominations or mislabel a
// total. amount<=0 is always non-convertible (nothing to display).
func convertCurrency(ctx context.Context, amount float64, from, target string) (float64, bool) {
	if amount <= 0 {
		return 0, false
	}
	conv, gotCur := currentCurrencyConverter()(ctx, amount, from, target)
	if gotCur != target {
		return 0, false
	}
	return conv, true
}

// cheapestFlightPriceInTarget returns the cheapest positive flight price
// after converting every flight's price into target via convertCurrency,
// together with an ok flag. Flights that are non-positive or inconvertible
// into target are skipped rather than shown unconverted or mislabelled.
// Returns (0, false) when nothing is convertible.
//
// This is the currency.go counterpart of positioning.go's cheapestFlightPriceIn.
// positioning.go, helpers.go and open_jaw.go still call
// destinations.ConvertCurrency directly and predate this seam; new detectors
// route through here instead so their currency-conversion behaviour is
// injectable in tests via the currency seam.
func cheapestFlightPriceInTarget(ctx context.Context, r *models.FlightSearchResult, target string) (float64, bool) {
	if r == nil || !r.Success {
		return 0, false
	}
	best := 0.0
	found := false
	for _, f := range r.Flights {
		conv, ok := convertCurrency(ctx, f.Price, f.Currency, target)
		if !ok {
			continue
		}
		if !found || conv < best {
			best, found = conv, true
		}
	}
	return best, found
}

// groundLegPriceInTarget returns the cheapest ground-transport price for a
// from/to/date/groundType search, converted into target: a live quote when
// ground.SearchByName returns one in a convertible currency, otherwise the
// staticEUR fallback estimate converted into target. Returns (0, false, false)
// when neither source can be expressed in target.
//
// The third return value, estimated, is true when the price came from the
// static EUR fallback rather than a live provider quote — callers surface this
// as an "(estimated fare)" marker so the number is honest about its provenance.
//
// Shared by ferry_positioning.go, multimodal_positioning.go, and
// multimodal_open_jaw_ground.go — each prices a ground leg by preferring a
// live provider quote over a conservative static EUR estimate, and each must
// suppress the candidate rather than mix currencies when neither converts.
//
// Only a live fare counts as a live quote: estimated and unavailable routes are
// ignored, so a leg priced by them falls back to the flagged static estimate.
// The search carries the caller's ground scope (see DetectorInput.groundScope).
func groundLegPriceInTarget(ctx context.Context, in *DetectorInput, from, to, groundType string, staticEUR float64, target string) (price float64, ok bool, estimated bool) {
	best, found := 0.0, false
	opts := in.groundScope()
	opts.Currency, opts.Type = target, groundType
	result, err := ground.SearchByName(ctx, from, to, in.Date, opts)
	if err == nil && result.Success {
		for _, r := range result.Routes {
			if r.PriceProvenance != models.PriceLive {
				continue
			}
			conv, ok := convertCurrency(ctx, r.Price, r.Currency, target)
			if !ok {
				continue
			}
			if !found || conv < best {
				best, found = conv, true
			}
		}
	}
	if found {
		return best, true, false
	}
	est, converted := convertCurrency(ctx, staticEUR, "EUR", target)
	return est, converted, converted
}
