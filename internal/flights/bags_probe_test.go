package flights

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// TestBagsProbe makes real batchexecute calls to Google Flights with different
// values at the bags filter position (outer[1][10]) to determine whether Google
// accepts checked bags as a second element in an array at that position.
//
// Variations tested:
//
//	nil             no filter
//	1               legacy scalar carry-on encoding (rejected by Google)
//	[1, 0]          carry-on 1, checked 0
//	[1, 1]          carry-on 1, checked 1
//	[0, 1]          no carry-on, checked 1
//	2               carry-on = 2
//	[2, 0]          carry-on 2, checked 0
//	[0, 0]          both zero (equivalent to nil?)
//
// Key question: if [1,1] returns FEWER results than [1,0], that proves the
// checked bags field works server-side.
func TestBagsProbe(t *testing.T) {
	testutil.RequireLiveProbe(t)

	type probe struct {
		name  string
		value any // value to place at outer[1][10]
	}

	probes := []probe{
		{"nil", nil},
		{"1", 1},
		{"[1,0]", []any{1, 0}},
		{"[1,1]", []any{1, 1}},
		{"[0,1]", []any{0, 1}},
		{"2", 2},
		{"[2,0]", []any{2, 0}},
		{"[0,0]", []any{0, 0}},
	}

	type result struct {
		name     string
		status   int
		count    int
		bodySize int
		err      error
	}

	client := batchexec.NewClient()
	client.SetNoCache(true)
	// Pace requests to stay under Google's rate limits.
	client.SetRateLimit(0.5)

	// Use a date ~30 days out (within Google Flights' bookable window).
	searchDate := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	t.Logf("Route: HEL -> NRT, date: %s", searchDate)

	// Base options -- call defaults() to match SearchFlightsWithClient behavior
	// (sets CabinClass=Economy which is required for valid requests).
	baseOpts := SearchOptions{Adults: 1}
	baseOpts.defaults()

	// Sanity check: the baseline search must return flights (one jittered
	// retry on throttling; skips if Google is still throttling the runner).
	throttle := &probeThrottle{}
	throttle.probeBaseline(t, client, "HEL", "NRT", searchDate, baseOpts)

	results := make([]result, len(probes))

	for i, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			throttle.skipIfThrottled(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			filters := buildFilters("HEL", "NRT", searchDate, baseOpts)
			patched := patchBagsPosition(t, filters, p.value)

			encoded, err := batchexec.EncodeFlightFilters(patched)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}

			status, body, err := client.SearchFlights(ctx, encoded)
			throttle.check(t, err)
			if err != nil {
				t.Logf("FAILED bags=%s: %v", p.name, err)
				results[i] = result{name: p.name, err: err}
				return
			}

			count := 0
			if status == 200 {
				count = countFlights(t, body)
			}
			if count == 0 && status != 200 {
				t.Logf("raw body: %s", truncateBytes(body, 300))
			}

			results[i] = result{
				name:     p.name,
				status:   status,
				count:    count,
				bodySize: len(body),
			}
			t.Logf("bags=%-8s  status=%d  flights=%d  body=%d bytes",
				p.name, status, count, len(body))
		})
	}

	// ---- Summary table ----
	t.Log("")
	t.Log("=== RESULTS ===")
	t.Logf("%-10s  %6s  %6s  %8s", "BAGS", "STATUS", "COUNT", "BODY")
	for _, r := range results {
		if r.err != nil {
			t.Logf("%-10s  %6s  %6s  %8s  %v", r.name, "ERR", "-", "-", r.err)
		} else {
			t.Logf("%-10s  %6d  %6d  %8d", r.name, r.status, r.count, r.bodySize)
		}
	}

	// ---- Analysis ----
	t.Log("")
	t.Log("=== ANALYSIS ===")

	// Collect counts by name for easy lookup.
	m := map[string]*result{}
	for i := range results {
		m[results[i].name] = &results[i]
	}

	// Check: scalar integers rejected?
	if r := m["1"]; r != nil && r.status == 400 {
		t.Log("CONFIRMED: scalar 1 at [10] returns 400 -- legacy scalar bag encoding is rejected")
	}
	if r := m["2"]; r != nil && r.status == 400 {
		t.Log("CONFIRMED: scalar 2 also returns 400 -- Google rejects scalar ints at [10]")
	}

	// Check: array format accepted?
	for _, name := range []string{"[1,0]", "[1,1]", "[0,1]", "[2,0]", "[0,0]"} {
		if r := m[name]; r != nil && r.status == 200 {
			t.Logf("OK: %s accepted (200, %d flights)", name, r.count)
		} else if r != nil {
			t.Logf("REJECTED: %s -> status %d", name, r.status)
		}
	}

	// Key comparison: [1,0] vs [1,1]
	r10, r11 := m["[1,0]"], m["[1,1]"]
	if r10 != nil && r11 != nil && r10.status == 200 && r11.status == 200 {
		t.Log("")
		if r11.count < r10.count {
			t.Logf("PROVEN: [1,1] has %d flights vs [1,0] with %d -- "+
				"checked bag filter WORKS (fewer results = stricter filter)",
				r11.count, r10.count)
		} else if r11.count == r10.count {
			if r11.bodySize != r10.bodySize {
				t.Logf("AMBIGUOUS: same count (%d) but different body sizes (%d vs %d) -- "+
					"flights may differ in baggage metadata",
					r10.count, r10.bodySize, r11.bodySize)
			} else {
				t.Logf("LIKELY IGNORED: [1,1] and [1,0] identical (%d flights, %d bytes) -- "+
					"second element appears to have no effect",
					r10.count, r10.bodySize)
			}
		} else {
			t.Logf("UNEXPECTED: [1,1] has MORE flights (%d) than [1,0] (%d)",
				r11.count, r10.count)
		}
	}

	// Compare [2,0] vs [1,0] -- does carry-on count filter?
	r20 := m["[2,0]"]
	if r20 != nil && r10 != nil && r20.status == 200 && r10.status == 200 {
		if r20.count < r10.count {
			t.Logf("CARRY-ON FILTERS: [2,0] has %d vs [1,0] with %d", r20.count, r10.count)
		} else if r20.count == r10.count {
			t.Logf("CARRY-ON SAME: [2,0] and [1,0] both have %d flights", r10.count)
		}
	}

	// Compare nil vs [0,0] -- is [0,0] equivalent to no filter?
	rNil, r00 := m["nil"], m["[0,0]"]
	if rNil != nil && r00 != nil && rNil.status == 200 && r00.status == 200 {
		if rNil.count == r00.count {
			t.Logf("CONFIRMED: [0,0] equivalent to nil (%d flights each)", rNil.count)
		} else {
			t.Logf("DIFFERENT: nil=%d vs [0,0]=%d", rNil.count, r00.count)
		}
	}
}

