package cookies

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/consent"
	trvlnab "github.com/MikkoParkkola/trvl/internal/nab"
)

// TestNabAgreesWithCookiesOnTheDecline pins that every reader of the browser
// cookie opt-out gives the same answer.
//
// It was written when the parsing rule genuinely existed twice: internal/nab has
// to answer "did the user decline?" before shelling out to a helper that reads
// cookie stores, and it could not call cookies.Disabled, because this package
// imports internal/nab and the reuse would have closed an import cycle. Both
// copies now delegate to internal/consent, so there is one rule and nothing left
// to drift.
//
// The test stays anyway, and asserts across all three names rather than two. It
// is cheap, and it is what fails if someone reintroduces a local copy in either
// package -- which is exactly how the duplication arrived the first time. A
// silent drift would mean a value the user believes is a decline being honoured
// by one reader and ignored by the other.
func TestNabAgreesWithCookiesOnTheDecline(t *testing.T) {
	for _, value := range []string{
		"", "0", "false", "FALSE", " false ", "1", "true", "TRUE", "yes", "no",
		"please", "off", "  ", "2",
	} {
		t.Setenv(DisableEnv, value)

		mine, theirs, shared := Disabled(), trvlnab.BrowserCookiesDeclined(), consent.CookiesDeclined()
		if mine != shared {
			t.Errorf("with %s=%q: cookies.Disabled() = %v but consent.CookiesDeclined() = %v",
				DisableEnv, value, mine, shared)
		}
		if theirs != shared {
			t.Errorf("with %s=%q: nab.BrowserCookiesDeclined() = %v but consent.CookiesDeclined() = %v",
				DisableEnv, value, theirs, shared)
		}
	}
}
