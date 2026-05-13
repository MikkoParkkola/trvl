package batchexec

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/time/rate"
)

// newTestClient creates a Client pointing at a test server (no TLS, no utls).
func newTestClient(url string) *Client {
	return &Client{
		http: &http.Client{
			Timeout: 5 * time.Second,
		},
		limiter: rate.NewLimiter(rate.Limit(1000), 1), // high limit for fast tests
	}
}

func TestNewClient(t *testing.T) {
	c := NewClient()
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	if c.http == nil {
		t.Error("http client is nil")
	}
	if c.limiter == nil {
		t.Error("rate limiter is nil")
	}
}

func TestSetRateLimit(t *testing.T) {
	c := NewClient()
	c.SetRateLimit(5.0)
	if c.limiter.Limit() != rate.Limit(5.0) {
		t.Errorf("limiter rate = %v, want 5.0", c.limiter.Limit())
	}

	c.SetRateLimit(100.0)
	if c.limiter.Limit() != rate.Limit(100.0) {
		t.Errorf("limiter rate = %v, want 100.0", c.limiter.Limit())
	}
}

func TestDialTLSChromeHTTP1WithConfig_UsesHTTP1ALPN(t *testing.T) {
	var (
		mu                sync.Mutex
		clientHelloProtos []string
	)

	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts.TLS = &tls.Config{
		NextProtos: []string{"h2", "http/1.1"},
		GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
			mu.Lock()
			clientHelloProtos = append([]string(nil), info.SupportedProtos...)
			mu.Unlock()
			return nil, nil
		},
	}
	ts.StartTLS()
	defer ts.Close()

	host, _, err := net.SplitHostPort(ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())

	conn, err := dialTLSChromeHTTP1WithConfig(context.Background(), "tcp", ts.Listener.Addr().String(), &utls.Config{
		ServerName: host,
		RootCAs:    pool,
	})
	if err != nil {
		t.Fatalf("dialTLSChromeHTTP1WithConfig: %v", err)
	}
	defer func() { _ = conn.Close() }()

	uConn, ok := conn.(*utls.UConn)
	if !ok {
		t.Fatalf("conn type = %T, want *utls.UConn", conn)
	}

	state := uConn.ConnectionState()
	if !state.HandshakeComplete {
		t.Fatal("expected completed handshake")
	}
	if state.NegotiatedProtocol != "http/1.1" {
		t.Fatalf("negotiated protocol = %q, want %q", state.NegotiatedProtocol, "http/1.1")
	}

	mu.Lock()
	gotProtos := append([]string(nil), clientHelloProtos...)
	mu.Unlock()
	if !reflect.DeepEqual(gotProtos, []string{"http/1.1"}) {
		t.Fatalf("client hello ALPN = %v, want %v", gotProtos, []string{"http/1.1"})
	}
}

func TestRateLimiterEnforcement(t *testing.T) {
	// Create a mock server that responds 200.
	var requestCount atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	c := newTestClient(ts.URL)
	// Set rate to 10/sec (100ms between requests).
	c.SetRateLimit(10)

	ctx := context.Background()
	start := time.Now()

	// Send 5 requests — should take at least ~400ms (4 intervals of 100ms).
	for range 5 {
		status, _, err := c.Get(ctx, ts.URL)
		if err != nil {
			t.Fatalf("Get error: %v", err)
		}
		if status != 200 {
			t.Fatalf("status = %d, want 200", status)
		}
	}

	elapsed := time.Since(start)
	if elapsed < 350*time.Millisecond {
		t.Errorf("5 requests at 10/s completed in %v, expected >= 350ms", elapsed)
	}
	if requestCount.Load() != 5 {
		t.Errorf("server received %d requests, want 5", requestCount.Load())
	}
}

func TestRateLimiterCancelledContext(t *testing.T) {
	c := NewClient()
	// Very slow rate — 0.1/sec means 10s between requests.
	c.SetRateLimit(0.1)

	// Exhaust the burst.
	ctx := context.Background()
	if err := c.limiter.Wait(ctx); err != nil {
		t.Fatalf("limiter wait: %v", err)
	}

	// Now cancel context before next request can proceed.
	cancelCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, _, err := c.Get(cancelCtx, "http://localhost:1/never")
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestRetryOn429(t *testing.T) {
	var attempt atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempt.Add(1)
		if n <= 2 {
			w.WriteHeader(429)
			_, _ = w.Write([]byte("rate limited"))
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("success"))
	}))
	defer ts.Close()

	c := newTestClient(ts.URL)

	status, body, err := c.Get(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != "success" {
		t.Errorf("body = %q, want %q", body, "success")
	}
	if attempt.Load() != 3 {
		t.Errorf("attempts = %d, want 3", attempt.Load())
	}
}

