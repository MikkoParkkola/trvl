package providers

import (
	"errors"
	"math"
	"net/http"
	"testing"
)

// TestConvertRatePeggedHRK proves a euro-pegged legacy currency the live feed
// no longer carries still converts via its fixed peg, so a HRK-quoted offer is
// not dropped for an unconvertible currency.
func TestConvertRatePeggedHRK(t *testing.T) {
	withOfflineFX(t)
	r, ok := ConvertRate("HRK", "EUR")
	if !ok {
		t.Fatal("HRK->EUR must convert via the fixed euro peg")
	}
	if want := 1 / 7.53450; math.Abs(r-want) > 1e-9 {
		t.Errorf("HRK->EUR = %v, want %v", r, want)
	}

	r, ok = ConvertRate("EUR", "HRK")
	if !ok || math.Abs(r-7.53450) > 1e-9 {
		t.Errorf("EUR->HRK = %v ok=%v, want 7.53450", r, ok)
	}

	// A genuinely unrecognized pair must still report no rate (the peg must not
	// mask it as convertible).
	if _, ok := ConvertRate("XYZ", "EUR"); ok {
		t.Error("unrecognized currency must not convert")
	}
}

// withOfflineFX gives the package FX cache a client that fails every request,
// so rates come from the built-in fallback table and no test reaches the live
// rates API (MIK-8107). Restored on cleanup; not for t.Parallel.
func withOfflineFX(t *testing.T) {
	t.Helper()
	old := defaultFXCache
	t.Cleanup(func() { defaultFXCache = old })
	defaultFXCache = newFXCache()
	defaultFXCache.client = &http.Client{Transport: failingFXTransport{}}
}

type failingFXTransport struct{}

func (failingFXTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("offline FX transport")
}
