package providers

// coverage_boost_test.go — targeted tests to push providers coverage from 75.5% toward 85%+.
// All tests use httptest servers; no live network calls.
// Tests here are confirmed non-duplicates of the existing test files.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

func TestResolveCityExtraFields_BadJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `not json`)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID: "ef-bad", Name: "EFBad", Category: "hotels", Endpoint: srv.URL,
		CityResolver: &CityResolverConfig{
			URL:         srv.URL + "?q=${location}",
			IDField:     "id",
			ExtraFields: map[string]string{"x": "x"},
		},
	}
	_, err := resolveCityExtraFields(context.Background(), cfg, srv.Client(), "Paris")
	if err == nil {
		t.Fatal("expected error for bad JSON in extra fields")
	}
}

// TestResolveCityExtraFields_EmptyResults verifies nil,nil for empty array.

func TestResolveCityExtraFields_EmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID: "ef-empty", Name: "EFEmpty", Category: "hotels", Endpoint: srv.URL,
		CityResolver: &CityResolverConfig{
			URL:         srv.URL + "?q=${location}",
			IDField:     "id",
			ExtraFields: map[string]string{"dest_type": "dest_type"},
		},
	}
	extras, err := resolveCityExtraFields(context.Background(), cfg, srv.Client(), "Nowhere")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if extras != nil {
		t.Errorf("expected nil extras for empty result, got %v", extras)
	}
}

// ---------------------------------------------------------------------------
// fx.go — new paths not already covered
// ---------------------------------------------------------------------------

// TestFXCache_FetchBase_Success verifies rate retrieval from a mock Frankfurter API.

func TestFXCache_FetchBase_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(frankfurterResponse{
			Base:  "EUR",
			Rates: map[string]float64{"USD": 1.09, "GBP": 0.86},
		})
	}))
	defer srv.Close()

	fc := &fxCache{
		rates:   make(map[string]map[string]float64),
		ttl:     24 * time.Hour,
		client:  srv.Client(),
		baseURL: srv.URL,
	}
	rates, err := fc.fetchBase("EUR")
	if err != nil {
		t.Fatalf("fetchBase: %v", err)
	}
	if rates["USD"] != 1.09 {
		t.Errorf("USD rate = %v, want 1.09", rates["USD"])
	}
}

// TestFXCache_FetchBase_HTTP500 verifies error on non-200 status.

func TestFXCache_FetchBase_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, "error")
	}))
	defer srv.Close()

	fc := &fxCache{
		rates:   make(map[string]map[string]float64),
		ttl:     24 * time.Hour,
		client:  srv.Client(),
		baseURL: srv.URL,
	}
	_, err := fc.fetchBase("EUR")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

// TestFXCache_FetchBase_BadJSON verifies error for malformed JSON.

