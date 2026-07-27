package main

import (
	"bytes"
	"strings"
	"testing"
)

// The golden text of the two empty-sweep outputs.
//
// Eleven revisions of this PR were spent on wording that claimed more than the code
// knows, and every one was caught by a reviewer reading a diff rather than by a
// test. A diff cannot show how lines read together, and the substring assertions
// elsewhere in this package deliberately check only that no cause is claimed. Neither
// shows what a person actually sees.
//
// When one of these fails, read the new text as prose and decide whether it is
// better. Do not paste in whatever the code now prints: that is how five of those
// revisions passed the tests written for the one before.
const (
	goldenEmptyPartialSweep = "" +
		"No hacks found. Not every detector was confirmed to finish, so this is not a finding that none exist.\n" +
		"Retrying may return more.\n"

	goldenEmptyFinishedSweep = "" +
		"No hacks detected for this route and date.\n" +
		"Try adding --return DATE to enable split-ticketing and date-flex checks.\n"
)

func TestPrintHacksTable_EmptySweepOutputMatchesGolden(t *testing.T) {
	for _, tc := range []struct {
		name     string
		complete bool
		want     string
	}{
		{"cut short", false, goldenEmptyPartialSweep},
		{"finished", true, goldenEmptyFinishedSweep},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				if err := printHacksTable("HEL", "AMS", "2026-04-15", 0, "EUR", nil, tc.complete); err != nil {
					t.Fatalf("printHacksTable returned error: %v", err)
				}
			})

			// The table header precedes the empty notice and is not what this pins,
			// so compare from the notice onward.
			idx := strings.Index(out, "No hacks")
			if idx < 0 {
				t.Fatalf("no empty-sweep notice in the output:\n%s", out)
			}
			if got := out[idx:]; got != tc.want {
				t.Errorf("empty-sweep text changed.\n got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// Guard against the two texts converging. A finished sweep that found nothing is a
// real finding and should read like one; a sweep that was cut short is not. If these
// ever match, the distinction this PR exists to draw has been lost.
func TestPrintHacksTable_EmptySweepTextsAreDistinct(t *testing.T) {
	if goldenEmptyPartialSweep == goldenEmptyFinishedSweep {
		t.Fatal("a cut-short sweep and a finished one read identically, which is the defect this PR removed")
	}
	if !bytes.Contains([]byte(goldenEmptyFinishedSweep), []byte("No hacks detected")) {
		t.Error("a finished empty sweep should state the finding plainly")
	}
	if bytes.Contains([]byte(goldenEmptyPartialSweep), []byte("No hacks detected")) {
		t.Error("a cut-short sweep must not report that none were detected")
	}
}
