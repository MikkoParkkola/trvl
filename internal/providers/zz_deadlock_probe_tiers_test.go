package providers

// The two probes in this file are the behavioural half of the preflight
// deadlock coverage: they drive runPreflight end-to-end through the recovery
// tiers that a real installation actually recovers on. They live apart from
// zz_deadlock_probe_test.go, which holds the structural guards, only because the
// two together exceed the repository's per-file line budget.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// exampleFixtureHost is the reserved documentation host the probes point at. It
// resolves nowhere and every request is redirected to a local test server by
// hostSwitchTransport; naming it once keeps the two probes on the same host.
const exampleFixtureHost = "example.com"

// TestRunPreflight_Tier3aRecoveryDoesNotDeadlock is the same end-to-end probe as
// the test above, but for Tier 3a — the browser-cookie tier that runs FIRST and
// is therefore the one a default installation actually recovers through.
//
// The test above cannot cover it: it declines browser reads to make Tier 3a
// return false deterministically, which pins the recovery on 3b. That left the
// busiest commit site (auth.go:116) with no behavioural coverage at all — only
// the source-text structural guard, which catches a careless re-edit and not a
// re-expressed one. An outside gap audit called that out and was right.
//
// The reason the gap stood as long as it did was a false premise of mine: I had
// recorded that Tier 3a needs a real browser profile and so could not be driven
// from a test. It does not. browserCookiesForURLWithHint consults the warm cache
// BEFORE it reaches kooky (cookies.go:562) whenever a browser hint is set, so a
// test that seeds that cache supplies the browser cookies itself and no real
// browser store is ever opened. That cache is production code that exists for
// startup latency; this only uses it.
//
// Verified by sabotage: swapping commitAuthValuesLocked for the self-locking
// commitAuthValues at auth.go:116 makes this test fail on its timeout. Reverted,
// it passes, and auth.go's checksum is unchanged.
func TestRunPreflight_Tier3aRecoveryDoesNotDeadlock(t *testing.T) {
	// Tier 3a must be PERMITTED here — the opposite of the test above. An
	// ambient decline in the developer's environment would make 3a return false
	// and this test would pass having exercised nothing, so clear it explicitly
	// rather than inheriting whatever the shell had.
	t.Setenv(consent.CookiesEnv, "")

	// Successful recovery persists to the on-disk cookie cache under the home
	// directory; keep it out of the developer's real one.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// The first preflight is refused; the retry after browser cookies are seeded
	// succeeds. Without the refusal the cascade never runs and this test would
	// pass vacuously, so the counter below is load-bearing, not decoration.
	var hits int32
	preflightSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, `<html>denied</html>`)
			return
		}
		_, _ = fmt.Fprint(w, `<html>csrf_token=RECOVERED_3A</html>`)
	}))
	defer preflightSrv.Close()

	const target = "https://" + exampleFixtureHost + "/"
	const hint = "chrome"

	// Seed the warm cache with the cookie a real browser store would have
	// returned. Closing done first means the read is already complete, so
	// warmBrowserCookiesResult returns immediately instead of waiting.
	entry := &warmCacheEntry{done: make(chan struct{})}
	entry.cookies = []*http.Cookie{{
		Name:   "browser_session",
		Value:  "FROM_BROWSER",
		Domain: exampleFixtureHost,
		Path:   "/",
	}}
	close(entry.done)
	key := warmCacheKey(target, hint)
	warmCache.mu.Lock()
	warmCache.entries[key] = entry
	warmCache.mu.Unlock()
	// The warm cache is package-global. Leaving this entry behind would hand a
	// synthetic browser cookie to every later test that happens to use the same
	// URL and hint, which is the flakiness class this file complains about.
	t.Cleanup(func() {
		warmCache.mu.Lock()
		delete(warmCache.entries, key)
		warmCache.mu.Unlock()
	})

	dir := t.TempDir()
	reg, _ := NewRegistryAt(dir)
	rt := NewRuntime(reg)

	cl := &http.Client{
		Transport: &hostSwitchTransport{fallbackTarget: preflightSrv.URL},
		Timeout:   5 * time.Second,
	}
	cl.Jar = newCookieVault()

	pc := &providerClient{
		config: &ProviderConfig{
			ID: "preflight-deadlock-3a", Name: "PD3A", Category: "hotels",
			Endpoint: preflightSrv.URL,
			Cookies:  CookieConfig{Browser: hint},
			Auth: &AuthConfig{
				Type:         "preflight",
				PreflightURL: target,
				Extractions:  map[string]Extraction{"csrf_token": {Pattern: `csrf_token=(\w+)`}},
			},
		},
		client:     cl,
		authValues: map[string]string{},
	}

	type outcome struct {
		snap map[string]string
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		snap, err := rt.runPreflight(context.Background(), pc, map[string]string{})
		done <- outcome{snap, err}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("runPreflight: %v", res.err)
		}
		if n := atomic.LoadInt32(&hits); n < 2 {
			t.Fatalf("fixture failure: preflight server saw %d request(s), so the recovery cascade — the only path that ever deadlocked — was never reached", n)
		}
		if res.snap["csrf_token"] != "RECOVERED_3A" {
			t.Fatalf("recovered csrf_token = %q, want RECOVERED_3A", res.snap["csrf_token"])
		}
		// Which tier recovered matters. Tier 3b cannot be what ran — no WAF
		// challenge is ever served — but prove 3a positively rather than by
		// elimination: the seeded browser cookie can only reach the jar through
		// applyBrowserCookies, which on this path only Tier 3a calls.
		u, _ := url.Parse(target)
		var seeded bool
		for _, c := range cl.Jar.Cookies(u) {
			if c.Name == "browser_session" && c.Value == "FROM_BROWSER" {
				seeded = true
			}
		}
		if !seeded {
			t.Fatal("fixture failure: the seeded browser cookie never reached the jar, so Tier 3a did not run — " +
				"whatever produced the successful retry, it was not the path this test claims to cover")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("DEADLOCK: runPreflight blocked while committing a successful Tier 3a recovery — " +
			"the tier or the commit is taking pc.authMu that runPreflight already holds")
	}
}

