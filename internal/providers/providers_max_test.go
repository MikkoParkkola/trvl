package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestDecompressBody_BrotliMax(t *testing.T) {
	// Exercises brotli with a larger payload to cover more of the reader path.
	original := strings.Repeat(`{"name":"Hotel","stars":5},`, 100)

	var buf bytes.Buffer
	bw := brotli.NewWriter(&buf)
	_, _ = bw.Write([]byte(original))
	_ = bw.Close()

	resp := &http.Response{
		Header: http.Header{"Content-Encoding": {"br"}},
		Body:   io.NopCloser(&buf),
	}

	got, err := decompressBody(resp, int64(len(original)+1024))
	if err != nil {
		t.Fatalf("decompressBody(br): %v", err)
	}
	if string(got) != original {
		t.Errorf("got len=%d, want len=%d", len(got), len(original))
	}
}

// ===========================================================================
// decompressBody — Zstd encoding
// ===========================================================================

func TestDecompressBody_ZstdEncoding(t *testing.T) {
	original := `{"results": [{"name": "Zstd Hotel"}]}`

	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd.NewWriter: %v", err)
	}
	_, _ = zw.Write([]byte(original))
	_ = zw.Close()

	resp := &http.Response{
		Header: http.Header{
			"Content-Encoding": {"zstd"},
		},
		Body: io.NopCloser(&buf),
	}

	got, err := decompressBody(resp, 4096)
	if err != nil {
		t.Fatalf("decompressBody(zstd): %v", err)
	}
	if string(got) != original {
		t.Errorf("got %q, want %q", string(got), original)
	}
}

// ===========================================================================
// decompressBody — unknown encoding treated as identity
// ===========================================================================

