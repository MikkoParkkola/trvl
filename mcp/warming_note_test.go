package mcp

import (
	"errors"
	"strings"
	"testing"
)

const (
	testPreflightURL  = "https://preflight.invalid/warm"
	testProviderLabel = "acme"
)

// TestBrowserWarmingNote_ClaimsOnlyWhatIsKnown pins the wording of the sentence a
// caller sees after trvl asks the platform to open a preflight URL.
//
// It used to read "Opened X in browser to warm cookies for Y. Future searches will use
// these cookies automatically." Both halves overstate what a nil error from the
// launcher establishes. The launcher is watched for a brief window only, so one that
// fails after it, which a cold launcher can, is indistinguishable from one that
// succeeded. A user whose browser never appeared was told it had, and then waited for
// cookies that could not arrive.
//
// The forbidden substrings are the specific claims that were made and were wrong,
// which is why this asserts on wording rather than on structure.
func TestBrowserWarmingNote_ClaimsOnlyWhatIsKnown(t *testing.T) {
	note := browserWarmingNote(testPreflightURL, testProviderLabel, nil)

	for _, forbidden := range []string{"opened", "will use these cookies"} {
		if strings.Contains(strings.ToLower(note), forbidden) {
			t.Errorf("the note claims more than a nil launcher error establishes (%q):\n%s", forbidden, note)
		}
	}
	// No unconditional future tense anywhere, which is the semantic rule rather than
	// a list of banned phrases. The first version of this test forbade only the exact
	// wording that had been wrong, "will use these cookies", and the replacement
	// sailed past it as "searches will pick the cookies up": the same promise about a
	// future that is not established, reworded. Opening a URL does not establish that
	// usable cookies were created, or that they can be extracted afterwards.
	//
	// Forbidding "will" outright is blunt and that is deliberate. Every honest thing
	// this note has to say is conditional, so the word has no legitimate use here, and
	// a rule that cannot be satisfied by rephrasing is the point.
	if strings.Contains(strings.ToLower(note), "will") {
		t.Errorf("the note promises a future it cannot establish; every claim here has to be conditional:\n%s", note)
	}
	// And it must actually be conditional rather than merely silent.
	if !strings.Contains(strings.ToLower(note), "can be reused") {
		t.Errorf("the note should say cookies CAN be reused, which is what is actually true:\n%s", note)
	}
	// It still has to be useful, or the fix would be achieved by saying nothing.
	// Compared case-insensitively: "Asked" opens the sentence, and a test that breaks
	// on capitalisation guards nothing worth guarding.
	lower := strings.ToLower(note)
	for _, want := range []string{"asked", testPreflightURL, testProviderLabel} {
		if !strings.Contains(lower, strings.ToLower(want)) {
			t.Errorf("the note should still say what was attempted (missing %q):\n%s", want, note)
		}
	}
}

// TestBrowserWarmingNote_ReportsAKnownFailure covers the one case where something is
// definitely known: the launcher failed inside the window. That is worth telling the
// user plainly, because it is the difference between "no window appeared, try
// yourself" as a precaution and as a fact.
func TestBrowserWarmingNote_ReportsAKnownFailure(t *testing.T) {
	note := browserWarmingNote(testPreflightURL, testProviderLabel, errors.New("exit status 4"))

	if !strings.Contains(note, "Could not start a browser") {
		t.Errorf("a launcher failure should be reported, not softened into a maybe:\n%s", note)
	}
	if strings.Contains(strings.ToLower(note), "asked your browser") {
		t.Errorf("a known failure must not read as a successful request:\n%s", note)
	}
	if !strings.Contains(note, "exit status 4") {
		t.Errorf("the underlying error should be visible so the user can act on it:\n%s", note)
	}
	// Same semantic rule as the success case. A failed launch is even less entitled to
	// promise anything about future searches.
	if strings.Contains(strings.ToLower(note), "will") {
		t.Errorf("a failure note promises a future it cannot establish:\n%s", note)
	}
}
