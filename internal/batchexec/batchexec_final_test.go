package batchexec

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/cache"
	"golang.org/x/time/rate"
)

// ---------------------------------------------------------------------------
// GetWithCookie — 0% → covered
// ---------------------------------------------------------------------------

func TestGetWithCookie_HappyPath(t *testing.T) {
	var gotCookie string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	c := &Client{
		http:    ts.Client(),
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
	}

	status, body, err := c.GetWithCookie(context.Background(), ts.URL, "SOCS=abc123; CONSENT=YES")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want ok", body)
	}
	if gotCookie != "SOCS=abc123; CONSENT=YES" {
		t.Errorf("cookie = %q, want SOCS=abc123; CONSENT=YES", gotCookie)
	}
}

func TestGetWithCookie_EmptyCookie(t *testing.T) {
	var gotCookie string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		w.WriteHeader(200)
	}))
	defer ts.Close()

	c := &Client{
		http:    ts.Client(),
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
	}

	status, _, err := c.GetWithCookie(context.Background(), ts.URL, "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if gotCookie != "" {
		t.Errorf("cookie header should be empty, got %q", gotCookie)
	}
}

func TestGetWithCookie_Retry(t *testing.T) {
	var attempt atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempt.Add(1)
		if n == 1 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("recovered"))
	}))
	defer ts.Close()

	c := &Client{
		http:    ts.Client(),
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
	}

	status, body, err := c.GetWithCookie(context.Background(), ts.URL, "session=x")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d", status)
	}
	if string(body) != "recovered" {
		t.Errorf("body = %q", body)
	}
}

// ---------------------------------------------------------------------------
// BatchExecute — happy path (new: verifies f.req body format)
// ---------------------------------------------------------------------------

func TestBatchExecute_HappyPath(t *testing.T) {
	var gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`)]}'\n[["wrb.fr","AtySUc","hotel data"]]`))
	}))
	defer ts.Close()

	c := NewTestClient(ts.URL)
	status, body, err := c.BatchExecute(context.Background(), "test_payload")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if len(body) == 0 {
		t.Error("body should not be empty")
	}
	if !strings.Contains(gotBody, "f.req=test_payload") {
		t.Errorf("body = %q, should contain f.req=test_payload", gotBody)
	}
}

func TestBatchExecute_NoCacheBypass(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(200)
		_, _ = w.Write([]byte("result"))
	}))
	defer ts.Close()

	c := NewTestClient(ts.URL)
	c.SetNoCache(true)

	_, _, _ = c.BatchExecute(context.Background(), "nocache_payload")
	_, _, _ = c.BatchExecute(context.Background(), "nocache_payload")

	if callCount != 2 {
		t.Errorf("server called %d times, want 2 (no cache)", callCount)
	}
}

// ---------------------------------------------------------------------------
// PostExplore — happy path (new: verifies body content)
// ---------------------------------------------------------------------------

func TestPostExplore_HappyPath(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("explore data"))
	}))
	defer ts.Close()

	c := NewTestClient(ts.URL)
	status, body, err := c.PostExplore(context.Background(), "explore_payload")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != "explore data" {
		t.Errorf("body = %q", body)
	}
}

// ---------------------------------------------------------------------------
// PostCalendarGraph — happy path (new)
// ---------------------------------------------------------------------------

func TestPostCalendarGraph_HappyPath(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("calendar graph data"))
	}))
	defer ts.Close()

	c := NewTestClient(ts.URL)
	status, body, err := c.PostCalendarGraph(context.Background(), "graph_payload")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != "calendar graph data" {
		t.Errorf("body = %q", body)
	}
}

// ---------------------------------------------------------------------------
// PostCalendarGrid — happy path (new)
// ---------------------------------------------------------------------------

func TestPostCalendarGrid_HappyPath(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("calendar grid data"))
	}))
	defer ts.Close()

	c := NewTestClient(ts.URL)
	status, body, err := c.PostCalendarGrid(context.Background(), "grid_payload")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != "calendar grid data" {
		t.Errorf("body = %q", body)
	}
}

// ---------------------------------------------------------------------------
// SharedClient — 0% → covered
// ---------------------------------------------------------------------------

func TestSharedClient_ReturnsSameInstance(t *testing.T) {
	c1 := SharedClient()
	c2 := SharedClient()
	if c1 != c2 {
		t.Error("SharedClient should return the same instance")
	}
	if c1 == nil {
		t.Fatal("SharedClient should not return nil")
	}
	if c1.http == nil {
		t.Error("SharedClient.http should not be nil")
	}
	if c1.limiter == nil {
		t.Error("SharedClient.limiter should not be nil")
	}
}

