package ground

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// TestSearchByName_PartialFailureEnvelope proves gap1: when a ground provider
// fails, its outcome is recorded in ProviderStatuses + Completeness instead of
// being silently dropped. Before the fix a provider error left no trace unless
// EVERY provider produced zero routes (the legacy Error-only-on-total-wipeout
// behaviour), so "no routes found" could not be distinguished from "the only
// provider we asked timed out".
func TestSearchByName_PartialFailureEnvelope(t *testing.T) {
	origClient := httpClient
	origLimiter := flixbusLimiter
	t.Cleanup(func() {
		httpClient = origClient
		flixbusLimiter = origLimiter
	})
	flixbusLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	// Every upstream call fails hard.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	httpClient = &http.Client{
		Transport: &urlRewriter{base: srv.URL},
		Timeout:   5 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Isolate to flixbus so the assertion is deterministic and offline.
	result, err := SearchByName(ctx, "city-berlin", "city-munich", "2026-07-01", SearchOptions{
		Currency:  "EUR",
		Providers: []string{"flixbus"},
		NoCache:   true,
	})
	if err != nil {
		t.Fatalf("SearchByName returned transport error: %v", err)
	}

	// The failure must be visible per-provider, not dropped.
	if len(result.ProviderStatuses) != 1 {
		t.Fatalf("ProviderStatuses = %#v, want exactly 1 (flixbus)", result.ProviderStatuses)
	}
	ps := result.ProviderStatuses[0]
	if ps.ID != "flixbus" {
		t.Errorf("status ID = %q, want flixbus", ps.ID)
	}
	if ps.Status == models.StatusOK {
		t.Errorf("status = %q, want a non-ok failure classification", ps.Status)
	}
	if ps.Error == "" {
		t.Error("failed provider status carries no error message")
	}

	// With the only queried provider down, absence is unproven: callers must NOT
	// be allowed to claim an exhaustive "no routes found".
	if result.Completeness.MayClaimExhaustive() {
		t.Errorf("Completeness=%+v claims exhaustive after a total provider failure", result.Completeness)
	}
	if result.Completeness.State != models.CompletenessBlocked {
		t.Errorf("Completeness.State = %q, want %q", result.Completeness.State, models.CompletenessBlocked)
	}
	if result.Success {
		t.Error("Success = true despite zero routes and a failed provider")
	}
}
