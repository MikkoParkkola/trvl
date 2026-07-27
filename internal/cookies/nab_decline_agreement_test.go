package cookies

import (
	"testing"

	trvlnab "github.com/MikkoParkkola/trvl/internal/nab"
)

// TestNabAgreesWithCookiesOnTheDecline pins the one duplicated definition in the
// opt-out.
//
// internal/nab has to answer "did the user decline browser cookie reads?" before
// it shells out to a helper that reads cookie stores, but it cannot call
// cookies.Disabled: this package already imports internal/nab, so the reuse
// would close an import cycle. The parsing rule is therefore written twice.
//
// This test is the reason that is safe. It lives here because this package can
// see both, and it fails the moment the two drift -- which matters, since a
// silent drift would mean a value the user believes is a decline being honoured
// by one reader and ignored by the other.
func TestNabAgreesWithCookiesOnTheDecline(t *testing.T) {
	for _, value := range []string{
		"", "0", "false", "FALSE", " false ", "1", "true", "TRUE", "yes", "no",
		"please", "off", "  ", "2",
	} {
		t.Setenv(DisableEnv, value)

		mine, theirs := Disabled(), trvlnab.BrowserCookiesDeclined()
		if mine != theirs {
			t.Errorf("with %s=%q: cookies.Disabled() = %v but nab.BrowserCookiesDeclined() = %v",
				DisableEnv, value, mine, theirs)
		}
	}
}