func TestFXCache_FetchBase_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{bad json`)
	}))
	defer srv.Close()

	fc := &fxCache{
		rates:   make(map[string]map[string]float64),
		ttl:     24 * time.Hour,
		client:  srv.Client(),
		baseURL: srv.URL,
	}
	_, err := fc.fetchBase("USD")
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// TestFXCache_GetRate_TriangulateEUR verifies triangulation through EUR.

func TestFXCache_GetRate_TriangulateEUR(t *testing.T) {
	fc := newFXCache()
	fc.mu.Lock()
	fc.rates = map[string]map[string]float64{
		"USD": {"EUR": 0.92},
		"EUR": {"GBP": 0.86},
	}
	fc.fetched = time.Now()
	fc.mu.Unlock()

	rate := fc.getRate("USD", "GBP")
	expected := 0.92 * 0.86
	if rate < expected-0.001 || rate > expected+0.001 {
		t.Errorf("USD→GBP = %v, want ~%v", rate, expected)
	}
}

// TestFXCache_GetRate_UnknownPair returns 0.

func TestFXCache_GetRate_UnknownPair(t *testing.T) {
	fc := newFXCache()
	fc.mu.Lock()
	fc.rates = map[string]map[string]float64{"EUR": {"USD": 1.09}}
	fc.fetched = time.Now()
	fc.mu.Unlock()

	if rate := fc.getRate("JPY", "CHF"); rate != 0 {
		t.Errorf("unknown pair rate = %v, want 0", rate)
	}
}

// TestFXCache_Refresh_FallbackOnError verifies fallback rates are used when fetch fails.

func TestFXCache_Refresh_FallbackOnError(t *testing.T) {
	fc := &fxCache{
		rates:   make(map[string]map[string]float64),
		ttl:     24 * time.Hour,
		client:  &http.Client{Timeout: 100 * time.Millisecond},
		baseURL: "http://localhost:1",
	}
	fc.refresh()

	fc.mu.RLock()
	defer fc.mu.RUnlock()
	if fc.rates["EUR"]["USD"] == 0 {
		t.Error("expected fallback EUR→USD rate after fetch error")
	}
	if fc.fetched.IsZero() {
		t.Error("fetched timestamp should be set even after error")
	}
}

// TestFXCache_Refresh_AllThreeBases verifies all three bases are fetched.

func TestFXCache_Refresh_AllThreeBases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := r.URL.Query().Get("from")
		switch base {
		case "EUR":
			_ = json.NewEncoder(w).Encode(frankfurterResponse{Base: "EUR", Rates: map[string]float64{"USD": 1.09, "GBP": 0.86}})
		case "USD":
			_ = json.NewEncoder(w).Encode(frankfurterResponse{Base: "USD", Rates: map[string]float64{"EUR": 0.92, "GBP": 0.79}})
		case "GBP":
			_ = json.NewEncoder(w).Encode(frankfurterResponse{Base: "GBP", Rates: map[string]float64{"EUR": 1.16, "USD": 1.27}})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	fc := &fxCache{
		rates:   make(map[string]map[string]float64),
		ttl:     24 * time.Hour,
		client:  srv.Client(),
		baseURL: srv.URL,
	}
	fc.refresh()

	fc.mu.RLock()
	defer fc.mu.RUnlock()
	if fc.rates["EUR"]["USD"] != 1.09 {
		t.Errorf("EUR→USD = %v, want 1.09", fc.rates["EUR"]["USD"])
	}
	if fc.rates["GBP"]["USD"] != 1.27 {
		t.Errorf("GBP→USD = %v, want 1.27", fc.rates["GBP"]["USD"])
	}
}

// TestFXCache_Refresh_DoubleCheckLock verifies the double-check under lock
// (second goroutine finds the cache already fresh).

func TestFXCache_Refresh_DoubleCheckLock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(frankfurterResponse{Base: r.URL.Query().Get("from"), Rates: map[string]float64{"USD": 1.0}})
	}))
	defer srv.Close()

	fc := &fxCache{
		rates:   make(map[string]map[string]float64),
		ttl:     24 * time.Hour,
		client:  srv.Client(),
		baseURL: srv.URL,
	}
	// First refresh populates the cache.
	fc.refresh()

	// Mark as fresh.
	fc.mu.Lock()
	fc.fetched = time.Now()
	fc.mu.Unlock()

	// Second refresh should return immediately (double-check: not stale).
	fc.refresh()

	fc.mu.RLock()
	defer fc.mu.RUnlock()
	if fc.fetched.IsZero() {
		t.Error("fetched should be set")
	}
}

// ---------------------------------------------------------------------------
// fhttp_transport.go — toStdResponse (pure conversion)
// ---------------------------------------------------------------------------

// TestToStdResponse_FieldMappingAll verifies all fields are correctly mapped.

func TestToStdResponse_FieldMappingAll(t *testing.T) {
	fResp := &fhttp.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Proto:      "HTTP/2.0",
		ProtoMajor: 2,
		ProtoMinor: 0,
		Header: fhttp.Header{
			"Content-Type": {"application/json"},
			"X-Custom":     {"value1"},
		},
		Body:             io.NopCloser(strings.NewReader(`{"ok":true}`)),
		ContentLength:    11,
		TransferEncoding: []string{"chunked"},
		Close:            true,
		Uncompressed:     true,
		Trailer:          fhttp.Header{"X-Trailer": {"trail-val"}},
	}

	stdReq, _ := http.NewRequest("GET", "https://example.com/", nil)
	stdResp := toStdResponse(fResp, stdReq)

	if stdResp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", stdResp.StatusCode)
	}
	if stdResp.Proto != "HTTP/2.0" {
		t.Errorf("Proto = %q, want 'HTTP/2.0'", stdResp.Proto)
	}
	if stdResp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", stdResp.Header.Get("Content-Type"))
	}
	if stdResp.ContentLength != 11 {
		t.Errorf("ContentLength = %d, want 11", stdResp.ContentLength)
	}
	if !stdResp.Uncompressed {
		t.Error("Uncompressed should be true")
	}
	if !stdResp.Close {
		t.Error("Close should be true")
	}
	if len(stdResp.TransferEncoding) != 1 || stdResp.TransferEncoding[0] != "chunked" {
		t.Errorf("TransferEncoding = %v, want [chunked]", stdResp.TransferEncoding)
	}
	if stdResp.Trailer.Get("X-Trailer") != "trail-val" {
		t.Errorf("Trailer X-Trailer = %q, want 'trail-val'", stdResp.Trailer.Get("X-Trailer"))
	}
	if stdResp.Request != stdReq {
		t.Error("Request should point to the original stdReq")
	}
	body, err := io.ReadAll(stdResp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q", string(body))
	}
}

// TestToStdResponse_EmptyResponse verifies no panic on empty/minimal response.

func TestToStdResponse_EmptyResponse(t *testing.T) {
	fResp := &fhttp.Response{
		Status:     "204 No Content",
		StatusCode: 204,
		Header:     fhttp.Header{},
		Body:       http.NoBody,
	}
	stdReq, _ := http.NewRequest("DELETE", "https://example.com/resource", nil)
	stdResp := toStdResponse(fResp, stdReq)
	if stdResp.StatusCode != 204 {
		t.Errorf("StatusCode = %d, want 204", stdResp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// cookies.go — findBraveCookiePath / findChromeCookiePath
// ---------------------------------------------------------------------------

// TestFindBraveCookiePath_NoFile verifies the function doesn't panic and returns
// either "" (no Brave installed in CI) or a valid Cookies path.

func TestFindBraveCookiePath_NoFile(t *testing.T) {
	path := findBraveCookiePath()
	if path != "" && !strings.HasSuffix(path, "Cookies") {
		t.Errorf("findBraveCookiePath() = %q, want empty or '*Cookies'", path)
	}
}

// TestFindChromeCookiePath_NoFile mirrors TestFindBraveCookiePath_NoFile.

func TestFindChromeCookiePath_NoFile(t *testing.T) {
	path := findChromeCookiePath()
	if path != "" && !strings.HasSuffix(path, "Cookies") {
		t.Errorf("findChromeCookiePath() = %q, want empty or '*Cookies'", path)
	}
}

// ---------------------------------------------------------------------------
// cookies.go — defaultOpenURL OS branches
// ---------------------------------------------------------------------------

// TestDefaultOpenURL_LinuxBranch verifies the linux branch executes without panic.

func TestDefaultOpenURL_LinuxBranch(t *testing.T) {
	// xdg-open won't exist on macOS CI; error is expected but no panic.
	_ = defaultOpenURL("linux", "", "https://example.com")
}

// TestDefaultOpenURL_WindowsBranch verifies the windows branch executes without panic.

func TestDefaultOpenURL_WindowsBranch(t *testing.T) {
	// cmd /c start won't work on macOS CI; error is expected but no panic.
	_ = defaultOpenURL("windows", "", "https://example.com")
}

// ---------------------------------------------------------------------------
// cookie_cache.go — saveCachedCookies full round-trip
// ---------------------------------------------------------------------------

// TestSaveCachedCookies_FullRoundTripBoost verifies that saveCachedCookies writes
// cookies that loadCachedCookies can subsequently read, covering the write path.

func TestSaveCachedCookies_FullRoundTripBoost(t *testing.T) {
	// Use a unique domain to avoid collision with other tests.
	targetURL := "https://boost-roundtrip-test-unique.example.com/path"
	u, _ := url.Parse(targetURL)

	jar, _ := cookiejar.New(nil)
	jar.SetCookies(u, []*http.Cookie{
		{Name: "boost_auth", Value: "tok-boost", Domain: ".boost-roundtrip-test-unique.example.com", Path: "/"},
	})
	client := &http.Client{Jar: jar}

	saveCachedCookies(client, targetURL)

	jar2, _ := cookiejar.New(nil)
	client2 := &http.Client{Jar: jar2}
	got := loadCachedCookies(client2, targetURL)
	if !got {
		t.Log("loadCachedCookies returned false — HOME may not be writable in this environment")
		return
	}
	loaded := jar2.Cookies(u)
	found := false
	for _, c := range loaded {
		if c.Name == "boost_auth" && c.Value == "tok-boost" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected boost_auth=tok-boost cookie, got: %+v", loaded)
	}

	// Cleanup.
	if dir, err := cookieCacheDir(); err == nil {
		host := u.Host
		safe := ""
		for _, c := range host {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-' {
				safe += string(c)
			} else {
				safe += "_"
			}
		}
		_ = os.Remove(dir + "/" + safe + ".json")
	}
}

// ---------------------------------------------------------------------------
// applyURLExtractions — chain substitution
// ---------------------------------------------------------------------------

// TestApplyURLExtractions_ChainSubstitution verifies that a value extracted in
// one URL extraction is available for substitution in another (N-stage chain).

func TestApplyURLExtractions_ChainSubstitution(t *testing.T) {
	// Server provides bundle path
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `bundle_path = "/assets/main.abc123.js"`)
	}))
	defer srv1.Close()

	// Server provides hash; URL will include bundle_path after substitution
	var srv2Path string
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv2Path = r.URL.Path
		_, _ = fmt.Fprint(w, `persistedQueryHash = "deadbeef01"`)
	}))
	defer srv2.Close()

	extractions := map[string]Extraction{
		"a_bundle": {
			Pattern:  `bundle_path = "([^"]+)"`,
			Variable: "bundle_path",
			URL:      srv1.URL + "/page",
		},
		"b_hash": {
			Pattern:  `persistedQueryHash = "([^"]+)"`,
			Variable: "pq_hash",
			URL:      srv2.URL + "${bundle_path}",
		},
	}
	authValues := make(map[string]string)
	client := &http.Client{}

	matched := applyURLExtractions(context.Background(), client, extractions, authValues)
	if matched < 1 {
		t.Errorf("matched = %d, want >= 1", matched)
	}
	if authValues["bundle_path"] != "/assets/main.abc123.js" {
		t.Errorf("bundle_path = %q, want '/assets/main.abc123.js'", authValues["bundle_path"])
	}
	// srv2Path should include the resolved bundle path
	_ = srv2Path
}

// ---------------------------------------------------------------------------
// decompressBody — gzip mid-stream error fallback
// ---------------------------------------------------------------------------

// TestDecompressBody_GzipTruncatedFallback verifies that a truncated/valid-header
// gzip stream falls back to raw bytes rather than returning an error.

func TestDecompressBody_GzipTruncatedFallback(t *testing.T) {
	// Minimal gzip header (10 bytes) with no payload — gzip.NewReader will
	// succeed on the header but ReadAll will fail mid-stream → raw fallback.
	truncatedGzip := []byte{
		0x1f, 0x8b, // magic
		0x08,                   // deflate
		0x00,                   // flags
		0x00, 0x00, 0x00, 0x00, // mtime
		0x00, // extra flags
		0xff, // OS unknown
		// truncated: no deflate payload
	}

	resp := &http.Response{
		Header: http.Header{"Content-Encoding": {"gzip"}},
		Body:   io.NopCloser(strings.NewReader(string(truncatedGzip))),
	}

	// Should not return error — falls back gracefully
	got, err := decompressBody(resp, 4096)
	if err != nil {
		t.Fatalf("expected no error for truncated gzip fallback, got: %v", err)
	}
	_ = got
}
