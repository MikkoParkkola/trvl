package cookies

import (
	"context"
	"testing"
	"time"
)

// TestDisabled_Parsing pins which values count as declining.
//
// The reading is deliberately generous: someone who sets a variable named
// TRVL_NO_BROWSER_COOKIES to "yes" has expressed a preference about their own
// credential store, and honouring only "1" would read their intent backwards on
// a technicality. The two spellings that mean the opposite are the exceptions.
func TestDisabled_Parsing(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"FALSE", false},
		{"  false  ", false},
		{"1", true},
		{"true", true},
		{"yes", true},
		{"no", true}, // a preference expressed clumsily is still a preference
		{"  1  ", true},
	} {
		t.Setenv(DisableEnv, tc.value)
		if got := Disabled(); got != tc.want {
			t.Errorf("Disabled() with %s=%q = %v, want %v", DisableEnv, tc.value, got, tc.want)
		}
	}
}

// TestBrowserCookiesContext_DeclinedReadsNothing pins that the gate returns
// before anything can reach a cookie store.
//
// Asserting the empty string alone would pass even if a helper had been spawned
// and its output discarded, which is the part that matters here: the objection
// this control answers is to the read happening at all, not to the value being
// used. So the call is also timed. A real extraction shells out to nab, which
// cannot complete in the time asserted below, so a fast empty answer is evidence
// that no process was started rather than evidence that one returned nothing.
func TestBrowserCookiesContext_DeclinedReadsNothing(t *testing.T) {
	t.Setenv(DisableEnv, "1")

	start := time.Now()
	got := BrowserCookiesContext(context.Background(), "thetrainline.com")
	elapsed := time.Since(start)

	if got != "" {
		t.Errorf("cookies returned while %s is set (%d bytes; value withheld, it is a live session)",
			DisableEnv, len(got))
	}
	// Generous by three orders of magnitude against a process launch, so this
	// does not become a timing-sensitive test on a loaded CI machine.
	if elapsed > 50*time.Millisecond {
		t.Errorf("declined read took %v, which is long enough that a helper may have run; "+
			"the gate must return before any extraction is attempted", elapsed)
	}
}

// TestBrowserCookies_DeclinedThroughTheContextlessWrapper covers the other
// exported entry point, which delegates but is what older call sites use.
func TestBrowserCookies_DeclinedThroughTheContextlessWrapper(t *testing.T) {
	t.Setenv(DisableEnv, "1")

	if got := BrowserCookies("eurostar.com"); got != "" {
		t.Errorf("cookies returned through BrowserCookies while %s is set (%d bytes; value withheld)",
			DisableEnv, len(got))
	}
}
