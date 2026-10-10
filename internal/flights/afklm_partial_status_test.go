package flights

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/flights/afklm"
)

// MIK-8088: credentials present but the provider cannot be set up -- the user
// expects AF-KLM fares, so the reason is reported instead of a silent skip.
func TestSearchAFKLMNativeRoundTrip_SetupFailureIsReported(t *testing.T) {
	_, statuses := searchAFKLMNativeRoundTrip(context.Background(), "AMS", "PRG", "2026-08-01", "2026-08-08", SearchOptions{
		afklmNewProvider: func() (*afklm.AFKLMProvider, error) { return nil, errors.New("cache dir not writable") },
	})
	if len(statuses) != 1 || statuses[0].Error != "setup: cache dir not writable" || statuses[0].Status == "" {
		t.Fatalf("statuses = %+v, want one AFKLM status carrying the setup error", statuses)
	}
	c := models.ComputeCompleteness(statuses)
	if c.Queried != 1 || c.Succeeded != 0 {
		t.Errorf("completeness = %+v, want the failed setup counted as an attempt that did not answer", c)
	}
}

// The composer is not a provider: with no upstream answer it is skipped, so an
// all-failed round trip reads as blocked and the composer is not listed as a
// failure. With an upstream answer it keeps its definitive status.
func TestRoundTripComposerStatus_NoUpstreamAnswerIsSkipped(t *testing.T) {
	legs := []models.ProviderStatus{
		{ID: "outbound:google_flights", Name: "outbound Google Flights", Status: models.StatusTimeout, Error: "deadline exceeded"},
		{ID: "inbound:google_flights", Name: "inbound Google Flights", Status: models.StatusTimeout, Error: "deadline exceeded"},
	}
	answered := models.ComputeCompleteness(legs).Succeeded > 0
	composer := roundTripComposerStatus(0, 0, 0, false, answered)
	if composer.Status != models.StatusSkipped {
		t.Fatalf("composer = %+v, want skipped when no upstream provider answered", composer)
	}
	all := append(legs, composer)
	if c := models.ComputeCompleteness(all); c.State != models.CompletenessBlocked {
		t.Errorf("completeness = %+v, want blocked when every upstream provider failed", c)
	}
	if lines := models.ProviderFailureLines(all); lines == "" || containsComposer(lines) {
		t.Errorf("failure lines = %q, want the legs listed and the composer omitted", lines)
	}

	ok := []models.ProviderStatus{{ID: "outbound:google_flights", Status: models.StatusOK, Results: 3}}
	if got := roundTripComposerStatus(3, 0, 0, false, true); got.Status != models.StatusCheckedNoHit {
		t.Errorf("composer with an upstream answer = %+v, want checked_no_hit", got)
	}
	quota := []models.ProviderStatus{ok[0], {ID: "native_roundtrip:afklm", Status: models.StatusRateLimited, Error: "daily request budget exhausted"}}
	if c := models.ComputeCompleteness(quota); c.State != models.CompletenessPartial {
		t.Errorf("completeness = %+v, want partial when only AFKLM's quota ran out", c)
	}
}

func containsComposer(s string) bool { return strings.Contains(s, "Round-trip composer") }
