package flights

import (
	"context"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// TestAdvancedProbe runs two live probes to fill remaining knowledge gaps:
//
//  1. Checked Bags Differentiation on a BUDGET route (HEL->STN)
//     The original bags probe on HEL->NRT showed [1,0] and [1,1] returning
//     identical counts (85 flights). Budget carriers (Ryanair, Wizz, easyJet)
//     are more likely to differentiate because checked bags cost extra.
//
//  2. ONEWORLD + SKYTEAM Alliance verification at segment[5]
//     STAR_ALLIANCE was confirmed working. Verify the other two alliances
//     and a multi-alliance combo on HEL->LHR (good alliance mix).
func TestAdvancedProbe(t *testing.T) {
	testutil.RequireLiveProbe(t)

	client := batchexec.NewClient()
	client.SetNoCache(true)
	client.SetRateLimit(0.5)

	t.Run("CheckedBagsBudgetRoute", func(t *testing.T) {
		testCheckedBagsBudgetRoute(t, client)
	})

	t.Run("AllianceVariants", func(t *testing.T) {
		testAllianceVariants(t, client)
	})
}

// testCheckedBagsBudgetRoute probes bags filter on HEL->STN (London Stansted),
// a route dominated by budget carriers where checked bags should differentiate.
//
// Variations:
//
//	[0,0] = baseline (no bag requirement)
//	[1,0] = carry-on only
//	[1,1] = carry-on + 1 checked
//	[0,1] = checked only (no carry-on)
//	[0,2] = 2 checked bags
//
// If any variant returns fewer flights than baseline, checked bags filtering
// WORKS on budget routes.
func testCheckedBagsBudgetRoute(t *testing.T, client *batchexec.Client) {
	searchDate := time.Now().AddDate(0, 0, 21).Format("2006-01-02")
	t.Logf("Route: HEL -> STN, date: %s", searchDate)

	baseOpts := SearchOptions{Adults: 1}
	baseOpts.defaults()

	// Baseline sanity check.
	{
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := SearchFlightsWithClient(ctx, client, "HEL", "STN", searchDate, baseOpts)
		if err != nil {
			t.Fatalf("baseline search failed: %v", err)
		}
		t.Logf("Baseline (SearchFlightsWithClient): %d flights", res.Count)
		if res.Count == 0 {
			t.Fatal("baseline returned 0 flights -- route/date unusable")
		}
	}

	probes := []filterProbe{
		{name: "[0,0]_baseline", value: []any{0, 0}, expectOK: true},
		{name: "[1,0]_carryon", value: []any{1, 0}, expectOK: true},
		{name: "[1,1]_carry+check", value: []any{1, 1}, expectOK: true},
		{name: "[0,1]_checked_only", value: []any{0, 1}, expectOK: true},
		{name: "[0,2]_2checked", value: []any{0, 2}, expectOK: true},
	}

	results := make([]probeResult, len(probes))

	for i, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			filters := buildFilters("HEL", "STN", searchDate, baseOpts)
			patched := patchBagsPosition(t, filters, p.value)

			encoded, err := batchexec.EncodeFlightFilters(patched)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}

			status, body, err := client.SearchFlights(ctx, encoded)
			if err != nil {
				t.Logf("FAILED %s: %v", p.name, err)
				results[i] = probeResult{name: p.name, err: err}
				return
			}

			count := 0
			if status == 200 {
				count = countFlightsProbe(t, body)
			} else {
				t.Logf("raw body: %s", truncateBytes(body, 300))
			}

			results[i] = probeResult{
				name:     p.name,
				status:   status,
				count:    count,
				bodySize: len(body),
				expectOK: p.expectOK,
			}
			t.Logf("%-20s  status=%d  flights=%d  body=%d bytes",
				p.name, status, count, len(body))
		})
	}

	analyzeResults(t, "BAGS_BUDGET_HEL_STN", results)

	// Extra analysis: compare body sizes even when counts match.
	t.Log("")
	t.Log("=== CHECKED BAGS DIFFERENTIATION ===")
	m := map[string]*probeResult{}
	for i := range results {
		m[results[i].name] = &results[i]
	}

	baseline := m["[0,0]_baseline"]
	carryon := m["[1,0]_carryon"]
	carryCheck := m["[1,1]_carry+check"]
	checkedOnly := m["[0,1]_checked_only"]
	twoChecked := m["[0,2]_2checked"]

	if carryon != nil && carryCheck != nil &&
		carryon.status == 200 && carryCheck.status == 200 {
		if carryCheck.count < carryon.count {
			t.Logf("PROVEN: checked bags DIFFERENTIATE on budget route "+
				"([1,1]=%d < [1,0]=%d)", carryCheck.count, carryon.count)
		} else if carryCheck.count == carryon.count {
			if carryCheck.bodySize != carryon.bodySize {
				t.Logf("AMBIGUOUS: same count (%d) but different body sizes "+
					"(%d vs %d) -- pricing may differ", carryon.count,
					carryon.bodySize, carryCheck.bodySize)
			} else {
				t.Logf("NO DIFFERENTIATION: [1,1] and [1,0] identical "+
					"(%d flights, %d bytes) even on budget route",
					carryon.count, carryon.bodySize)
			}
		} else {
			t.Logf("UNEXPECTED: [1,1] has MORE flights (%d) than [1,0] (%d)",
				carryCheck.count, carryon.count)
		}
	}

	if baseline != nil && checkedOnly != nil &&
		baseline.status == 200 && checkedOnly.status == 200 {
		if checkedOnly.count < baseline.count {
			t.Logf("PROVEN: [0,1] filters flights (%d < baseline %d)",
				checkedOnly.count, baseline.count)
		}
	}

	if baseline != nil && twoChecked != nil &&
		baseline.status == 200 && twoChecked.status == 200 {
		if twoChecked.count < baseline.count {
			t.Logf("PROVEN: [0,2] filters flights (%d < baseline %d)",
				twoChecked.count, baseline.count)
		}
	}
}

