package hotels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// TestSearchAgodaRetriesTransientStatus asserts the bounded retry loop recovers
// when /graphql/search answers an intermittent anti-bot status (e.g. 400) before
// serving the real 200 payload — the root cause of the reported "search status
// 400". The first attempt returns 400, the second 200; the search must succeed
// with the fixture's hotels rather than abort on the blip.
func TestSearchAgodaRetriesTransientStatus(t *testing.T) {
	searchJSON, err := os.ReadFile("testdata/agoda_search.json")
	if err != nil {
		t.Fatalf("read search fixture: %v", err)
	}
	suggestJSON, err := os.ReadFile("testdata/agoda_suggest.json")
	if err != nil {
		t.Fatalf("read suggest fixture: %v", err)
	}

	var searchHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/cronos/search/GetUnifiedSuggestResult"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(suggestJSON)
		case r.URL.Path == "/graphql/search":
			// First call: transient anti-bot 400. Subsequent: the real payload.
			if atomic.AddInt32(&searchHits, 1) == 1 {
				http.Error(w, "bot challenge", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(searchJSON)
		default:
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	origURL, origEnabled := agodaBaseURL, agodaEnabled
	agodaBaseURL = srv.URL
	agodaEnabled = true
	t.Setenv("AGODA_CITY_ID", "")
	defer func() { agodaBaseURL, agodaEnabled = origURL, origEnabled }()

	hotels, err := SearchAgoda(context.Background(), "Berlin", HotelSearchOptions{Currency: "EUR"})
	if err != nil {
		t.Fatalf("SearchAgoda after transient 400: %v", err)
	}
	if len(hotels) != 3 {
		t.Fatalf("want 3 hotels after retry, got %d", len(hotels))
	}
	if got := atomic.LoadInt32(&searchHits); got != 2 {
		t.Errorf("search endpoint hits = %d, want 2 (one 400 + one 200)", got)
	}
}

// TestSearchAgodaNonRetryableStatus asserts a non-transient status (404) fails
// fast without exhausting the retry budget — the retry policy must not mask a
// genuinely broken request.
func TestSearchAgodaNonRetryableStatus(t *testing.T) {
	suggestJSON, err := os.ReadFile("testdata/agoda_suggest.json")
	if err != nil {
		t.Fatalf("read suggest fixture: %v", err)
	}
	var searchHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/cronos/search/GetUnifiedSuggestResult"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(suggestJSON)
		case r.URL.Path == "/graphql/search":
			atomic.AddInt32(&searchHits, 1)
			http.Error(w, "gone", http.StatusNotFound)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	origURL, origEnabled := agodaBaseURL, agodaEnabled
	agodaBaseURL = srv.URL
	agodaEnabled = true
	t.Setenv("AGODA_CITY_ID", "")
	defer func() { agodaBaseURL, agodaEnabled = origURL, origEnabled }()

	_, err = SearchAgoda(context.Background(), "Berlin", HotelSearchOptions{Currency: "EUR"})
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
	if got := atomic.LoadInt32(&searchHits); got != 1 {
		t.Errorf("search endpoint hits = %d, want 1 (no retry on 404)", got)
	}
}

// TestAgodaRetryableStatus pins the transient/non-transient status policy.
func TestAgodaRetryableStatus(t *testing.T) {
	for _, c := range []int{400, 403, 408, 429, 500, 502, 503, 504} {
		if !agodaRetryableStatus(c) {
			t.Errorf("status %d should be retryable", c)
		}
	}
	for _, c := range []int{200, 301, 401, 404, 410, 418} {
		if agodaRetryableStatus(c) {
			t.Errorf("status %d should NOT be retryable", c)
		}
	}
}

// TestSearchAgodaExhaustedRetryHonestVerdict asserts that when every attempt is
// met with an anti-bot status, the retry budget is bounded (exactly
// agodaMaxAttempts hits, no infinite loop) and the surfaced error is an honest,
// diagnostic verdict — naming the status and the transient anti-bot root cause
// — rather than a bare "search status N" or a fabricated permanent block. This
// is the in-scope equivalent of the easyJet typed-status pattern: Agoda's block
// is intermittent (200 on retry in live capture), so the honest representation
// is a diagnostic transient error, not a permanent AKAMAI_BLOCK.
func TestSearchAgodaExhaustedRetryHonestVerdict(t *testing.T) {
	suggestJSON, err := os.ReadFile("testdata/agoda_suggest.json")
	if err != nil {
		t.Fatalf("read suggest fixture: %v", err)
	}
	var searchHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/cronos/search/GetUnifiedSuggestResult"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(suggestJSON)
		case r.URL.Path == "/graphql/search":
			atomic.AddInt32(&searchHits, 1)
			http.Error(w, "bot challenge", http.StatusBadRequest) // every attempt blocked
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	origURL, origEnabled := agodaBaseURL, agodaEnabled
	agodaBaseURL = srv.URL
	agodaEnabled = true
	t.Setenv("AGODA_CITY_ID", "")
	defer func() { agodaBaseURL, agodaEnabled = origURL, origEnabled }()

	_, err = SearchAgoda(context.Background(), "Berlin", HotelSearchOptions{Currency: "EUR"})
	if err == nil {
		t.Fatal("expected error when every attempt is blocked, got nil")
	}
	// Bounded: exactly agodaMaxAttempts hits, never an unbounded retry loop.
	if got := atomic.LoadInt32(&searchHits); got != int32(agodaMaxAttempts) {
		t.Errorf("search endpoint hits = %d, want %d (bounded retry budget)", got, agodaMaxAttempts)
	}
	// Honest diagnostic verdict: names the status and the anti-bot root cause.
	msg := err.Error()
	for _, want := range []string{"400", "anti-bot"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q should mention %q (honest transient-block verdict)", msg, want)
		}
	}
}
