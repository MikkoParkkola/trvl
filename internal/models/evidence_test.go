package models

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestClassifyProviderError(t *testing.T) {
	if got := ClassifyProviderError(nil); got != StatusOK {
		t.Errorf("nil err -> %q, want ok", got)
	}
	if got := ClassifyProviderError(context.DeadlineExceeded); got != StatusTimeout {
		t.Errorf("deadline -> %q, want timeout", got)
	}
	if got := ClassifyProviderError(fmt.Errorf("request timed out after 30s")); got != StatusTimeout {
		t.Errorf("timed-out msg -> %q, want timeout", got)
	}
	if got := ClassifyProviderError(fmt.Errorf("403 forbidden")); got != StatusFailed {
		t.Errorf("hard err -> %q, want failed", got)
	}
}

func TestComputeCompleteness(t *testing.T) {
	// All reached: complete; may claim exhaustive.
	all := []ProviderStatus{
		{ID: "google_flights", Status: StatusOK, Results: 3},
		{ID: "kiwi", Status: StatusCheckedNoHit},
		{ID: "skiplagged", Status: StatusSkipped},
	}
	c := ComputeCompleteness(all)
	if c.State != CompletenessComplete || !c.MayClaimExhaustive() {
		t.Errorf("all-reached -> %+v, want complete", c)
	}
	if c.Queried != 2 { // skipped excluded
		t.Errorf("Queried = %d, want 2", c.Queried)
	}

	// A timeout makes it partial and forbids exhaustive claims.
	partial := []ProviderStatus{
		{ID: "google_flights", Status: StatusOK, Results: 3},
		{ID: "kiwi", Status: StatusTimeout},
	}
	c = ComputeCompleteness(partial)
	if c.State != CompletenessPartial || c.MayClaimExhaustive() {
		t.Errorf("timeout -> %+v, want partial + not-exhaustive", c)
	}
	if len(c.Missing) != 1 || c.Missing[0] != "kiwi" {
		t.Errorf("Missing = %v, want [kiwi]", c.Missing)
	}

	// A circuit-broken provider also makes coverage partial: it was not a
	// checked no-hit, so renderers must not claim exhaustive hotel coverage.
	circuitBroken := []ProviderStatus{
		{ID: "google_hotels", Status: StatusCheckedHit, Results: 3},
		{ID: "booking", Status: StatusCircuitBroken},
	}
	c = ComputeCompleteness(circuitBroken)
	if c.State != CompletenessPartial || c.MayClaimExhaustive() {
		t.Errorf("circuit-broken -> %+v, want partial + not-exhaustive", c)
	}
	if len(c.Missing) != 1 || c.Missing[0] != "booking" {
		t.Errorf("Missing = %v, want [booking]", c.Missing)
	}

	// Nothing definitive: blocked.
	blocked := []ProviderStatus{
		{ID: "google_flights", Status: StatusTimeout},
		{ID: "kiwi", Status: StatusFailed},
	}
	if c = ComputeCompleteness(blocked); c.State != CompletenessBlocked || c.MayClaimExhaustive() {
		t.Errorf("all-failed -> %+v, want blocked", c)
	}
}

// TestRateLimitClassification covers the typed rate-limit path added for #228:
// ErrRateLimited (raw or wrapped) classifies to StatusRateLimited and wins over
// a deadline, and ComputeCompleteness treats a rate-limited provider as Missing
// (partial coverage), never as a definitive empty result.
func TestRateLimitClassification(t *testing.T) {
	if got := ClassifyProviderError(ErrRateLimited); got != StatusRateLimited {
		t.Errorf("ErrRateLimited -> %q, want rate_limited", got)
	}
	wrapped := fmt.Errorf("google flights rate-limited (HTTP 429): %w", ErrRateLimited)
	if got := ClassifyProviderError(wrapped); got != StatusRateLimited {
		t.Errorf("wrapped ErrRateLimited -> %q, want rate_limited", got)
	}
	if !errors.Is(wrapped, ErrRateLimited) {
		t.Errorf("wrapped error should satisfy errors.Is(ErrRateLimited)")
	}
	// Rate-limit must win over a deadline: a 429 returned just past the budget
	// is still a retryable rate-limit, not a plain timeout.
	rlPastDeadline := fmt.Errorf("%w (after %w)", ErrRateLimited, context.DeadlineExceeded)
	if got := ClassifyProviderError(rlPastDeadline); got != StatusRateLimited {
		t.Errorf("rate-limit+deadline -> %q, want rate_limited (rate-limit wins)", got)
	}

	// A rate-limited provider is Missing -> coverage is partial, not exhaustive.
	statuses := []ProviderStatus{
		{ID: "google_flights", Status: StatusOK, Results: 2},
		{ID: "kiwi", Status: StatusRateLimited},
	}
	c := ComputeCompleteness(statuses)
	if c.State != CompletenessPartial || c.MayClaimExhaustive() {
		t.Errorf("rate-limited -> %+v, want partial + not-exhaustive", c)
	}
	if len(c.Missing) != 1 || c.Missing[0] != "kiwi" {
		t.Errorf("Missing = %v, want [kiwi]", c.Missing)
	}

	// When the ONLY provider is rate-limited, nothing definitive came back ->
	// blocked, and exhaustive claims are forbidden.
	if c = ComputeCompleteness([]ProviderStatus{{ID: "kiwi", Status: StatusRateLimited}}); c.State != CompletenessBlocked || c.MayClaimExhaustive() {
		t.Errorf("only-rate-limited -> %+v, want blocked", c)
	}
}

func TestIncompleteNote(t *testing.T) {
	complete := ComputeCompleteness([]ProviderStatus{{ID: "g", Status: StatusOK}})
	if note := complete.IncompleteNote(); note != "" {
		t.Errorf("complete note = %q, want empty", note)
	}
	partial := ComputeCompleteness([]ProviderStatus{
		{ID: "g", Status: StatusOK}, {ID: "kiwi", Status: StatusTimeout},
	})
	note := partial.IncompleteNote()
	if note == "" || !containsSub(note, "kiwi") || !containsSub(note, "incomplete") {
		t.Errorf("partial note = %q, want mention of kiwi + incomplete", note)
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
