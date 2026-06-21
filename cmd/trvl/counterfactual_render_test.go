package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/counterfactual"
)

func TestPrintSavings_SplitsCallFreeFromProbed(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	savings := []counterfactual.Saving{
		{Kind: counterfactual.KindSameDay, Description: "cheapest saves 70", Amount: 70, CallFree: true, AsOf: now},
		{Kind: counterfactual.KindProbe, Description: "nearby airport saves 40", Amount: 40, CallFree: false, AsOf: now},
	}
	var b bytes.Buffer
	printSavings(&b, savings, now)
	out := b.String()
	if !strings.Contains(out, "no extra searches") {
		t.Fatalf("call-free section header missing: %q", out)
	}
	if !strings.Contains(out, "deeper search") {
		t.Fatalf("probed section header missing: %q", out)
	}
	// Honesty: the probed saving must appear under the deeper-search header, not
	// the call-free one.
	free, probed := splitSections(out)
	if !strings.Contains(free, "cheapest saves 70") {
		t.Fatalf("call-free saving misplaced: %q", free)
	}
	if !strings.Contains(probed, "nearby airport saves 40") {
		t.Fatalf("probed saving misplaced: %q", probed)
	}
}

func splitSections(out string) (free, probed string) {
	idx := strings.Index(out, "deeper search")
	if idx < 0 {
		return out, ""
	}
	return out[:idx], out[idx:]
}

func TestPrintSavings_Empty(t *testing.T) {
	var b bytes.Buffer
	printSavings(&b, nil, time.Now())
	if b.Len() != 0 {
		t.Fatalf("no savings must print nothing, got %q", b.String())
	}
}

func TestAsOfSuffix_StalenessLabel(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	if s := asOfSuffix(now.Add(-30*time.Minute), now); s != "" {
		t.Fatalf("fresh (<1h) data must have no suffix, got %q", s)
	}
	if s := asOfSuffix(now.Add(-5*time.Hour), now); !strings.Contains(s, "5h ago") {
		t.Fatalf("want '5h ago', got %q", s)
	}
	if s := asOfSuffix(now.Add(-50*time.Hour), now); !strings.Contains(s, "2d ago") {
		t.Fatalf("want '2d ago', got %q", s)
	}
	if s := asOfSuffix(time.Time{}, now); s != "" {
		t.Fatalf("zero time must have no suffix, got %q", s)
	}
}
