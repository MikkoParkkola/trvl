package flights

import (
	"context"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// TestAllianceCodes probes the Google Flights batchexecute API to determine:
//
//  1. Whether numeric alliance codes (1, 2, 3) work at segment[5]
//  2. Whether string-number codes ("1", "2", "3") work
//  3. Whether multi-alliance is AND (intersection) or OR (union)
//  4. Whether alliance names are case-sensitive
//
// Route: HEL -> LHR, 2026-06-15. All probes hit segment[5] (verified position).
//
// Run: go test ./internal/flights/ -run TestAllianceCodes -v -count=1 -timeout 300s
func TestAllianceCodes(t *testing.T) {
	testutil.RequireLiveProbe(t)

	client := batchexec.NewClient()
	client.SetNoCache(true)
	client.SetRateLimit(0.5) // ~2s between requests

	const (
		origin = "HEL"
		dest   = "LHR"
		date   = "2026-06-15"
	)
	t.Logf("Route: %s -> %s, date: %s", origin, dest, date)

	baseOpts := SearchOptions{Adults: 1}
	baseOpts.defaults()

	// ---- Baseline (no alliance filter) ----
	var baseline int
	{
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := SearchFlightsWithClient(ctx, client, origin, dest, date, baseOpts)
		if err != nil {
			t.Fatalf("baseline search failed: %v", err)
		}
		baseline = res.Count
		t.Logf("BASELINE: %d flights (no alliance filter)", baseline)
		if baseline == 0 {
			t.Fatal("baseline returned 0 flights -- route/date unusable")
		}
	}

	// ---- Part 1: Numeric codes at segment[5] ----
	t.Run("NumericCodes", func(t *testing.T) {
		probes := []filterProbe{
			{"int_1", []any{1}},
			{"int_2", []any{2}},
			{"int_3", []any{3}},
			{"str_1", []any{"1"}},
			{"str_2", []any{"2"}},
			{"str_3", []any{"3"}},
		}
		results := runSegmentProbes(t, client, origin, dest, date, baseOpts, 5, probes)

		t.Log("")
		t.Logf("=== NUMERIC CODES RESULTS (baseline=%d) ===", baseline)
		t.Logf("%-25s  %6s  %6s  %8s", "FORMAT", "STATUS", "COUNT", "BODY")
		for _, r := range results {
			if r.err != nil {
				t.Logf("%-25s  %6s  %6s  %8s  %v", r.name, "ERR", "-", "-", r.err)
				continue
			}
			t.Logf("%-25s  %6d  %6d  %8d", r.name, r.status, r.count, r.bodySize)
			if r.status == 200 && r.count > 0 && r.count < baseline {
				t.Logf("  -> WORKS: fewer results than baseline (%d < %d)", r.count, baseline)
			} else if r.status == 200 && r.count == baseline {
				t.Logf("  -> IGNORED: same count as baseline (filter had no effect)")
			} else if r.status == 200 && r.count == 0 {
				t.Logf("  -> EMPTY: returned 0 flights (invalid code or no matches)")
			}
		}
	})

	// ---- Part 2: Known string codes for reference ----
	t.Run("StringCodes", func(t *testing.T) {
		probes := []filterProbe{
			{"STAR_ALLIANCE", []any{"STAR_ALLIANCE"}},
			{"ONEWORLD", []any{"ONEWORLD"}},
			{"SKYTEAM", []any{"SKYTEAM"}},
		}
		results := runSegmentProbes(t, client, origin, dest, date, baseOpts, 5, probes)

		t.Log("")
		t.Logf("=== STRING CODES RESULTS (baseline=%d) ===", baseline)
		t.Logf("%-25s  %6s  %6s", "ALLIANCE", "STATUS", "COUNT")
		for _, r := range results {
			if r.err != nil {
				t.Logf("%-25s  %6s  %6s  %v", r.name, "ERR", "-", r.err)
				continue
			}
			t.Logf("%-25s  %6d  %6d", r.name, r.status, r.count)
		}
	})

	// ---- Part 3: Multi-alliance AND vs OR ----
	t.Run("MultiAlliance", func(t *testing.T) {
		probes := []filterProbe{
			{"SA+OW_combined", []any{"STAR_ALLIANCE", "ONEWORLD"}},
			{"SA+ST_combined", []any{"STAR_ALLIANCE", "SKYTEAM"}},
			{"OW+ST_combined", []any{"ONEWORLD", "SKYTEAM"}},
			{"all_three", []any{"STAR_ALLIANCE", "ONEWORLD", "SKYTEAM"}},
		}
		results := runSegmentProbes(t, client, origin, dest, date, baseOpts, 5, probes)

		t.Log("")
		t.Logf("=== MULTI-ALLIANCE RESULTS (baseline=%d) ===", baseline)
		t.Logf("%-25s  %6s  %6s", "COMBINATION", "STATUS", "COUNT")
		for _, r := range results {
			if r.err != nil {
				t.Logf("%-25s  %6s  %6s  %v", r.name, "ERR", "-", r.err)
				continue
			}
			t.Logf("%-25s  %6d  %6d", r.name, r.status, r.count)
		}

		// Cross-reference with single-alliance counts from Part 2.
		// If combined < min(single_A, single_B), it is AND (intersection).
		// If combined > max(single_A, single_B), it is OR (union).
		t.Log("")
		t.Log("=== AND vs OR ANALYSIS ===")
		t.Log("If combined > max(single_A, single_B) -> OR (union)")
		t.Log("If combined < min(single_A, single_B) -> AND (intersection)")
		t.Log("If combined == sum(single_A, single_B) -> OR (no overlap)")
		t.Log("(Cross-reference with StringCodes sub-test counts above)")
	})

	// ---- Part 4: Case sensitivity ----
	t.Run("CaseSensitivity", func(t *testing.T) {
		probes := []filterProbe{
			{"STAR_ALLIANCE_upper", []any{"STAR_ALLIANCE"}},
			{"star_alliance_lower", []any{"star_alliance"}},
			{"Star_Alliance_mixed", []any{"Star_Alliance"}},
			{"StarAlliance_nounderscore", []any{"StarAlliance"}},
		}
		results := runSegmentProbes(t, client, origin, dest, date, baseOpts, 5, probes)

		t.Log("")
		t.Logf("=== CASE SENSITIVITY RESULTS (baseline=%d) ===", baseline)
		t.Logf("%-30s  %6s  %6s", "FORMAT", "STATUS", "COUNT")
		for _, r := range results {
			if r.err != nil {
				t.Logf("%-30s  %6s  %6s  %v", r.name, "ERR", "-", r.err)
				continue
			}
			t.Logf("%-30s  %6d  %6d", r.name, r.status, r.count)

			if r.status == 200 && r.count == baseline {
				t.Logf("  -> IGNORED (same as baseline -- filter not recognized)")
			}
		}

		// Compare upper vs lower.
		upper := findResult(results, "STAR_ALLIANCE_upper")
		lower := findResult(results, "star_alliance_lower")
		mixed := findResult(results, "Star_Alliance_mixed")
		if upper != nil && upper.status == 200 && upper.count < baseline {
			if lower != nil && lower.status == 200 {
				switch lower.count {
				case upper.count:
					t.Logf("CASE INSENSITIVE: lowercase returns same count (%d)", lower.count)
				case baseline:
					t.Logf("CASE SENSITIVE: lowercase ignored (returns baseline %d vs upper %d)", lower.count, upper.count)
				default:
					t.Logf("AMBIGUOUS: upper=%d, lower=%d, baseline=%d", upper.count, lower.count, baseline)
				}
			}
			if mixed != nil && mixed.status == 200 {
				switch mixed.count {
				case upper.count:
					t.Logf("MIXED CASE: same as upper (%d) -- case insensitive", mixed.count)
				case baseline:
					t.Logf("MIXED CASE: ignored (returns baseline %d) -- case sensitive", mixed.count)
				}
			}
		}
	})
}

// findResult finds a probeResult by name in a slice, or nil if not found.
func findResult(results []probeResult, name string) *probeResult {
	for i := range results {
		if results[i].name == name {
			return &results[i]
		}
	}
	return nil
}