func TestDecompressBody_UnknownEncoding(t *testing.T) {
	body := `plain text body`
	resp := &http.Response{
		Header: http.Header{
			"Content-Encoding": {"deflate"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}

	got, err := decompressBody(resp, 4096)
	if err != nil {
		t.Fatalf("decompressBody(deflate): %v", err)
	}
	if string(got) != body {
		t.Errorf("got %q, want %q", string(got), body)
	}
}

// ===========================================================================
// decompressBody — limit enforcement
// ===========================================================================

func TestDecompressBody_LimitEnforced(t *testing.T) {
	body := strings.Repeat("x", 1000)
	resp := &http.Response{
		Header: http.Header{},
		Body:   io.NopCloser(strings.NewReader(body)),
	}

	got, err := decompressBody(resp, 100)
	if err != nil {
		t.Fatalf("decompressBody: %v", err)
	}
	if len(got) != 100 {
		t.Errorf("got %d bytes, want 100", len(got))
	}
}

// ===========================================================================
// saveCachedCookies + loadCachedCookies — round trip
// ===========================================================================

func TestSaveThenLoadCachedCookies_RoundTrip(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	targetURL := "https://roundtrip-test.example.com/search"
	u, _ := url.Parse(targetURL)

	// Seed cookies into the jar.
	jar.SetCookies(u, []*http.Cookie{
		{Name: "session", Value: "abc123", Domain: ".example.com", Path: "/"},
		{Name: "csrf", Value: "tok456", Domain: ".example.com", Path: "/"},
	})

	// Save.
	saveCachedCookies(client, targetURL)
	t.Cleanup(func() {
		// Clean up the cache file.
		path, _ := cookieCachePath("roundtrip-test.example.com")
		_ = os.Remove(path)
	})

	// Create a fresh client and load the cached cookies.
	jar2, _ := cookiejar.New(nil)
	client2 := &http.Client{Jar: jar2}

	loaded := loadCachedCookies(client2, targetURL)
	if !loaded {
		t.Fatal("expected loadCachedCookies to return true")
	}

	// Verify cookies are in the new jar.
	cookies := jar2.Cookies(u)
	if len(cookies) < 2 {
		t.Fatalf("expected at least 2 cookies, got %d", len(cookies))
	}

	names := map[string]string{}
	for _, c := range cookies {
		names[c.Name] = c.Value
	}
	if names["session"] != "abc123" {
		t.Errorf("session = %q, want 'abc123'", names["session"])
	}
	if names["csrf"] != "tok456" {
		t.Errorf("csrf = %q, want 'tok456'", names["csrf"])
	}
}

// ===========================================================================
// saveCachedCookies — edge cases
// ===========================================================================

func TestSaveCachedCookies_URLWithNoHost(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	// URL parses but has no host — early return.
	saveCachedCookies(client, "/just-a-path")
}

func TestSaveCachedCookies_WritesAndLoads(t *testing.T) {
	// Verify that saveCachedCookies produces a file that loadCachedCookies can read.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	targetURL := "https://save-writes-test.example.com/page"
	u, _ := url.Parse(targetURL)

	jar.SetCookies(u, []*http.Cookie{
		{Name: "tok", Value: "xyz", Domain: ".example.com", Path: "/"},
	})

	saveCachedCookies(client, targetURL)
	t.Cleanup(func() {
		path, _ := cookieCachePath("save-writes-test.example.com")
		_ = os.Remove(path)
	})

	// Verify the file exists.
	path, _ := cookieCachePath("save-writes-test.example.com")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cache file not created: %v", err)
	}

	// Load into fresh client.
	jar2, _ := cookiejar.New(nil)
	client2 := &http.Client{Jar: jar2}
	if !loadCachedCookies(client2, targetURL) {
		t.Fatal("loadCachedCookies returned false after save")
	}
}

func TestSaveCachedCookies_EmptyURL(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	// Should not panic.
	saveCachedCookies(client, "")
}

func TestSaveCachedCookies_MalformedURL(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	// Should not panic.
	saveCachedCookies(client, "::not-a-url::")
}

func TestSaveCachedCookies_NoCookies(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	// No cookies in jar -> no-op, no panic.
	saveCachedCookies(client, "https://nocookies.example.com/page")
}

// ===========================================================================
// loadCachedCookies — edge cases
// ===========================================================================

func TestLoadCachedCookies_NoFile(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	got := loadCachedCookies(client, "https://no-cache-file.example.com/page")
	if got {
		t.Error("expected false when no cache file exists")
	}
}

func TestLoadCachedCookies_Expired(t *testing.T) {
	// Write an expired cache file directly.
	targetURL := "https://expired-cache.example.com"
	path, err := cookieCachePath("expired-cache.example.com")
	if err != nil {
		t.Fatal(err)
	}

	expired := []cachedCookie{
		{
			Name:    "old",
			Value:   "stale",
			Domain:  ".example.com",
			Path:    "/",
			SavedAt: time.Now().Add(-48 * time.Hour), // 48h ago — beyond 24h TTL
		},
	}
	data, _ := json.Marshal(expired)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, data, 0o600)
	t.Cleanup(func() { _ = os.Remove(path) })

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	got := loadCachedCookies(client, targetURL)
	if got {
		t.Error("expected false for expired cache")
	}
}

func TestLoadCachedCookies_InvalidJSON(t *testing.T) {
	path, err := cookieCachePath("bad-json-cache.example.com")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte(`{not valid json}`), 0o600)
	t.Cleanup(func() { _ = os.Remove(path) })

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	got := loadCachedCookies(client, "https://bad-json-cache.example.com")
	if got {
		t.Error("expected false for invalid JSON cache file")
	}
}

func TestLoadCachedCookies_EmptyArray(t *testing.T) {
	path, err := cookieCachePath("empty-array-cache.example.com")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte(`[]`), 0o600)
	t.Cleanup(func() { _ = os.Remove(path) })

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	got := loadCachedCookies(client, "https://empty-array-cache.example.com")
	if got {
		t.Error("expected false for empty cookie array")
	}
}

