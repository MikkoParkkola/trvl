package mcp

import (
	"errors"
	"testing"
)

const (
	testPreflightURL  = "https://preflight.invalid/warm"
	testProviderLabel = "acme"
	// A provider whose name contains a word an earlier version of this test forbade.
	// Kept as a rendering case: interpolation has to be transparent to the caller's
	// data, and the rule below no longer inspects substrings at all, which is the
	// point. See the history note in the doc comment.
	testAwkwardProvider = "Willow Rail"
)

// TestBrowserWarmingNote pins, exactly, every sentence a caller can see after trvl asks
// the platform to open a preflight URL.
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
// So this asserts on the whole rendered string instead. Every reachable note is
// enumerated here in full. What that buys: no rewording, synonym, or added sentence can
// pass silently, because there is nothing left to slip through — the note either is one
// of these strings or the test fails. What it does not buy, recorded here rather than
// left implied: exact comparison cannot distinguish a supported claim from an
// unsupported one. Changing the note and updating the literal to match passes. The
// protection is that the claim then appears in the diff as a literal, next to this
// comment describing what the launcher does and does not establish, where whether the
// code supports it is a question a reviewer can answer. That is the ceiling for a test
// about wording, and the two earlier versions failed by assuming a substring rule
// reached higher.
func TestBrowserWarmingNote(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		err      error
		want     string
	}{
		{
			// Nothing is established except that trvl asked. The note says that and
			// stops: cookies "can be reused", conditional, because whether any exist
			// depends on a browser trvl is no longer watching. The second sentence is
			// what the user can do about the case trvl cannot detect.
			name:     "launcher accepted the request",
			provider: testProviderLabel,
			want: "\n\nAsked your browser to open https://preflight.invalid/warm so cookies for acme " +
				"can be reused. If no window appeared, open that URL yourself.",
		},
		{
			// The one case where something is definitely known. Worth stating as fact
			// rather than softening into a maybe, and the underlying error is included
			// because it is the only thing that tells the user which failure they have.
			name:     "launcher failed inside the window",
			provider: testProviderLabel,
			err:      errors.New("exit status 4"),
			want: "\n\nCould not start a browser for acme (exit status 4). " +
				"Open https://preflight.invalid/warm yourself so those cookies can be reused.",
		},
		{
			// Interpolation is transparent to the provider's name. This case exists
			// because a previous rule fired on the letters in "Willow", making a test
			// about honesty fail on someone else's naming — the kind of false red that
			// gets a real rule weakened to make a provider pass.
			name:     "provider name is interpolated verbatim",
			provider: testAwkwardProvider,
			want: "\n\nAsked your browser to open https://preflight.invalid/warm so cookies for Willow Rail " +
				"can be reused. If no window appeared, open that URL yourself.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := browserWarmingNote(testPreflightURL, tc.provider, tc.err)
			if got != tc.want {
				t.Errorf("the note is not one of the sentences trvl is entitled to say.\n got: %q\nwant: %q\n\n"+
					"If the new wording is correct, update the literal above and say in the commit "+
					"which line of code establishes the claim it makes.", got, tc.want)
			}
		})
	}
}
