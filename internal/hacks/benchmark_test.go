package hacks

import (
	"context"
	"testing"
	"time"
)

// realisticInput returns a DetectorInput for HEL→BCN round-trip, a realistic
// European short-haul search that exercises most detector code paths.
func realisticInput() DetectorInput {
	return DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        time.Now().AddDate(0, 0, 30).Format("2006-01-02"), // 30 days out
		ReturnDate:  time.Now().AddDate(0, 0, 37).Format("2006-01-02"), // +7 day trip
		Currency:    "EUR",
		CarryOnOnly: true,
		NaivePrice:  350,
		Passengers:  1,
	}
}

// noMatchInput returns a DetectorInput with unknown airports that should not
// trigger any detector heuristic, measuring pure goroutine-scheduling and
// map-lookup overhead.
func noMatchInput() DetectorInput {
	return DetectorInput{
		Origin:      "XYZ",
		Destination: "QQQ",
		Date:        time.Now().AddDate(0, 0, 30).Format("2006-01-02"),
		ReturnDate:  time.Now().AddDate(0, 0, 37).Format("2006-01-02"),
		Currency:    "EUR",
		CarryOnOnly: false,
		NaivePrice:  200,
		Passengers:  1,
	}
}

// BenchmarkDetectAll measures the full 35-detector DetectAll with a realistic
// HEL→BCN round-trip input.
//
// Observed results (Apple M4 Pro, -count=3):
//
//	BenchmarkDetectAll-12    1    ~20,000ms/op    ~119MB/op    ~1,676K allocs/op
//
// The ~20s wall time is dominated by the per-detector timeout (detectorTimeout =
// 20s). Some detectors make live HTTP calls to Google Flights, Kiwi, ground
// transport providers (FlixBus, RegioJet, ferryhopper, Tallink), and the slowest
// one hitting the 20s deadline caps the entire call. The ~119 MB / ~1.67M allocs
// come from HTTP client buffers, JSON decoding, and flight result structs in the
// network-calling detectors — NOT from the goroutine fan-out itself.
//
// Performance findings:
//
//  1. TIMEOUT APPROPRIATENESS: The 20s per-detector timeout is appropriate for
//     production use. Detectors that make multiple chained API calls (e.g.,
//     detectDateFlex searches +-3 days = 7 parallel flight searches) need time
//     for all sub-calls. However, for the MCP server response path, the caller
//     should impose its own context deadline (e.g., 30s) to cap total latency.
//     The child context.WithTimeout correctly inherits the parent's earlier
//     deadline, as verified by TestDetectAll_DeadlineExceeded.
//
//  2. ALLOCATION ANALYSIS: ~1.67M allocs/op is high but is dominated by
//     network I/O (HTTP response parsing, JSON decoding, TLS handshakes).
//     The pure goroutine overhead is minimal: 35 goroutines x (context +
//     timer + channel send) is ~35 x ~3 allocs = ~105 allocs. Compare with
//     BenchmarkDetectAll_NoMatch (~5K-11K allocs) which still hits some
//     network providers (Kiwi, ferryhopper). The computational detectors
//     themselves are allocation-efficient.
//
//  3. UNNECESSARY WORK: Some detectors do heavy string formatting (fmt.Sprintf
//     for descriptions, steps, citations) before checking whether the hack
//     should fire. For example, detectFareBreakpoint builds full Hack structs
//     with formatted strings for every candidate hub, even hubs that will be
//     discarded by the distance check. This is a minor concern — the string
//     formatting cost (~100ns per Sprintf) is dwarfed by network latency.
//     Not worth optimizing unless DetectAll is ever called in a tight loop.
//
//  4. GOROUTINE PRESSURE: 35 goroutines per DetectAll call is reasonable.
//     The Go runtime handles thousands of goroutines efficiently. The
//     buffered channel (cap = len(detectors)) prevents goroutine leaks.
//     No shared mutable state between detectors — each is fully independent.
func BenchmarkDetectAll(b *testing.B) {
	in := realisticInput()
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_ = DetectAll(ctx, in)
	}
}

// BenchmarkDetectAll_NoMatch measures DetectAll with unknown airport codes,
// establishing the baseline goroutine-scheduling overhead when no detector
// produces output.
//
// Observed results (Apple M4 Pro, -count=3):
//
//	BenchmarkDetectAll_NoMatch-12    1    ~4,400ms/op    ~1.2MB/op    ~7K allocs/op
//
// The ~3-6s wall time is NOT goroutine overhead — it comes from detectors
// that still attempt network calls even for unknown airports. Specifically:
//   - detectNightTransport, detectFerryCabin, detectMultiModalReturnSplit and
//     similar ground-transport detectors call into providers (ferryhopper,
//     Kiwi for flight legs) regardless of whether the airports are known.
//   - These providers fail quickly (~3s) with decode errors or timeouts.
//
// The ~7K allocs/1.2MB is the true "no match" baseline: HTTP client setup,
// failed network calls, context timers. Pure computational detectors
// contribute almost nothing here — they bail out at the first map lookup.
//
// Finding: Some detectors that make network calls could short-circuit
// earlier by checking whether origin/destination are in a known set
// before making expensive API calls. This would reduce no-match
// latency from ~4s to <1ms. Not a production issue (unknown airports
// are rare in real usage) but would improve benchmark isolation.
func BenchmarkDetectAll_NoMatch(b *testing.B) {
	in := noMatchInput()
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_ = DetectAll(ctx, in)
	}
}

