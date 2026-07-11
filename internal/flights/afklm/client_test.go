package afklm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// newTestClient creates a Client pointing at the given test server.
// Quota and cache are isolated to a temp directory.
func newTestClient(t *testing.T, srv *httptest.Server, nowFn func() time.Time) *Client {
	t.Helper()
	dir := t.TempDir()
	c, err := NewCache(dir, nowFn)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	client := &Client{
		baseURL:    srv.URL,
		host:       "KL",
		key:        "test-api-key",
		httpClient: srv.Client(),
		limiter:    newTestLimiter(),
		cache:      c,
		now:        nowFn,
	}
	return client
}

// newTestLimiter returns a limiter that allows all requests immediately.
func newTestLimiter() *rate.Limiter {
	return rate.NewLimiter(rate.Inf, 1)
}

func TestClientHeaders(t *testing.T) {
	var gotHost, gotKey, gotAccept, gotContentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Header.Get("AFKL-TRAVEL-Host")
		gotKey = r.Header.Get("API-Key")
		gotAccept = r.Header.Get("Accept")
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/hal+json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"recommendations":[]}`))
	}))
	defer srv.Close()

	now := time.Now()
	client := newTestClient(t, srv, func() time.Time { return now })

	if err := os.Setenv("AFKLM_KEY", "test-key-headers"); err != nil {
		t.Fatalf("Setenv: %v", err)
	}
	defer func() { _ = os.Unsetenv("AFKLM_KEY") }()

	req := AvailableOffersRequest{
		BookingFlow:          "LEISURE",
		Passengers:           []Passenger{{ID: 1, Type: "ADT"}},
		RequestedConnections: []RequestedConnection{{DepartureDate: "2026-05-15", Origin: Place{Type: "AIRPORT", Code: "AMS"}, Destination: Place{Type: "AIRPORT", Code: "PRG"}}},
	}
	_, _, err := client.AvailableOffers(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotHost != "KL" {
		t.Errorf("AFKL-TRAVEL-Host: want KL, got %q", gotHost)
	}
	if gotKey == "" {
		t.Error("API-Key header must be present")
	}
	if gotAccept != "application/hal+json" {
		t.Errorf("Accept: want application/hal+json, got %q", gotAccept)
	}
	if gotContentType != "application/hal+json" {
		t.Errorf("Content-Type: want application/hal+json, got %q", gotContentType)
	}
}

func TestClientCredentialNotInError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer srv.Close()

	secretKey := "super-secret-credential-abc123"
	now := time.Now()
	dir := t.TempDir()
	c, _ := NewCache(dir, func() time.Time { return now })

	client := &Client{
		baseURL:    srv.URL,
		host:       "KL",
		key:        secretKey,
		httpClient: srv.Client(),
		limiter:    newTestLimiter(),
		cache:      c,
		now:        func() time.Time { return now },
	}

	req := AvailableOffersRequest{
		BookingFlow:          "LEISURE",
		Passengers:           []Passenger{{ID: 1, Type: "ADT"}},
		RequestedConnections: []RequestedConnection{{DepartureDate: "2026-05-15", Origin: Place{Type: "AIRPORT", Code: "AMS"}, Destination: Place{Type: "AIRPORT", Code: "PRG"}}},
	}
	_, _, err := client.AvailableOffers(context.Background(), req)
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if strings.Contains(err.Error(), secretKey) {
		t.Errorf("credential leaked in error message: %v", err)
	}
}

func TestClient429RetryOnce(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":"over qps"}`))
			return
		}
		w.Header().Set("Content-Type", "application/hal+json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"recommendations":[]}`))
	}))
	defer srv.Close()

	now := time.Now()
	dir := t.TempDir()
	c, _ := NewCache(dir, func() time.Time { return now })

	client := &Client{
		baseURL:    srv.URL,
		host:       "KL",
		key:        "test-key",
		httpClient: srv.Client(),
		limiter:    newTestLimiter(),
		cache:      c,
		now:        func() time.Time { return now },
	}

	// Override retry delay to 0 for tests by using a short-circuit context.
	// We patch the retry sleep indirectly by keeping the test timeout short.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := AvailableOffersRequest{
		BookingFlow:          "LEISURE",
		Passengers:           []Passenger{{ID: 1, Type: "ADT"}},
		RequestedConnections: []RequestedConnection{{DepartureDate: "2026-05-15", Origin: Place{Type: "AIRPORT", Code: "AMS"}, Destination: Place{Type: "AIRPORT", Code: "PRG"}}},
	}
	_, _, err := client.AvailableOffers(ctx, req)
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 server calls (1 429 + 1 retry), got %d", calls)
	}
}

