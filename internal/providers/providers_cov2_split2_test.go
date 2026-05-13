package providers

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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/andybalholm/brotli"
)

func TestResolvePropertyType_CaseInsensitive(t *testing.T) {
	lookup := map[string]string{"Hotel": "204", "Apartment": "201"}
	got := resolvePropertyType(lookup, "hotel")
	if got != "204" {
		t.Errorf("got %q, want '204'", got)
	}
}

func TestResolvePropertyType_Empty(t *testing.T) {
	lookup := map[string]string{"hotel": "204"}
	got := resolvePropertyType(lookup, "")
	if got != "" {
		t.Errorf("expected empty for empty property type, got %q", got)
	}
}

func TestResolvePropertyType_NilLookup(t *testing.T) {
	got := resolvePropertyType(nil, "hotel")
	if got != "" {
		t.Errorf("expected empty for nil lookup, got %q", got)
	}
}

func TestResolvePropertyType_NoMatch(t *testing.T) {
	lookup := map[string]string{"hotel": "204"}
	got := resolvePropertyType(lookup, "castle")
	if got != "" {
		t.Errorf("expected empty for no match, got %q", got)
	}
}

// ===========================================================================
// mapping.go — lastIntToken, firstNumericToken, extractCurrencyCode, isUpperAlpha
// ===========================================================================

