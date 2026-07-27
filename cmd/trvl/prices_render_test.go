package main

import (
	"bytes"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/booking"
)

// The golden text of the capped-readiness output.
//
// This exists because every review of the ceiling work read diffs, and a diff
// cannot show how two lines read together. Rendering it revealed that the
// unobtainable signal was named twice on consecutive lines, in the exact output an
// external tester was being invited to retest, for a fix whose subject was that
// this output was confusing.
//
// Pinning the whole block rather than substrings is deliberate. The failure mode
// here is not a missing word, it is a sentence that reads badly next to its
// neighbour, and only the full text can catch that. When this test fails, read the
// diff as prose and decide whether the new wording is better; do not update the
// constant to match whatever the code now prints.
const goldenCappedReadiness = "" +
	"\nBooking readiness: caution — every obtainable signal confirmed; refundability_known not available from this source\n" +
	"  (caution is the best this command can report. Use `trvl rooms` for a room-level verdict that can reach ready.)\n"

func TestPrintReadiness_CappedOutputMatchesGolden(t *testing.T) {
	v := booking.EvaluateWith(
		booking.Input{Verified: booking.True(), LinkStable: booking.True(), IdentityConfirmed: booking.True()},
		booking.Availability{NoRefundability: true},
	)

	var buf bytes.Buffer
	printReadiness(&buf, &v)

	if got := buf.String(); got != goldenCappedReadiness {
		t.Errorf("rendered readiness block changed.\n got:\n%s\nwant:\n%s", got, goldenCappedReadiness)
	}
}
