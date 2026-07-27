package hacks

import "testing"

// swapCurrencyConverter installs fn for the duration of the test and restores
// the previous converter afterwards.
//
// Tests must go through this rather than assigning a package var. Detector
// goroutines can outlive the DetectAll call that started them — that is the
// point of returning at the caller's deadline — so a straggler from an earlier
// test may still be reading the seam while this one writes it. The atomic
// pointer behind this helper is what makes that safe; a plain assignment was a
// data race that CI caught under -race.
func swapCurrencyConverter(t *testing.T, fn currencyConverter) {
	t.Helper()
	prev := currentCurrencyConverter()
	setCurrencyConverter(fn)
	t.Cleanup(func() { setCurrencyConverter(prev) })
}
