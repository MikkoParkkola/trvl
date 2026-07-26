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

// TestDetectTravelHacks_ReportsCompleteSweep is the other half: an ordinary
// search must not carry a partial warning, or the flag becomes noise that
// readers learn to ignore.
func TestDetectTravelHacks_ReportsCompleteSweep(t *testing.T) {
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

	if payload.Complete == nil || !*payload.Complete {
		t.Fatalf("expected complete=true for an uninterrupted sweep, got %v", payload.Complete)
	}
	if payload.Note != "" {
		t.Fatalf("a completed sweep must carry no truncation note, got %q", payload.Note)
	}
}