func TestClientQPSSpacing(t *testing.T) {
	if testing.Short() {
		t.Skip("QPS spacing test skipped in -short mode")
	}

	var callTimes []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callTimes = append(callTimes, time.Now())
		w.Header().Set("Content-Type", "application/hal+json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"recommendations":[]}`))
	}))
	defer srv.Close()

	now := time.Now()
	dir := t.TempDir()
	c, _ := NewCache(dir, func() time.Time { return now })

	// Use the real 1 QPS limiter (not test limiter).
	realLimiter := rate.NewLimiter(rate.Every(time.Second), 1)
	client := &Client{
		baseURL:    srv.URL,
		host:       "KL",
		key:        "test-key",
		httpClient: srv.Client(),
		limiter:    realLimiter,
		cache:      c,
		now:        func() time.Time { return now },
	}

	req := AvailableOffersRequest{
		BookingFlow:          "LEISURE",
		Passengers:           []Passenger{{ID: 1, Type: "ADT"}},
		RequestedConnections: []RequestedConnection{{DepartureDate: "2026-05-15", Origin: Place{Type: "AIRPORT", Code: "AMS"}, Destination: Place{Type: "AIRPORT", Code: "PRG"}}},
	}

	for i := 0; i < 2; i++ {
		// Different departure dates to avoid cache hits.
		req.RequestedConnections[0].DepartureDate = []string{"2026-05-15", "2026-06-15"}[i]
		_, _, err := client.AvailableOffers(context.Background(), req)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	if len(callTimes) < 2 {
		t.Fatalf("expected 2 server calls, got %d", len(callTimes))
	}
	gap := callTimes[1].Sub(callTimes[0])
	if gap < 900*time.Millisecond {
		t.Errorf("QPS gap too short: %v (want >= 900ms)", gap)
	}
}