// ---------------------------------------------------------------------------
// ResolveCityCode — 24% → higher via httptest (full happy path with mock)
// ---------------------------------------------------------------------------

func TestResolveCityCode_HappyPath_MockServer(t *testing.T) {
	ResetCityCache()
	defer ResetCityCache()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := `)]}'

33
[["wrb.fr","H028ib","[[[[3,\"Helsinki\",\"Helsinki\",\"Finland\",\"/m/01lbs\",1,0,null,null,null,null,null,[\"HEL\"]]],null]]",null,null,null,"generic"]]
`
		w.WriteHeader(200)
		_, _ = w.Write([]byte(resp))
	}))
	defer ts.Close()

	c := NewTestClient(ts.URL)
	code, err := ResolveCityCode(context.Background(), c, "HEL")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if code != "/m/01lbs" {
		t.Errorf("code = %q, want /m/01lbs", code)
	}

	// Verify it was cached.
	cityCacheMu.RLock()
	cached, ok := cityCache["HEL"]
	cityCacheMu.RUnlock()
	if !ok || cached != "/m/01lbs" {
		t.Errorf("cache miss: ok=%v, cached=%q", ok, cached)
	}
}

func TestResolveCityCode_403_ReturnsErrBlocked(t *testing.T) {
	ResetCityCache()
	defer ResetCityCache()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	}))
	defer ts.Close()

	c := NewTestClient(ts.URL)
	_, err := ResolveCityCode(context.Background(), c, "TEST_BLOCK")
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if err != ErrBlocked {
		t.Errorf("err = %v, want ErrBlocked", err)
	}
}

func TestResolveCityCode_NonOKStatus(t *testing.T) {
	ResetCityCache()
	defer ResetCityCache()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer ts.Close()

	c := NewTestClient(ts.URL)
	_, err := ResolveCityCode(context.Background(), c, "TEST_500")
	if err == nil {
		t.Fatal("expected error for 500 status")
	}
	if !strings.Contains(err.Error(), "unexpected status 500") {
		t.Errorf("error = %q, should contain 'unexpected status 500'", err.Error())
	}
}

func TestResolveCityCode_ParseError(t *testing.T) {
	ResetCityCache()
	defer ResetCityCache()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("not a valid response"))
	}))
	defer ts.Close()

	c := NewTestClient(ts.URL)
	_, err := ResolveCityCode(context.Background(), c, "BOGUS_PARSE")
	if err == nil {
		t.Error("expected error for unparseable response")
	}
}

// ---------------------------------------------------------------------------
// EncodeFlightFilters — error path
// ---------------------------------------------------------------------------

func TestEncodeFlightFilters_Unmarshallable(t *testing.T) {
	ch := make(chan int)
	_, err := EncodeFlightFilters(ch)
	if err == nil {
		t.Error("expected error for unmarshallable input")
	}
}

// ---------------------------------------------------------------------------
// SearchFlightsGL — caching with gl parameter
// ---------------------------------------------------------------------------

func TestSearchFlightsGL_CacheHitWithGL(t *testing.T) {
	// Verify that SearchFlightsGL with a gl param populates and reads cache.
	c := &Client{
		http:    &http.Client{},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
		cache:   cache.New(),
	}
	// Pre-populate cache with the key that SearchFlightsGL would use.
	url := FlightsURL + "&gl=US"
	payload := "f.req=gl_payload"
	c.setCached(url, payload, []byte("cached_gl_data"), FlightCacheTTL)

	status, body, err := c.SearchFlightsGL(context.Background(), "gl_payload", "US")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != "cached_gl_data" {
		t.Errorf("body = %q, want cached_gl_data", body)
	}
}

// ---------------------------------------------------------------------------
// NewTestClient — URL rewriting
// ---------------------------------------------------------------------------

func TestNewTestClient_URLRewrite(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(200)
	}))
	defer ts.Close()

	c := NewTestClient(ts.URL)
	_, _, _ = c.Get(context.Background(), "https://www.google.com/some/path?q=test")
	if gotPath != "/some/path" {
		t.Errorf("path = %q, want /some/path", gotPath)
	}
}

// ---------------------------------------------------------------------------
// SetNoCache — flag verification
// ---------------------------------------------------------------------------

