package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/multimodal"
)

// TestPrintMultimodalPlan_DisclosesLegDetail is the same-package regression test
// for the cmd/trvl half of the "price each leg in its discovered mode, stop
// relabeling ferries as buses" fix. The pricing logic that *computes* a leg's
// intermodal Detail (e.g. "via bus + ferry") is covered in
// internal/multimodal/pricing_mode_test.go; this test guards the rendering half:
// printMultimodalPlan must actually surface a non-empty PricedLeg.Detail so an
// embedded ferry is disclosed to the user rather than silently hidden behind a
// single "bus" label.
//
// Fail-before/pass-after: before the fix the leg line was printed without the
// Detail suffix, so the "via bus + ferry" disclosure never reached stdout.
func TestPrintMultimodalPlan_DisclosesLegDetail(t *testing.T) {
	plan := &multimodal.Plan{
		From:       "Helsinki",
		To:         "Tallinn",
		Date:       "2026-07-09",
		Discovered: 1,
		Itineraries: []multimodal.Itinerary{
			{
				From:       "Helsinki",
				To:         "Tallinn",
				Date:       "2026-07-09",
				Currency:   "EUR",
				TotalPrice: 42,
				ModeChain:  "bus → ferry",
				Legs: []multimodal.PricedLeg{
					{
						Mode:     "bus",
						From:     "Helsinki",
						To:       "Tallinn",
						Price:    42,
						Currency: "EUR",
						Provider: "FlixBus",
						Detail:   "via bus + ferry",
					},
					{
						Mode:     "train",
						From:     "Tallinn",
						To:       "Tartu",
						Price:    12,
						Currency: "EUR",
						Provider: "Elron",
						// No Detail: a single-mode leg must render no suffix.
					},
				},
			},
		},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	printMultimodalPlan(plan)
	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "via bus + ferry") {
		t.Fatalf("leg Detail not disclosed: ferry crossing hidden behind bus label\n--- output ---\n%s", out)
	}

	// The single-mode train leg must not gain a spurious detail suffix. Its line
	// should end at the provider with no trailing "via ..." chain disclosure.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Tallinn→Tartu") && strings.Contains(line, "via bus + ferry") {
			t.Fatalf("single-mode leg leaked an unrelated Detail suffix: %q", line)
		}
	}
}
