package flights

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeCredentialHelper installs an executable named `name` on PATH that records
// its own invocation and then hangs. Hanging matters: a helper that returns
// quickly would let a regression hide behind a fast failure.
func fakeCredentialHelper(t *testing.T, dir, name string) string {
	t.Helper()
	marker := filepath.Join(dir, name+".invoked")
	script := "#!/bin/sh\necho invoked >> \"" + marker + "\"\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
	return marker
}

// TestDefaultRoundTripMergeSpawnsNoCredentialHelper is the integration-level
// regression test for #507.
//
// It deliberately does NOT stub opts.afklmNewProvider: the whole point is to
// exercise the production wiring, including which credential policy the default
// merge selects. A unit test on ResolveCredential cannot catch this class of
// regression, because flipping this call site back to PolicyExternal would
// leave every such test passing while re-breaking the reported bug.
//
// The contract under test: a default round-trip search, which the user did not
// ask to include AF-KLM in, must not execute any credential helper.
func TestDefaultRoundTripMergeSpawnsNoCredentialHelper(t *testing.T) {
	dir := t.TempDir()
	opMarker := fakeCredentialHelper(t, dir, "op")
	secMarker := fakeCredentialHelper(t, dir, "security")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// No key in the environment, but a 1Password reference is present: a build
	// that consults external stores on this path would find something to try.
	t.Setenv("AFKLM_KEY", "")
	t.Setenv("AFKLM_OP_REF", "op://Private/AF-KLM/credential")

	start := time.Now()
	flights, statuses := searchAFKLMNativeRoundTrip(
		context.Background(),
		"AMS", "HEL",
		"2026-09-01", "2026-09-08",
		SearchOptions{ReturnDate: "2026-09-08"},
	)
	elapsed := time.Since(start)

	if _, err := os.Stat(opMarker); err == nil {
		t.Fatal("default round-trip merge invoked the 1Password CLI; a search the user did not ask for must never run a credential helper (#507)")
	}
	if _, err := os.Stat(secMarker); err == nil {
		t.Fatal("default round-trip merge invoked the Keychain helper; a search the user did not ask for must never run a credential helper (#507)")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("default merge took %v with no credential configured; the skip must be immediate", elapsed)
	}
	if len(flights) != 0 {
		t.Fatalf("expected no AF-KLM flights when unconfigured, got %d", len(flights))
	}
	if len(statuses) != 0 {
		t.Fatalf("expected a silent skip when unconfigured, got statuses: %v", statuses)
	}
}
