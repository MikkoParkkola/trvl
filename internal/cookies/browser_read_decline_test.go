package cookies

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// TestBrowserReadPageHonoursTheDecline pins the sixth path of this family: the
// one that drives the user's REAL browser. BrowserReadPage activates Chrome or
// Safari via osascript and reads the rendered page out of the user's logged-in
// session. It consulted nothing before doing that.
//
// The decline is checked ahead of SkipBrowserRead, and the two return different
// errors on purpose. SkipBrowserRead is how automated contexts switch the
// feature off, and it is true in most test runs -- so a test that accepted any
// error here would pass without the decline being consulted at all. That is the
// same vacuity trap the hotel test in this branch fell into: two guards, one
// sentinel, nothing proved. Asserting the specific sentinel is what closes it.
func TestBrowserReadPageHonoursTheDecline(t *testing.T) {
	t.Setenv(consent.CookiesEnv, "1")

	// Deliberately left false, which is the state where the browser WOULD open.
	// If the decline check were removed, this test reaches osascript rather
	// than failing tidily -- so it is written to run fast and refuse early.
	SkipBrowserRead = false
	t.Cleanup(func() { SkipBrowserRead = false })

	start := time.Now()
	out, err := BrowserReadPage(context.Background(), "https://www.thetrainline.com/search", 4)

	if err == nil {
		t.Fatal("the page read was attempted despite the decline")
	}
	if !errors.Is(err, ErrBrowserReadDeclined) {
		t.Fatalf("refused, but not for the declared reason: err = %v, want it to wrap ErrBrowserReadDeclined", err)
	}
	if out != "" {
		t.Errorf("page content came back from a declined read: %q", out)
	}
	// The function's own wait is 4 seconds before it reads anything. Returning
	// well inside that is evidence it refused BEFORE launching a browser,
	// rather than launching one and failing afterwards.
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("refusal took %v, long enough that the browser may have been launched first", elapsed)
	}
}