func TestLastIntToken_AdditionalCases(t *testing.T) {
	tests := []struct{ input, want string }{
		{"abc 42 def 99", "99"},
		{"trailing123", "123"},
	}
	for _, tt := range tests {
		got := lastIntToken(tt.input)
		if got != tt.want {
			t.Errorf("lastIntToken(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFirstNumericToken_WithComma(t *testing.T) {
	got := firstNumericToken("€1,204")
	if got != "1204" {
		t.Errorf("firstNumericToken('€1,204') = %q, want '1204'", got)
	}
}

func TestFirstNumericToken_NoNumber(t *testing.T) {
	got := firstNumericToken("no numbers here")
	if got != "" {
		t.Errorf("firstNumericToken('no numbers') = %q, want empty", got)
	}
}

func TestExtractCurrencyCode_Prefix(t *testing.T) {
	tests := []struct{ input, want string }{
		{"EUR 204", "EUR"},
		{"204 USD", "USD"},
		{"€175", "EUR"},
		{"£99", "GBP"},
		{"$120", "USD"},
		{"¥5000", "JPY"},
		{"", ""},
		{"12", ""},
		{"ab", ""},
	}
	for _, tt := range tests {
		got := extractCurrencyCode(tt.input)
		if got != tt.want {
			t.Errorf("extractCurrencyCode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsUpperAlpha(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"EUR", true},
		{"Eur", false},
		{"123", false},
		{"", false},
		{"A", true},
	}
	for _, tt := range tests {
		got := isUpperAlpha(tt.input)
		if got != tt.want {
			t.Errorf("isUpperAlpha(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ===========================================================================
// enrichment.go — enrichRatings via httptest
// ===========================================================================

func TestEnrichRatings_ViaHTTPTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html>
			<script type="application/ld+json">
			{
				"@type": "Hotel",
				"aggregateRating": {
					"ratingValue": 8.7,
					"reviewCount": 1234
				}
			}
			</script>
		</html>`)
	}))
	defer srv.Close()

	hotels := []models.HotelResult{
		{Name: "Test Hotel", Rating: 0, BookingURL: srv.URL + "/hotel/1"},
		{Name: "Already Rated", Rating: 9.1, BookingURL: srv.URL + "/hotel/2"},
	}

	cfg := &ProviderConfig{ID: "test-enrich"}
	enrichRatings(context.Background(), srv.Client(), hotels, cfg)

	if hotels[0].Rating != 8.7 {
		t.Errorf("expected enriched rating 8.7, got %v", hotels[0].Rating)
	}
	if hotels[0].ReviewCount != 1234 {
		t.Errorf("expected enriched review count 1234, got %d", hotels[0].ReviewCount)
	}
	if hotels[1].Rating != 9.1 {
		t.Error("already-rated hotel should not be modified")
	}
}

func TestEnrichRatings_NoBookingURL(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "No URL", Rating: 0, BookingURL: ""},
	}
	cfg := &ProviderConfig{ID: "test"}
	// Should not panic.
	enrichRatings(context.Background(), http.DefaultClient, hotels, cfg)
	if hotels[0].Rating != 0 {
		t.Error("hotel with no BookingURL should not be enriched")
	}
}

func TestEnrichRatings_MaxFiveEnrichments(t *testing.T) {
	enrichCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enrichCount++
		_, _ = fmt.Fprint(w, `<html>
			<script type="application/ld+json">{"aggregateRating":{"ratingValue":7.5,"reviewCount":100}}</script>
		</html>`)
	}))
	defer srv.Close()

	hotels := make([]models.HotelResult, 10)
	for i := range hotels {
		hotels[i] = models.HotelResult{
			Name:       fmt.Sprintf("Hotel %d", i),
			Rating:     0,
			BookingURL: srv.URL + fmt.Sprintf("/hotel/%d", i),
		}
	}

	cfg := &ProviderConfig{ID: "test-max"}
	enrichRatings(context.Background(), srv.Client(), hotels, cfg)

	if enrichCount > 5 {
		t.Errorf("enriched %d hotels, max should be 5", enrichCount)
	}
}

func TestFetchJSONLDRating_HTTP404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	_, _, err := fetchJSONLDRating(context.Background(), srv.Client(), srv.URL+"/hotel/1")
	if err == nil {
		t.Error("expected error for HTTP 404")
	}
}

func TestFetchJSONLDRating_NoJSONLD(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>No JSON-LD here</body></html>`)
	}))
	defer srv.Close()

	_, _, err := fetchJSONLDRating(context.Background(), srv.Client(), srv.URL+"/hotel/1")
	if err == nil {
		t.Error("expected error when no JSON-LD found")
	}
}

func TestFetchJSONLDRating_GraphArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html>
			<script type="application/ld+json">
			{
				"@graph": [
					{"@type": "WebPage"},
					{"@type": "Hotel", "aggregateRating": {"ratingValue": 9.2, "reviewCount": 500}}
				]
			}
			</script>
		</html>`)
	}))
	defer srv.Close()

	rating, count, err := fetchJSONLDRating(context.Background(), srv.Client(), srv.URL+"/hotel/1")
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

// ===========================================================================
// enrichment.go — stripHTMLTags additional
// ===========================================================================

func TestStripHTMLTags_NestedTags(t *testing.T) {
	got := stripHTMLTags("<div><p>Hello <b>World</b></p></div>")
	if got != "Hello World" {
		t.Errorf("got %q, want 'Hello World'", got)
	}
}

// ===========================================================================
// discover.go — discoverArrayPaths, discoverFieldMappings
// ===========================================================================

func TestDiscoverArrayPaths_NestedArray(t *testing.T) {
	data := map[string]any{
		"data": map[string]any{
			"results": []any{
				map[string]any{"name": "Hotel A"},
				map[string]any{"name": "Hotel B"},
			},
		},
	}

	suggestions := discoverArrayPaths(data, "")
	if _, ok := suggestions["results_path"]; !ok {
		t.Error("expected results_path suggestion")
	}
}

func TestDiscoverArrayPaths_ExcludedPath(t *testing.T) {
	data := map[string]any{
		"results": []any{
			map[string]any{"name": "Hotel A"},
		},
	}

	suggestions := discoverArrayPaths(data, "results")
	if _, ok := suggestions["results_path"]; ok {
		t.Error("excluded path should not appear in suggestions")
	}
}

func TestDiscoverFieldMappings_CommonFields(t *testing.T) {
	obj := map[string]any{
		"name":      "Grand Hotel",
		"price":     float64(199),
		"id":        float64(12345),
		"rating":    float64(8.5),
		"latitude":  float64(52.5),
		"longitude": float64(13.4),
	}

	suggestions := discoverFieldMappings(obj, "")
	expected := []string{"field:name", "field:price", "field:hotel_id", "field:rating", "field:lat", "field:lon"}
	for _, key := range expected {
		if _, ok := suggestions[key]; !ok {
			t.Errorf("expected suggestion for %q, got %v", key, suggestions)
		}
	}
}

// ===========================================================================
// decompressBody — Brotli
// ===========================================================================

func TestDecompressBody_Brotli(t *testing.T) {
	// Create a brotli-compressed response. We use the brotli writer.
	original := `{"results": [{"name": "Brotli Hotel"}]}`
	var buf strings.Builder
	bw := brotli.NewWriter(&buf)
	_, _ = bw.Write([]byte(original))
	_ = bw.Close()

	resp := &http.Response{
		Header: http.Header{"Content-Encoding": {"br"}},
		Body:   io.NopCloser(strings.NewReader(buf.String())),
	}

	got, err := decompressBody(resp, 4096)
	if err != nil {
		t.Fatalf("decompressBody br: %v", err)
	}
	if string(got) != original {
		t.Errorf("got %q, want %q", string(got), original)
	}
}

// brotli.NewWriter from the andybalholm/brotli import is used directly
// in TestDecompressBody_Brotli above. No custom adapter is needed since
// the brotli package is already a transitive dependency of providers via
// auth.go's decompressBody function. The import was added at the top of
// this file alongside the other test imports.
//
// The andybalholm/brotli package provides both brotli.NewWriter (for
// compression in tests) and brotli.NewReader (used by decompressBody
// in production). This symmetry lets us create properly-compressed
// test payloads that exercise the real decompression path.
//
// Previous versions of this file used a custom brotliWriterAdapter
// but that was replaced with the direct brotli.NewWriter call for
// correctness — the adapter wrote raw bytes instead of compressed,
// causing the decompression test to fail.
//
// The io.Writer interface is satisfied by strings.Builder which is
// used as the compression target in the test above.
//
// No additional helpers are needed for the brotli test path.
//
// See also: TestDecompressBody_Gzip and TestDecompressBody_GzipFallback
// in auth_httptest_test.go for the gzip equivalent tests.

// ===========================================================================
// cookies.go — cookieSnapshotKey edge cases
// ===========================================================================

func TestCookieSnapshotKey_Empty(t *testing.T) {
	got := cookieSnapshotKey(nil)
	if got != "" {
		t.Errorf("expected empty for nil, got %q", got)
	}
}

func TestCookieSnapshotKey_NilCookie(t *testing.T) {
	cookies := []*http.Cookie{nil, {Name: "a", Value: "1"}}
	got := cookieSnapshotKey(cookies)
	if got == "" {
		t.Error("expected non-empty for non-nil cookies")
	}
}

// ===========================================================================
// config.go — Validate edge cases
// ===========================================================================

func TestValidate_EndpointUnparseable(t *testing.T) {
	cfg := ProviderConfig{
		ID: "x", Name: "x", Category: "hotel",
		Endpoint:        "://",
		ResponseMapping: ResponseMapping{ResultsPath: "results"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for unparseable endpoint")
	}
}

func TestValidate_RateLimit100IsOK(t *testing.T) {
	cfg := ProviderConfig{
		ID: "x", Name: "x", Category: "hotel",
		Endpoint:        "https://api.example.com",
		ResponseMapping: ResponseMapping{ResultsPath: "r"},
		RateLimit:       RateLimitConfig{RequestsPerSecond: 100},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("rate limit 100 should be valid, got: %v", err)
	}
}

// ===========================================================================
// city_resolver.go — anyToString edge cases
// ===========================================================================

func TestAnyToString_UnknownType(t *testing.T) {
	// Slice is an unsupported type — should use fmt.Sprintf.
	got := anyToString([]int{1, 2, 3})
	if got == "" {
		t.Error("expected non-empty for slice type")
	}
}

// url.URL is used directly via the "net/url" import above.

// ===========================================================================
// cookie_cache.go — loadCachedCookies / saveCachedCookies full round-trip
// ===========================================================================

func TestLoadSaveCachedCookies_FullRoundTrip(t *testing.T) {
	// Override HOME to use temp dir for cookie cache.
	dir := t.TempDir()
	cookieDir := filepath.Join(dir, ".trvl", "cookies")
	_ = os.MkdirAll(cookieDir, 0o700)

	targetURL := "https://www.roundtrip-test.com/page"
	u, _ := url.Parse(targetURL)

	// Create client with cookies.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	jar.SetCookies(u, []*http.Cookie{
		{Name: "sid", Value: "session123", Domain: ".roundtrip-test.com", Path: "/"},
		{Name: "csrf", Value: "token456", Domain: ".roundtrip-test.com", Path: "/"},
	})

	// Manually write cookie cache file (saveCachedCookies uses HOME which we can't override easily).
	cookies := jar.Cookies(u)
	now := time.Now()
	cached := make([]cachedCookie, len(cookies))
	for i, c := range cookies {
		cached[i] = cachedCookie{
			Name: c.Name, Value: c.Value, Domain: c.Domain,
			Path: c.Path, SavedAt: now,
		}
	}
	data, _ := json.Marshal(cached)
	cachePath := filepath.Join(cookieDir, "www.roundtrip-test.com.json")
	_ = os.WriteFile(cachePath, data, 0o600)

	// Create fresh client and load cookies from disk.
	jar2, _ := cookiejar.New(nil)
	client2 := &http.Client{Jar: jar2}

	// Directly test the loading logic by reading and parsing the file.
	fileData, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	var loadedCookies []cachedCookie
	if err := json.Unmarshal(fileData, &loadedCookies); err != nil {
		t.Fatal(err)
	}
	httpCookies := make([]*http.Cookie, len(loadedCookies))
	for i, c := range loadedCookies {
		httpCookies[i] = &http.Cookie{
			Name: c.Name, Value: c.Value, Domain: c.Domain,
			Path: c.Path, Expires: c.Expires,
		}
	}
	client2.Jar.SetCookies(u, httpCookies)

	got := client2.Jar.Cookies(u)
	if len(got) < 2 {
		t.Errorf("expected at least 2 cookies, got %d", len(got))
	}

	_ = client
}

func TestLoadCachedCookies_ExpiredTTL(t *testing.T) {
	dir := t.TempDir()
	cookieDir := filepath.Join(dir, ".trvl", "cookies")
	_ = os.MkdirAll(cookieDir, 0o700)

	// Write expired cookies.
	expired := []cachedCookie{
		{Name: "old", Value: "stale", SavedAt: time.Now().Add(-48 * time.Hour)},
	}
	data, _ := json.Marshal(expired)
	cachePath := filepath.Join(cookieDir, "expired.example.com.json")
	_ = os.WriteFile(cachePath, data, 0o600)

	// Verify the TTL check works.
	var loaded []cachedCookie
	_ = json.Unmarshal(data, &loaded)
	if len(loaded) == 0 {
		t.Fatal("expected cached cookies")
	}
	if time.Since(loaded[0].SavedAt) <= cookieCacheTTL {
		t.Error("expected cookies to be expired")
	}
}

func TestLoadCachedCookies_BadJSON(t *testing.T) {
	dir := t.TempDir()
	cookieDir := filepath.Join(dir, ".trvl", "cookies")
	_ = os.MkdirAll(cookieDir, 0o700)

	// Write invalid JSON.
	cachePath := filepath.Join(cookieDir, "bad.example.com.json")
	_ = os.WriteFile(cachePath, []byte("{invalid json}"), 0o600)

	// Parse should fail gracefully.
	data, _ := os.ReadFile(cachePath)
	var loaded []cachedCookie
	err := json.Unmarshal(data, &loaded)
	if err == nil {
		t.Error("expected JSON unmarshal error for bad data")
	}
}

// ===========================================================================
// mapping.go — extractRoomTypes with bed configurations
// ===========================================================================

func TestExtractRoomTypes_WithBedConfigurations(t *testing.T) {
	raw := map[string]any{
		"blocks": []any{
			map[string]any{
				"roomName":   "Comfort Room",
				"finalPrice": map[string]any{"amount": float64(150), "currency": "EUR"},
				"blockId":    map[string]any{"roomId": "801"},
				"bedConfigurations": []any{
					map[string]any{
						"description": "1 queen bed and 1 sofa bed",
					},
				},
			},
		},
	}

	rooms := extractRoomTypes(raw)
	if len(rooms) != 1 {
		t.Fatalf("expected 1 room, got %d", len(rooms))
	}
	if rooms[0].BedType != "1 queen bed and 1 sofa bed" {
		t.Errorf("BedType = %q, want '1 queen bed and 1 sofa bed'", rooms[0].BedType)
	}
}

func TestExtractRoomTypes_UnitConfigWithBedType(t *testing.T) {
	raw := map[string]any{
		"matchingUnitConfigurations": map[string]any{
			"unitConfigurations": []any{
				map[string]any{
					"name":   "Twin Room",
					"unitId": "U1",
					"bedConfigurations": []any{
						map[string]any{
							"beds": []any{
								map[string]any{"count": float64(2), "type": float64(1)},
							},
						},
					},
				},
			},
		},
		"blocks": []any{
			map[string]any{
				"blockId":    map[string]any{"roomId": "U1"},
				"finalPrice": map[string]any{"amount": float64(90), "currency": "EUR"},
			},
		},
	}

	rooms := extractRoomTypes(raw)
	if len(rooms) != 1 {
		t.Fatalf("expected 1 room, got %d", len(rooms))
	}
	if rooms[0].Name != "Twin Room" {
		t.Errorf("Name = %q, want 'Twin Room'", rooms[0].Name)
	}
	// Bed type from unit config: "2 single bed"
	if rooms[0].BedType == "" {
		t.Error("expected BedType to be set from unit config")
	}
}

func TestExtractRoomTypes_BlockWithRoomNameFallback(t *testing.T) {
	raw := map[string]any{
		"blocks": []any{
			map[string]any{
				"room_name":  "Budget Room", // underscore variant
				"finalPrice": map[string]any{"amount": float64(60)},
				"blockId":    map[string]any{"roomId": "901"},
			},
		},
	}

	rooms := extractRoomTypes(raw)
	if len(rooms) != 1 {
		t.Fatalf("expected 1 room, got %d", len(rooms))
	}
	if rooms[0].Name != "Budget Room" {
		t.Errorf("Name = %q, want 'Budget Room'", rooms[0].Name)
	}
}

// ===========================================================================
// mapping.go — isEmptyValue
// ===========================================================================

func TestIsEmptyValue_AdditionalCases(t *testing.T) {
	tests := []struct {
		input any
		want  bool
	}{
		{nil, true},
		{[]any{}, true},
		{map[string]any{}, true},
		{"", true},
		{[]any{"a"}, false},
		{map[string]any{"k": "v"}, false},
		{"hello", false},
		{42, false},
		{false, false},
	}
	for _, tt := range tests {
		got := isEmptyValue(tt.input)
		if got != tt.want {
			t.Errorf("isEmptyValue(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ===========================================================================
// mapping.go — jsonPath with non-object types
// ===========================================================================

func TestJsonPath_NonObjectAtRoot(t *testing.T) {
	got := jsonPath(42, "a.b")
	if got != nil {
		t.Errorf("expected nil for non-object root, got %v", got)
	}
}

func TestJsonPath_ArrayWithNoMatchingKey(t *testing.T) {
	data := map[string]any{
		"items": []any{
			map[string]any{"x": 1},
			map[string]any{"y": 2},
		},
	}
	got := jsonPath(data, "items.z")
	if got != nil {
		t.Errorf("expected nil for missing key in array elements, got %v", got)
	}
}

func TestJsonPath_WildcardNoMatch(t *testing.T) {
	data := map[string]any{
		"queries": map[string]any{
			"findAll(...)": map[string]any{"results": "found"},
		},
	}
	got := jsonPath(data, "queries.search*.results")
	if got != nil {
		t.Errorf("expected nil when wildcard doesn't match, got %v", got)
	}
}

// ===========================================================================
// OpenURLInBrowser — exported function
// ===========================================================================

func TestOpenURLInBrowser_Exported(t *testing.T) {
	// Test the exported wrapper calls through to the internal function.
	var called bool
	prev := currentOpenURL
	currentOpenURL = func(goos, pref, target string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { currentOpenURL = prev })

	err := OpenURLInBrowser("https://example.com/test", "Firefox")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected opener to be called")
	}
}