// testAllianceVariants verifies ONEWORLD and SKYTEAM at segment[5], and tests
// multi-alliance combo. STAR_ALLIANCE was confirmed working (45/115 flights on
// HEL->LHR = 61% reduction).
func testAllianceVariants(t *testing.T, client *batchexec.Client) {
	searchDate := time.Now().AddDate(0, 0, 21).Format("2006-01-02")
	t.Logf("Route: HEL -> LHR, date: %s", searchDate)

	baseOpts := SearchOptions{Adults: 1}
	baseOpts.defaults()

	// Baseline sanity check.
	{
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := SearchFlightsWithClient(ctx, client, "HEL", "LHR", searchDate, baseOpts)
		if err != nil {
			t.Fatalf("baseline search failed: %v", err)
		}
		t.Logf("Baseline (SearchFlightsWithClient): %d flights", res.Count)
		if res.Count == 0 {
			t.Fatal("baseline returned 0 flights -- route/date unusable")
		}
	}

	probes := []filterProbe{
		{name: "nil_baseline", value: nil, expectOK: true},
		{name: "ONEWORLD", value: []any{"ONEWORLD"}, expectOK: true},
		{name: "SKYTEAM", value: []any{"SKYTEAM"}, expectOK: true},
		{name: "STAR_ALLIANCE", value: []any{"STAR_ALLIANCE"}, expectOK: true},
		{name: "STAR+ONEWORLD", value: []any{"STAR_ALLIANCE", "ONEWORLD"}, expectOK: true},
	}

	results := make([]probeResult, len(probes))

	for i, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			filters := buildFilters("HEL", "LHR", searchDate, baseOpts)
			patched := patchSegmentPosition(t, filters, 5, p.value)

			encoded, err := batchexec.EncodeFlightFilters(patched)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}

			status, body, err := client.SearchFlights(ctx, encoded)
			if err != nil {
				t.Logf("FAILED %s: %v", p.name, err)
				results[i] = probeResult{name: p.name, err: err}
				return
			}

			count := 0
			if status == 200 {
				count = countFlightsProbe(t, body)
			} else {
				t.Logf("raw body: %s", truncateBytes(body, 300))
			}

			results[i] = probeResult{
				name:     p.name,
				status:   status,
				count:    count,
				bodySize: len(body),
				expectOK: p.expectOK,
			}
			t.Logf("%-20s  status=%d  flights=%d  body=%d bytes",
				p.name, status, count, len(body))
		})
	}

	analyzeResults(t, "ALLIANCE_SEG5", results)

	// Extra analysis: union of alliances should be >= individual alliances.
	t.Log("")
	t.Log("=== ALLIANCE UNION CHECK ===")
	m := map[string]*probeResult{}
	for i := range results {
		m[results[i].name] = &results[i]
	}

	star := m["STAR_ALLIANCE"]
	ow := m["ONEWORLD"]
	combo := m["STAR+ONEWORLD"]

	if star != nil && ow != nil && combo != nil &&
		star.status == 200 && ow.status == 200 && combo.status == 200 {
		if combo.count >= star.count && combo.count >= ow.count {
			t.Logf("UNION OK: STAR+ONEWORLD=%d >= STAR=%d, >= ONEWORLD=%d",
				combo.count, star.count, ow.count)
		} else {
			t.Logf("UNION UNEXPECTED: STAR+ONEWORLD=%d, STAR=%d, ONEWORLD=%d",
				combo.count, star.count, ow.count)
		}
		// Sum should approximate the union (assuming little overlap).
		sum := star.count + ow.count
		if combo.count <= sum {
			t.Logf("PLAUSIBLE: combo %d <= sum %d (no double-counting)",
				combo.count, sum)
		} else {
			t.Logf("SUSPICIOUS: combo %d > sum %d (unexpected overlap)",
				combo.count, sum)
		}
	}
}
