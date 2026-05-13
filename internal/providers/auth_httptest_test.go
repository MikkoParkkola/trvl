package providers

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// applyExtractions
// ---------------------------------------------------------------------------

// TestApplyExtractions_BodyMatch verifies regex extraction from the response body.
func TestApplyExtractions_BodyMatch(t *testing.T) {
	body := []byte(`<html><meta name="csrf" content="tok-abc123"></html>`)
	resp := &http.Response{
		Header: make(http.Header),
	}
	extractions := map[string]Extraction{
		"csrf": {
			Pattern:  `content="(tok-[^"]+)"`,
			Variable: "csrf_token",
		},
	}
	authValues := make(map[string]string)

	matched := applyExtractions(extractions, resp, body, authValues)
	if matched != 1 {
		t.Errorf("matched = %d, want 1", matched)
	}
	if authValues["csrf_token"] != "tok-abc123" {
		t.Errorf("csrf_token = %q, want 'tok-abc123'", authValues["csrf_token"])
	}
}

// TestApplyExtractions_HeaderMatch verifies regex extraction from a response header.
func TestApplyExtractions_HeaderMatch(t *testing.T) {
	body := []byte(`irrelevant body`)
	resp := &http.Response{
		Header: http.Header{
			"X-Session-Id": {"session-42xyz"},
		},
	}
	extractions := map[string]Extraction{
		"session": {
			Pattern:  `(session-[a-z0-9]+)`,
			Variable: "session_id",
			Header:   "X-Session-Id",
		},
	}
	authValues := make(map[string]string)

	matched := applyExtractions(extractions, resp, body, authValues)
	if matched != 1 {
		t.Errorf("matched = %d, want 1", matched)
	}
	if authValues["session_id"] != "session-42xyz" {
		t.Errorf("session_id = %q, want 'session-42xyz'", authValues["session_id"])
	}
}

// TestApplyExtractions_NoMatch verifies that unmatched extractions don't set values.
func TestApplyExtractions_NoMatch(t *testing.T) {
	body := []byte(`no token here`)
	resp := &http.Response{
		Header: make(http.Header),
	}
	extractions := map[string]Extraction{
		"token": {
			Pattern:  `token=([a-z]+)`,
			Variable: "auth_token",
		},
	}
	authValues := make(map[string]string)

	matched := applyExtractions(extractions, resp, body, authValues)
	if matched != 0 {
		t.Errorf("matched = %d, want 0", matched)
	}
	if _, ok := authValues["auth_token"]; ok {
		t.Error("auth_token should not be set when pattern doesn't match")
	}
}

// TestApplyExtractions_Default verifies that the default value is used when
// the pattern doesn't match.
func TestApplyExtractions_Default(t *testing.T) {
	body := []byte(`no hash here`)
	resp := &http.Response{Header: make(http.Header)}
	extractions := map[string]Extraction{
		"hash": {
			Pattern:  `sha256:([a-f0-9]{64})`,
			Variable: "sha_hash",
			Default:  "fallback-hash",
		},
	}
	authValues := make(map[string]string)

	matched := applyExtractions(extractions, resp, body, authValues)
	if matched != 1 {
		t.Errorf("matched = %d, want 1 (default counts)", matched)
	}
	if authValues["sha_hash"] != "fallback-hash" {
		t.Errorf("sha_hash = %q, want 'fallback-hash'", authValues["sha_hash"])
	}
}

// TestApplyExtractions_SkipsURLExtractions verifies that extractions with
// a URL field are skipped (handled by applyURLExtractions).
func TestApplyExtractions_SkipsURLExtractions(t *testing.T) {
	body := []byte(`token=abc123`)
	resp := &http.Response{Header: make(http.Header)}
	extractions := map[string]Extraction{
		"body_token": {
			Pattern:  `token=([a-z0-9]+)`,
			Variable: "tok",
		},
		"url_token": {
			Pattern:  `hash=([a-f0-9]+)`,
			Variable: "hash",
			URL:      "https://example.com/bundle.js",
		},
	}
	authValues := make(map[string]string)

	matched := applyExtractions(extractions, resp, body, authValues)
	if matched != 1 {
		t.Errorf("matched = %d, want 1 (url_token should be skipped)", matched)
	}
	if authValues["tok"] != "abc123" {
		t.Errorf("tok = %q, want 'abc123'", authValues["tok"])
	}
	if _, ok := authValues["hash"]; ok {
		t.Error("hash should not be set (URL extraction skipped)")
	}
}

// TestApplyExtractions_InvalidRegex verifies that an invalid regex pattern
// doesn't panic and doesn't match.
func TestApplyExtractions_InvalidRegex(t *testing.T) {
	body := []byte(`anything`)
	resp := &http.Response{Header: make(http.Header)}
	extractions := map[string]Extraction{
		"bad": {
			Pattern:  `[invalid(regex`,
			Variable: "bad_var",
		},
	}
	authValues := make(map[string]string)

	matched := applyExtractions(extractions, resp, body, authValues)
	if matched != 0 {
		t.Errorf("matched = %d, want 0 for invalid regex", matched)
	}
}

