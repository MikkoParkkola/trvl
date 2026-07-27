package mcp

import (
	"errors"
	"fmt"
	"testing"
)

// TestWarmingNoteWording pins the two sentences trvl is entitled to say, exactly.
//
// The defect this guards against is a claim the code cannot establish. The note used to
// read "Opened X in browser to warm cookies for Y. Future searches will use these
// cookies automatically." Both halves overstate a nil error from the launcher: the
// launcher is watched for a brief window only, so one that fails after it, which a cold
// launcher can, is indistinguishable from one that succeeded. A user whose browser never
// appeared was told it had, and then waited for cookies that could not arrive.
//
// Two earlier versions of this test asserted on wording and both were defeated by
// rephrasing. The first forbade the exact phrase that had been wrong, "will use these
// cookies", and "searches will pick the cookies up" sailed past it. The second forbade
// "will" outright, and review pointed out that a promise does not need the word:
// "Future searches automatically reuse them" makes the identical unestablished claim
// with no future tense in it and satisfies every forbidden-substring rule. A blacklist
// cannot express "does not promise", because the promise is a meaning and the blacklist
// reads letters.
//
// So this compares the templates whole. Any added, reworded, or synonymous sentence
// changes the constant and fails here, and the new claim then appears in the diff as a
// literal beside the comment recording what the launcher does and does not establish,
// where whether the code supports it is a question a reviewer can answer.
//
// This half alone is not sufficient: it pins the templates but says nothing about which
// template a given input gets, so a promise added for one provider only would leave it
// green. TestWarmingNoteIsTheTemplateForEveryInput closes that.
func TestWarmingNoteWording(t *testing.T) {
	const wantAsked = "\n\nAsked your browser to open %s so cookies for %s can be reused. " +
		"If no window appeared, open that URL yourself."
	const wantFailed = "\n\nCould not start a browser for %s (%v). " +
		"Open %s yourself so those cookies can be reused."

	// "asked", never "opened", and "can be reused", never "will be": everything this
	// note has to say about cookies is conditional, because whether any exist depends on
	// a browser trvl is no longer watching. The second sentence is what the user can do
	// about the case trvl cannot detect.
	if warmingNoteAsked != wantAsked {
		t.Errorf("the note trvl shows after asking for a browser has changed.\n got: %q\nwant: %q\n\n"+
			"If the new wording is correct, update the literal here and say in the commit which line "+
			"of code establishes the claim it makes.", warmingNoteAsked, wantAsked)
	}
	// The one case where something is definitely known, stated as fact rather than
	// softened into a maybe. The error is included because it is the only thing telling
	// the user which failure they have.
	if warmingNoteFailed != wantFailed {
		t.Errorf("the note trvl shows after a failed launch has changed.\n got: %q\nwant: %q\n\n"+
			"Same bar: name the line of code that establishes any new claim.", warmingNoteFailed, wantFailed)
	}
}

// TestWarmingNoteIsTheTemplateForEveryInput pins that the note is the template filled in
// and nothing else, for every input, which is what makes the wording test above binding.
//
// Review of the previous version found the hole this closes. Comparing the note against
// fixed expected strings covers the inputs the test happens to name; a sentence appended
// for one provider, one host, or one error value would keep those cases green while the
// user of some other provider reads a promise trvl cannot support. Pinning the wording
// and pinning the rendering separately leaves nowhere for that to live: the wording test
// catches a changed template, and this one catches a note that is not its template.
//
// Deliberately no expected strings here. This asserts a relationship — rendered note
// equals template applied to arguments — and the strings themselves are pinned above by
// literal. Restating them here would assert against a value the test computed, which is
// the other way a test lies.
func TestWarmingNoteIsTheTemplateForEveryInput(t *testing.T) {
	providers := []string{
		"acme",
		// Contains a word an earlier version of this test forbade outright, which is why
		// no substring rule survives: it made a test about honesty fail on someone
		// else's naming, and that is how a real rule gets weakened to let a provider
		// pass.
		"Willow Rail",
		"",                            // an unnamed provider still gets a coherent sentence
		"Ryanair (DAA)",               // punctuation a naive template could mangle
		"Wizz Air Malta",              // multiple words
		"航空会社",                        // non-ASCII, since provider names come from config
		"%s%v%%",                      // format verbs in the data must not be interpreted
		"a-very-long-" + longFiller(), // length must not truncate or wrap
	}
	urls := []string{
		"https://preflight.invalid/warm",
		"https://preflight.invalid/warm?next=%2Fsearch&t=1",
		"https://sub.domain.preflight.invalid:8443/a/b/c",
		"",
	}
	errs := []error{
		errors.New("exit status 4"),
		errors.New("exec: \"open\": executable file not found in $PATH"),
		errors.New(""),
		fmt.Errorf("wrapped: %w", errors.New("context deadline exceeded")),
	}

	for _, provider := range providers {
		for _, url := range urls {
			// The nil-error branch, which is the common one and the one that used to
			// over-promise.
			if got, want := browserWarmingNote(url, provider, nil), fmt.Sprintf(warmingNoteAsked, url, provider); got != want {
				t.Errorf("the note is not its template for provider %q, url %q.\n got: %q\nwant: %q\n\n"+
					"A note that varies with its inputs beyond substitution is how a promise gets added "+
					"for one provider while tests naming other providers stay green.", provider, url, got, want)
			}
			for _, err := range errs {
				if got, want := browserWarmingNote(url, provider, err), fmt.Sprintf(warmingNoteFailed, provider, err, url); got != want {
					t.Errorf("the failure note is not its template for provider %q, url %q, error %q.\n got: %q\nwant: %q",
						provider, url, err, got, want)
				}
			}
		}
	}
}

// longFiller returns a name long enough to catch truncation or wrapping, built rather
// than pasted so the intent is visible.
func longFiller() string {
	const chunk = "provider-name-segment-"
	out := ""
	for range 8 {
		out += chunk
	}
	return out
}
