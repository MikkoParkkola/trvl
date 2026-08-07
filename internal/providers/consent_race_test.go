package providers

import (
	"context"
	"net/http"
	"os"
	"reflect"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/cookies"
	"github.com/browserutils/kooky"
)

// TRVL.KOOKY.2m -- consent withdrawn DURING a read must be honoured, and must
// be reported as declined.
//
// This is the race permittedAfterRead exists for: the read takes seconds, and
// the user can revoke access while it is in flight. Every previous test set the
// opt-out BEFORE calling, which exercises the entry check and passes whether or
// not the post-read gate works at all -- a test that cannot fail against the bug
// it names.
//
// Driving it requires a seam, because nothing in the public API can revoke
// consent mid-read. The reader is swapped for one that flips the opt-out and
// then returns cookies, which is precisely the interleaving the production race
// produces.
//
// Both halves are asserted. The cookies must be dropped -- otherwise the gate
// does nothing -- AND the outcome must say declined, because an outcome of
// "found" beside zero cookies is the ambiguity #529 exists to remove, and both
// second-opinion reviewers flagged it.
func TestConsentWithdrawnDuringTheReadIsHonoured(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
	t.Setenv(cookies.DisableEnv, "")

	prev := readCookies
	t.Cleanup(func() { readCookies = prev })

	// The interleaving: consent is live when the read starts and gone when it
	// returns, with real cookies in hand.
	readCookies = func(_ context.Context, _ ...kooky.Filter) (kooky.Cookies, error) {
		_ = os.Setenv(cookies.DisableEnv, "1")
		return kooky.Cookies{
			&kooky.Cookie{Cookie: http.Cookie{Name: "session", Value: "SECRET", Domain: "example.invalid", Path: "/"}},
		}, nil
	}

	out, outcome, _ := browserCookiesForURLWithOutcome("https://example.invalid/")

	if len(out) != 0 {
		t.Errorf("consent was withdrawn during the read and %d cookies were returned anyway; the "+
			"post-read gate is the only thing standing between a revoked opt-out and the user's "+
			"session data leaving their machine", len(out))
	}
	if outcome == outcomeFound {
		t.Error("outcome is found with no cookies returned: a caller acting on the outcome would " +
			"report a successful read that produced nothing")
	}
	if outcome != outcomeDeclined {
		t.Errorf("outcome = %v, want declined -- the stores DID hold cookies, so reporting no_match "+
			"would be a claim about the user's account rather than about their consent", outcome)
	}
}

// The seam must default to the real reader. Without this, a stray assignment
// anywhere in the package would silently disable browser cookies in production
// while every test above still passed.
func TestReadCookiesDefaultsToTheRealReader(t *testing.T) {
	want := reflect.ValueOf(kooky.ReadCookies).Pointer()
	got := reflect.ValueOf(readCookies).Pointer()
	if got != want {
		t.Error("readCookies does not point at kooky.ReadCookies; production would read cookies " +
			"through a test seam")
	}
}