func TestLoadCachedCookies_NilJarClient(t *testing.T) {
	// Write a valid cache file.
	path, err := cookieCachePath("nil-jar-cache.example.com")
	if err != nil {
		t.Fatal(err)
	}

	valid := []cachedCookie{
		{
			Name:    "ok",
			Value:   "fine",
			Domain:  ".example.com",
			Path:    "/",
			SavedAt: time.Now(),
		},
	}
	data, _ := json.Marshal(valid)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, data, 0o600)
	t.Cleanup(func() { _ = os.Remove(path) })

	// Client with no jar -> can't set cookies.
	client := &http.Client{}
	got := loadCachedCookies(client, "https://nil-jar-cache.example.com")
	if got {
		t.Error("expected false when client has no jar")
	}
}

// ===========================================================================
// cookieCachePath — sanitization
// ===========================================================================

func TestCookieCachePath_SpecialChars(t *testing.T) {
	path, err := cookieCachePath("my:weird/host name")
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(path)
	// Colons, slashes, and spaces should be replaced with underscores.
	if strings.ContainsAny(base, ":/") {
		t.Errorf("path %q contains unsanitized characters", base)
	}
}

// ===========================================================================
// enrichRatings via httptest
// ===========================================================================

func TestEnrichRatings_FromJSONLD(t *testing.T) {
	jsonLD := `{
		"@type": "Hotel",
		"aggregateRating": {
			"ratingValue": 8.5,
			"reviewCount": 1234
		}
	}`
	html := fmt.Sprintf(`<html><head>
		<script type="application/ld+json">%s</script>
	</head><body></body></html>`, jsonLD)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, html)
	}))
	defer srv.Close()

	hotels := []models.HotelResult{
		{Name: "Rated Hotel", Rating: 9.0, BookingURL: srv.URL + "/hotel/1"},
		{Name: "Unrated Hotel", Rating: 0, BookingURL: srv.URL + "/hotel/2"},
		{Name: "No URL", Rating: 0, BookingURL: ""},
	}

	cfg := &ProviderConfig{ID: "test"}
	enrichRatings(context.Background(), srv.Client(), hotels, cfg)

	if hotels[0].Rating != 9.0 {
		t.Errorf("rated hotel should keep its rating, got %v", hotels[0].Rating)
	}
	if hotels[1].Rating != 8.5 {
		t.Errorf("unrated hotel should get enriched rating, got %v", hotels[1].Rating)
	}
	if hotels[1].ReviewCount != 1234 {
		t.Errorf("review count should be enriched, got %d", hotels[1].ReviewCount)
	}
}

func TestEnrichRatings_MaxEnrichments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><script type="application/ld+json">{"aggregateRating":{"ratingValue":7.0}}</script></html>`)
	}))
	defer srv.Close()

	// Create 10 unrated hotels — only 5 should be enriched.
	hotels := make([]models.HotelResult, 10)
	for i := range hotels {
		hotels[i] = models.HotelResult{
			Name:       fmt.Sprintf("Hotel %d", i),
			BookingURL: srv.URL + fmt.Sprintf("/hotel/%d", i),
		}
	}

	cfg := &ProviderConfig{ID: "test"}
	enrichRatings(context.Background(), srv.Client(), hotels, cfg)

	enrichedCount := 0
	for _, h := range hotels {
		if h.Rating > 0 {
			enrichedCount++
		}
	}
	if enrichedCount != 5 {
		t.Errorf("expected exactly 5 enrichments (max), got %d", enrichedCount)
	}
}

func TestEnrichRatings_HTTP404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	hotels := []models.HotelResult{
		{Name: "Missing Hotel", Rating: 0, BookingURL: srv.URL + "/hotel/missing"},
	}

	cfg := &ProviderConfig{ID: "test"}
	enrichRatings(context.Background(), srv.Client(), hotels, cfg)

	if hotels[0].Rating != 0 {
		t.Errorf("rating should stay 0 on 404, got %v", hotels[0].Rating)
	}
}

// ===========================================================================
// fetchJSONLDRating — edge cases
// ===========================================================================

func TestFetchJSONLDRating_NoStructuredData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>No structured data</body></html>`)
	}))
	defer srv.Close()

	_, _, err := fetchJSONLDRating(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error when no JSON-LD is present")
	}
}

