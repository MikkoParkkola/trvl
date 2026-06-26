package hacks

import (
	"context"
	"testing"
)

// TestRegisteredDetectorCount pins the public "N detectors" claim to the actual
// registered detector roster. If a detector is added to or removed from
// allDetectors(), this fails until RegisteredDetectorCount() and the marketed
// docs (asserted in cmd/trvl/public_claims_test.go) are reconciled — so the
// count can never silently drift again.
//
// The expected value is intentionally a literal: bumping it is the deliberate
// signal to also update README/AGENTS/docs and the public-claims doc tripwire.
func TestRegisteredDetectorCount(t *testing.T) {
	const wantRegisteredDetectors = 36

	if got := RegisteredDetectorCount(); got != wantRegisteredDetectors {
		t.Fatalf("RegisteredDetectorCount()=%d, want %d; if you added/removed a detector, "+
			"update this literal AND the marketed detector count in README.md, AGENTS.md, "+
			"docs/PROVIDERS.md, docs/CLI.md, npm/README.md, docs/COMPARISON.md, CLAUDE.md "+
			"(and the doc tripwire in cmd/trvl/public_claims_test.go)", got, wantRegisteredDetectors)
	}
}

// TestRegisteredDetectorCountMatchesDetectAll guards against allDetectors() and
// the live DetectAll wiring diverging: every detector counted must also be one
// DetectAll executes. We assert the count rather than re-listing the slice so
// the two stay structurally identical (DetectAll consumes allDetectors()).
func TestRegisteredDetectorCountMatchesDetectAll(t *testing.T) {
	if got := len(allDetectors()); got != RegisteredDetectorCount() {
		t.Fatalf("allDetectors() len=%d disagrees with RegisteredDetectorCount()=%d", got, RegisteredDetectorCount())
	}

	// Smoke: DetectAll runs without panicking on an empty input and the
	// detector roster is non-empty (a zero roster would make the count claim
	// vacuously true).
	if RegisteredDetectorCount() == 0 {
		t.Fatal("detector roster is empty")
	}
	_ = DetectAll(context.Background(), DetectorInput{})
}
