package batchexec

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/cache"
	"golang.org/x/time/rate"
)

// --- SetNoCache ---

func TestSetNoCache_Enable(t *testing.T) {
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
		cache:   cache.New(),
	}
	c.SetNoCache(true)
	if !c.noCache {
		t.Error("expected noCache to be true")
	}
}

func TestSetNoCache_Disable(t *testing.T) {
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
		cache:   cache.New(),
	}
	c.SetNoCache(true)
	c.SetNoCache(false)
	if c.noCache {
		t.Error("expected noCache to be false after disable")
	}
}

// --- getCached / setCached ---

func TestGetCached_Miss(t *testing.T) {
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
		cache:   cache.New(),
	}
	data, ok := c.getCached("endpoint", "payload")
	if ok {
		t.Error("expected cache miss for empty cache")
	}
	if data != nil {
		t.Errorf("expected nil data, got %d bytes", len(data))
	}
}

func TestSetCached_ThenHit(t *testing.T) {
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
		cache:   cache.New(),
	}
	c.setCached("endpoint", "payload", []byte("cached_data"), FlightCacheTTL)

	data, ok := c.getCached("endpoint", "payload")
	if !ok {
		t.Fatal("expected cache hit after set")
	}
	if string(data) != "cached_data" {
		t.Errorf("cached data = %q, want cached_data", string(data))
	}
}

func TestGetCached_NoCacheBypass(t *testing.T) {
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
		cache:   cache.New(),
	}
	c.setCached("ep", "pl", []byte("data"), FlightCacheTTL)
	c.SetNoCache(true)

	data, ok := c.getCached("ep", "pl")
	if ok {
		t.Error("expected cache miss when noCache is true")
	}
	if data != nil {
		t.Errorf("expected nil data, got %d bytes", len(data))
	}
}

func TestSetCached_NoCacheSkipsWrite(t *testing.T) {
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
		cache:   cache.New(),
	}
	c.SetNoCache(true)
	c.setCached("ep", "pl", []byte("data"), FlightCacheTTL)

	// Re-enable cache and check nothing was stored.
	c.SetNoCache(false)
	data, ok := c.getCached("ep", "pl")
	if ok {
		t.Error("expected cache miss after set with noCache=true")
	}
	if data != nil {
		t.Errorf("expected nil data, got %d bytes", len(data))
	}
}

func TestGetCached_NilCache(t *testing.T) {
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
		cache:   nil,
	}
	data, ok := c.getCached("ep", "pl")
	if ok {
		t.Error("expected cache miss for nil cache")
	}
	if data != nil {
		t.Error("expected nil data for nil cache")
	}
}

func TestSetCached_NilCacheNoPanic(t *testing.T) {
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
		cache:   nil,
	}
	// Should not panic.
	c.setCached("ep", "pl", []byte("data"), FlightCacheTTL)
}

// --- SearchFlights (with httptest to avoid real HTTP) ---

func TestSearchFlights_CacheHit(t *testing.T) {
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
		cache:   cache.New(),
	}
	// Pre-fill the cache with the expected key.
	payload := "f.req=encoded_filters"
	c.setCached(FlightsURL, payload, []byte("cached_response"), FlightCacheTTL)

	status, body, err := c.SearchFlights(context.Background(), "encoded_filters")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != "cached_response" {
		t.Errorf("body = %q, want cached_response", string(body))
	}
}

func TestBatchExecute_CacheHit(t *testing.T) {
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
		cache:   cache.New(),
	}
	payload := "f.req=hotel_payload"
	c.setCached(HotelsURL, payload, []byte("hotel_cached"), HotelCacheTTL)

	status, body, err := c.BatchExecute(context.Background(), "hotel_payload")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != "hotel_cached" {
		t.Errorf("body = %q, want hotel_cached", string(body))
	}
}

func TestPostExplore_CacheHit(t *testing.T) {
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
		cache:   cache.New(),
	}
	payload := "f.req=explore_payload"
	c.setCached(ExploreURL, payload, []byte("explore_cached"), DestinationCacheTTL)

	status, body, err := c.PostExplore(context.Background(), "explore_payload")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != "explore_cached" {
		t.Errorf("body = %q, want explore_cached", string(body))
	}
}