func TestFetchJSONLDRating_GraphArrayNested(t *testing.T) {
	jsonLD := `{
		"@graph": [
			{"@type": "WebPage"},
			{"@type": "Hotel", "aggregateRating": {"ratingValue": 9.2, "reviewCount": 500}}
		]
	}`
	html := fmt.Sprintf(`<html><script type="application/ld+json">%s</script></html>`, jsonLD)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, html)
	}))
	defer srv.Close()

	rating, count, err := fetchJSONLDRating(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rating != 9.2 {
		t.Errorf("rating = %v, want 9.2", rating)
	}
	if count != 500 {
		t.Errorf("count = %d, want 500", count)
	}
}

func TestFetchJSONLDRating_MalformedJSON(t *testing.T) {
	html := `<html><script type="application/ld+json">not valid json</script></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, html)
	}))
	defer srv.Close()

	_, _, err := fetchJSONLDRating(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error for invalid JSON-LD")
	}
}

// ===========================================================================
// enrichAirbnbDescriptions via httptest
// ===========================================================================

func TestEnrichAirbnbDescriptions_FromNiobe(t *testing.T) {
	niobeData := map[string]any{
		"niobeClientData": []any{
			[]any{
				"CacheKey:1",
				map[string]any{
					"data": map[string]any{
						"presentation": map[string]any{
							"stayProductDetailPage": map[string]any{
								"sections": map[string]any{
									"metadata": map[string]any{
										"sharingConfig": map[string]any{
											"description": "A beautiful beachfront apartment.",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	niobeJSON, _ := json.Marshal(niobeData)
	html := fmt.Sprintf(`<html><script data-deferred-state-0>%s</script></html>`, niobeJSON)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, html)
	}))
	defer srv.Close()

	hotels := []models.HotelResult{
		{Name: "Beach Apt", Description: "", BookingURL: srv.URL + "/rooms/12345"},
		{Name: "Already Described", Description: "Has a desc", BookingURL: srv.URL + "/rooms/67890"},
		{Name: "No URL", Description: ""},
	}

	enrichAirbnbDescriptions(context.Background(), srv.Client(), hotels)

	if hotels[0].Description != "A beautiful beachfront apartment." {
		t.Errorf("description = %q, want 'A beautiful beachfront apartment.'", hotels[0].Description)
	}
	if hotels[1].Description != "Has a desc" {
		t.Errorf("already-described should keep its description, got %q", hotels[1].Description)
	}
}

func TestEnrichAirbnbDescriptions_FallbackToSection(t *testing.T) {
	niobeData := map[string]any{
		"niobeClientData": []any{
			[]any{
				"CacheKey:1",
				map[string]any{
					"data": map[string]any{
						"presentation": map[string]any{
							"stayProductDetailPage": map[string]any{
								"sections": map[string]any{
									"metadata": map[string]any{
										"sharingConfig": map[string]any{},
									},
									"sections": []any{
										map[string]any{
											"sectionComponentType": "OTHER",
											"section": map[string]any{
												"title": "Not the description",
											},
										},
										map[string]any{
											"sectionComponentType": "DESCRIPTION_DEFAULT",
											"section": map[string]any{
												"body": map[string]any{
													"htmlText": "<b>Cozy</b> apartment with <br/>great views.",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	niobeJSON, _ := json.Marshal(niobeData)
	html := fmt.Sprintf(`<html><script data-deferred-state-0>%s</script></html>`, niobeJSON)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, html)
	}))
	defer srv.Close()

	hotels := []models.HotelResult{
		{Name: "Apt", Description: "", BookingURL: srv.URL + "/rooms/1"},
	}

	enrichAirbnbDescriptions(context.Background(), srv.Client(), hotels)

	// Should have HTML stripped.
	if !strings.Contains(hotels[0].Description, "Cozy") {
		t.Errorf("description = %q, want to contain 'Cozy'", hotels[0].Description)
	}
	if strings.Contains(hotels[0].Description, "<b>") {
		t.Errorf("description should have HTML stripped: %q", hotels[0].Description)
	}
}
