package consent

import (
	"errors"
	"strings"
	"testing"
)

// TestWarmingClaimsAreDecidedNotWritten asserts over the claim set rather than
// the prose, which is the property the exact-string test it replaces could not
// have (#528).
//
// A string comparison covers the inputs it enumerates and no others, so a
// production change that appends an unsupported promise for one more provider,
// URL or error keeps every case green. Here the decision is a closed set of
// identifiers and the text is a table lookup, so the input space stops mattering.
func TestWarmingClaimsAreDecidedNotWritten(t *testing.T) {
	boom := errors.New("exec: \"open\": not found")

	for _, tc := range []struct {
		name  string
		facts WarmingFacts
		want  []WarmingClaim
	}{
		{"launcher accepted", WarmingFacts{ProviderName: "trainline", PreflightURL: "https://x/"}, []WarmingClaim{ClaimLaunchAsked}},
		{"launcher failed", WarmingFacts{ProviderName: "trainline", PreflightURL: "https://x/", LaunchErr: boom}, []WarmingClaim{ClaimLaunchFailed}},
		{"declined", WarmingFacts{ProviderName: "trainline", PreflightURL: "https://x/", Declined: true}, []WarmingClaim{ClaimDeclined}},
		// A decline outranks a launch outcome: if the user said no, what the
		// launcher would have done is not a thing to report.
		{"declined outranks a failure", WarmingFacts{ProviderName: "t", PreflightURL: "https://x/", LaunchErr: boom, Declined: true}, []WarmingClaim{ClaimDeclined}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := WarmingClaims(tc.facts)
			if len(got) != len(tc.want) {
				t.Fatalf("claims = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("claims = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestEveryRenderedSentenceComesFromAJustifiedClaim is the no-false-green half.
//
// It fails without any edit to itself against both compiling mutants: a
// WarmingClaims that returns an identifier with no registry entry, and a
// WarmingNote that appends a sentence of its own. The expected value is built
// from the registry rather than from a copy of the wording, so the only way to
// add text to the user-visible message is to add a registry entry next to the
// condition that justifies it.
func TestEveryRenderedSentenceComesFromAJustifiedClaim(t *testing.T) {
	for _, f := range []WarmingFacts{
		{ProviderName: "trainline", PreflightURL: "https://a/"},
		{ProviderName: "sncf", PreflightURL: "https://b/", LaunchErr: errors.New("boom")},
		{ProviderName: "eurostar", PreflightURL: "https://c/", Declined: true},
		{}, // zero facts: a renderer that reaches past the table still must not.
	} {
		var want strings.Builder
		for _, c := range WarmingClaims(f) {
			render, ok := warmingClaimText[c]
			if !ok {
				t.Fatalf("WarmingClaims returned %q, which has no entry in warmingClaimText; "+
					"a claim with no justification beside it must not reach a user", c)
			}
			want.WriteString(render(f))
		}
		if got := WarmingNote(f); got != want.String() {
			t.Errorf("WarmingNote said something no claim justifies.\n got: %q\nwant: %q", got, want.String())
		}
	}
}

// TestWarmingClaimRegistryIsClosed makes adding a claim a reviewed act.
//
// The list below is the closed set. A new registry entry fails here until
// someone adds it, which is the moment its justification gets read.
func TestWarmingClaimRegistryIsClosed(t *testing.T) {
	known := map[WarmingClaim]bool{ClaimLaunchAsked: true, ClaimLaunchFailed: true, ClaimDeclined: true}
	for c := range warmingClaimText {
		if !known[c] {
			t.Errorf("claim %q is renderable but not in the reviewed set", c)
		}
	}
	for c := range known {
		if _, ok := warmingClaimText[c]; !ok {
			t.Errorf("claim %q is declared but renders nothing", c)
		}
	}
}

// TestWarmingWordingIsUnchanged pins what the user sees, byte for byte, against
// the three messages that shipped before the restructuring (#528 AC4). This is a
// rewrite of the plumbing and not of the prose; without this, the restructuring
// could quietly change what a user reads and every other test here would pass.
func TestWarmingWordingIsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		facts WarmingFacts
		want  string
	}{
		{
			WarmingFacts{ProviderName: "trainline", PreflightURL: "https://www.thetrainline.com/"},
			"\n\nAsked your browser to open https://www.thetrainline.com/ so cookies for trainline can be reused. If no window appeared, open that URL yourself.",
		},
		{
			WarmingFacts{ProviderName: "sncf", PreflightURL: "https://www.sncf-connect.com/", LaunchErr: errors.New("no browser")},
			"\n\nCould not start a browser for sncf (no browser). Open https://www.sncf-connect.com/ yourself so those cookies can be reused.",
		},
		{
			WarmingFacts{ProviderName: "eurostar", PreflightURL: "https://www.eurostar.com/", Declined: true},
			"\n\nDid not open a browser to warm cookies: TRVL_NO_BROWSER_COOKIES declines access to your browser. The provider is enabled and will start without warm cookies.",
		},
	} {
		if got := WarmingNote(tc.facts); got != tc.want {
			t.Errorf("wording changed.\n got: %q\nwant: %q", got, tc.want)
		}
	}
}
