package cookies

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAttachBrowserCookiesRefusedAfterADecline pins the ninth path of this
// family, and the first that never involves a cookie jar at all.
//
// A jar is not the only way a browser credential reaches the wire. Trainline's
// Tier-1 retry and Rome2Rio's Cloudflare path read the user's browser once and
// then set the cookies on the request directly. The consent check sat before
// the read — which takes seconds — so a decline arriving during it lost: the
// read returned afterwards and the credentials went out anyway.
//
// The check therefore belongs at the last point before transmission. The test
// is ordered around a CONTROL so the decline assertion is about the decline and
// not about a helper that never attaches anything.
func TestAttachBrowserCookiesRefusedAfterADecline(t *testing.T) {
	fromBrowser := []*http.Cookie{{Name: "datadome", Value: "from-the-users-browser"}}

	newReq := func(t *testing.T) *http.Request {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, "https://example.test/search", nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		return req
	}

	t.Run("attached while nothing is declined", func(t *testing.T) {
		req := newReq(t)
		if !AttachBrowserCookies(req, fromBrowser) {
			t.Fatal("the attach was refused with no opt-out in force")
		}
		if got := req.Header.Get("Cookie"); !strings.Contains(got, "datadome=from-the-users-browser") {
			t.Fatalf("the fixture attached nothing, so the decline case would prove nothing: %q", got)
		}
	})

	t.Run("refused once the user declines", func(t *testing.T) {
		req := newReq(t)
		t.Setenv(DisableEnv, "1")

		// These cookies came from a browser read that began before the decline.
		// They still must not go out.
		if AttachBrowserCookies(req, fromBrowser) {
			t.Error("browser cookies read before the opt-out were attached after it")
		}
		if got := req.Header.Get("Cookie"); got != "" {
			t.Errorf("the request would still carry browser cookies: %q", got)
		}
	})
}

// TestProvidersDoNotSendBrowserCookiesDirectly keeps the seam a seam.
//
// The defects this family is made of were never a wrong check; they were call
// sites that never asked. Nothing in the type system stops the next one, so this
// walks the provider packages and fails on a browser cookie reaching the wire
// without a post-read consent check.
//
// Its job changed in round 11. It used to be the primary defence and it covered
// internal/ground only — which is exactly why review found a Booking.com path in
// internal/hotels that it could not see. The guarantee now lives one layer down,
// in providers.permittedAfterRead, which re-checks the decline on the way out of
// every browser read. This test is the TRIPWIRE for a reader added outside that
// gated path. So the fix for a failure here is a wrap or a gated reader, never a
// new allowlist entry — an entry is only for cookies that never came from the
// user's browser, and it must say why.
func TestProvidersDoNotSendBrowserCookiesDirectly(t *testing.T) {
	allowed := map[string]string{
		// Tallink: cookies come from Tallink's own booking session (Set-Cookie on
		// a prior response in the same flow), never from the user's browser.
		"ground/tallink.go": "server-established booking session, not browser-derived",
		// Google consent bypass: a hardcoded constant (SOCS/CONSENT), not read
		// from anyone's browser. See googleConsentCookie in that file.
		"hotels/search_fetch.go": "hardcoded consent constant, not browser-derived",
	}

	root := ".."
	// Only the seam package itself, which is where the wrappers are DEFINED.
	// Nothing else is exempt: an earlier version of this test also skipped
	// internal/batchexec on the grounds that it is "just the transport", which
	// bought nothing (it holds the GetWithCookie definition, no call sites) and
	// would have hidden the day it grew one.
	skipDirs := map[string]bool{"cookies": true}

	var walked int
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if info.IsDir() {
			if skipDirs[rel] {
				return filepath.SkipDir
			}
			return nil
		}
		name := info.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		if _, ok := allowed[rel]; ok {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		walked++
		permitted := func(line string) bool {
			return strings.Contains(line, "HeaderIfPermitted(") ||
				strings.Contains(line, "AttachBrowserCookies(")
		}
		for _, line := range strings.Split(string(src), "\n") {
			// Every read of the user's browser must be wrapped where it is read,
			// not where it is sent: the read is the slow part, and a decline
			// arriving during it has to win.
			if strings.Contains(line, "BrowserCookies(ctx") && !permitted(line) {
				t.Errorf("internal/%s reads browser cookies without a post-read consent "+
					"check: %s\n\twrap it in cookies.HeaderIfPermitted(...) so an opt-out "+
					"arriving during the read still stops the credential", rel, strings.TrimSpace(line))
			}
			// The last line before transmission. Round 11 found two of these in
			// booking_rooms.go handing a raw browser Cookie header to the client.
			if strings.Contains(line, ".GetWithCookie(") && !permitted(line) {
				t.Errorf("internal/%s sends a raw Cookie header: %s\n\twrap the value in "+
					"cookies.HeaderIfPermitted(...), or add the file to the allowlist in "+
					"this test with the reason its cookies are not browser-derived",
					rel, strings.TrimSpace(line))
			}
			if strings.Contains(line, ".AddCookie(") && !permitted(line) {
				t.Errorf("internal/%s attaches cookies directly: %s\n\troute browser-derived "+
					"cookies through cookies.AttachBrowserCookies so an opt-out arriving "+
					"during the browser read still stops them, or add the file to the "+
					"allowlist in this test with the reason its cookies are not "+
					"browser-derived", rel, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		// NOT a skip. A detector that disarms itself when its own I/O fails
		// reports "no bypasses" for the one reason that proves nothing, which is
		// the failure mode this whole family is made of.
		t.Fatalf("the walk could not complete, so it proves nothing: %v", err)
	}
	// A walk that silently covered nothing would pass forever.
	if walked < 50 {
		t.Fatalf("the walk only read %d files; it is not covering the provider packages", walked)
	}
}

// TestHeaderIfPermittedRefusedAfterADecline covers the string half of the seam:
// the providers that hand a Cookie header value to a request constructor rather
// than attaching cookies one at a time (Trainline, SNCF, Eurostar). Same shape,
// same reason — the check has to sit after the browser read, not before it.
func TestHeaderIfPermittedRefusedAfterADecline(t *testing.T) {
	const harvested = "datadome=from-the-users-browser"

	t.Run("passed through while nothing is declined", func(t *testing.T) {
		if got := HeaderIfPermitted(harvested); got != harvested {
			t.Fatalf("the header was dropped with no opt-out in force: %q", got)
		}
	})

	t.Run("dropped once the user declines", func(t *testing.T) {
		t.Setenv(DisableEnv, "1")
		if got := HeaderIfPermitted(harvested); got != "" {
			t.Errorf("a browser cookie header read before the opt-out survived it: %q", got)
		}
	})
}
