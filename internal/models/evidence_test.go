package models

import (
	"context"
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

	// Nothing definitive: blocked.
	blocked := []ProviderStatus{
		{ID: "google_flights", Status: StatusTimeout},
		{ID: "kiwi", Status: StatusFailed},
	}
	if c = ComputeCompleteness(blocked); c.State != CompletenessBlocked || c.MayClaimExhaustive() {
		t.Errorf("all-failed -> %+v, want blocked", c)
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
