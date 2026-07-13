package flights

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// TestAllianceProbe exhaustively probes the Google Flights batchexecute API to
// find the correct wire position and format for the alliance filter.
//
// Background: outer[1][25] returns 400 for all string/int formats tested so far.
// This test systematically tries every plausible position in both the settings
// array (outer[1]) and the segment array (outer[1][13][0]).
//
// For each position, four formats are tried:
//   - []any{"STAR_ALLIANCE"}   (string array)
//   - []any{1}                 (numeric code: 1=Star Alliance)
//   - 1                        (scalar int)
//   - []any{[]any{"STAR_ALLIANCE"}} (nested array)
//
// Route: HEL->LHR (many alliance carriers). If a position+format returns 200
// with FEWER flights than the baseline, that is the correct slot.
//
// Run: go test ./internal/flights/ -run TestAllianceProbe -v -count=1 -timeout 300s
func TestAllianceProbe(t *testing.T) {
	testutil.RequireLiveProbe(t)

	client := batchexec.NewClient()
	client.SetNoCache(true)
	client.SetRateLimit(0.5) // ~2s between requests

	searchDate := time.Now().AddDate(0, 0, 21).Format("2006-01-02")
	t.Logf("Route: HEL -> LHR, date: %s", searchDate)

	baseOpts := SearchOptions{Adults: 1}
	baseOpts.defaults()

	// ---- Baseline ----
	var baseline int
	{
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := SearchFlightsWithClient(ctx, client, "HEL", "LHR", searchDate, baseOpts)
		if err != nil {
			if errors.Is(err, models.ErrRateLimited) {
				t.Skipf("baseline rate-limited/challenged upstream (not a wire-format drift): %v", err)
			}
			t.Fatalf("baseline search failed: %v", err)
		}
		baseline = res.Count
		t.Logf("BASELINE: %d flights", baseline)
		if baseline == 0 {
			t.Fatal("baseline returned 0 flights -- route/date unusable")
		}
	}

	// Alliance value formats to try at each position.
	formats := []filterProbe{
		{name: "str_STAR_ALLIANCE", value: []any{"STAR_ALLIANCE"}},
		{name: "int_1", value: []any{1}},
		{name: "scalar_1", value: 1},
		{name: "nested_str", value: []any{[]any{"STAR_ALLIANCE"}}},
	}

	// ---- Part 1: Settings positions (outer[1][pos]) ----
	settingsPositions := []int{4, 24, 25, 26, 27, 28}

	t.Run("Settings", func(t *testing.T) {
		for _, pos := range settingsPositions {
			pos := pos
			t.Run(fmt.Sprintf("pos%d", pos), func(t *testing.T) {
				results := runSettingsProbes(t, client, "HEL", "LHR", searchDate, baseOpts, pos, formats)
				analyzeAllianceResults(t, fmt.Sprintf("settings[%d]", pos), baseline, results)
			})
		}
	})

	// ---- Part 2: Segment positions (outer[1][13][0][pos]) ----
	segmentPositions := []int{4, 5, 10, 11, 12, 13, 14}

	t.Run("Segment", func(t *testing.T) {
		for _, pos := range segmentPositions {
			pos := pos
			t.Run(fmt.Sprintf("pos%d", pos), func(t *testing.T) {
				results := runSegmentProbes(t, client, "HEL", "LHR", searchDate, baseOpts, pos, formats)
				analyzeAllianceResults(t, fmt.Sprintf("segment[%d]", pos), baseline, results)
			})
		}
	})

	// ---- Part 3: Extra formats at pos 25 (wider exploration) ----
	t.Run("Settings_pos25_extra", func(t *testing.T) {
		extraFormats := []filterProbe{
			{name: "int_2_skyteam", value: []any{2}},
			{name: "int_3_oneworld", value: []any{3}},
			{name: "nested_int", value: []any{[]any{1}}},
			{name: "str_lowercase", value: []any{"star_alliance"}},
			{name: "str_StarAlliance", value: []any{"StarAlliance"}},
			{name: "double_nested", value: []any{[]any{[]any{"STAR_ALLIANCE"}}}},
			{name: "str_with_nil", value: []any{"STAR_ALLIANCE", nil}},
			{name: "int_pair", value: []any{1, nil}},
		}
		results := runSettingsProbes(t, client, "HEL", "LHR", searchDate, baseOpts, 25, extraFormats)
		analyzeAllianceResults(t, "settings[25]_extra", baseline, results)
	})
}

// analyzeAllianceResults logs each probe result and flags any position+format
// that returns 200 with fewer flights than the baseline.
func analyzeAllianceResults(t *testing.T, label string, baseline int, results []probeResult) {
	t.Helper()

	t.Log("")
	t.Logf("=== %s RESULTS (baseline=%d) ===", label, baseline)
	t.Logf("%-25s  %6s  %6s  %8s", "FORMAT", "STATUS", "COUNT", "BODY")

	foundCandidate := false
	for _, r := range results {
		if r.err != nil {
			t.Logf("%-25s  %6s  %6s  %8s  %v", r.name, "ERR", "-", "-", r.err)
			continue
		}
		t.Logf("%-25s  %6d  %6d  %8d", r.name, r.status, r.count, r.bodySize)

		if r.status == 200 && r.count > 0 && r.count < baseline {
			delta := baseline - r.count
			pct := float64(delta) / float64(baseline) * 100
			t.Logf("*** CANDIDATE: %s/%s -> %d flights (-%d, -%.0f%% vs baseline %d) ***",
				label, r.name, r.count, delta, pct, baseline)
			foundCandidate = true
		}
	}

	if !foundCandidate {
		// Summarize: all 400s, all same count, or mixed.
		n400, n200same, n200more := 0, 0, 0
		for _, r := range results {
			switch {
			case r.status == 400:
				n400++
			case r.status == 200 && r.count == baseline:
				n200same++
			case r.status == 200 && r.count > baseline:
				n200more++
			}
		}
		t.Logf("NO CANDIDATE at %s: %d rejected(400), %d same-count, %d more-count",
			label, n400, n200same, n200more)
	}
}