// BenchmarkDetectFlightTips measures the curated subset of zero-API-call
// detectors (advance_purchase, fare_breakpoint, destination_airport,
// group_split, departure_tax). These run sequentially, not in parallel.
//
// Observed results (Apple M4 Pro, -count=3):
//
//	BenchmarkDetectFlightTips-12    ~480K    ~2,520 ns/op    3,513 B/op    59 allocs/op
//
// This is excellent: 2.5 microseconds per call with 59 allocations. The
// 5 computational detectors run sequentially in a simple loop (no goroutines),
// which is the right design — goroutine spawn overhead (~1us each) would
// dominate the actual computation.
//
// Allocation breakdown (59 allocs, 3,513 bytes):
//   - detectAdvancePurchase: ~20 allocs (fmt.Sprintf for title, description,
//     steps; Hack struct; string slices for Risks/Steps)
//   - detectFareBreakpoint: ~25 allocs (iterates fareBreakpointHubs, builds
//     Hack per qualifying hub with distance calculations)
//   - detectDestinationAirport: ~10 allocs (map lookup + Hack struct)
//   - detectGroupSplit: 0 allocs (returns nil for Passengers < 3)
//   - detectDepartureTax: ~4 allocs (map lookups, NearbyAirports)
//
// This function is safe for hot-path use (auto-triggered after every flight
// search). At 2.5us it adds negligible latency.
func BenchmarkDetectFlightTips(b *testing.B) {
	in := realisticInput()
	// Set passengers=4 and NaivePrice so group_split fires too.
	in.Passengers = 4
	in.NaivePrice = 1400
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_ = DetectFlightTips(ctx, in)
	}
}

// BenchmarkDetectAll_Parallel stress-tests DetectAll under concurrent access.
// This simulates multiple users triggering hack detection simultaneously.
//
// Observed results (Apple M4 Pro, -count=3, GOMAXPROCS=12):
//
//	BenchmarkDetectAll_Parallel-12    1    ~20,003ms/op    ~111MB/op    ~1,660K allocs/op
//
// With RunParallel, up to 12 goroutines call DetectAll simultaneously, each
// spawning 35 child goroutines = up to 420 goroutines concurrently. The
// results are nearly identical to the sequential benchmark, confirming:
//   - No lock contention: each DetectAll call is fully independent
//     (channel-per-call, no shared mutable state).
//   - The bottleneck is network I/O (HTTP calls hitting the 20s timeout),
//     not CPU or goroutine scheduling.
//   - The Go runtime handles 420+ concurrent goroutines without degradation.
func BenchmarkDetectAll_Parallel(b *testing.B) {
	in := realisticInput()
	ctx := context.Background()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = DetectAll(ctx, in)
		}
	})
}

// TestDetectAll_CancelledContext verifies that DetectAll respects context
// cancellation and returns promptly without blocking on detector completion.
func TestDetectAll_CancelledContext(t *testing.T) {
	in := realisticInput()

	// Cancel context immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	hacks := DetectAll(ctx, in)
	elapsed := time.Since(start)

	// With an already-cancelled context, DetectAll should return very fast.
	// All detectors that check ctx.Err() or use ctx for API calls should
	// bail out immediately. We allow 500ms to be generous (goroutine scheduling).
	if elapsed > 500*time.Millisecond {
		t.Errorf("DetectAll with cancelled context took %v, expected <500ms", elapsed)
	}

	// Result may be empty or contain hacks from detectors that don't check
	// context (pure computational detectors ignore context). This is acceptable:
	// computational detectors finish in microseconds anyway.
	_ = hacks
}

// TestDetectAll_DeadlineExceeded verifies that DetectAll returns within a
// tight deadline and does not hang waiting for slow detectors.
func TestDetectAll_DeadlineExceeded(t *testing.T) {
	in := realisticInput()

	// Give a very short deadline — 1ms.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	start := time.Now()
	hacks := DetectAll(ctx, in)
	elapsed := time.Since(start)

	// Should return promptly. The per-detector timeout is 20s, but the
	// parent context deadline of 1ms propagates to child contexts via
	// context.WithTimeout (child inherits parent's earlier deadline).
	if elapsed > 500*time.Millisecond {
		t.Errorf("DetectAll with 1ms deadline took %v, expected <500ms", elapsed)
	}

	_ = hacks
}
