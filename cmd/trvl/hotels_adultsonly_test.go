package main

import (
	"testing"
)

// TestHotelsCmd_ChildrenFlagDefault verifies the --children flag exists and
// defaults to 0 (no exclusion).
//
// The adults-only exclusion logic itself now lives in
// internal/hotels.ApplySharedHotelPolicy (shared by the CLI and MCP surfaces)
// and is tested there (internal/hotels/policy_test.go). This file only pins the
// CLI-specific flag wiring.
func TestHotelsCmd_ChildrenFlagDefault(t *testing.T) {
	cmd := hotelsCmd()
	f := cmd.Flags().Lookup("children")
	if f == nil {
		t.Fatal("expected --children flag to be registered")
	}
	if f.DefValue != "0" {
		t.Errorf("expected --children default 0, got %q", f.DefValue)
	}
}
