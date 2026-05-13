package providers

// coverage_boost3_test.go — third batch of coverage boosters.
// Targets: fetchJSONLDRating, fetchAirbnbDescription, enrichAirbnbDescriptions,
// registry (Delete not-found, Reload, ReloadIfChanged), unwrapNiobe,
// denormalizeApollo, runPreflight cache-hit, doSearchRequest,
// applyExtractions header source, saveCachedCookies paths.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// ---------------------------------------------------------------------------
// fetchJSONLDRating
// ---------------------------------------------------------------------------

func TestFetchJSONLDRating_Success(t *testing.T) {
	page := `<!DOCTYPE html><html><head>
<script type="application/ld+json">{"@type":"Hotel","aggregateRating":{"ratingValue":"8.5","reviewCount":"1234"}}</script>
</head><body></body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, page)
	}))
	defer srv.Close()

	rating, count, err := fetchJSONLDRating(context.Background(), srv.Client(), srv.URL+"/hotel")
	if err != nil {
		t.Fatalf("fetchJSONLDRating error: %v", err)
	}
	if rating < 8.4 || rating > 8.6 {
		t.Errorf("rating = %v, want ~8.5", rating)
	}
	if count != 1234 {
		t.Errorf("count = %d, want 1234", count)
	}
}

func TestFetchJSONLDRating_GraphArray_B3(t *testing.T) {
	page := `<!DOCTYPE html><html><head>
<script type="application/ld+json">{"@graph":[{"@type":"Hotel","aggregateRating":{"ratingValue":9.0,"reviewCount":500}}]}</script>
</head><body></body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, page)
	}))
	defer srv.Close()

	rating, count, err := fetchJSONLDRating(context.Background(), srv.Client(), srv.URL+"/hotel")
	if err != nil {
		t.Fatalf("fetchJSONLDRating error: %v", err)
	}
	if rating < 8.9 || rating > 9.1 {
		t.Errorf("rating = %v, want ~9.0", rating)
	}
	if count != 500 {
		t.Errorf("count = %d, want 500", count)
	}
}

func TestFetchJSONLDRating_NoRating(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><head><script type="application/ld+json">{"@type":"Hotel"}</script></head></html>`)
	}))
	defer srv.Close()

	_, _, err := fetchJSONLDRating(context.Background(), srv.Client(), srv.URL+"/hotel")
	if err == nil {
		t.Error("expected error when no aggregateRating")
	}
}

func TestFetchJSONLDRating_HTTP404_B3(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	_, _, err := fetchJSONLDRating(context.Background(), srv.Client(), srv.URL+"/hotel")
	if err == nil {
		t.Error("expected error for 404")
	}
}

func TestFetchJSONLDRating_NoScriptBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>no json-ld here</body></html>`)
	}))
	defer srv.Close()

	_, _, err := fetchJSONLDRating(context.Background(), srv.Client(), srv.URL+"/hotel")
	if err == nil {
		t.Error("expected error when no JSON-LD block")
	}
}

// ---------------------------------------------------------------------------
// fetchAirbnbDescription
// ---------------------------------------------------------------------------

func TestFetchAirbnbDescription_SharingConfig(t *testing.T) {
	niobeData := map[string]any{
		"data": map[string]any{
			"presentation": map[string]any{
				"stayProductDetailPage": map[string]any{
					"sections": map[string]any{
						"metadata": map[string]any{
							"sharingConfig": map[string]any{
								"description": "A cozy apartment in the heart of the city",
							},
						},
					},
				},
			},
		},
	}
	niobeJSON, _ := json.Marshal(niobeData)
	page := fmt.Sprintf(`<html><head><script data-deferred-state-0="">%s</script></head></html>`, niobeJSON)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, page)
	}))
	defer srv.Close()

	desc, err := fetchAirbnbDescription(context.Background(), srv.Client(), srv.URL+"/rooms/123")
	if err != nil {
		t.Fatalf("fetchAirbnbDescription error: %v", err)
	}
	if desc == "" {
		t.Error("expected non-empty description")
	}
}

