//go:build unix

package main

import (
	"bytes"
	"github.com/MikkoParkkola/trvl/internal/booking"
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

// TestPrintReadiness_CappedVerdictDoesNotRepeatItself renders the output instead
// of grepping the source, which is how the stutter above it got missed.
//
// On a capped path with no ordinary findings, Summary() already names the signals
// the source cannot supply. The ceiling line named them a second time, so the exact
// phrase appeared on two consecutive lines of the output invited for retesting, in
// a fix whose whole subject is that this output was confusing. The ceiling line now
// carries only what Summary cannot: which verdict is the ceiling, and where to go
// for one that can reach ready.
func TestPrintReadiness_CappedVerdictDoesNotRepeatItself(t *testing.T) {
	v := booking.EvaluateWith(
		booking.Input{Verified: booking.True(), LinkStable: booking.True(), IdentityConfirmed: booking.True()},
		booking.Availability{NoRefundability: true},
	)
	if !v.Capped() {
		t.Fatalf("precondition: expected a capped verdict, got ceiling %q", v.Ceiling)
	}

	var buf bytes.Buffer
	printReadiness(&buf, &v)
	out := buf.String()

	for _, reason := range v.CeilingReasons {
		if n := strings.Count(out, reason); n > 1 {
			t.Errorf("%q appears %d times in the output; the reader sees the same sentence twice:\n%s", reason, n, out)
		}
	}
	// The ceiling still has to be explained, or removing the repetition would have
	// been achieved by saying less.
	for _, want := range []string{"best this command can report", "trvl rooms", "refundability_known"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should still contain %q:\n%s", want, out)
		}
	}
}

// TestPrintReadiness_NilVerdictPrintsNothing guards the early return, so a command
// with no readiness to report does not emit a stray blank line or a bare header.
func TestPrintReadiness_NilVerdictPrintsNothing(t *testing.T) {
	var buf bytes.Buffer
	printReadiness(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("expected no output for a nil verdict, got %q", buf.String())
	}
}
