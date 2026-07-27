package main

import (
	"go/ast"
	"go/parser"
	"go/token"
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
	// Not "before the deadline": a sweep also ends on a cancellation that carries
	// no deadline, so this assertion used to pin wording that was itself wrong.
	// Case-insensitive: the phrase starts a sentence here and appears mid-sentence
	// elsewhere, and a test that breaks on capitalisation guards nothing useful.
	for _, want := range []string{"not every detector was confirmed to finish", "not a finding that none exist"} {
		if !strings.Contains(strings.ToLower(out), want) {
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

// TestPrintHacksTable_PartialAdviceIsActionable is the CLI half of the same
// contract. "Retry with more time, or narrow the search" was wrong twice over: the
// sweep stops at bounds no flag raises (20s per detector, 25s overall, both under
// the 120s default on --timeout), and narrowing the route leaves the detector
// roster unchanged.
func TestPrintHacksTable_PartialAdviceIsActionable(t *testing.T) {
	out := captureStdout(t, func() {
		if err := printHacksTable("HEL", "AMS", "2026-04-15", 0, "EUR", nil, false); err != nil {
			t.Fatalf("printHacksTable returned error: %v", err)
		}
	})

	// Two failure modes, not one. The first pair are knobs the reader does not
	// have. The second pair are diagnoses this code cannot support: an incomplete
	// sweep also happens when the caller's own deadline is short, or when a
	// detector doing local work overruns, with every provider healthy. The first
	// version of this test forbade only the knobs, and a fabricated provider
	// diagnosis walked straight through it.
	//
	// Forbidding substrings is blunt and will complain about legitimate future
	// wording that happens to contain these words. That is the intended trade: the
	// words are cheap to avoid, and each one marks a claim that was actually made
	// here and was actually wrong.
	for _, forbidden := range []string{"more time", "narrow the search"} {
		if strings.Contains(strings.ToLower(out), forbidden) {
			t.Errorf("the output suggests a control the reader does not have (%q):\n%s", forbidden, out)
		}
	}
	for _, forbidden := range []string{"unreachable", "provider is slow"} {
		if strings.Contains(strings.ToLower(out), forbidden) {
			t.Errorf("the output diagnoses a cause it cannot know (%q):\n%s", forbidden, out)
		}
	}
	// A sweep also ends on a plain cancellation carrying no deadline, so naming a
	// deadline is the same overclaim one level down. Asserting neutrality rather
	// than a specific phrase is deliberate: pinning "sweep's bounds" would have
	// locked in wording that was itself slightly wrong.
	if strings.Contains(strings.ToLower(out), "deadline") {
		t.Errorf("the output blames a deadline, which need not exist when a sweep is cancelled:\n%s", out)
	}
	// Removing the bad advice must not amount to saying nothing, so the output still
	// has to state that the sweep was cut short. This checked for "did not finish"
	// until that phrase appeared twice in consecutive sentences and one was cut.
	if !strings.Contains(strings.ToLower(out), "not every detector was confirmed to finish") {
		t.Errorf("the output should still say the sweep was cut short:\n%s", out)
	}
}

// TestHacksCLI_NoStringClaimsWhyASweepEndedEarly is the CLI half of the invariant
// that guards the MCP surface, and it exists because the MCP one did not cover this
// file, which is exactly how "Some detectors did not finish in time" shipped one
// revision after the same claim was removed as "deadline".
//
// Seven revisions of this PR each deleted one message asserting a cause the code
// cannot know, and every one of them was found by review rather than by an
// assertion, because assertions were written against the summary text while the
// claims kept reappearing in a progress callback, a note, a stderr line. Walking
// the literals is the only form of this guard that does not depend on remembering
// which paths exist. The source is parsed, not grepped, so the comments that
// discuss these words deliberately are excluded.
func TestHacksCLI_NoStringClaimsWhyASweepEndedEarly(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "hacks.go", nil, 0)
	if err != nil {
		t.Fatalf("parse hacks.go: %v", err)
	}

	// Kept in step with forbiddenSweepCauseClaims() in the mcp package by hand. The
	// duplication is deliberate: a test should read the file it guards, and sharing
	// the list would mean one of the two packages importing the other's test code.
	forbidden := []string{
		"deadline", "timed out", "timeout", "in time", "ran out",
		"unreachable", "provider is slow", "too slow",
		"more time", "narrow the search",
		// See the note beside the mcp package's copy: these read as fact and are
		// sometimes false, because a detector that finished just before its
		// allowance expired is still recorded as truncated.
		"ended before every detector", "ended early",
	}

	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text := strings.ToLower(lit.Value)
		for _, bad := range forbidden {
			if strings.Contains(text, bad) {
				t.Errorf("%s: string claims a cause the code cannot know (%q): %s",
					fset.Position(lit.Pos()), bad, lit.Value)
			}
		}
		return true
	})
}