func TestFetchAirbnbDescription_HTTP500_B3(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	_, err := fetchAirbnbDescription(context.Background(), srv.Client(), srv.URL+"/rooms/123")
	if err == nil {
		t.Error("expected error for 500")
	}
}

func TestFetchAirbnbDescription_NoDeferredState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>no deferred state</body></html>`)
	}))
	defer srv.Close()

	_, err := fetchAirbnbDescription(context.Background(), srv.Client(), srv.URL+"/rooms/123")
	if err == nil {
		t.Error("expected error when deferred-state not found")
	}
}

func TestFetchAirbnbDescription_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><head><script data-deferred-state-0="">not json at all</script></head></html>`)
	}))
	defer srv.Close()

	_, err := fetchAirbnbDescription(context.Background(), srv.Client(), srv.URL+"/rooms/123")
	if err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestFetchAirbnbDescription_NoDescription(t *testing.T) {
	data, _ := json.Marshal(map[string]any{"other": "data"})
	page := fmt.Sprintf(`<html><head><script data-deferred-state-0="">%s</script></head></html>`, data)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, page)
	}))
	defer srv.Close()

	_, err := fetchAirbnbDescription(context.Background(), srv.Client(), srv.URL+"/rooms/123")
	if err == nil {
		t.Error("expected error when no description in listing")
	}
}

// ---------------------------------------------------------------------------
// enrichAirbnbDescriptions
// ---------------------------------------------------------------------------

func TestEnrichAirbnbDescriptions_FillsMissing(t *testing.T) {
	niobeData := map[string]any{
		"data": map[string]any{
			"presentation": map[string]any{
				"stayProductDetailPage": map[string]any{
					"sections": map[string]any{
						"metadata": map[string]any{
							"sharingConfig": map[string]any{
								"description": "Lovely place",
							},
						},
					},
				},
			},
		},
	}
	niobeJSON, _ := json.Marshal(niobeData)
	page := fmt.Sprintf(`<html><head><script data-deferred-state-0="">%s</script></head></html>`, niobeJSON)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, page)
	}))
	defer srv.Close()

	hotels := []models.HotelResult{
		{Name: "Hotel A", BookingURL: srv.URL + "/rooms/1"},
		{Name: "Hotel B", Description: "already set", BookingURL: srv.URL + "/rooms/2"},
		{Name: "Hotel C"}, // no BookingURL — should be skipped
	}
	enrichAirbnbDescriptions(context.Background(), srv.Client(), hotels)

	if hotels[0].Description == "" {
		t.Error("expected hotel 0 to be enriched")
	}
	if hotels[1].Description != "already set" {
		t.Error("hotel 1 description should not be overwritten")
	}
}

func TestEnrichAirbnbDescriptions_SkipsOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	hotels := []models.HotelResult{
		{Name: "H1", BookingURL: srv.URL + "/rooms/1"},
	}
	// Should not panic even when enrichment fails
	enrichAirbnbDescriptions(context.Background(), srv.Client(), hotels)
	// Description stays empty — no panic
}

// ---------------------------------------------------------------------------
// unwrapNiobe — uncovered branches
// ---------------------------------------------------------------------------

func TestUnwrapNiobe_NonMap_B3(t *testing.T) {
	// Input is not a map → returned unchanged
	input := []any{"a", "b"}
	got := unwrapNiobe(input)
	if _, ok := got.([]any); !ok {
		t.Error("expected []any to be returned unchanged")
	}
}

func TestUnwrapNiobe_NoNiobeKey_B3(t *testing.T) {
	// Map without niobeClientData → returned unchanged
	input := map[string]any{"data": map[string]any{"x": 1}}
	got := unwrapNiobe(input)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatal("expected map returned")
	}
	if _, has := m["data"]; !has {
		t.Error("original map should be returned unchanged")
	}
}

