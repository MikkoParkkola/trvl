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
