package providers

import "testing"

// TestConvertRate covers the exported FX helper wired into accommodation
// currency normalization (issue #277, defect 1). USD/EUR/GBP always resolve
// because fx.go ships hardcoded fallback rates, so this stays deterministic
// whether or not the live Frankfurter endpoint is reachable from CI.
func TestConvertRate(t *testing.T) {
	withOfflineFX(t)
	if r, ok := ConvertRate("EUR", "EUR"); !ok || r != 1 {
		t.Fatalf("identity: got (%v,%v), want (1,true)", r, ok)
	}
	if r, ok := ConvertRate("eur", "EUR"); !ok || r != 1 {
		t.Fatalf("identity is case-insensitive: got (%v,%v), want (1,true)", r, ok)
	}
	if r, ok := ConvertRate("", "USD"); ok || r != 0 {
		t.Fatalf("empty from: got (%v,%v), want (0,false)", r, ok)
	}
	if r, ok := ConvertRate("USD", ""); ok || r != 0 {
		t.Fatalf("empty to: got (%v,%v), want (0,false)", r, ok)
	}
	// withOfflineFX forces the built-in fallback table.
	if r, ok := ConvertRate("USD", "EUR"); !ok || r != fallbackRates["USD"]["EUR"] {
		t.Fatalf("USD->EUR: got (%v,%v), want the fallback rate %v", r, ok, fallbackRates["USD"]["EUR"])
	}
}
