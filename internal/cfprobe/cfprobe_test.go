package cfprobe

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hacks"
)

var now = time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

func TestProbeRunsWhenBudgetAvailable(t *testing.T) {
	e := NewEngine(2, 0, nil)
	called := false
	fan := func() []hacks.Hack {
		called = true
		return []hacks.Hack{
			{Type: "positioning", Title: "Fly from BGY", Description: "save via nearby airport", Savings: 40, Currency: "EUR"},
			{Type: "split", Title: "Split ticket", Savings: 0, Currency: "EUR"}, // dropped (no saving)
		}
	}
	out, st := e.Probe(now, fan)
	if st != StatusRan {
		t.Fatalf("want StatusRan, got %s", st)
	}
	if !called {
		t.Fatalf("fanOut must run when budget available")
	}
	if len(out) != 1 || out[0].Amount != 40 {
		t.Fatalf("want 1 saving of 40, got %+v", out)
	}
	if out[0].CallFree {
		t.Fatalf("probe savings must NOT be marked call-free")
	}
}

// The safety invariant: an exhausted probe lane refuses the fan-out entirely —
// fanOut is never invoked, so no provider calls happen.
func TestProbeRefusesWhenBudgetExhausted(t *testing.T) {
	e := NewEngine(1, 0, nil) // one token, no refill
	fan := func() []hacks.Hack { return []hacks.Hack{{Title: "x", Savings: 10, Currency: "EUR"}} }

	if _, st := e.Probe(now, fan); st != StatusRan {
		t.Fatalf("first probe should run, got %s", st)
	}

	called := false
	guarded := func() []hacks.Hack { called = true; return nil }
	out, st := e.Probe(now, guarded)
	if st != StatusBudgetExhausted {
		t.Fatalf("want StatusBudgetExhausted, got %s", st)
	}
	if called {
		t.Fatalf("fanOut must NOT run when budget is exhausted (no silent fan-out)")
	}
	if out != nil {
		t.Fatalf("exhausted probe must return no savings, got %+v", out)
	}
}

func TestHacksToSavingsDropsZero(t *testing.T) {
	in := []hacks.Hack{
		{Title: "a", Savings: 0},
		{Title: "b", Savings: -5},
		{Title: "c", Description: "d", Savings: 25, Currency: "EUR"},
	}
	out := HacksToSavings(in, now)
	if len(out) != 1 {
		t.Fatalf("want 1 positive saving, got %d", len(out))
	}
	if out[0].Description != "c — d" {
		t.Fatalf("unexpected description %q", out[0].Description)
	}
}

func TestDefaultSingleton(t *testing.T) {
	if Default() == nil {
		t.Fatal("Default engine must not be nil")
	}
}
