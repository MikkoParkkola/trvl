package providers

import (
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// TestTier1JarCannotOutliveADecline pins the twelfth path of this family, and
// the one the earlier rounds could not reach.
//
// Every other fix in this branch stops browser cookies from being READ after a
// decline. This client is different: it is seeded ONCE and then lives for as
// long as the caller holds it, and the tls-client jar attaches whatever it holds
// to every later request from inside the library — there is no per-request hook
// to refuse at. So a decline that arrives after seeding used to change nothing:
// the credentials were already in the jar and kept going out.
//
// The vault had exactly this shape in round 8 and was fixed by swapping the jar
// rather than by re-checking on the way in. This does the same, and the test
// asserts on the jar because the jar is what the next request will send.
func TestTier1JarCannotOutliveADecline(t *testing.T) {
	const target = "https://example.test/search"

	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("fixture URL: %v", err)
	}

	// A client seeded before any decline, exactly as SeedCookies leaves it.
	seeded := func(t *testing.T) *Tier1Client {
		t.Helper()
		c, err := NewTier1Client()
		if err != nil {
			t.Fatalf("client: %v", err)
		}
		c.inner.SetCookies(u, toFHTTPCookies([]*http.Cookie{
			{Name: "datadome", Value: "from-the-users-browser", Domain: u.Hostname(), Path: "/"},
		}))
		c.seededMu.Lock()
		c.seeded = true
		c.seededMu.Unlock()
		return c
	}

	// CONTROL. Nothing declined: the jar must still hold the cookie, or the
	// decline case below would be passing against a jar that never held one.
	t.Run("kept while nothing is declined", func(t *testing.T) {
		c := seeded(t)
		c.discardSeededIfDeclined()
		if got := c.inner.GetCookies(u); len(got) != 1 {
			t.Fatalf("the fixture seeded nothing, so the decline case would prove nothing: %v", got)
		}
	})

	t.Run("discarded once the user declines", func(t *testing.T) {
		c := seeded(t)
		t.Setenv(consent.CookiesEnv, "1")

		// This is what a later request does before the jar gets its hands on it.
		c.discardSeededIfDeclined()

		if got := c.inner.GetCookies(u); len(got) != 0 {
			t.Errorf("cookies seeded before the opt-out stayed in the jar and would be sent: %v", got)
		}
		if got := c.Cookies(target); len(got) != 0 {
			t.Errorf("the reader handed out cookies seeded before the opt-out: %v", got)
		}
	})

	t.Run("an unseeded jar is left alone", func(t *testing.T) {
		c, err := NewTier1Client()
		if err != nil {
			t.Fatalf("client: %v", err)
		}
		// A server-established session, which the browser opt-out is not about.
		c.inner.SetCookies(u, toFHTTPCookies([]*http.Cookie{
			{Name: "PHPSESSID", Value: "server-established", Domain: u.Hostname(), Path: "/"},
		}))
		t.Setenv(consent.CookiesEnv, "1")

		c.discardSeededIfDeclined()
		if got := c.inner.GetCookies(u); len(got) != 1 {
			t.Errorf("the opt-out threw away a session it was never about: %v", got)
		}
	})

	// The swap has to leave a usable jar behind, not a nil one, or the next
	// request panics instead of proceeding without the user's cookies.
	t.Run("the client still works after the swap", func(t *testing.T) {
		c := seeded(t)
		t.Setenv(consent.CookiesEnv, "1")
		c.discardSeededIfDeclined()

		if c.inner.GetCookieJar() == nil {
			t.Fatal("the swap left no jar, so the next request would fail on a nil jar")
		}
		c.inner.SetCookies(u, toFHTTPCookies([]*http.Cookie{
			{Name: "PHPSESSID", Value: "server-established", Domain: u.Hostname(), Path: "/"},
		}))
		if got := c.inner.GetCookies(u); len(got) != 1 {
			t.Errorf("the replacement jar does not hold cookies: %v", got)
		}
	})
}

// TestTier1RevocationIsAtomic covers what round 13 found: the revocation was
// correct in a single goroutine and wrong in two.
//
// Seeding used to inject the cookies and mark the client afterwards, so a
// request landing between the two saw an unseeded client, skipped revocation
// entirely, and sent cookies the user had already declined. The jar swap was
// also a bare pointer assignment racing every in-flight request — a revocation
// an already-started request could outrun, and a data race besides.
//
// Run with -race; the assertion is that nothing escapes and nothing races.
func TestTier1RevocationIsAtomic(t *testing.T) {
	const target = "https://example.test/search"

	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("fixture URL: %v", err)
	}

	c, err := NewTier1Client()
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	c.seededMu.Lock()
	c.seeded = true
	c.inner.SetCookies(u, toFHTTPCookies([]*http.Cookie{
		{Name: "datadome", Value: "from-the-users-browser", Domain: u.Hostname(), Path: "/"},
	}))
	c.seededMu.Unlock()

	t.Setenv(consent.CookiesEnv, "1")

	// Readers and revokers at the same time, which is the shape a real search
	// has: several providers on one client while the user's decline lands.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := c.Cookies(target); len(got) != 0 {
				t.Errorf("a concurrent reader got cookies after the decline: %v", got)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.discardSeededIfDeclined()
		}()
	}
	wg.Wait()

	if got := c.inner.GetCookies(u); len(got) != 0 {
		t.Errorf("cookies survived a decline under concurrency: %v", got)
	}
}

// TestTier1RevocationRacesCleanly is the same concern as above with the one
// weakness removed: `seeded` is a latch, so a single decline produces exactly
// ONE jar swap no matter how many goroutines call the revoker, and a single swap
// is too small a target for the race detector to have anything to interleave
// with. The revokers here re-arm the client between revocations, so N swaps
// really do overlap N reads.
//
// The oracle is -race, not an assertion: readers deliberately assert nothing
// about what they see, because a re-arm landing between a reader's revocation
// check and its read legitimately puts a cookie back. What must hold is that
// none of it races, and that the jar is empty once the churn stops.
func TestTier1RevocationRacesCleanly(t *testing.T) {
	const target = "https://example.test/search"

	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("fixture URL: %v", err)
	}
	browserCookies := toFHTTPCookies([]*http.Cookie{
		{Name: "datadome", Value: "from-the-users-browser", Domain: u.Hostname(), Path: "/"},
	})

	c, err := NewTier1Client()
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	t.Setenv(consent.CookiesEnv, "1")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				c.Cookies(target)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				// Re-arm exactly as SeedCookies does, then revoke.
				c.seededMu.Lock()
				c.seeded = true
				c.inner.SetCookies(u, browserCookies)
				c.seededMu.Unlock()

				c.discardSeededIfDeclined()
			}
		}()
	}
	wg.Wait()

	c.discardSeededIfDeclined()
	if got := c.inner.GetCookies(u); len(got) != 0 {
		t.Errorf("the churn left declined cookies in the jar: %v", got)
	}
}
