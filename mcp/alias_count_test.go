package mcp

import "testing"

// expectedCompatibilityAliasCount is the authoritative compatibility-alias
// count cited by docs. It is a deliberate tripwire: when the alias surface
// changes, this test fails and forces a single, intentional update here (and a
// docs sweep), rather than letting the number drift silently across files.
const expectedCompatibilityAliasCount = 66

func TestAdvertisedSurfaceIsSingleSmartTool(t *testing.T) {
	if got := AdvertisedToolCount(); got != 1 {
		t.Fatalf("AdvertisedToolCount() = %d, want 1 (default tools/list must advertise only the `travel` smart router)", got)
	}
}

func TestCompatibilityAliasCountMatchesExpected(t *testing.T) {
	if got := CompatibilityAliasCount(); got != expectedCompatibilityAliasCount {
		t.Fatalf("CompatibilityAliasCount() = %d, want %d; if the alias surface changed intentionally, update expectedCompatibilityAliasCount and sweep docs that cite the number", got, expectedCompatibilityAliasCount)
	}
}