// TestRunPreflight_Tier4RecoveryDoesNotDeadlock closes the last uncovered commit
// site, auth.go:133 — Tier 4, the interactive browser escape hatch.
//
// I had this one recorded as untestable too, for the same reason I wrongly
// recorded Tier 3a as untestable: I assumed it opens a real browser. It does not
// have to. headlessFirstResolve (challenge.go:247) is a package-level var whose
// comment says in as many words that it is the seam for tests, and whose default
// refuses to spawn a browser inside a `go test` binary at all. Substituting it
// returns a cleared challenge plus the cookies the window would have produced,
// and everything after that point — finishEscapeHatch — is plain HTTP.
//
// Reaching Tier 4 means the two tiers before it have to genuinely decline:
//   - Tier 3a finds no browser cookies. There is no warm-cache entry here (the
//     3a probe above installs one) and the kooky read is blocked in a test
//     binary, so it returns nothing without touching a real profile.
//   - Tier 3b is served a plain 403 with no challenge script to solve.
//
// Note the consent variable stays CLEARED. Declining browser cookies is how the
// 3b probe forces 3a off, but that same decline is checked at the top of
// tryBrowserEscapeHatch (auth.go:244), so using it here would switch off the
// tier under test and this would pass having exercised nothing.
func TestRunPreflight_Tier4RecoveryDoesNotDeadlock(t *testing.T) {
	t.Setenv(consent.CookiesEnv, "")

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Tier 3a has to decline for Tier 4 to be reached at all. It reads the warm
	// cache before it reaches kooky, and that cache is package-global, so an
	// entry left behind by an earlier test for this host would let 3a recover
	// first. The resolveCalls assertion below would catch that and fail loudly
	// rather than pass falsely, but a loud flake is still a flake.
	resetWarmCache(t)

	// Substitute the seam. Restoring it is not optional: the var is
	// package-global and a leaked stub would answer for every later test.
	prevResolve := headlessFirstResolve
	t.Cleanup(func() { headlessFirstResolve = prevResolve })

	// Stubbing the headless seam alone is not enough to keep this test off the
	// developer's machine. If the retry inside finishEscapeHatch ever fails, the
	// tier falls through to the VISIBLE window path (auth.go:302) — a real
	// browser launch followed by a fifteen-second wait for the cookie store to
	// change, which would outlive this test's own watchdog. It cannot happen with
	// the fixture as written, but "cannot happen with the fixture as written" is
	// one careless edit away from a browser window opening during `go test`.
	// Failing the launch turns that whole path into an immediate false return.
	prevOpen := currentOpenURL
	t.Cleanup(func() { currentOpenURL = prevOpen })
	var visibleLaunches int32
	currentOpenURL = func(_, _, _ string) error {
		atomic.AddInt32(&visibleLaunches, 1)
		return fmt.Errorf("test stub: refusing to open a real browser window")
	}

	var resolveCalls int32
	headlessFirstResolve = func(_ context.Context, _ string) (*ChallengeResult, error) {
		atomic.AddInt32(&resolveCalls, 1)
		return &ChallengeResult{
			Status: ChallengeCleared,
			Cookies: []*http.Cookie{{
				Name:   "escape_hatch",
				Value:  "FROM_WINDOW",
				Domain: exampleFixtureHost,
				Path:   "/",
			}},
		}, nil
	}

	var hits int32
	preflightSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, `<html>denied</html>`)
			return
		}
		_, _ = fmt.Fprint(w, `<html>csrf_token=RECOVERED_T4</html>`)
	}))
	defer preflightSrv.Close()

	const target = "https://" + exampleFixtureHost + "/"

	dir := t.TempDir()
	reg, _ := NewRegistryAt(dir)
	rt := NewRuntime(reg)

	cl := &http.Client{
		Transport: &hostSwitchTransport{fallbackTarget: preflightSrv.URL},
		Timeout:   5 * time.Second,
	}
	cl.Jar = newCookieVault()

	pc := &providerClient{
		config: &ProviderConfig{
			ID: "preflight-deadlock-t4", Name: "PDT4", Category: "hotels",
			Endpoint: preflightSrv.URL,
			Auth: &AuthConfig{
				Type:               "preflight",
				PreflightURL:       target,
				BrowserEscapeHatch: true,
				Extractions:        map[string]Extraction{"csrf_token": {Pattern: `csrf_token=(\w+)`}},
			},
		},
		client:     cl,
		authValues: map[string]string{},
	}

	type outcome struct {
		snap map[string]string
		err  error
	}
	done := make(chan outcome, 1)
	stopped := make(chan struct{})
	// Tier 4 is gated on the interactive opt-in as well as the config flag; a
	// background context would skip the tier entirely.
	ctx, cancel := context.WithCancel(WithInteractive(context.Background()))
	go func() {
		defer close(stopped)
		snap, err := rt.runPreflight(ctx, pc, map[string]string{})
		done <- outcome{snap, err}
	}()
	// On the watchdog path the worker is by definition still running. Cleanup is
	// LIFO, so registering the join here — after t.Setenv and after the two stub
	// restores — makes it run FIRST: the worker is stopped and drained before the
	// real HOME comes back and before the seams are put back. Otherwise a hung
	// worker could reach saveCachedCookies (auth.go:143) against the restored
	// HOME and drop this fixture's synthetic cookies into the developer's cache.
	// Waiting on stopped rather than done keeps this correct on the success
	// path too, where the assertions below have already taken the outcome.
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(20 * time.Second):
			t.Error("runPreflight worker did not stop after cancellation; " +
				"leaving it running would let it write against the restored HOME")
		}
	})

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("runPreflight: %v", res.err)
		}
		if n := atomic.LoadInt32(&resolveCalls); n == 0 {
			t.Fatal("fixture failure: the escape hatch was never entered, so Tier 4 did not run — " +
				"an earlier tier recovered and this test proved nothing about auth.go:133")
		}
		if n := atomic.LoadInt32(&visibleLaunches); n != 0 {
			t.Fatalf("the visible-window path was taken %d time(s); on a real machine that is a browser window opening during `go test`", n)
		}
		if n := atomic.LoadInt32(&hits); n < 2 {
			t.Fatalf("fixture failure: preflight server saw %d request(s), so the recovery cascade was never reached", n)
		}
		if res.snap["csrf_token"] != "RECOVERED_T4" {
			t.Fatalf("recovered csrf_token = %q, want RECOVERED_T4", res.snap["csrf_token"])
		}
		// Positive proof rather than proof by elimination, as in the 3a probe:
		// the escape-hatch cookie can only reach the jar through the seed inside
		// finishEscapeHatch, which nothing but Tier 4 calls.
		u, _ := url.Parse(target)
		var seeded bool
		for _, c := range cl.Jar.Cookies(u) {
			if c.Name == "escape_hatch" && c.Value == "FROM_WINDOW" {
				seeded = true
			}
		}
		if !seeded {
			t.Fatal("fixture failure: the escape-hatch cookie never reached the jar, so the recovery did not come from Tier 4")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("DEADLOCK: runPreflight blocked while committing a successful Tier 4 recovery — " +
			"the tier or the commit is taking pc.authMu that runPreflight already holds")
	}
}