// TestSharedLimiterAcrossProviders proves that two AFKLMProvider / Client
// instances obtained via the default NewClient path (no injected limiter)
// share the package singleton and are rate-limited to aggregate 1 QPS.
// Uses real shared limiter (not Inf test one). 2 requests is sufficient
// and deterministic for the assertion.
func TestSharedLimiterAcrossProviders(t *testing.T) {
	t.Setenv("AFKLM_KEY", "dummy-shared-limiter-test")
	// Use distinct cache dirs so quota files don't interfere across test runs.
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/hal+json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"recommendations":[]}`))
	}))
	defer srv.Close()

	// Create via NewClient (exercises default shared limiter path).
	c1, err := NewClient(ClientOptions{
		Credential: "dummy",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		CacheDir:   dir1,
		Now:        time.Now,
	})
	if err != nil {
		t.Fatalf("NewClient c1: %v", err)
	}
	c2, err := NewClient(ClientOptions{
		Credential: "dummy",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		CacheDir:   dir2,
		Now:        time.Now,
	})
	if err != nil {
		t.Fatalf("NewClient c2: %v", err)
	}

	// mkReq builds a fully independent request (own RequestedConnections slice)
	// so the two goroutines below never write a shared backing array under -race.
	mkReq := func(date string) AvailableOffersRequest {
		return AvailableOffersRequest{
			BookingFlow: "LEISURE",
			Passengers:  []Passenger{{ID: 1, Type: "ADT"}},
			RequestedConnections: []RequestedConnection{{
				DepartureDate: date,
				Origin:        Place{Type: "AIRPORT", Code: "AMS"},
				Destination:   Place{Type: "AIRPORT", Code: "PRG"},
			}},
		}
	}

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _, _ = c1.AvailableOffers(context.Background(), mkReq("2026-09-01"))
	}()
	go func() {
		defer wg.Done()
		_, _, _ = c2.AvailableOffers(context.Background(), mkReq("2026-09-02"))
	}()
	wg.Wait()
	elapsed := time.Since(start)

	if elapsed < 900*time.Millisecond {
		t.Errorf("shared limiter did not serialize 2 concurrent requests: elapsed=%v (want >=900ms)", elapsed)
	}
}

// --- daily quota tests per #474 spec ---

func TestAFKLMDailyQuota_IncrementPersists_CacheHitsDoNotIncrement(t *testing.T) {
	d := t.TempDir()
	now := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/hal+json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"recommendations":[]}`))
	}))
	defer srv.Close()

	lim := newTestLimiter()
	c1, err := NewClient(ClientOptions{
		Credential: "k1",
		CacheDir:   d,
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Now:        func() time.Time { return now },
		Limiter:    lim,
	})
	if err != nil {
		t.Fatalf("NewClient c1: %v", err)
	}

	req := AvailableOffersRequest{
		BookingFlow: "LEISURE",
		Passengers:  []Passenger{{ID: 1, Type: "ADT"}},
		RequestedConnections: []RequestedConnection{{
			DepartureDate: "2026-08-01",
			Origin:        Place{Type: "AIRPORT", Code: "AMS"},
			Destination:   Place{Type: "AIRPORT", Code: "PRG"},
		}},
	}

	// First call: live (miss) -> increments
	if _, _, err := c1.AvailableOffers(context.Background(), req); err != nil {
		t.Fatalf("c1 first call: %v", err)
	}
	used1, err := c1.Cache().QuotaUsed(now)
	if err != nil {
		t.Fatalf("QuotaUsed: %v", err)
	}
	if used1 != 1 {
		t.Fatalf("after live call: want count=1, got %d", used1)
	}

	// New client instance, same dir: count persists
	c2, err := NewClient(ClientOptions{
		Credential: "k1",
		CacheDir:   d,
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Now:        func() time.Time { return now },
		Limiter:    lim,
	})
	if err != nil {
		t.Fatalf("NewClient c2: %v", err)
	}
	used2, _ := c2.Cache().QuotaUsed(now)
	if used2 != 1 {
		t.Fatalf("new client instance must see persisted count=1, got %d", used2)
	}

	// Second call on c2: should be cache hit -> no increment
	if _, _, err := c2.AvailableOffers(context.Background(), req); err != nil {
		t.Fatalf("c2 second (hit) call: %v", err)
	}
	used3, _ := c2.Cache().QuotaUsed(now)
	if used3 != 1 {
		t.Fatalf("cache hit must not increment, still 1, got %d", used3)
	}
}

func TestAFKLMDailyQuota_AtThreshold_ReturnsErr_NoHTTPCall(t *testing.T) {
	d := t.TempDir()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	called := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.Header().Set("Content-Type", "application/hal+json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"recommendations":[]}`))
	}))
	defer srv.Close()

	cc, err := NewCache(d, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	for i := 0; i < quotaHardLimit; i++ {
		_ = cc.IncQuota(now)
	}

	cli := &Client{
		baseURL:    srv.URL,
		host:       "KL",
		key:        "t",
		httpClient: srv.Client(),
		limiter:    newTestLimiter(),
		cache:      cc,
		now:        func() time.Time { return now },
	}
	if _, _, err := cli.AvailableOffers(context.Background(), AvailableOffersRequest{
		BookingFlow: "LEISURE",
		Passengers:  []Passenger{{ID: 1, Type: "ADT"}},
		RequestedConnections: []RequestedConnection{{
			DepartureDate: "2026-08-01", Origin: Place{Type: "AIRPORT", Code: "HEL"}, Destination: Place{Type: "AIRPORT", Code: "ARN"},
		}},
	}); !errors.Is(err, ErrDailyQuota) {
		t.Fatalf("client must return ErrDailyQuota at threshold, got %v", err)
	}
	if atomic.LoadInt32(&called) != 0 {
		t.Error("no HTTP call must be made at/above daily threshold")
	}
}

