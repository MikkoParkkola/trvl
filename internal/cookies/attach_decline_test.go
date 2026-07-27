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

// TestGroundProvidersDoNotAttachCookiesDirectly keeps the seam a seam.
//
// The defect above was not a wrong check; it was a call site that never asked.
// Nothing in the type system stops the next one, so this walks internal/ground
// and fails on a direct req.AddCookie. A provider whose cookies genuinely never
// touch the browser — a session it established itself — belongs in the
// allowlist below WITH the reason, which is the review this test exists to
// force.
func TestGroundProvidersDoNotAttachCookiesDirectly(t *testing.T) {
	// tallink: cookies come from Tallink's own booking session (Set-Cookie on a
	// prior response in the same flow), never from the user's browser.
	allowed := map[string]string{"tallink.go": "server-established booking session, not browser-derived"}

	dir := filepath.Join("..", "ground")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("internal/ground not readable: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if _, ok := allowed[name]; ok {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		// Every read of the user's browser must be wrapped where it is read,
		// not where it is sent: the read is the slow part, and a decline
		// arriving during it has to win.
		for _, line := range strings.Split(string(src), "\n") {
			if !strings.Contains(line, "BrowserCookies(ctx") {
				continue
			}
			if !strings.Contains(line, "HeaderIfPermitted(") && !strings.Contains(line, "AttachBrowserCookies(") {
				t.Errorf("internal/ground/%s reads browser cookies without a post-read consent "+
					"check: %s\n\twrap it in cookies.HeaderIfPermitted(...) so an opt-out arriving "+
					"during the read still stops the credential", name, strings.TrimSpace(line))
			}
		}
		if strings.Contains(string(src), ".AddCookie(") {
			t.Errorf("internal/ground/%s attaches cookies directly; route browser-derived "+
				"cookies through cookies.AttachBrowserCookies so an opt-out arriving during "+
				"the browser read still stops them, or add the file to the allowlist in this "+
				"test with the reason its cookies are not browser-derived", name)
		}
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
