package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestEnrichAirbnbDescriptions_MaxThreeEnrichments(t *testing.T) {
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
											"description": "Test desc",
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

	// Create 5 undescribed hotels — only 3 should be enriched.
	hotels := make([]models.HotelResult, 5)
	for i := range hotels {
		hotels[i] = models.HotelResult{
			Name:       fmt.Sprintf("Apt %d", i),
			BookingURL: srv.URL + fmt.Sprintf("/rooms/%d", i),
		}
	}

	enrichAirbnbDescriptions(context.Background(), srv.Client(), hotels)

	enrichedCount := 0
	for _, h := range hotels {
		if h.Description != "" {
			enrichedCount++
		}
	}
	if enrichedCount != 3 {
		t.Errorf("expected exactly 3 enrichments (max), got %d", enrichedCount)
	}
}

// ===========================================================================
// fetchAirbnbDescription — edge cases
// ===========================================================================

func TestFetchAirbnbDescription_NoNiobeScript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>No deferred state</body></html>`)
	}))
	defer srv.Close()

	_, err := fetchAirbnbDescription(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error when no data-deferred-state-0 script")
	}
}

func TestFetchAirbnbDescription_HTTP500_Max(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	_, err := fetchAirbnbDescription(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error on HTTP 500")
	}
}

func TestFetchAirbnbDescription_SubtitleFallback(t *testing.T) {
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
											"sectionComponentType": "DESCRIPTION_DEFAULT",
											"section": map[string]any{
												"subtitle": "A cozy place to stay",
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

	desc, err := fetchAirbnbDescription(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc != "A cozy place to stay" {
		t.Errorf("desc = %q, want 'A cozy place to stay'", desc)
	}
}

func TestFetchAirbnbDescription_TitleFallback(t *testing.T) {
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
											"sectionComponentType": "DESCRIPTION_DEFAULT",
											"section": map[string]any{
												"title": "About this space",
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

	desc, err := fetchAirbnbDescription(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc != "About this space" {
		t.Errorf("desc = %q, want 'About this space'", desc)
	}
}

// ===========================================================================
// stripHTMLTags — edge cases
// ===========================================================================

func TestStripHTMLTags_SelfClosingBr(t *testing.T) {
	got := stripHTMLTags("Hello<br/>World")
	if got != "Hello World" {
		t.Errorf("got %q, want 'Hello World'", got)
	}
}

func TestStripHTMLTags_MultipleTags(t *testing.T) {
	got := stripHTMLTags("<p>Hello</p><br><span>World</span>")
	if got != "Hello World" {
		t.Errorf("got %q, want 'Hello World'", got)
	}
}

func TestStripHTMLTags_NoTags(t *testing.T) {
	got := stripHTMLTags("Just plain text")
	if got != "Just plain text" {
		t.Errorf("got %q, want 'Just plain text'", got)
	}
}

func TestStripHTMLTags_Empty(t *testing.T) {
	got := stripHTMLTags("")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

// ===========================================================================
// defaultOpenURL — unsupported OS
// ===========================================================================

func TestDefaultOpenURL_UnsupportedOS(t *testing.T) {
	err := defaultOpenURL("freebsd", "", "https://example.com")
	if err == nil {
		t.Fatal("expected error for unsupported OS")
	}
	if !strings.Contains(err.Error(), "unsupported OS") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ===========================================================================
// resolveCityExtraFields via httptest
// ===========================================================================

func TestResolveCityExtraFields_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []any{
				map[string]any{
					"dest_id":   "-2140479",
					"dest_type": "city",
					"city_name": "Helsinki",
				},
			},
		})
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID: "test",
		CityResolver: &CityResolverConfig{
			URL:        srv.URL + "/autocomplete?text=${location}",
			ResultPath: "results",
			IDField:    "dest_id",
			ExtraFields: map[string]string{
				"dest_type": "dest_type",
			},
		},
	}

	extras, err := resolveCityExtraFields(context.Background(), cfg, srv.Client(), "Helsinki")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if extras["dest_type"] != "city" {
		t.Errorf("dest_type = %q, want 'city'", extras["dest_type"])
	}
}

func TestResolveCityExtraFields_NilResolver(t *testing.T) {
	cfg := &ProviderConfig{ID: "test"}
	extras, err := resolveCityExtraFields(context.Background(), cfg, http.DefaultClient, "Helsinki")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if extras != nil {
		t.Errorf("expected nil extras for nil resolver, got %v", extras)
	}
}

func TestResolveCityExtraFields_NoExtraFields(t *testing.T) {
	cfg := &ProviderConfig{
		ID:           "test",
		CityResolver: &CityResolverConfig{},
	}
	extras, err := resolveCityExtraFields(context.Background(), cfg, http.DefaultClient, "Helsinki")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if extras != nil {
		t.Errorf("expected nil extras for empty extra_fields, got %v", extras)
	}
}

func TestResolveCityExtraFields_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID: "test",
		CityResolver: &CityResolverConfig{
			URL:        srv.URL + "/autocomplete?text=${location}",
			ResultPath: "results",
			IDField:    "dest_id",
			ExtraFields: map[string]string{
				"dest_type": "dest_type",
			},
		},
	}

	_, err := resolveCityExtraFields(context.Background(), cfg, srv.Client(), "Helsinki")
	if err == nil {
		t.Fatal("expected error on HTTP 500")
	}
}

// ===========================================================================
// resolveCityIDDynamic via httptest
// ===========================================================================

func TestResolveCityIDDynamic_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []any{
				map[string]any{
					"dest_id":   float64(12345),
					"city_name": "Prague",
				},
			},
		})
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID: "test-resolver",
		CityResolver: &CityResolverConfig{
			URL:        srv.URL + "/autocomplete?text=${location}",
			ResultPath: "results",
			IDField:    "dest_id",
			NameField:  "city_name",
		},
	}

	cityID, err := resolveCityIDDynamic(context.Background(), cfg, srv.Client(), "Prague", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cityID != "12345" {
		t.Errorf("cityID = %q, want '12345'", cityID)
	}

	// Check it was cached.
	if cfg.CityLookup["prague"] != "12345" {
		t.Errorf("CityLookup[prague] = %q, want '12345'", cfg.CityLookup["prague"])
	}
}

func TestResolveCityIDDynamic_NilResolver(t *testing.T) {
	cfg := &ProviderConfig{ID: "test"}
	_, err := resolveCityIDDynamic(context.Background(), cfg, http.DefaultClient, "Prague", nil)
	if err == nil {
		t.Fatal("expected error for nil resolver")
	}
}

func TestResolveCityIDDynamic_EmptyURL(t *testing.T) {
	cfg := &ProviderConfig{
		ID:           "test",
		CityResolver: &CityResolverConfig{},
	}
	_, err := resolveCityIDDynamic(context.Background(), cfg, http.DefaultClient, "Prague", nil)
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestResolveCityIDDynamic_EmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []any{},
		})
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID: "test",
		CityResolver: &CityResolverConfig{
			URL:        srv.URL + "/autocomplete?text=${location}",
			ResultPath: "results",
			IDField:    "dest_id",
		},
	}

	_, err := resolveCityIDDynamic(context.Background(), cfg, srv.Client(), "NoCity", nil)
	if err == nil {
		t.Fatal("expected error for empty results")
	}
}

// ===========================================================================
// cookieDomainMatchesHost — additional cases
// ===========================================================================

func TestCookieDomainMatchesHost_CaseInsensitive(t *testing.T) {
	if !cookieDomainMatchesHost("BOOKING.COM", "booking.com") {
		t.Error("should match case-insensitively")
	}
	if !cookieDomainMatchesHost(".Booking.Com", "www.booking.com") {
		t.Error("should match case-insensitively with dot prefix")
	}
}

func TestCookieDomainMatchesHost_BothEmpty(t *testing.T) {
	if cookieDomainMatchesHost("", "") {
		t.Error("both empty should not match")
	}
}

// ===========================================================================
// isTestBinary — exercises the function
// ===========================================================================

func TestIsTestBinary_ReturnsTrue(t *testing.T) {
	// When running under go test, this should return true.
	if !isTestBinary() {
		t.Error("expected isTestBinary() to return true during tests")
	}
}

// ===========================================================================
// applyURLExtractions — invalid regex in URL extraction
// ===========================================================================

func TestApplyURLExtractions_InvalidRegex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `some content`)
	}))
	defer srv.Close()

	extractions := map[string]Extraction{
		"bad": {
			Pattern:  `[invalid(regex`,
			Variable: "bad_var",
			URL:      srv.URL + "/bundle.js",
		},
	}
	authValues := make(map[string]string)

	matched := applyURLExtractions(context.Background(), srv.Client(), extractions, authValues)
	if matched != 0 {
		t.Errorf("matched = %d, want 0 for invalid regex", matched)
	}
}

// ===========================================================================
// applyURLExtractions — no match no default
// ===========================================================================

func TestApplyURLExtractions_NoMatchNoDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `content without the pattern`)
	}))
	defer srv.Close()

	extractions := map[string]Extraction{
		"hash": {
			Pattern:  `sha256:([a-f0-9]{64})`,
			Variable: "sha",
			URL:      srv.URL + "/bundle.js",
			// No Default
		},
	}
	authValues := make(map[string]string)

	matched := applyURLExtractions(context.Background(), srv.Client(), extractions, authValues)
	if matched != 0 {
		t.Errorf("matched = %d, want 0 for no match with no default", matched)
	}
}

// ===========================================================================
// applyExtractions — variable fallback with default
// ===========================================================================

func TestApplyExtractions_DefaultFallbackToName(t *testing.T) {
	body := []byte(`no match here`)
	resp := &http.Response{Header: make(http.Header)}
	extractions := map[string]Extraction{
		"my_var": {
			Pattern: `never_matches`,
			Default: "fallback_value",
			// Variable deliberately empty — should use map key "my_var"
		},
	}
	authValues := make(map[string]string)

	matched := applyExtractions(extractions, resp, body, authValues)
	if matched != 1 {
		t.Errorf("matched = %d, want 1", matched)
	}
	if authValues["my_var"] != "fallback_value" {
		t.Errorf("my_var = %q, want 'fallback_value'", authValues["my_var"])
	}
}

// ===========================================================================
// anyToString — edge cases
// ===========================================================================