func TestSetNoCache_DisablesGetAndSetCached(t *testing.T) {
	c := NewTestClient("http://localhost")
	c.SetNoCache(true)

	_, hit := c.getCached("test", "payload")
	if hit {
		t.Error("cache should miss when noCache is set")
	}

	c.setCached("test", "payload", []byte("data"), 0)
	_, hit = c.getCached("test", "payload")
	if hit {
		t.Error("cache should still miss after setCached with noCache")
	}
}

// ---------------------------------------------------------------------------
// ExtractFlightData — both buckets
// ---------------------------------------------------------------------------

func TestExtractFlightData_BothBuckets(t *testing.T) {
	inner := []any{
		nil, nil,
		[]any{[]any{"flight1", "flight2"}},
		[]any{[]any{"flight3"}},
	}
	flights, err := ExtractFlightData(inner)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(flights) != 3 {
		t.Errorf("expected 3 flights, got %d", len(flights))
	}
}

func TestExtractFlightData_OnlyBucket2(t *testing.T) {
	inner := []any{
		nil, nil,
		[]any{[]any{"flight1"}},
	}
	flights, err := ExtractFlightData(inner)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(flights) != 1 {
		t.Errorf("expected 1 flight, got %d", len(flights))
	}
}

// ---------------------------------------------------------------------------
// BuildHotelSearchPayload / BuildHotelPricePayload
// ---------------------------------------------------------------------------

func TestBuildHotelSearchPayload_ContainsRPCID(t *testing.T) {
	payload := BuildHotelSearchPayload("Helsinki", [3]int{2026, 5, 1}, [3]int{2026, 5, 3}, 2)
	if payload == "" {
		t.Error("payload should not be empty")
	}
	if !strings.Contains(payload, "AtySUc") {
		t.Error("payload should contain rpcid AtySUc")
	}
}

func TestBuildHotelPricePayload_ContainsRPCID(t *testing.T) {
	payload := BuildHotelPricePayload("ChIJxyz", [3]int{2026, 5, 1}, [3]int{2026, 5, 3}, "EUR")
	if payload == "" {
		t.Error("payload should not be empty")
	}
	if !strings.Contains(payload, "yY52ce") {
		t.Error("payload should contain rpcid yY52ce")
	}
}

func TestBuildHotelReviewPayload_ContainsRPCID(t *testing.T) {
	payload := BuildHotelReviewPayload("ChIJxyz", 20)
	if payload == "" {
		t.Error("payload should not be empty")
	}
	if !strings.Contains(payload, "ocp93e") {
		t.Error("payload should contain rpcid ocp93e")
	}
}

// ---------------------------------------------------------------------------
// Cache TTL and URL constants
// ---------------------------------------------------------------------------

func TestCacheTTLOrdering(t *testing.T) {
	if FlightCacheTTL <= 0 {
		t.Error("FlightCacheTTL should be > 0")
	}
	if HotelCacheTTL <= FlightCacheTTL {
		t.Error("HotelCacheTTL should be > FlightCacheTTL")
	}
	if DestinationCacheTTL <= HotelCacheTTL {
		t.Error("DestinationCacheTTL should be > HotelCacheTTL")
	}
}

func TestEndpointURLs_NonEmpty(t *testing.T) {
	urls := map[string]string{
		"FlightsURL":       FlightsURL,
		"ExploreURL":       ExploreURL,
		"CalendarGraphURL": CalendarGraphURL,
		"CalendarGridURL":  CalendarGridURL,
		"HotelsURL":        HotelsURL,
	}
	for name, u := range urls {
		if u == "" {
			t.Errorf("%s is empty", name)
		}
		if !strings.HasPrefix(u, "https://") {
			t.Errorf("%s = %q, should start with https://", name, u)
		}
	}
}

// ---------------------------------------------------------------------------
// DecodeBatchResponse — only-prefix edge case
// ---------------------------------------------------------------------------

func TestDecodeBatchResponse_OnlyPrefixStripped(t *testing.T) {
	_, err := DecodeBatchResponse([]byte(")]}'\n"))
	if err == nil {
		t.Error("expected error for empty after stripping prefix")
	}
}

// ---------------------------------------------------------------------------
// DecodeBatchResponse — single data after prefix (no newline)
// ---------------------------------------------------------------------------

func TestDecodeBatchResponse_DirectArray(t *testing.T) {
	body := []byte(`)]}'
["single","chunk"]`)
	results, err := DecodeBatchResponse(body)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 entries (direct parse), got %d", len(results))
	}
}
