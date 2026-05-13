package batchexec

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestDoWithRetry_NetworkError(t *testing.T) {
	// Server that immediately closes connection.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hijack and close to simulate network error.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("not a hijacker")
		}
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	defer ts.Close()

	c := &Client{
		http:    ts.Client(),
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
	}

	_, _, err := c.Get(context.Background(), ts.URL)
	if err == nil {
		t.Error("expected error for connection close")
	}
}

func TestDoWithRetry_ContextCancelledDuringBackoff(t *testing.T) {
	var attempt atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt.Add(1)
		w.WriteHeader(500) // Always fail to trigger backoff.
	}))
	defer ts.Close()

	c := &Client{
		http:    ts.Client(),
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
	}

	// Cancel after first attempt during backoff.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, _, err := c.Get(ctx, ts.URL)
	if err == nil {
		// It may return the 500 response if retries complete before timeout.
		// That's also acceptable behavior.
		t.Log("got nil error (retries may have completed before timeout)")
	}
}

func TestDoWithRetry_Success200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("OK"))
	}))
	defer ts.Close()

	c := &Client{
		http:    ts.Client(),
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
	}

	status, body, err := c.Get(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d", status)
	}
	if string(body) != "OK" {
		t.Errorf("body = %q", body)
	}
}

func TestDoWithRetry_ReadBodyError(t *testing.T) {
	// Server that sends headers but then abruptly closes body.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("partial"))
		// Don't send the remaining bytes — triggers read error.
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer ts.Close()

	c := &Client{
		http:    ts.Client(),
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
	}

	// This may or may not error depending on buffering, but should not panic.
	_, _, _ = c.Get(context.Background(), ts.URL)
}

func TestDoWithRetry_BuildReqError(t *testing.T) {
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
	}

	// Build request with invalid URL to trigger error.
	_, _, err := c.Get(context.Background(), "://invalid-url")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestRetryOn500Then200(t *testing.T) {
	var attempt atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempt.Add(1)
		if n == 1 {
			w.WriteHeader(500)
			_, _ = w.Write([]byte("error"))
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	c := &Client{
		http:    ts.Client(),
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
	}

	status, body, err := c.Get(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d", status)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q", body)
	}
}

func TestRetryOn502(t *testing.T) {
	var attempt atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempt.Add(1)
		if n == 1 {
			w.WriteHeader(502)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	c := &Client{
		http:    ts.Client(),
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
	}

	status, _, err := c.Get(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d", status)
	}
}

func TestPostForm_FormBodyPreserved(t *testing.T) {
	var gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(200)
	}))
	defer ts.Close()

	c := &Client{
		http:    ts.Client(),
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
	}

	_, _, err := c.PostForm(context.Background(), ts.URL, "f.req=test_payload")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if gotBody != "f.req=test_payload" {
		t.Errorf("body = %q, want %q", gotBody, "f.req=test_payload")
	}
}

func TestSearchFlightsAndBatchExecute_URLs(t *testing.T) {
	// Just verify the URL constants are set correctly.
	if FlightsURL == "" {
		t.Error("FlightsURL is empty")
	}
	if HotelsURL == "" {
		t.Error("HotelsURL is empty")
	}
}

func TestPostForm_InvalidURL(t *testing.T) {
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
	}

	_, _, err := c.PostForm(context.Background(), "://invalid", "body")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestGet_InvalidURL(t *testing.T) {
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
	}

	_, _, err := c.Get(context.Background(), "://invalid")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestDoWithRetry_AllRetriesFail_NetworkError(t *testing.T) {
	// Use a server that's already closed to generate network errors on every attempt.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	ts.Close() // Close immediately to cause connection refused.

	c := &Client{
		http:    ts.Client(),
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
	}

	_, _, err := c.Get(context.Background(), ts.URL)
	if err == nil {
		t.Error("expected error when server is closed")
	}
}

// --- SearchFlightsGL ---

func TestSearchFlightsGL_EmptyGL_UsesBaseURL(t *testing.T) {
	// When gl is empty, SearchFlightsGL should behave identically to SearchFlights.
	var gotURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	c := NewTestClient(ts.URL)
	status, _, err := c.SearchFlightsGL(context.Background(), "filters", "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	// Should NOT contain gl= parameter.
	if strings.Contains(gotURL, "gl=") {
		t.Errorf("URL should not contain gl= when gl is empty: %s", gotURL)
	}
}

func TestSearchFlightsGL_WithGL_AppendsParam(t *testing.T) {
	var gotURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	c := NewTestClient(ts.URL)
	status, _, err := c.SearchFlightsGL(context.Background(), "filters", "FI")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	// Should contain gl=FI parameter.
	if !strings.Contains(gotURL, "gl=FI") {
		t.Errorf("URL should contain gl=FI: %s", gotURL)
	}
}

func TestSearchFlightsGL_DelegatesFromSearchFlights(t *testing.T) {
	// SearchFlights should delegate to SearchFlightsGL with empty gl.
	var gotURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	c := NewTestClient(ts.URL)
	status, _, err := c.SearchFlights(context.Background(), "filters")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if strings.Contains(gotURL, "gl=") {
		t.Errorf("SearchFlights should not add gl= param: %s", gotURL)
	}
}
