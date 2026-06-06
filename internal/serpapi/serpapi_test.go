package serpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchHotels_HTTP200(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "test_key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "test_key" {
			t.Errorf("api_key = %q, want test_key", r.URL.Query().Get("api_key"))
		}
		if r.URL.Query().Get("engine") != "google_hotels" {
			t.Errorf("engine = %q, want google_hotels", r.URL.Query().Get("engine"))
		}
		resp := Response{
			SearchMetadata: struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}{Status: "Success"},
			Properties: []Hotel{{
				Name: "Test Hotel",
				RatePerNight: struct {
					Lowest     string  `json:"lowest"`
					Extracted  float64 `json:"extracted_lowest"`
					BeforeFees float64 `json:"extracted_before_taxes_fees,omitempty"`
				}{Extracted: 99, Lowest: "$99"},
				TotalRate: struct {
					Lowest     string  `json:"lowest"`
					Extracted  float64 `json:"extracted_lowest"`
					BeforeFees float64 `json:"extracted_before_taxes_fees,omitempty"`
				}{Extracted: 693, Lowest: "$693"},
			}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	// Override URL temporarily
	origSearch := searchURL
	searchURL = srv.URL + "/search"
	defer func() { searchURL = origSearch }()

	result, err := SearchHotels(context.Background(), "Test", "2026-01-01", "2026-01-08", "EUR")
	if err != nil {
		t.Fatalf("SearchHotels failed: %v", err)
	}
	if len(result.Properties) != 1 {
		t.Fatalf("expected 1 property, got %d", len(result.Properties))
	}
	h := result.Properties[0]
	if h.Name != "Test Hotel" {
		t.Errorf("Name = %q, want Test Hotel", h.Name)
	}
	if h.PricePerNight() != 99 {
		t.Errorf("PricePerNight = %.0f, want 99", h.PricePerNight())
	}
	if h.TotalPrice() != 693 {
		t.Errorf("TotalPrice = %.0f, want 693", h.TotalPrice())
	}
}

func TestSearchHotels_HTTPError(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "test_key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	origSearch := searchURL
	searchURL = srv.URL + "/search"
	defer func() { searchURL = origSearch }()

	_, err := SearchHotels(context.Background(), "Test", "2026-01-01", "2026-01-02", "EUR")
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestSearchHotels_MissingKey(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "")
	_, err := SearchHotels(context.Background(), "Test", "2026-01-01", "2026-01-02", "EUR")
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestSearchHotels_ErrorStatus(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "test_key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := Response{
			SearchMetadata: struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}{Status: "Error"},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	origSearch := searchURL
	searchURL = srv.URL + "/search"
	defer func() { searchURL = origSearch }()

	_, err := SearchHotels(context.Background(), "Test", "2026-01-01", "2026-01-02", "EUR")
	if err == nil {
		t.Fatal("expected error for Error status")
	}
}