// TestApplyExtractions_VariableFallbackToName verifies that when Variable is
// empty, the extraction map key is used as the variable name.
func TestApplyExtractions_VariableFallbackToName(t *testing.T) {
	body := []byte(`api_key: mykey123`)
	resp := &http.Response{Header: make(http.Header)}
	extractions := map[string]Extraction{
		"api_key": {
			Pattern: `api_key: (\w+)`,
			// Variable deliberately omitted
		},
	}
	authValues := make(map[string]string)

	matched := applyExtractions(extractions, resp, body, authValues)
	if matched != 1 {
		t.Errorf("matched = %d, want 1", matched)
	}
	if authValues["api_key"] != "mykey123" {
		t.Errorf("api_key = %q, want 'mykey123'", authValues["api_key"])
	}
}

// ---------------------------------------------------------------------------
// applyURLExtractions
// ---------------------------------------------------------------------------

// TestApplyURLExtractions_Success verifies two-stage extraction: fetch a URL
// and extract a value from its body.
func TestApplyURLExtractions_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `var hash = "abc123def456";`)
	}))
	defer srv.Close()

	extractions := map[string]Extraction{
		"bundle_hash": {
			Pattern:  `hash = "([a-f0-9]+)"`,
			Variable: "js_hash",
			URL:      srv.URL + "/bundle.js",
		},
	}
	authValues := make(map[string]string)

	matched := applyURLExtractions(context.Background(), srv.Client(), extractions, authValues)
	if matched != 1 {
		t.Errorf("matched = %d, want 1", matched)
	}
	if authValues["js_hash"] != "abc123def456" {
		t.Errorf("js_hash = %q, want 'abc123def456'", authValues["js_hash"])
	}
}

// TestApplyURLExtractions_WithVarSubstitution verifies that ${var} placeholders
// in the URL are resolved from previously-extracted values.
func TestApplyURLExtractions_WithVarSubstitution(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		_, _ = fmt.Fprint(w, `sha256Hash":"deadbeef0123456789abcdef0123456789abcdef0123456789abcdef01234567"`)
	}))
	defer srv.Close()

	extractions := map[string]Extraction{
		"sha_hash": {
			Pattern:  `sha256Hash":"([a-f0-9]{64})"`,
			Variable: "sha",
			URL:      srv.URL + "/${bundle_url}",
		},
	}
	authValues := map[string]string{
		"bundle_url": "assets/main.js",
	}

	matched := applyURLExtractions(context.Background(), srv.Client(), extractions, authValues)
	if matched != 1 {
		t.Errorf("matched = %d, want 1", matched)
	}
	if receivedPath != "/assets/main.js" {
		t.Errorf("path = %q, want '/assets/main.js'", receivedPath)
	}
}

// TestApplyURLExtractions_HTTP500 verifies that a non-2xx response from the
// URL extraction endpoint doesn't crash and counts as no match.
func TestApplyURLExtractions_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	extractions := map[string]Extraction{
		"token": {
			Pattern:  `token=(\w+)`,
			Variable: "tok",
			URL:      srv.URL + "/fail",
		},
	}
	authValues := make(map[string]string)

	matched := applyURLExtractions(context.Background(), srv.Client(), extractions, authValues)
	if matched != 0 {
		t.Errorf("matched = %d, want 0 for 500 response", matched)
	}
}

// TestApplyURLExtractions_SkipsNonURLExtractions verifies that extractions
// without a URL field are skipped.
func TestApplyURLExtractions_SkipsNonURLExtractions(t *testing.T) {
	extractions := map[string]Extraction{
		"body_only": {
			Pattern:  `token=(\w+)`,
			Variable: "tok",
			// No URL field
		},
	}
	authValues := make(map[string]string)

	matched := applyURLExtractions(context.Background(), http.DefaultClient, extractions, authValues)
	if matched != 0 {
		t.Errorf("matched = %d, want 0 (body-only extraction should be skipped)", matched)
	}
}

// TestApplyURLExtractions_NilClient verifies no panic with nil client.
func TestApplyURLExtractions_NilClient(t *testing.T) {
	extractions := map[string]Extraction{
		"token": {
			Pattern: `token=(\w+)`,
			URL:     "https://example.com/bundle.js",
		},
	}
	authValues := make(map[string]string)

	matched := applyURLExtractions(context.Background(), nil, extractions, authValues)
	if matched != 0 {
		t.Errorf("matched = %d, want 0 for nil client", matched)
	}
}