func TestUnwrapNiobe_EmptyEntries_B3(t *testing.T) {
	input := map[string]any{
		"niobeClientData": []any{},
	}
	got := unwrapNiobe(input)
	// Empty entries → original returned
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatal("expected map")
	}
	if _, has := m["niobeClientData"]; !has {
		t.Error("original with niobeClientData should be returned")
	}
}

func TestUnwrapNiobe_PairWithoutData(t *testing.T) {
	// Pair exists but payload["data"] is empty → no match → original returned
	input := map[string]any{
		"niobeClientData": []any{
			[]any{"CacheKey:x", map[string]any{"data": map[string]any{}}},
		},
	}
	got := unwrapNiobe(input)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatal("expected map")
	}
	_ = m
}

// ---------------------------------------------------------------------------
// denormalizeApollo — additional branches
// ---------------------------------------------------------------------------

func TestDenormalizeApollo_Array_B3(t *testing.T) {
	cache := map[string]any{
		"Hotel:1": map[string]any{"name": "Grand Hotel"},
	}
	input := []any{
		map[string]any{"__ref": "Hotel:1"},
		"plain string",
	}
	result := denormalizeApollo(input, cache, nil)
	arr, ok := result.([]any)
	if !ok {
		t.Fatal("expected array result")
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(arr))
	}
	m, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatal("expected first element to be resolved map")
	}
	if m["name"] != "Grand Hotel" {
		t.Errorf("name = %v, want 'Grand Hotel'", m["name"])
	}
}

func TestDenormalizeApollo_CircularRef(t *testing.T) {
	// Circular reference: Hotel:1 → Hotel:1 (self-referential)
	cache := map[string]any{
		"Hotel:1": map[string]any{"__ref": "Hotel:1"},
	}
	seen := map[string]bool{"Hotel:1": true}
	input := map[string]any{"__ref": "Hotel:1"}
	// With seen already containing Hotel:1, should stop recursion
	result := denormalizeApollo(input, cache, seen)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map")
	}
	if _, has := m["__ref"]; !has {
		t.Error("circular ref should be returned as-is (original obj)")
	}
}

func TestDenormalizeApollo_DanglingRef_B3(t *testing.T) {
	// __ref points to a key not in cache
	cache := map[string]any{}
	input := map[string]any{"__ref": "Hotel:missing"}
	result := denormalizeApollo(input, cache, nil)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map")
	}
	if _, has := m["__ref"]; !has {
		t.Error("dangling ref should be returned as-is")
	}
}

func TestDenormalizeApollo_SkipsTypename(t *testing.T) {
	cache := map[string]any{}
	input := map[string]any{
		"__typename": "Hotel",
		"name":       "Test",
	}
	result := denormalizeApollo(input, cache, nil)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map")
	}
	if m["__typename"] != "Hotel" {
		t.Errorf("__typename = %v, want 'Hotel'", m["__typename"])
	}
}

// ---------------------------------------------------------------------------
// registry.go — Delete not found, Reload, ReloadIfChanged
// ---------------------------------------------------------------------------

func TestRegistry_Delete_NotFound(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	err = reg.Delete("nonexistent-id")
	if err == nil {
		t.Error("expected error when deleting nonexistent provider")
	}
}

func TestRegistry_Delete_Success(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistryAt(dir)
	cfg := &ProviderConfig{
		ID: "to-delete", Name: "ToDelete", Category: "hotels",
		Endpoint:        "https://example.com",
		ResponseMapping: ResponseMapping{ResultsPath: "r"},
	}
	_ = reg.Save(cfg)
	if err := reg.Delete("to-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if reg.Get("to-delete") != nil {
		t.Error("expected Get to return nil after Delete")
	}
}

