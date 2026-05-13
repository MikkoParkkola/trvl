package afklm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
