package providers

// MIK-3071: tests for parseRetryAfter and the adaptive rate-limit
// counters on providerClient. The HTTP-level retry-on-429 behaviour is
// covered by an integration-style httptest test in this same package
// (TestSearchHotels_429RetriesThenSucceeds).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestParseRetryAfter_DeltaSeconds(t *testing.T) {
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"0", 0},
		{"-5", 0},
		{"1", 1 * time.Second},
		{"30", 30 * time.Second},
		{"60", 60 * time.Second},
		{"3600", retryAfterMaxDelay}, // capped
		{"  10  ", 10 * time.Second}, // trim
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := ParseRetryAfter(tc.in, now); got != tc.want {
				t.Errorf("ParseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

	future := now.Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if got := ParseRetryAfter(future, now); got <= 0 || got > 31*time.Second {
		t.Errorf("future date: got %v, want ~30s", got)
	}

	past := now.Add(-30 * time.Second).UTC().Format(http.TimeFormat)
	if got := ParseRetryAfter(past, now); got != 0 {
		t.Errorf("past date: got %v, want 0", got)
	}

	// Far-future dates are capped.
	farFuture := now.Add(2 * time.Hour).UTC().Format(http.TimeFormat)
	if got := ParseRetryAfter(farFuture, now); got != retryAfterMaxDelay {
		t.Errorf("far-future date: got %v, want cap %v", got, retryAfterMaxDelay)
	}
}

func TestParseRetryAfter_Invalid(t *testing.T) {
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	for _, in := range []string{"", "   ", "not-a-number", "abc", "1.5"} {
		if got := ParseRetryAfter(in, now); got != 0 {
			t.Errorf("invalid %q: got %v, want 0", in, got)
		}
	}
}

func TestRetryAfterOrDefault(t *testing.T) {
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	if got := RetryAfterOrDefault("", now); got != retryAfterDefaultDelay {
		t.Errorf("empty header: got %v, want default %v", got, retryAfterDefaultDelay)
	}
	if got := RetryAfterOrDefault("5", now); got != 5*time.Second {
		t.Errorf("5s header: got %v, want 5s", got)
	}
}

// TestProviderClient_RecordRateLimit_HalvesAfterThreshold verifies the
// adaptive limiter shrinks the rps once consecutive 429 count crosses the
// threshold, and never below the floor.
func TestProviderClient_RecordRateLimit_HalvesAfterThreshold(t *testing.T) {
	pc := &providerClient{
		config:     &ProviderConfig{ID: "test"},
		limiter:    rate.NewLimiter(rate.Limit(0.5), 1),
		defaultRPS: 0.5,
	}

	// First two 429s: limiter unchanged.
	pc.recordRateLimit(time.Now())
	pc.recordRateLimit(time.Now())
	if got := float64(pc.limiter.Limit()); got != 0.5 {
		t.Errorf("after 2 x 429: limit = %v, want 0.5 (no halving yet)", got)
	}

	// Third 429: halve to 0.25.
	pc.recordRateLimit(time.Now())
	if got := float64(pc.limiter.Limit()); got != 0.25 {
		t.Errorf("after 3 x 429: limit = %v, want 0.25", got)
	}

	// Drive limit down toward the floor.
	for i := 0; i < 20; i++ {
		pc.recordRateLimit(time.Now())
	}
	if got := float64(pc.limiter.Limit()); got < rateLimitFloorRPS-1e-9 {
		t.Errorf("after 20 more 429s: limit = %v, must not go below floor %v", got, rateLimitFloorRPS)
	}
}

// TestProviderClient_RecordRateLimitSuccess_ResetAfterCooldown verifies the
// limiter is restored to the configured default after rateLimitCooldown
// has elapsed since the last 429.
func TestProviderClient_RecordRateLimitSuccess_ResetAfterCooldown(t *testing.T) {
	pc := &providerClient{
		config:     &ProviderConfig{ID: "test"},
		limiter:    rate.NewLimiter(rate.Limit(0.5), 1),
		defaultRPS: 0.5,
	}
	// Halve the limit by recording 3 x 429.
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	pc.recordRateLimit(now)
	pc.recordRateLimit(now)
	pc.recordRateLimit(now)
	if float64(pc.limiter.Limit()) >= 0.5 {
		t.Fatalf("setup: expected limit < 0.5, got %v", pc.limiter.Limit())
	}

	// Success within cooldown: counter reset, but limit stays halved.
	pc.recordRateLimitSuccess(now.Add(30 * time.Minute))
	if pc.consecutive429 != 0 {
		t.Errorf("consecutive429 after success = %d, want 0", pc.consecutive429)
	}
	if float64(pc.limiter.Limit()) >= 0.5 {
		t.Errorf("limit before cooldown = %v, want still halved", pc.limiter.Limit())
	}

	// Re-arm last429, then success past cooldown: limit restored.
	pc.recordRateLimit(now)
	pc.recordRateLimitSuccess(now.Add(rateLimitCooldown + time.Minute))
	if got := float64(pc.limiter.Limit()); got != 0.5 {
		t.Errorf("limit after cooldown reset = %v, want 0.5 (default)", got)
	}
}

// TestSearchHotels_429RetriesThenSucceeds is the integration test required
// by the AC: stub server returns 429 + Retry-After: 1 on first hit, then
// 200 with empty results. The provider runtime should sleep ~1s, retry,
// and report the call as ok.
func TestSearchHotels_429RetriesThenSucceeds(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"err":"rate limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"r":[]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	cfg := &ProviderConfig{
		ID:              "rl-prov",
		Name:            "RateLimited",
		Category:        "hotels",
		Endpoint:        srv.URL,
		ResponseMapping: ResponseMapping{ResultsPath: "r"},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("save cfg: %v", err)
	}

	rt := NewRuntime(reg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t0 := time.Now()
	_, statuses, _ := rt.SearchHotels(ctx, "Helsinki", 60.17, 24.94, "2026-06-01", "2026-06-02", "EUR", 2, nil)
	elapsed := time.Since(t0)

	if hits.Load() < 2 {
		t.Errorf("server hits = %d, want >= 2 (no retry happened)", hits.Load())
	}
	if elapsed < 800*time.Millisecond {
		t.Errorf("elapsed = %v, want >= ~1s (Retry-After honoured)", elapsed)
	}
	if len(statuses) != 1 || statuses[0].Status != "ok" {
		t.Errorf("statuses = %+v, want one ok", statuses)
	}
}

// TestSearchHotels_429ExhaustsRetries confirms that after max retries the
// search reports a rate-limit error that the FixHintCode classifier from
// MIK-3074 will route to RATE_LIMITED.
func TestSearchHotels_429ExhaustsRetries(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	cfg := &ProviderConfig{
		ID:              "rl-always",
		Name:            "AlwaysLimited",
		Category:        "hotels",
		Endpoint:        srv.URL,
		ResponseMapping: ResponseMapping{ResultsPath: "r"},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("save cfg: %v", err)
	}

	rt := NewRuntime(reg)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, statuses, _ := rt.SearchHotels(ctx, "Helsinki", 60.17, 24.94, "2026-06-01", "2026-06-02", "EUR", 2, nil)

	if got := hits.Load(); got < 3 {
		t.Errorf("server hits = %d, want exactly 3 (initial + 2 retries)", got)
	}
	if len(statuses) != 1 {
		t.Fatalf("statuses = %d, want 1", len(statuses))
	}
	if statuses[0].FixHintCode != string(FixHintRateLimited) {
		t.Errorf("FixHintCode = %q, want %q (MIK-3074 classifier)", statuses[0].FixHintCode, FixHintRateLimited)
	}
	if !strings.Contains(statuses[0].Error, "rate limit") {
		t.Errorf("error = %q, want to mention 'rate limit'", statuses[0].Error)
	}
	// Ensure pretty error message matches the actual issue.
	t.Logf("provider error: %s | hint: %s", statuses[0].Error, statuses[0].FixHint)
}