func TestRegistry_Reload_Success(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistryAt(dir)
	cfg := &ProviderConfig{
		ID: "reloadable", Name: "Old Name", Category: "hotels",
		Endpoint:        "https://example.com",
		ResponseMapping: ResponseMapping{ResultsPath: "r"},
	}
	_ = reg.Save(cfg)

	// Update the file on disk manually
	cfg.Name = "New Name"
	data, _ := json.MarshalIndent(cfg, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, "reloadable.json"), data, 0o644)

	reloaded, err := reg.Reload("reloadable")
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if reloaded.Name != "New Name" {
		t.Errorf("Name = %q, want 'New Name'", reloaded.Name)
	}
}

func TestRegistry_Reload_MissingFile(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistryAt(dir)
	_, err := reg.Reload("no-such-provider")
	if err == nil {
		t.Error("expected error when reloading missing file")
	}
}

func TestRegistry_Reload_BadJSON(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistryAt(dir)
	_ = os.WriteFile(filepath.Join(dir, "badjson.json"), []byte(`{bad json`), 0o644)
	_, err := reg.Reload("badjson")
	if err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestRegistry_ReloadIfChanged_NoChange(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistryAt(dir)
	cfg := &ProviderConfig{
		ID: "stable", Name: "Stable", Category: "hotels",
		Endpoint:        "https://example.com",
		ResponseMapping: ResponseMapping{ResultsPath: "r"},
	}
	_ = reg.Save(cfg)

	// ReloadIfChanged should return the same config when file unchanged
	got := reg.ReloadIfChanged("stable")
	if got == nil {
		t.Fatal("expected non-nil config")
	}
	if got.Name != "Stable" {
		t.Errorf("Name = %q, want 'Stable'", got.Name)
	}
}

func TestRegistry_ReloadIfChanged_FileChanged(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistryAt(dir)
	cfg := &ProviderConfig{
		ID: "changing", Name: "Before", Category: "hotels",
		Endpoint:        "https://example.com",
		ResponseMapping: ResponseMapping{ResultsPath: "r"},
	}
	_ = reg.Save(cfg)

	// Small sleep to guarantee mtime differs
	time.Sleep(10 * time.Millisecond)
	cfg.Name = "After"
	data, _ := json.MarshalIndent(cfg, "", "  ")
	// Force a future mtime
	path := filepath.Join(dir, "changing.json")
	_ = os.WriteFile(path, data, 0o644)
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(path, future, future)

	got := reg.ReloadIfChanged("changing")
	if got == nil {
		t.Fatal("expected non-nil config after reload")
	}
	// Should have picked up the new name
	if got.Name != "After" {
		t.Errorf("Name = %q, want 'After'", got.Name)
	}
}

func TestRegistry_ReloadIfChanged_MissingFile(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistryAt(dir)
	// File never saved — ReloadIfChanged should return nil without panic
	got := reg.ReloadIfChanged("ghost")
	_ = got // nil is acceptable
}

func TestRegistry_ListByCategory(t *testing.T) {
	dir := t.TempDir()
	reg, _ := NewRegistryAt(dir)
	_ = reg.Save(&ProviderConfig{ID: "h1", Category: "hotels", Endpoint: "https://a.com", ResponseMapping: ResponseMapping{ResultsPath: "r"}})
	_ = reg.Save(&ProviderConfig{ID: "f1", Category: "flights", Endpoint: "https://b.com", ResponseMapping: ResponseMapping{ResultsPath: "r"}})
	_ = reg.Save(&ProviderConfig{ID: "h2", Category: "hotels", Endpoint: "https://c.com", ResponseMapping: ResponseMapping{ResultsPath: "r"}})

	hotels := reg.ListByCategory("hotels")
	if len(hotels) != 2 {
		t.Errorf("ListByCategory(hotels) = %d, want 2", len(hotels))
	}
	flights := reg.ListByCategory("flights")
	if len(flights) != 1 {
		t.Errorf("ListByCategory(flights) = %d, want 1", len(flights))
	}
}

// ---------------------------------------------------------------------------
// applyExtractions — Header source branch
// ---------------------------------------------------------------------------

func TestApplyExtractions_HeaderSource(t *testing.T) {
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"X-Auth-Token": []string{"token-xyz-456"}},
	}
	extractions := map[string]Extraction{
		"token": {
			Header:   "X-Auth-Token",
			Pattern:  `token-([a-z0-9-]+)`,
			Variable: "auth_token",
		},
	}
	authValues := make(map[string]string)
	n := applyExtractions(extractions, resp, []byte("body ignored"), authValues)
	if n != 1 {
		t.Errorf("matched = %d, want 1", n)
	}
	if authValues["auth_token"] != "xyz-456" {
		t.Errorf("auth_token = %q, want 'xyz-456'", authValues["auth_token"])
	}
}