func TestAFKLMDailyQuota_NowFuncRolloverResets(t *testing.T) {
	orig := nowFunc
	defer func() { nowFunc = orig }()

	day1 := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return day1 }

	d := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/hal+json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"recommendations":[]}`))
	}))
	defer srv.Close()

	lim := newTestLimiter()
	c1, _ := NewClient(ClientOptions{
		Credential: "k",
		CacheDir:   d,
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Limiter:    lim,
		// Now: nil -> picks current nowFunc
	})
	req1 := AvailableOffersRequest{
		BookingFlow: "LEISURE", Passengers: []Passenger{{ID: 1, Type: "ADT"}},
		RequestedConnections: []RequestedConnection{{DepartureDate: "2026-09-01", Origin: Place{Type: "AIRPORT", Code: "AMS"}, Destination: Place{Type: "AIRPORT", Code: "PRG"}}},
	}
	_, _, _ = c1.AvailableOffers(context.Background(), req1)
	if u, _ := c1.Cache().QuotaUsed(day1); u != 1 {
		t.Fatalf("day1 count want 1 got %d", u)
	}

	// Roll day via nowFunc; new client picks it up; different req forces live
	day2 := day1.AddDate(0, 0, 1)
	nowFunc = func() time.Time { return day2 }

	c2, _ := NewClient(ClientOptions{
		Credential: "k",
		CacheDir:   d,
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Limiter:    lim,
	})
	req2 := req1 // same would hit cache entry; mutate date for miss+live
	req2.RequestedConnections[0].DepartureDate = "2026-09-02"
	_, _, _ = c2.AvailableOffers(context.Background(), req2)

	// day2 count should be 1 (reset + inc)
	if u, _ := c2.Cache().QuotaUsed(day2); u != 1 {
		t.Fatalf("after day rollover via nowFunc, day2 count want 1 got %d", u)
	}
	// old day via its key reports 0 (single file has day2)
	if u, _ := c2.Cache().QuotaUsed(day1); u != 0 {
		t.Fatalf("old day after rollover must report 0, got %d", u)
	}
}

func TestAFKLMDailyQuota_EnvOverridesThreshold(t *testing.T) {
	d := t.TempDir()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	t.Setenv("AFKLM_DAILY_LIMIT", "3")
	// ensure unset after? t.Setenv does restore in Go 1.17+
	defer func() { _ = os.Unsetenv("AFKLM_DAILY_LIMIT") }() // belt and suspenders

	called := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.Header().Set("Content-Type", "application/hal+json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"recommendations":[]}`))
	}))
	defer srv.Close()

	cc, _ := NewCache(d, func() time.Time { return now })
	// prefill to the *overridden* threshold
	for i := 0; i < 3; i++ {
		_ = cc.IncQuota(now)
	}

	cli := &Client{
		baseURL:    srv.URL,
		host:       "KL",
		key:        "t",
		httpClient: srv.Client(),
		limiter:    newTestLimiter(),
		cache:      cc,
		now:        func() time.Time { return now },
	}
	_, _, err := cli.AvailableOffers(context.Background(), AvailableOffersRequest{
		BookingFlow: "LEISURE", Passengers: []Passenger{{ID: 1, Type: "ADT"}},
		RequestedConnections: []RequestedConnection{{DepartureDate: "2026-08-01", Origin: Place{Type: "AIRPORT", Code: "HEL"}, Destination: Place{Type: "AIRPORT", Code: "CPH"}}},
	})
	if !errors.Is(err, ErrDailyQuota) {
		t.Fatalf("with AFKLM_DAILY_LIMIT=3, threshold should be 3; got err=%v", err)
	}
	if atomic.LoadInt32(&called) != 0 {
		t.Error("no HTTP when at overridden limit")
	}
}
