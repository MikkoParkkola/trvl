//go:build unix

package main

import (
	"os"
	"strings"
	"testing"
)

// TestPricesCmd_SurfacesTheReadinessCeiling pins that `trvl prices` says when its
// verdict is capped, in both channels it speaks through.
//
// This is the reported defect: an external tester ran six indexed hotels, got
// "caution" every time, and could not tell a structurally capped path from a
// finding about each property. The hotel-prices endpoint carries no cancellation
// terms, so it can never reach "ready" — and unless the output says so, a reader
// infers a problem that is not there.
//
// Wiring the JSON field and forgetting the human line (or the reverse) is the easy
// mistake, since each looks complete on its own. Asserted against the command's
// source rather than by running a live price lookup.
func TestPricesCmd_SurfacesTheReadinessCeiling(t *testing.T) {
	b, err := os.ReadFile("prices.go")
	if err != nil {
		t.Fatalf("read prices.go: %v", err)
	}
	src := string(b)

	if !strings.Contains(src, "booking_readiness_ceiling") {
		t.Error("the JSON payload carries no ceiling; a piped consumer cannot tell a capped path from a property finding")
	}
	if !strings.Contains(src, "readiness.Capped()") {
		t.Error("nothing checks Capped(); the ceiling fields would be dead weight")
	}
	if !strings.Contains(src, "best this command can report") {
		t.Error("no human-readable line explaining the ceiling; the person reading the table is the one who reported this")
	}
	if !strings.Contains(src, "trvl rooms") {
		t.Error("the ceiling note should point at the command that can reach ready, or the reader knows only that they are stuck")
	}
}