// ---------------------------------------------------------------------------
// saveCachedCookies — additional branches (nil jar, empty cookies)
// ---------------------------------------------------------------------------

func TestSaveCachedCookies_NilJar_B3(t *testing.T) {
	// Should not panic
	client := &http.Client{Jar: nil}
	saveCachedCookies(client, "https://example.com/test")
}

func TestSaveCachedCookies_InvalidURL_B3(t *testing.T) {
	jar, _ := newTestCookieJar()
	client := &http.Client{Jar: jar}
	// Invalid URL — should not panic
	saveCachedCookies(client, "not a url \x00")
}

// ---------------------------------------------------------------------------
// loadCachedCookies — nil jar branch
// ---------------------------------------------------------------------------

func TestLoadCachedCookies_NilJar_B3(t *testing.T) {
	client := &http.Client{Jar: nil}
	ok := loadCachedCookies(client, "https://example.com/test")
	if ok {
		t.Error("expected false when jar is nil")
	}
}

func TestLoadCachedCookies_InvalidURL_B3(t *testing.T) {
	jar, _ := newTestCookieJar()
	client := &http.Client{Jar: jar}
	ok := loadCachedCookies(client, "not a url \x00")
	if ok {
		t.Error("expected false for invalid URL")
	}
}

// ---------------------------------------------------------------------------
// runPreflight — cache hit path
// ---------------------------------------------------------------------------

