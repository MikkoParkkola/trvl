package main

import (
	"os"
	"strings"
	"testing"
)

// TestHacksCmd_SurfacesPartialSweep pins that the CLI says when a hack sweep was
// cut short, in both channels it speaks through.
//
// DetectAll returns what it gathered when a deadline passes. A person reading the
// table sees a short list with no reason for it, and anything piping
// `--format json` has no field to check, unless the command reports it. Wiring one
// channel and forgetting the other is the easy mistake, because each looks
// complete on its own.
//
// Asserted against the command's own source rather than by running it: a live run
// would call real providers, and what matters here is that both surfaces exist.
func TestHacksCmd_SurfacesPartialSweep(t *testing.T) {
	b, err := os.ReadFile("hacks.go")
	if err != nil {
		t.Fatalf("read hacks.go: %v", err)
	}
	src := string(b)

	if !strings.Contains(src, `"complete":`) {
		t.Error("the JSON payload does not carry `complete`; a piped consumer cannot tell a truncated list from a full one")
	}
	if !strings.Contains(src, "results below are partial") {
		t.Error("no human-readable warning on a truncated sweep; a person reading the table sees a short list and no reason for it")
	}
	if !strings.Contains(src, "hacks.DetectAll(ctx, input)") {
		t.Error("expected the command to call DetectAll directly; if that changed, the assertions above no longer describe the live path")
	}
}

// TestHacksCmd_Constructs is the cheap guard that the command still builds, so
// the source assertions above cannot pass against a file nobody wires up.
func TestHacksCmd_Constructs(t *testing.T) {
	if cmd := hacksCmd(); cmd == nil || cmd.Use == "" {
		t.Fatal("hacksCmd returned an unusable command")
	}
}

// TestPrintHacksTable_EmptyPartialSweepDoesNotClaimNoneExist pins a defect the
// adversarial review found after the first fix. The note that a sweep was cut
// short went to stderr while stdout still said "No hacks detected for this route
// and date." Stdout is what gets read, piped and pasted, so the user's takeaway
// was a finding the sweep never made: nothing came back because it ran out of
// time, not because the route has no savings. The MCP surface already got this
// right, which is what made the gap easy to miss.
func TestPrintHacksTable_EmptyPartialSweepDoesNotClaimNoneExist(t *testing.T) {
	out := captureStdout(t, func() {
		if err := printHacksTable("HEL", "AMS", "2026-04-15", 0, "EUR", nil, false); err != nil {
			t.Fatalf("printHacksTable returned error: %v", err)
		}
	})

	if strings.Contains(out, "No hacks detected for this route and date.") {
		t.Errorf("an unfinished sweep reported that none exist:\n%s", out)
	}
	for _, want := range []string{"before the deadline", "did not finish"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should say the sweep was cut short (missing %q):\n%s", want, out)
		}
	}
}

// TestPrintHacksTable_EmptyFinishedSweepStillSaysNoneFound guards the other
// direction, so the fix above cannot be satisfied by hedging on every empty
// result. A sweep that genuinely finished and found nothing is a real finding and
// should read like one.
func TestPrintHacksTable_EmptyFinishedSweepStillSaysNoneFound(t *testing.T) {
	out := captureStdout(t, func() {
		if err := printHacksTable("HEL", "AMS", "2026-04-15", 0, "EUR", nil, true); err != nil {
			t.Fatalf("printHacksTable returned error: %v", err)
		}
	})

	if !strings.Contains(out, "No hacks detected for this route and date.") {
		t.Errorf("a finished empty sweep should state the finding plainly:\n%s", out)
	}
	if strings.Contains(out, "did not finish") {
		t.Errorf("a finished sweep must not hedge as though it were cut short:\n%s", out)
	}
}