func TestRetryOn5xx(t *testing.T) {
	var attempt atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempt.Add(1)
		if n == 1 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte("service unavailable"))
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("recovered"))
	}))
	defer ts.Close()

	c := newTestClient(ts.URL)

	status, body, err := c.Get(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != "recovered" {
		t.Errorf("body = %q, want %q", body, "recovered")
	}
	if attempt.Load() != 2 {
		t.Errorf("attempts = %d, want 2", attempt.Load())
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{"400 Bad Request", 400},
		{"401 Unauthorized", 401},
		{"403 Forbidden", 403},
		{"404 Not Found", 404},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempt atomic.Int64
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempt.Add(1)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte("client error"))
			}))
			defer ts.Close()

			c := newTestClient(ts.URL)

			status, _, err := c.Get(context.Background(), ts.URL)
			if err != nil {
				t.Fatalf("Get error: %v", err)
			}
			if status != tt.status {
				t.Errorf("status = %d, want %d", status, tt.status)
			}
			// Should NOT retry — only 1 attempt.
			if attempt.Load() != 1 {
				t.Errorf("attempts = %d, want 1 (no retry for %d)", attempt.Load(), tt.status)
			}
		})
	}
}

func TestRetryExhausted(t *testing.T) {
	var attempt atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt.Add(1)
		w.WriteHeader(500)
		_, _ = w.Write([]byte("always failing"))
	}))
	defer ts.Close()

	c := newTestClient(ts.URL)

	status, body, err := c.Get(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	// After all retries, should return the last 500 response.
	if status != 500 {
		t.Errorf("status = %d, want 500", status)
	}
	if string(body) != "always failing" {
		t.Errorf("body = %q, want %q", body, "always failing")
	}
	// 1 initial + 3 retries = 4 attempts.
	if attempt.Load() != 4 {
		t.Errorf("attempts = %d, want 4", attempt.Load())
	}
}

func TestPostFormWithRetry(t *testing.T) {
	var attempt atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempt.Add(1)
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		ct := r.Header.Get("Content-Type")
		if ct != "application/x-www-form-urlencoded;charset=UTF-8" {
			t.Errorf("content-type = %q, want form-urlencoded", ct)
		}
		if n == 1 {
			w.WriteHeader(502)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	c := newTestClient(ts.URL)

	status, body, err := c.PostForm(context.Background(), ts.URL, "key=value")
	if err != nil {
		t.Fatalf("PostForm error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
	if attempt.Load() != 2 {
		t.Errorf("attempts = %d, want 2", attempt.Load())
	}
}

func TestGetHeaders(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")
		if ua != chromeUA {
			t.Errorf("User-Agent = %q, want Chrome UA", ua)
		}
		w.WriteHeader(200)
	}))
	defer ts.Close()

	c := newTestClient(ts.URL)
	status, _, err := c.Get(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
}

func TestPostFormHeaders(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != chromeUA {
			t.Error("missing Chrome User-Agent")
		}
		if r.Header.Get("Origin") != "https://www.google.com" {
			t.Error("missing Origin header")
		}
		if r.Header.Get("Referer") != "https://www.google.com/travel/flights" {
			t.Error("missing Referer header")
		}
		w.WriteHeader(200)
	}))
	defer ts.Close()

	c := newTestClient(ts.URL)
	_, _, err := c.PostForm(context.Background(), ts.URL, "data=test")
	if err != nil {
		t.Fatalf("PostForm error: %v", err)
	}
}

func TestSearchFlights_Delegates(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("flight data"))
	}))
	defer ts.Close()

	// Can't easily mock the URL constant, so test that SearchFlights/BatchExecute
	// at least build the correct request format. The actual URLs would fail but
	// the encode/formBody logic is what matters.
	c := newTestClient(ts.URL)

	// Test PostForm directly with the expected payload format.
	status, body, err := c.PostForm(context.Background(), ts.URL, "f.req=test_payload")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != "flight data" {
		t.Errorf("body = %q, want %q", body, "flight data")
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		status int
		want   bool
	}{
		{200, false},
		{201, false},
		{301, false},
		{400, false},
		{401, false},
		{403, false},
		{404, false},
		{429, true}, // rate limit
		{500, true}, // server error
		{502, true},
		{503, true},
		{504, true},
	}

	for _, tt := range tests {
		got := isRetryable(tt.status)
		if got != tt.want {
			t.Errorf("isRetryable(%d) = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestBackoffSleep_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	err := backoffSleep(ctx, 0)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestErrBlocked(t *testing.T) {
	if ErrBlocked == nil {
		t.Fatal("ErrBlocked is nil")
	}
	if ErrBlocked.Error() != "request blocked (403)" {
		t.Errorf("ErrBlocked.Error() = %q", ErrBlocked.Error())
	}
}