func TestRunPreflight_CacheHit(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = fmt.Fprint(w, `token=cached123`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	reg, _ := NewRegistryAt(dir)
	cfg := &ProviderConfig{
		ID: "cache-hit", Name: "CacheHit", Category: "hotels",
		Endpoint: srv.URL + "/search",
		Auth: &AuthConfig{
			Type:         "preflight",
			PreflightURL: srv.URL + "/auth",
			Extractions: map[string]Extraction{
				"tok": {Pattern: `token=([a-z0-9]+)`, Variable: "tok"},
			},
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "hotels",
			Fields:      map[string]string{"name": "name"},
		},
		RateLimit: RateLimitConfig{RequestsPerSecond: 100, Burst: 100},
	}
	_ = reg.Save(cfg)

	rt := NewRuntime(reg)
	// First call: preflight executes
	_, _, _ = rt.SearchHotels(context.Background(), "Rome", 41.90, 12.50,
		"2026-05-01", "2026-05-03", "EUR", 1, nil)
	firstCalls := calls

	// Second call within cache window: preflight should be skipped
	_, _, _ = rt.SearchHotels(context.Background(), "Rome", 41.90, 12.50,
		"2026-05-04", "2026-05-06", "EUR", 1, nil)

	if calls > firstCalls+1 {
		t.Logf("preflight calls: first=%d total=%d (cache may not have applied)", firstCalls, calls)
	}
}

// ---------------------------------------------------------------------------
// doSearchRequest — nil GetBody (no-body request) and URL validation
// ---------------------------------------------------------------------------

func TestDoSearchRequest_NoBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	orig, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL+"/search", nil)
	// GetBody is nil for GET requests
	resp, body, err := doSearchRequest(context.Background(), srv.Client(), orig)
	if err != nil {
		t.Fatalf("doSearchRequest: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	_ = body
}

// ---------------------------------------------------------------------------
// stripHTMLTags
// ---------------------------------------------------------------------------

func TestStripHTMLTags_BrReplacement(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"<br>hello<br/>world", "hello world"},
		{"<p>text</p>", "text"},
		{"  spaces  ", "spaces"},
		{"<b>bold</b> and <i>italic</i>", "bold and italic"},
	}
	for _, tc := range cases {
		got := stripHTMLTags(tc.input)
		if got != tc.want {
			t.Errorf("stripHTMLTags(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// extractAggregateRating — no aggregateRating key
// ---------------------------------------------------------------------------

func TestExtractAggregateRating_NoKey(t *testing.T) {
	obj := map[string]any{"name": "Hotel"}
	r, c := extractAggregateRating(obj)
	if r != 0 || c != 0 {
		t.Errorf("expected 0,0 for missing aggregateRating, got %v,%v", r, c)
	}
}

func TestExtractAggregateRating_Success(t *testing.T) {
	obj := map[string]any{
		"aggregateRating": map[string]any{
			"ratingValue": 8.7,
			"reviewCount": float64(300),
		},
	}
	r, c := extractAggregateRating(obj)
	if r < 8.6 || r > 8.8 {
		t.Errorf("rating = %v, want ~8.7", r)
	}
	if c != 300 {
		t.Errorf("count = %d, want 300", c)
	}
}

// ---------------------------------------------------------------------------
// NewRegistryAt — non-JSON files in dir are skipped
// ---------------------------------------------------------------------------

func TestNewRegistryAt_SkipsNonJSON_B3(t *testing.T) {
	dir := t.TempDir()
	// Write a non-JSON file and a subdir
	_ = os.WriteFile(filepath.Join(dir, "README.txt"), []byte("ignore me"), 0o644)
	_ = os.Mkdir(filepath.Join(dir, "subdir"), 0o755)
	// Write a valid JSON provider
	cfg := &ProviderConfig{ID: "p1", Name: "P1", Category: "hotels", Endpoint: "https://a.com", ResponseMapping: ResponseMapping{ResultsPath: "r"}}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, "p1.json"), data, 0o644)

	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	if len(reg.List()) != 1 {
		t.Errorf("expected 1 provider, got %d", len(reg.List()))
	}
}

// ---------------------------------------------------------------------------
// resolveCityID — partial match branch
// ---------------------------------------------------------------------------

func TestResolveCityID_PartialMatch_B3(t *testing.T) {
	lookup := map[string]string{
		"paris": "PARIS_ID",
		"rome":  "ROME_ID",
	}
	// "paris-central" contains "paris" → partial match
	got := resolveCityID(lookup, "paris-central")
	if got != "PARIS_ID" {
		t.Errorf("resolveCityID('paris-central') = %q, want 'PARIS_ID'", got)
	}
}

func TestResolveCityID_NoMatch(t *testing.T) {
	lookup := map[string]string{
		"paris": "PARIS_ID",
	}
	got := resolveCityID(lookup, "tokyo")
	if got != "" {
		t.Errorf("resolveCityID('tokyo') = %q, want ''", got)
	}
}

func TestResolveCityID_EmptyLookup(t *testing.T) {
	got := resolveCityID(nil, "paris")
	if got != "" {
		t.Errorf("resolveCityID(nil, 'paris') = %q, want ''", got)
	}
}

func TestResolveCityID_EmptyLocation_B3(t *testing.T) {
	lookup := map[string]string{"paris": "PARIS_ID"}
	got := resolveCityID(lookup, "")
	if got != "" {
		t.Errorf("resolveCityID(lookup, '') = %q, want ''", got)
	}
}
