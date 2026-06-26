package main

import "testing"

// TestGroundCmd_ReturnFlagRegistered proves the ground command exposes a
// --return flag with the expected default so round-trip / return-leg searches
// are reachable from the CLI (parity with the MCP search_ground tool).
func TestGroundCmd_ReturnFlagRegistered(t *testing.T) {
	cmd := groundCmd()
	f := cmd.Flags().Lookup("return")
	if f == nil {
		t.Fatal("ground missing --return flag")
	}
	if f.DefValue != "" {
		t.Errorf("ground --return default = %q, want empty", f.DefValue)
	}
}

// TestGroundCmd_ReturnFlagCapturesValue proves a parsed --return value is
// captured by the flag binding. If the binding is dropped, GetString returns
// the empty default and this test fails.
func TestGroundCmd_ReturnFlagCapturesValue(t *testing.T) {
	cmd := groundCmd()
	if err := cmd.ParseFlags([]string{"--return", "2026-07-08"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	got, err := cmd.Flags().GetString("return")
	if err != nil {
		t.Fatalf("GetString(return): %v", err)
	}
	if got != "2026-07-08" {
		t.Errorf("--return = %q, want %q", got, "2026-07-08")
	}
}
