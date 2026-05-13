package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveCityIDDynamic(t *testing.T) {
	// Fake autocomplete endpoint returning Booking-style JSON.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("text")
		var resp any
		switch query {
		case "Tokyo":
			resp = map[string]any{
				"results": []any{
					map[string]any{
						"dest_id":   "-246227",
						"dest_type": "city",
						"city_name": "Tokyo",
					},
				},
			}
		case "Empty":
			resp = map[string]any{
				"results": []any{},
			}
		default:
			resp = map[string]any{
				"results": []any{
					map[string]any{
						"dest_id":   "-999",
						"dest_type": "city",
						"city_name": query,
					},
				},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-booking",
		Name:     "Test Booking",
		Category: "hotels",
		CityResolver: &CityResolverConfig{
			URL:        srv.URL + "?text=${location}&limit=1",
			Method:     "GET",
			ResultPath: "results",
			IDField:    "dest_id",
			NameField:  "city_name",
		},
	}

	ctx := context.Background()
	client := srv.Client()

	t.Run("resolves unknown city", func(t *testing.T) {
		id, err := resolveCityIDDynamic(ctx, cfg, client, "Tokyo", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "-246227" {
			t.Errorf("got %q, want %q", id, "-246227")
		}
		// Check that the result was cached in CityLookup.
		if cfg.CityLookup == nil {
			t.Fatal("CityLookup was not initialized")
		}
		if cached := cfg.CityLookup["tokyo"]; cached != "-246227" {
			t.Errorf("cached value: got %q, want %q", cached, "-246227")
		}
	})

	t.Run("empty results returns error", func(t *testing.T) {
		emptyCfg := &ProviderConfig{
			ID:       "test-empty",
			Name:     "Test Empty",
			Category: "hotels",
			CityResolver: &CityResolverConfig{
				URL:        srv.URL + "?text=${location}&limit=1",
				ResultPath: "results",
				IDField:    "dest_id",
			},
		}
		_, err := resolveCityIDDynamic(ctx, emptyCfg, client, "Empty", nil)
		if err == nil {
			t.Error("expected error for empty results, got nil")
		}
	})

	t.Run("no resolver configured", func(t *testing.T) {
		noResolverCfg := &ProviderConfig{ID: "test-none", Name: "Test", Category: "hotels"}
		_, err := resolveCityIDDynamic(ctx, noResolverCfg, client, "Tokyo", nil)
		if err == nil {
			t.Error("expected error when no resolver configured")
		}
	})

	t.Run("caches name field too", func(t *testing.T) {
		freshCfg := &ProviderConfig{
			ID:       "test-name-cache",
			Name:     "Test Name Cache",
			Category: "hotels",
			CityResolver: &CityResolverConfig{
				URL:        srv.URL + "?text=${location}&limit=1",
				ResultPath: "results",
				IDField:    "dest_id",
				NameField:  "city_name",
			},
		}
		id, err := resolveCityIDDynamic(ctx, freshCfg, client, "Berlin", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "-999" {
			t.Errorf("got %q, want %q", id, "-999")
		}
		if freshCfg.CityLookup["berlin"] != "-999" {
			t.Errorf("cached by query: got %q", freshCfg.CityLookup["berlin"])
		}
	})

	t.Run("persists to registry", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Write a minimal valid config file.
		persistCfg := &ProviderConfig{
			ID:       "persist-test",
			Name:     "Persist Test",
			Category: "hotels",
			Endpoint: "https://example.com/search",
			ResponseMapping: ResponseMapping{
				ResultsPath: "results",
				Fields:      map[string]string{"name": "name"},
			},
			CityResolver: &CityResolverConfig{
				URL:        srv.URL + "?text=${location}&limit=1",
				ResultPath: "results",
				IDField:    "dest_id",
				NameField:  "city_name",
			},
		}
		data, _ := json.MarshalIndent(persistCfg, "", "  ")
		_ = os.WriteFile(filepath.Join(tmpDir, "persist-test.json"), data, 0o644)

		reg, err := NewRegistryAt(tmpDir)
		if err != nil {
			t.Fatalf("NewRegistryAt: %v", err)
		}

		// Use the registry's copy of the config.
		regCfg := reg.Get("persist-test")
		if regCfg == nil {
			t.Fatal("config not loaded by registry")
		}

		id, err := resolveCityIDDynamic(ctx, regCfg, client, "Osaka", reg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "-999" {
			t.Errorf("got %q, want %q", id, "-999")
		}

		// Verify the file was updated on disk.
		diskData, err := os.ReadFile(filepath.Join(tmpDir, "persist-test.json"))
		if err != nil {
			t.Fatalf("read persisted file: %v", err)
		}
		var diskCfg ProviderConfig
		_ = json.Unmarshal(diskData, &diskCfg)
		if diskCfg.CityLookup["osaka"] != "-999" {
			t.Errorf("disk cache: got %q, want %q", diskCfg.CityLookup["osaka"], "-999")
		}
	})
}

func TestResolveCityExtraFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"results": []any{
				map[string]any{
					"dest_id":   "-246227",
					"dest_type": "city",
					"city_name": "Tokyo",
					"country":   "Japan",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-extras",
		Name:     "Test Extras",
		Category: "hotels",
		CityResolver: &CityResolverConfig{
			URL:        srv.URL + "?text=${location}",
			ResultPath: "results",
			IDField:    "dest_id",
			ExtraFields: map[string]string{
				"dest_type": "dest_type",
				"country":   "country",
			},
		},
	}

	extras, err := resolveCityExtraFields(context.Background(), cfg, srv.Client(), "Tokyo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if extras["dest_type"] != "city" {
		t.Errorf("dest_type: got %q, want %q", extras["dest_type"], "city")
	}
	if extras["country"] != "Japan" {
		t.Errorf("country: got %q, want %q", extras["country"], "Japan")
	}
}

func TestAnyToString(t *testing.T) {
	tests := []struct {
		input any
		want  string
	}{
		{"hello", "hello"},
		{float64(42), "42"},
		{float64(-2140479), "-2140479"},
		{float64(3.14), "3.14"},
		{json.Number("12345"), "12345"},
		{true, "true"},
		{nil, ""},
	}
	for _, tt := range tests {
		got := anyToString(tt.input)
		if got != tt.want {
			t.Errorf("anyToString(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestResolveCityIDDynamic_HostelworldStyle(t *testing.T) {
	// Hostelworld-style response: nested under suggestions.cities.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"suggestions": map[string]any{
				"cities": []any{
					map[string]any{
						"id":   float64(612),
						"name": "Helsinki",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-hostelworld",
		Name:     "Test Hostelworld",
		Category: "hotels",
		CityResolver: &CityResolverConfig{
			URL:        srv.URL + "?query=${location}",
			ResultPath: "suggestions.cities",
			IDField:    "id",
			NameField:  "name",
		},
	}

	id, err := resolveCityIDDynamic(context.Background(), cfg, srv.Client(), "Helsinki", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "612" {
		t.Errorf("got %q, want %q", id, "612")
	}
}

func TestResolveCityIDDynamic_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-500",
		Name:     "Test 500",
		Category: "hotels",
		CityResolver: &CityResolverConfig{
			URL:        srv.URL + "?text=${location}",
			ResultPath: "results",
			IDField:    "dest_id",
		},
	}

	_, err := resolveCityIDDynamic(context.Background(), cfg, srv.Client(), "Paris", nil)
	if err == nil {
		t.Error("expected error for HTTP 500, got nil")
	}
}

func TestSearchProviderWithCityResolver(t *testing.T) {
	// End-to-end test: searchProvider should call the resolver when the city
	// is not in the static lookup, and the resolved ID should appear in the
	// URL sent to the provider endpoint.

	var receivedURL string
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []any{
				map[string]any{
					"name":  "Test Hotel",
					"price": 100,
				},
			},
		})
	}))
	defer providerSrv.Close()

	resolverSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []any{
				map[string]any{
					"dest_id":   "-999888",
					"city_name": "Kyoto",
				},
			},
		})
	}))
	defer resolverSrv.Close()

	tmpDir := t.TempDir()
	cfg := &ProviderConfig{
		ID:       "resolver-e2e",
		Name:     "Resolver E2E",
		Category: "hotels",
		Endpoint: providerSrv.URL + "/search?city_id=${city_id}&checkin=${checkin}",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name", "price": "price"},
		},
		CityResolver: &CityResolverConfig{
			URL:        resolverSrv.URL + "?text=${location}",
			ResultPath: "results",
			IDField:    "dest_id",
			NameField:  "city_name",
		},
	}

	data, _ := json.MarshalIndent(cfg, "", "  ")
	_ = os.WriteFile(filepath.Join(tmpDir, "resolver-e2e.json"), data, 0o644)

	reg, err := NewRegistryAt(tmpDir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	rt := NewRuntime(reg)
	regCfg := reg.Get("resolver-e2e")

	hotels, err := rt.searchProvider(context.Background(), regCfg, "Kyoto", 35.0, 135.7, "2026-06-01", "2026-06-03", "EUR", 2, nil)
	if err != nil {
		t.Fatalf("searchProvider: %v", err)
	}
	if len(hotels) == 0 {
		t.Fatal("expected at least one hotel result")
	}

	// The resolved city_id should appear in the URL.
	if receivedURL == "" {
		t.Fatal("provider endpoint was not called")
	}
	if !contains(receivedURL, "city_id=-999888") {
		t.Errorf("resolved city_id not in URL: %s", receivedURL)
	}

	// Verify the ID was cached.
	if regCfg.CityLookup["kyoto"] != "-999888" {
		t.Errorf("city not cached: %v", regCfg.CityLookup)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