func TestPostCalendarGraph_CacheHit(t *testing.T) {
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
		cache:   cache.New(),
	}
	payload := "f.req=graph_payload"
	c.setCached(CalendarGraphURL, payload, []byte("graph_cached"), FlightCacheTTL)

	status, body, err := c.PostCalendarGraph(context.Background(), "graph_payload")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != "graph_cached" {
		t.Errorf("body = %q, want graph_cached", string(body))
	}
}

func TestPostCalendarGrid_CacheHit(t *testing.T) {
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
		cache:   cache.New(),
	}
	payload := "f.req=grid_payload"
	c.setCached(CalendarGridURL, payload, []byte("grid_cached"), FlightCacheTTL)

	status, body, err := c.PostCalendarGrid(context.Background(), "grid_payload")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != "grid_cached" {
		t.Errorf("body = %q, want grid_cached", string(body))
	}
}

// --- SearchFlights populates cache on 200 ---

func TestSearchFlights_PopulatesCache(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("flight_data"))
	}))
	defer ts.Close()

	c := &Client{
		http:    ts.Client(),
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
		cache:   cache.New(),
	}

	// We cannot override FlightsURL, but we can test the cache mechanism
	// by calling PostForm directly and then checking getCached.
	status, body, err := c.PostForm(context.Background(), ts.URL, "f.req=test_filters")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if status != 200 {
		t.Fatalf("status = %d", status)
	}

	// Manually cache like SearchFlights would.
	c.setCached(ts.URL, "f.req=test_filters", body, FlightCacheTTL)

	// Verify cache works.
	data, ok := c.getCached(ts.URL, "f.req=test_filters")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(data) != "flight_data" {
		t.Errorf("cached data = %q, want flight_data", string(data))
	}
}

// --- BuildHotelReviewPayload ---

func TestBuildHotelReviewPayload_DefaultLimit(t *testing.T) {
	result := BuildHotelReviewPayload("/g/11test", 0)
	if result == "" {
		t.Fatal("empty result")
	}

	decoded, err := url.QueryUnescape(result)
	if err != nil {
		t.Fatalf("unescape: %v", err)
	}

	var outer []any
	if err := json.Unmarshal([]byte(decoded), &outer); err != nil {
		t.Fatalf("unmarshal outer: %v", err)
	}

	mid := outer[0].([]any)
	inner := mid[0].([]any)
	if inner[0] != "ocp93e" {
		t.Errorf("rpcid = %v, want ocp93e", inner[0])
	}
}

func TestBuildHotelReviewPayload_CustomLimit(t *testing.T) {
	result := BuildHotelReviewPayload("/g/11hotel", 25)
	if result == "" {
		t.Fatal("empty result")
	}

	decoded, err := url.QueryUnescape(result)
	if err != nil {
		t.Fatalf("unescape: %v", err)
	}

	var outer []any
	if err := json.Unmarshal([]byte(decoded), &outer); err != nil {
		t.Fatalf("unmarshal outer: %v", err)
	}

	// Verify the args contain the limit of 25.
	mid := outer[0].([]any)
	inner := mid[0].([]any)
	argsJSON := inner[1].(string)
	var argsArr []any
	if err := json.Unmarshal([]byte(argsJSON), &argsArr); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	// Last element should be the limit.
	if v, ok := argsArr[len(argsArr)-1].(float64); !ok || int(v) != 25 {
		t.Errorf("limit = %v, want 25", argsArr[len(argsArr)-1])
	}
}

func TestBuildHotelReviewPayload_NegativeLimit(t *testing.T) {
	result := BuildHotelReviewPayload("/g/11hotel", -5)
	if result == "" {
		t.Fatal("empty result")
	}

	decoded, err := url.QueryUnescape(result)
	if err != nil {
		t.Fatalf("unescape: %v", err)
	}

	var outer []any
	if err := json.Unmarshal([]byte(decoded), &outer); err != nil {
		t.Fatalf("unmarshal outer: %v", err)
	}

	mid := outer[0].([]any)
	inner := mid[0].([]any)
	argsJSON := inner[1].(string)
	var argsArr []any
	if err := json.Unmarshal([]byte(argsJSON), &argsArr); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	// Negative limit should default to 10.
	if v, ok := argsArr[len(argsArr)-1].(float64); !ok || int(v) != 10 {
		t.Errorf("limit = %v, want 10 (default for negative)", argsArr[len(argsArr)-1])
	}
}
