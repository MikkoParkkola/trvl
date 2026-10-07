package flights

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestSearchGoogleFlights_Code13OneUpstreamCall(t *testing.T) {
	const body = ")]}'\n\n[[\"wrb.fr\",null,null,null,null,[13]],[\"di\",34]]"
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	opts := SearchOptions{Adults: 1, Currency: "EUR"}
	_, err := searchGoogleFlightsWithClient(context.Background(), client, "AMS", "HEL", "2026-10-09", opts)
	if !errors.Is(err, models.ErrRateLimited) {
		t.Fatalf("first err = %v, want rate limited", err)
	}
	if !strings.Contains(err.Error(), "quota refusal (code 13)") {
		t.Fatalf("first err = %q, want the wrb code", err)
	}

	_, err = searchGoogleFlightsWithClient(context.Background(), client, "AMS", "HEL", "2026-10-10", opts)
	if !errors.Is(err, models.ErrRateLimited) || !strings.Contains(err.Error(), "cooling down") {
		t.Fatalf("second err = %v, want cooling down", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", hits.Load())
	}
}

func TestSearchGoogleFlights_RateLimitEnvelopeArmsCooldown(t *testing.T) {
	body, err := os.ReadFile("testdata/google_flights_error_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	opts := SearchOptions{Adults: 1, Currency: "EUR"}
	_, err = searchGoogleFlightsWithClient(context.Background(), client, "AMS", "HEL", "2026-10-09", opts)
	if !errors.Is(err, models.ErrRateLimited) {
		t.Fatalf("first err = %v", err)
	}
	if !strings.Contains(err.Error(), "non-flight error/challenge payload") {
		t.Fatalf("envelope err = %q", err)
	}
	_, err = searchGoogleFlightsWithClient(context.Background(), client, "HEL", "AMS", "2026-10-16", opts)
	if !strings.Contains(err.Error(), "cooling down") {
		t.Fatalf("second err = %v, want cooling down", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", hits.Load())
	}
}
