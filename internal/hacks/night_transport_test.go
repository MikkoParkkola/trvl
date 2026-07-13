package hacks

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// TestDetectNightTransport_nonEURTarget_suppressedWhenInconvertible proves
// case (b): a non-EUR target currency that can't be honestly converted
// (XXX is the ISO 4217 "no currency" placeholder — it will never appear in
// a real exchange-rate table) suppresses the hack entirely instead of
// labeling a EUR-denominated hotel-saving estimate with the wrong currency.
// The guard runs before the ground-transport search, so this is
// deterministic and network-independent. Uses a fake convertCurrency
// injected via the seam in currency.go so the "can't convert" contract is
// exercised offline instead of dialing the live rates table (which would
// otherwise be reached first to resolve EUR, before XXX is even considered).
// No t.Parallel — the seam var is shared package state, set/restored
// sequentially like railGroundSearcher.
func TestDetectNightTransport_nonEURTarget_suppressedWhenInconvertible(t *testing.T) {
	orig := convertCurrencyFn
	convertCurrencyFn = func(_ context.Context, amount float64, from, to string) (float64, string) {
		if from == to {
			return amount, to
		}
		return amount, from // can't convert — same contract as the real function
	}
	t.Cleanup(func() { convertCurrencyFn = orig })

	hacks := detectNightTransport(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "PRG",
		Date:        "2026-07-01",
		Currency:    "XXX",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for inconvertible target currency, got %d", len(hacks))
	}
}

// TestDetectNightTransport_eurTarget_labelsEURAndConverts proves cases (a)
// and (c): EUR passes through untouched and, when a hack surfaces, its
// Currency matches the target. Ground-transport search needs the live
// network, so this is gated the same way as the existing
// TestDetectNightTransport_validRoute in coverage_target_test.go.
func TestDetectNightTransport_eurTarget_labelsEURAndConverts(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	hacks := detectNightTransport(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "PRG",
		Date:        "2026-07-01",
		Currency:    "EUR",
	})
	for _, h := range hacks {
		if h.Currency != "EUR" {
			t.Errorf("Currency = %q, want EUR", h.Currency)
		}
	}
}