// patchBagsPosition round-trips filters through JSON, then replaces outer[1][10].
func patchBagsPosition(t *testing.T, filters any, val any) any {
	t.Helper()

	data, err := json.Marshal(filters)
	if err != nil {
		t.Fatalf("marshal filters: %v", err)
	}
	var tree []any
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatalf("unmarshal filters: %v", err)
	}

	settings, ok := tree[1].([]any)
	if !ok {
		t.Fatalf("outer[1] is not []any: %T", tree[1])
	}
	if len(settings) <= 10 {
		t.Fatalf("settings too short: %d", len(settings))
	}

	settings[10] = val
	tree[1] = settings
	return tree
}

// countFlights decodes a raw Google Flights response and returns the number of
// flight entries, or 0 on any decode error.
func countFlights(t *testing.T, body []byte) int {
	t.Helper()

	inner, err := batchexec.DecodeFlightResponse(body)
	if err != nil {
		t.Logf("decode response: %v", err)
		return 0
	}

	rawFlights, err := batchexec.ExtractFlightData(inner)
	if err != nil {
		t.Logf("extract flights: %v", err)
		return 0
	}

	return len(rawFlights)
}

// debugPayload logs the JSON payload for manual inspection.
// Keep for manual debugging; underscore-assign to satisfy staticcheck.
var _ = debugPayload

func debugPayload(t *testing.T, filters any) {
	t.Helper()
	data, _ := json.MarshalIndent(filters, "", "  ")
	t.Logf("Filters JSON:\n%s", truncateBytes(data, 2000))
}

func truncateBytes(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + fmt.Sprintf("... [truncated, %d total]", len(b))
}
