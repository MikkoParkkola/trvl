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
		{Kind: counterfactual.KindProbe, Description: "split ticket saves 25 precomputed", Amount: 25, CallFree: true, AsOf: now},
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
	if !strings.Contains(out, "watch monitor") {
		t.Fatalf("precomputed section header missing: %q", out)
	}
	// Honesty: a probe-derived saving (even when free to read now) must NOT sit
	// under the pure by-product "no extra searches" Tier-0 header.
	tier0, rest := cut(out, "Pre-computed")
	if strings.Contains(tier0, "nearby airport") || strings.Contains(tier0, "precomputed") {
		t.Fatalf("probe-derived saving leaked into the Tier-0 by-product section: %q", tier0)
	}
	if !strings.Contains(rest, "split ticket saves 25 precomputed") {
		t.Fatalf("precomputed saving misplaced: %q", rest)
	}
	if !strings.Contains(tier0, "cheapest saves 70") {
		t.Fatalf("genuine by-product saving misplaced: %q", tier0)
	}
}

func cut(s, sep string) (before, after string) {
	idx := strings.Index(s, sep)
	if idx < 0 {
		return s, ""
	}
	return s[:idx], s[idx:]
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