// TestApplyURLExtractions_DefaultValue verifies that when the URL extraction
// doesn't match, the default value is used.
func TestApplyURLExtractions_DefaultValue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `no matching content here`)
	}))
	defer srv.Close()

	extractions := map[string]Extraction{
		"hash": {
			Pattern:  `sha256:([a-f0-9]{64})`,
			Variable: "sha",
			URL:      srv.URL + "/bundle.js",
			Default:  "default-sha-value",
		},
	}
	authValues := make(map[string]string)

	matched := applyURLExtractions(context.Background(), srv.Client(), extractions, authValues)
	if matched != 1 {
		t.Errorf("matched = %d, want 1 (default counts)", matched)
	}
	if authValues["sha"] != "default-sha-value" {
		t.Errorf("sha = %q, want 'default-sha-value'", authValues["sha"])
	}
}

// ---------------------------------------------------------------------------
// decompressBody
// ---------------------------------------------------------------------------

// TestDecompressBody_Identity verifies raw (uncompressed) body passthrough.
func TestDecompressBody_Identity(t *testing.T) {
	body := `{"results": []}`
	resp := &http.Response{
		Header: http.Header{},
		Body:   io.NopCloser(strings.NewReader(body)),
	}

	got, err := decompressBody(resp, 1024)
	if err != nil {
		t.Fatalf("decompressBody: %v", err)
	}
	if string(got) != body {
		t.Errorf("got %q, want %q", string(got), body)
	}
}

// TestDecompressBody_Gzip verifies gzip decompression.
func TestDecompressBody_Gzip(t *testing.T) {
	original := `{"results": [{"name": "Gzip Hotel"}]}`

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(original))
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}

	resp := &http.Response{
		Header: http.Header{
			"Content-Encoding": {"gzip"},
		},
		Body: io.NopCloser(&buf),
	}

	got, err := decompressBody(resp, 4096)
	if err != nil {
		t.Fatalf("decompressBody: %v", err)
	}
	if string(got) != original {
		t.Errorf("got %q, want %q", string(got), original)
	}
}

// TestDecompressBody_GzipFallback verifies that when Content-Encoding says gzip
// but the body is not actually gzip-encoded, the raw bytes are returned.
func TestDecompressBody_GzipFallback(t *testing.T) {
	rawBody := `plain text, not gzip`
	resp := &http.Response{
		Header: http.Header{
			"Content-Encoding": {"gzip"},
		},
		Body: io.NopCloser(strings.NewReader(rawBody)),
	}

	got, err := decompressBody(resp, 4096)
	if err != nil {
		t.Fatalf("decompressBody: %v", err)
	}
	// Should fall back to raw bytes since it's not valid gzip.
	if string(got) != rawBody {
		t.Errorf("got %q, want %q (should fall back to raw)", string(got), rawBody)
	}
}

// TestDecompressBody_Uncompressed verifies that when resp.Uncompressed is true,
// the body is read raw regardless of Content-Encoding header.
func TestDecompressBody_Uncompressed(t *testing.T) {
	body := `already decompressed`
	resp := &http.Response{
		Header: http.Header{
			"Content-Encoding": {"gzip"},
		},
		Body:         io.NopCloser(strings.NewReader(body)),
		Uncompressed: true,
	}

	got, err := decompressBody(resp, 4096)
	if err != nil {
		t.Fatalf("decompressBody: %v", err)
	}
	if string(got) != body {
		t.Errorf("got %q, want %q", string(got), body)
	}
}

// ---------------------------------------------------------------------------
// doPreflightRequest
// ---------------------------------------------------------------------------

// TestDoPreflightRequest_GET verifies a basic GET preflight request.
func TestDoPreflightRequest_GET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %q, want GET", r.Method)
		}
		_, _ = fmt.Fprint(w, `<html>token=xyz</html>`)
	}))
	defer srv.Close()

	auth := &AuthConfig{
		PreflightURL: srv.URL + "/page",
	}

	resp, body, err := doPreflightRequest(context.Background(), srv.Client(), auth)
	if err != nil {
		t.Fatalf("doPreflightRequest: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "token=xyz") {
		t.Errorf("body = %q, want to contain 'token=xyz'", string(body))
	}
}

// TestDoPreflightRequest_POST verifies a POST preflight with body and headers.
func TestDoPreflightRequest_POST(t *testing.T) {
	var receivedBody string
	var receivedContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %q, want POST", r.Method)
		}
		receivedContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		_, _ = fmt.Fprint(w, `{"token":"abc"}`)
	}))
	defer srv.Close()

	auth := &AuthConfig{
		PreflightURL:    srv.URL + "/auth",
		PreflightMethod: "POST",
		PreflightBody:   "grant_type=client_credentials",
		PreflightHeaders: map[string]string{
			"Content-Type": "application/x-www-form-urlencoded",
		},
	}

	resp, body, err := doPreflightRequest(context.Background(), srv.Client(), auth)
	if err != nil {
		t.Fatalf("doPreflightRequest: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if receivedBody != "grant_type=client_credentials" {
		t.Errorf("body = %q, want 'grant_type=client_credentials'", receivedBody)
	}
	if receivedContentType != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want 'application/x-www-form-urlencoded'", receivedContentType)
	}
	if !strings.Contains(string(body), "token") {
		t.Errorf("response body = %q, want to contain 'token'", string(body))
	}
}

