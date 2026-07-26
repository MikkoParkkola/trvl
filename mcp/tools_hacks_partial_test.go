package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestDetectTravelHacks_ReportsPartialSweep pins the honesty half of the
// DetectAll deadline fix at the boundary an agent actually reads.
//
// DetectAll returns what it gathered when the deadline passes. If that arrives
// here as a bare list, the tool answers "count: N" and 100% progress whether N
// was the answer or merely as far as it got, and an agent presents a truncated
// list as complete. The response has to say which.
func TestDetectTravelHacks_ReportsPartialSweep(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	_, result, err := handleDetectTravelHacks(ctx, map[string]any{
		"origin":      "HEL",
		"destination": "BCN",
		"date":        "2026-09-01",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	var payload struct {
		Complete *bool  `json:"complete"`
		Note     string `json:"note"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if payload.Complete == nil {
		t.Fatal("the response omits `complete`; an agent cannot tell a full sweep from a truncated one")
	}
	if *payload.Complete {
		t.Fatal("`complete` was true after a 1ms deadline; a truncated sweep must not be reported as the whole answer")
	}
	if !strings.Contains(payload.Note, "partial") {
		t.Fatalf("expected a note explaining the truncation, got %q", payload.Note)
	}
}

// TestDetectTravelHacks_FlagAndNoteAgree pins the contract that is actually
// environment-independent: whatever `complete` says, the note must agree with it.
//
// An earlier version asserted complete=true for an ordinary search. That premise
// was wrong once a detector cut short by its own allowance began counting against
// completeness — against live providers, one reliably does, so `false` is the
// honest answer and the test was asserting a fiction. What must always hold is
// that the two fields cannot contradict each other.
func TestDetectTravelHacks_FlagAndNoteAgree(t *testing.T) {
	_, result, err := handleDetectTravelHacks(context.Background(), map[string]any{
		"origin":      "HEL",
		"destination": "BCN",
		"date":        "2026-09-01",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, _ := json.Marshal(result)
	var payload struct {
		Complete *bool  `json:"complete"`
		Note     string `json:"note"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if payload.Complete == nil {
		t.Fatal("the response omits `complete`; an agent cannot tell a full sweep from a truncated one")
	}
	if *payload.Complete && payload.Note != "" {
		t.Fatalf("a complete sweep carried a truncation note: %q", payload.Note)
	}
	if !*payload.Complete && !strings.Contains(payload.Note, "partial") {
		t.Fatalf("an incomplete sweep carried no explanation, note was %q", payload.Note)
	}
}

// TestBuildHacksSummary_EmptyPartialDoesNotClaimNoneExist pins that the prose
// agrees with the structured flag.
//
// An empty partial sweep used to read "No travel hacks detected", which states a
// finding the sweep never made: nothing was found because it ran out of time, not
// because the route has no savings. A reader acting on that text draws the wrong
// conclusion, and the structured complete=false beside it does not help anyone
// reading the sentence.
func TestBuildHacksSummary_EmptyPartialDoesNotClaimNoneExist(t *testing.T) {
	partial := buildHacksSummary("HEL", "BCN", "2026-09-01", nil, false)

	if strings.Contains(partial, "No travel hacks detected") {
		t.Fatalf("an unfinished sweep reported as a finding: %q", partial)
	}
	if !strings.Contains(partial, "did not finish") {
		t.Fatalf("expected the text to say the sweep was cut short, got %q", partial)
	}

	complete := buildHacksSummary("HEL", "BCN", "2026-09-01", nil, true)
	if !strings.Contains(complete, "No travel hacks detected") {
		t.Fatalf("a finished sweep with no results should say so plainly, got %q", complete)
	}
}