// ---------------------------------------------------------------------------
// doSearchRequest
// ---------------------------------------------------------------------------

// TestDoSearchRequest_Retry verifies that doSearchRequest clones and retries
// a search request correctly.
func TestDoSearchRequest_Retry(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []any{
				map[string]any{"name": "Retried Hotel"},
			},
		})
	}))
	defer srv.Close()

	// Create original request.
	orig, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL+"/search", nil)
	orig.Header.Set("X-Custom", "test-value")

	resp, body, err := doSearchRequest(context.Background(), srv.Client(), orig)
	if err != nil {
		t.Fatalf("doSearchRequest: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Retried Hotel") {
		t.Errorf("body should contain 'Retried Hotel': %s", string(body))
	}
}

// TestDoSearchRequest_POST verifies that POST body is correctly replayed.
func TestDoSearchRequest_POST(t *testing.T) {
	var receivedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"results":[]}`)
	}))
	defer srv.Close()

	bodyStr := `{"query":"hotels"}`
	orig, _ := http.NewRequestWithContext(context.Background(), "POST", srv.URL+"/graphql", strings.NewReader(bodyStr))
	orig.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(bodyStr)), nil
	}
	orig.Header.Set("Content-Type", "application/json")

	_, _, err := doSearchRequest(context.Background(), srv.Client(), orig)
	if err != nil {
		t.Fatalf("doSearchRequest: %v", err)
	}
	if receivedBody != bodyStr {
		t.Errorf("body = %q, want %q", receivedBody, bodyStr)
	}
}

// ---------------------------------------------------------------------------
// needsBrowserCookieFallback
// ---------------------------------------------------------------------------

// TestNeedsBrowserCookieFallback_Scenarios verifies the fallback decision logic.
func TestNeedsBrowserCookieFallback_Scenarios(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		extracted   int
		extractions map[string]Extraction
		want        bool
	}{
		{"202 accepted", http.StatusAccepted, 0, nil, true},
		{"403 forbidden", http.StatusForbidden, 0, nil, true},
		{"200 with extractions but none matched", http.StatusOK, 0, map[string]Extraction{"x": {}}, true},
		{"200 with extractions all matched", http.StatusOK, 1, map[string]Extraction{"x": {}}, false},
		{"200 no extractions", http.StatusOK, 0, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := needsBrowserCookieFallback(tc.status, tc.extracted, tc.extractions)
			if got != tc.want {
				t.Errorf("needsBrowserCookieFallback(%d, %d, %v) = %v, want %v",
					tc.status, tc.extracted, tc.extractions, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isAkamaiChallenge
// ---------------------------------------------------------------------------

// TestIsAkamaiChallenge_Extended verifies additional challenge detection cases.
func TestIsAkamaiChallenge_Extended(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"202 with awswaf", 202, `<html><script src="awswaf"></script></html>`, true},
		{"202 with window.aws", 202, `<html><script>window.aws</script></html>`, true},
		{"202 with reportChallengeError", 202, `<html>reportChallengeError</html>`, true},
		{"202 with JSON (not challenge)", 202, `{"status":"accepted"}`, false},
		{"202 with JSON array (not challenge)", 202, `[{"id":1}]`, false},
		{"200 with challenge markers (wrong status)", 200, `<script src="challenge.js"></script>`, false},
		{"202 empty body", 202, ``, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isAkamaiChallenge(tc.status, []byte(tc.body))
			if got != tc.want {
				t.Errorf("isAkamaiChallenge(%d, %q) = %v, want %v", tc.status, tc.body, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// providerFixHint
// ---------------------------------------------------------------------------

// TestProviderFixHint verifies the fix hint generation for common errors.
func TestProviderFixHint(t *testing.T) {
	tests := []struct {
		err  string
		want string // substring expected in the hint
	}{
		{"preflight request failed", "test_provider"},
		{"results_path did not resolve", "response structure changed"},
		{"http 403: blocked", "WAF"},
		{"http 202: challenge page", "WAF"},
		{"rate limit exceeded", "Rate limited"},
		{"connection refused", "connectivity"},
	}
	for _, tc := range tests {
		hint := providerFixHint(fmt.Errorf("%s", tc.err))
		if !containsSubstring(hint, tc.want) {
			t.Errorf("providerFixHint(%q) = %q, want to contain %q", tc.err, hint, tc.want)
		}
	}
}
