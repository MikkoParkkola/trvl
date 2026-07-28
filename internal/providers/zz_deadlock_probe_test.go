package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// TestTier3aBrowserCookieRetry_NoDeadlockUnderCallerHeldWriteLock is the
// Tier-3a half of the contract pinned by
// TestRecoveryTiers_NoDeadlockUnderCallerHeldWriteLock below.
//
// Why a second test rather than a wider one: only a *successful* tier ever
// reached the commit, so the pre-existing failure-path tests could not have
// caught this bug — they return before the commit is attempted. The success
// fixture here mirrors TestTryBrowserCookieRetry_Success, with the one
// difference that matters: the caller already holds pc.authMu for writing, the
// way runPreflight does.
func TestTier3aBrowserCookieRetry_NoDeadlockUnderCallerHeldWriteLock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html>csrf_token=TIER3A_TOK</html>`)
	}))
	defer srv.Close()

	targetURL := srv.URL + "/page"
	resetWarmCache(t)
	entry := &warmCacheEntry{done: make(chan struct{})}
	u, _ := url.Parse(srv.URL)
	entry.cookies = []*http.Cookie{{Name: "sid", Value: "test", Domain: u.Hostname()}}
	close(entry.done)
	warmCache.mu.Lock()
	warmCache.entries[warmCacheKey(targetURL, "")] = entry
	warmCache.mu.Unlock()

	cl := srv.Client()
	cl.Jar = newCookieVault()
	pc := &providerClient{
		config: &ProviderConfig{
			ID: "tier3a-deadlock", Name: "T3A", Category: "hotels",
			Endpoint: srv.URL,
			Cookies:  CookieConfig{Source: "browser"},
		},
		client:     cl,
		authValues: map[string]string{"csrf_token": "INITIAL"},
	}
	auth := &AuthConfig{
		PreflightURL: targetURL,
		Extractions:  map[string]Extraction{"csrf_token": {Pattern: `csrf_token=(\w+)`}},
	}

	type outcome struct {
		vals map[string]string
		ok   bool
	}
	done := make(chan outcome, 1)

	// The lock runPreflight would be holding across the whole cascade.
	pc.authMu.Lock()
	go func() {
		vals, ok := tryBrowserCookieRetry(context.Background(), pc, auth)
		done <- outcome{vals, ok}
	}()

	select {
	case res := <-done:
		if !res.ok {
			pc.authMu.Unlock()
			t.Fatal("fixture failure: Tier 3a did not succeed, so the commit path — the only path that ever deadlocked — was never reached")
		}
		commitAuthValuesLocked(pc, res.vals)
		pc.authMu.Unlock()
		if got := pc.authValues["csrf_token"]; got != "TIER3A_TOK" {
			t.Fatalf("committed csrf_token = %q, want TIER3A_TOK", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DEADLOCK: Tier 3a blocked while the caller held pc.authMu — the tiers must not acquire the lock themselves")
	}
}

// TestTier3bWAFSolve_NoDeadlockUnderCallerHeldWriteLock is the Tier-3b half of
// the contract. Like Tier 3a, only a *successful* solve reaches the commit, so
// the pre-existing Tier-3b tests — all failure paths — could not have caught
// the deadlock.
//
// Reaching success needs the WAF challenge to actually run through the JS
// engine. The fixture is modelled on TestSolver_AwsWafIntegrationShim in
// internal/waf: a fake challenge.js that installs AwsWafIntegration.getToken()
// and sets the token cookie. The one addition is a host-switching transport,
// because tryWAFSolve issues two different requests on the same client — the
// challenge fetch and then the preflight retry — and the waf package's
// single-target rewriteTransport would send both to the challenge server.
func TestTier3bWAFSolve_NoDeadlockUnderCallerHeldWriteLock(t *testing.T) {
	challenge := `
		window.AwsWafIntegration = {
			getToken: function () {
				return new Promise(function (resolve) {
					setTimeout(function () {
						document.cookie = "aws-waf-token=TIER3B_TOKEN; Path=/";
						resolve("TIER3B_TOKEN");
					}, 5);
				});
			}
		};
	`
	challengeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = fmt.Fprint(w, challenge)
	}))
	defer challengeSrv.Close()

	preflightSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html>csrf_token=TIER3B_TOK</html>`)
	}))
	defer preflightSrv.Close()

	page := `<html><body>
<script>window.gokuProps={};</script>
<script src="https://fake.awswaf.com/challenge.js"></script>
</body></html>`

	cl := &http.Client{
		Transport: &hostSwitchTransport{
			wafTarget:      challengeSrv.URL + "/challenge.js",
			fallbackTarget: preflightSrv.URL,
		},
		Timeout: 5 * time.Second,
	}
	cl.Jar = newCookieVault()

	pc := &providerClient{
		config:     &ProviderConfig{ID: "tier3b-deadlock", Name: "T3B", Category: "hotels"},
		client:     cl,
		authValues: map[string]string{"csrf_token": "INITIAL"},
	}
	auth := &AuthConfig{
		PreflightURL: "https://example.com/",
		Extractions:  map[string]Extraction{"csrf_token": {Pattern: `csrf_token=(\w+)`}},
	}

	type outcome struct {
		vals map[string]string
		ok   bool
	}
	done := make(chan outcome, 1)

	// The lock runPreflight would be holding across the whole cascade.
	pc.authMu.Lock()
	go func() {
		vals, ok := tryWAFSolve(context.Background(), pc, auth, http.StatusAccepted, []byte(page))
		done <- outcome{vals, ok}
	}()

	select {
	case res := <-done:
		if !res.ok {
			pc.authMu.Unlock()
			t.Fatal("fixture failure: Tier 3b did not succeed, so the commit path — the only path that ever deadlocked — was never reached")
		}
		commitAuthValuesLocked(pc, res.vals)
		pc.authMu.Unlock()
		if got := pc.authValues["csrf_token"]; got != "TIER3B_TOK" {
			t.Fatalf("committed csrf_token = %q, want TIER3B_TOK", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DEADLOCK: Tier 3b blocked while the caller held pc.authMu — the tiers must not acquire the lock themselves")
	}
}

// hostSwitchTransport routes *.awswaf.com to the fake challenge script and
// everything else to the preflight server, so one client can serve both legs of
// tryWAFSolve.
type hostSwitchTransport struct {
	wafTarget      string
	fallbackTarget string
}

func (t *hostSwitchTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target := t.fallbackTarget
	if strings.Contains(req.URL.Host, "awswaf.com") {
		target = t.wafTarget
	}
	proxied, err := http.NewRequest(req.Method, target, req.Body)
	if err != nil {
		return nil, err
	}
	proxied.Header = req.Header.Clone()
	// Carry the caller's context across the rewrite. Without this the client's
	// timeout — which is implemented as a context deadline — is silently
	// dropped, and a hung server would be caught only by the test watchdog,
	// which reports a deadlock. A slow network would then be misreported as a
	// lock bug.
	proxied = proxied.WithContext(req.Context())
	return http.DefaultTransport.RoundTrip(proxied)
}

// TestRecoveryTiersDoNotReferenceAuthMu is the structural half of the same
// contract. The three behavioural tests each pin one tier; this one pins the
// property itself, for all four tiers and for tiers added later, which no
// per-tier fixture can do.
//
// It is a weaker instrument — it reads text, so it only sees lock acquisitions
// it can recognise — but the self-locking helper set it checks against is
// derived from the package and closed transitively, so it does not depend on
// anyone remembering to update it, and it does not care how many calls deep the
// acquisition sits.
func TestRecoveryTiersDoNotReferenceAuthMu(t *testing.T) {
	src, err := os.ReadFile("auth.go")
	if err != nil {
		t.Fatalf("read auth.go: %v", err)
	}
	helpers := selfLockingHelpers(t)
	tiers := []string{
		"tryBrowserCookieRetry",
		"tryWAFSolve",
		"tryBrowserEscapeHatch",
		"finishEscapeHatch",
	}
	for _, name := range tiers {
		re := regexp.MustCompile(`(?ms)^func ` + name + `\(.*?\n\}`)
		body := re.Find(src)
		if body == nil {
			t.Fatalf("could not locate func %s in auth.go — if it moved, move this guard with it", name)
		}
		if regexp.MustCompile(`authMu`).Match(body) {
			t.Errorf("%s references authMu. The recovery tiers run under two different lock disciplines "+
				"(runPreflight holds the write lock; the search-path recovery holds nothing), so a tier that "+
				"locks is correct for only one caller and deadlocks the other. Return the values and let the "+
				"caller commit.", name)
		}
		// The original defect did NOT name the mutex inside the tier: it called
		// replaceAuthValuesLocked, whose acquisition lived elsewhere. Grepping
		// for authMu alone would have missed the very bug this file exists to
		// prevent, so every function that takes the lock itself is checked too.
		// A tier that calls one of these deadlocks under runPreflight's held
		// write lock exactly as the original did.
		for _, helper := range helpers {
			if helper == name {
				continue // the direct authMu check above already covers this
			}
			if regexp.MustCompile(`\b` + helper + `\(`).Match(body) {
				t.Errorf("%s calls %s, which acquires pc.authMu — directly, or through something it calls. That is the shape of the original "+
					"deadlock — the lock was never named in the tier, it was taken inside the helper the tier "+
					"called. Use the caller-locked variant, or return the values and let the caller commit.",
					name, helper)
			}
		}
	}
}

// TestRunPreflightCriticalSectionTakesNoLock guards the other end of the same
// contract. TestRecoveryTiersDoNotReferenceAuthMu pins the tiers; this pins the
// caller. runPreflight takes pc.authMu for writing and holds it across the whole
// cascade, so from that acquisition to the end of the function, every call must
// be lock-free or caller-locked — a self-locking one re-enters a
// non-reentrant sync.RWMutex and hangs the process.
//
// Why this exists on top of the behavioural tests: only one of the three commit
// sites (Tier 3b) can be driven end-to-end from a test — 3a needs a real browser
// profile and Tier 4 needs an interactive terminal. Swapping the commit at the
// other two sites for the self-locking variant would reintroduce the exact
// shipped bug with nothing failing. This reads text instead of executing it,
// which is the weaker instrument, but it covers all three sites and any site
// added later.
func TestRunPreflightCriticalSectionTakesNoLock(t *testing.T) {
	src, err := os.ReadFile("auth.go")
	if err != nil {
		t.Fatalf("read auth.go: %v", err)
	}
	body := regexp.MustCompile(`(?ms)^func \(rt \*Runtime\) runPreflight\(.*?\n\}`).Find(src)
	if body == nil {
		t.Fatal("could not locate runPreflight in auth.go — if it moved, move this guard with it")
	}
	// The write lock is taken partway down; everything before it is the
	// fast-path read and is allowed to lock for itself.
	acq := regexp.MustCompile(`pc\.authMu\.Lock\(\)`).FindIndex(body)
	if acq == nil {
		t.Fatal("runPreflight no longer takes pc.authMu for writing. If the lock discipline changed, " +
			"this guard and the tier guard both describe a contract that no longer exists — rewrite them " +
			"rather than deleting them, because the tiers are still shared with the lock-free search path.")
	}
	critical := body[acq[1]:]

	// Anti-vacuity: the critical section must actually contain the commits this
	// guard is about. If a refactor moves them out, the guard would pass on an
	// empty region and quietly stop protecting anything.
	if !regexp.MustCompile(`commitAuthValuesLocked\(`).Match(critical) {
		t.Fatal("no commitAuthValuesLocked call inside runPreflight's critical section — either the " +
			"cascade's commits moved, or the region this guard slices is wrong. Either way it is no " +
			"longer checking what it claims to check.")
	}

	for _, helper := range selfLockingHelpers(t) {
		// runPreflight is deliberately NOT skipped here even though it is the
		// function holding the lock. Its own name cannot appear in this slice by
		// accident — the declaration sits above the acquisition — so a match
		// means a recursive call, which takes the same non-reentrant mutex and
		// hangs exactly like the original bug.
		if regexp.MustCompile(`\b` + helper + `\(`).Match(critical) {
			t.Errorf("runPreflight calls %s while holding pc.authMu for writing. That helper acquires the "+
				"same mutex — directly, or through something it calls — and sync.RWMutex is not reentrant, "+
				"so this deadlocks every successful recovery. Use the caller-locked variant (the Locked "+
				"suffix means the caller already holds the lock, never that the function locks for you).",
				helper)
		}
	}
}

// funcBodies splits gofmt'd Go source into (name, body) pairs, one per
// top-level func. The terminator is the first `}` in column 0, which is exact
// for gofmt'd code — with one caveat worth knowing before you edit auth.go: a
// backtick raw string containing a line that starts with `}` would truncate
// that body early and under-inspect the rest of it. No such literal exists in
// this package today; if you add one, this splitter needs a real parser.
//
// Keys are bare function names, so two same-named methods on different types
// would collide and the last one scanned would win. Nothing in this package does
// that today, and the anti-vacuity pin below means a collision cannot silently
// empty the helper set — but it is not full coverage, so do not assume it is.
func funcBodies(src []byte) map[string][]byte {
	out := map[string][]byte{}
	// Receiver is optional, so this catches both methods and plain funcs.
	re := regexp.MustCompile(`(?ms)^func (?:\([^)]*\) )?(\w+)\(.*?\n\}`)
	for _, m := range re.FindAllSubmatch(src, -1) {
		out[string(m[1])] = m[0]
	}
	return out
}

// selfLockingHelpers returns every function in the package that acquires
// pc.authMu — directly, or by calling something that does, to any depth.
//
// This is DERIVED, not hand-written, and that is the whole point. An earlier
// revision listed the three helpers by hand; a reviewer correctly observed that
// a hand-written list has exactly the weakness it was written to fix — it only
// catches lock acquisitions somebody remembered to name. A helper added to this
// package next month would not be in it, and the guard would silently pass a
// tier that calls the new helper.
//
// The transitive step closes the same hole one level deeper. The original bug
// was already indirect: the tier named no mutex, it called a helper that took
// one. A guard that only followed one hop would miss the identical bug wearing
// one more layer — tier calls A, A calls commitAuthValues — which is a shape a
// refactor produces by accident, not by malice. Iterating to a fixpoint means
// depth does not matter.
func selfLockingHelpers(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	bodies := map[string][]byte{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for fn, body := range funcBodies(src) {
			bodies[fn] = body
		}
	}

	acquires := regexp.MustCompile(`authMu\.(RLock|Lock)\(\)`)
	locking := map[string]bool{}
	for fn, body := range bodies {
		if acquires.Match(body) {
			locking[fn] = true
		}
	}
	// Fixpoint: anything calling a locker is itself a locker.
	for changed := true; changed; {
		changed = false
		for fn, body := range bodies {
			if locking[fn] {
				continue
			}
			for target := range locking {
				if target == fn {
					continue
				}
				if regexp.MustCompile(`\b` + target + `\(`).Match(body) {
					locking[fn] = true
					changed = true
					break
				}
			}
		}
	}

	names := make([]string, 0, len(locking))
	for fn := range locking {
		names = append(names, fn)
	}
	sort.Strings(names)

	// A derivation that silently yields nothing would turn this guard into a
	// test that cannot fail. Pin the helpers we know exist so a broken splitter
	// or a moved file fails loudly instead of passing vacuously.
	for _, must := range []string{"commitAuthValues", "snapshotAuthValues", "discardBrowserSeededAuth"} {
		if !slices.Contains(names, must) {
			t.Fatalf("derivation failed: %s acquires pc.authMu but was not found (got %v). "+
				"Fix the scan before trusting this guard — an empty set passes everything.", must, names)
		}
	}
	t.Logf("derived self-locking set (%d): %v", len(names), names)
	return names
}

// Regression: the auth recovery tiers must never take pc.authMu themselves.
//
// runPreflight holds pc.authMu for writing across the entire Tier-3/3b/4
// cascade — deliberately, since that write lock is what stops two concurrent
// searches from both opening a browser window. The tiers used to commit their
// recovered values by calling replaceAuthValuesLocked, which acquired the same
// non-reentrant sync.RWMutex. Every *successful* recovery therefore blocked
// forever, still holding the write lock, wedging every later search for that
// provider too.
//
// The fix splits extraction from the commit: tiers return their values and the
// caller commits under whichever discipline it already has. This test pins that
// contract by running the Tier-4 tail exactly as runPreflight composes it —
// from inside a caller-held write lock — and failing on a timeout rather than
// hanging the suite.
func TestRecoveryTiers_NoDeadlockUnderCallerHeldWriteLock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html>csrf_token=FRESH_TOK</html>`)
	}))
	defer srv.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	pc := &providerClient{
		config:     &ProviderConfig{ID: "deadlock-prov"},
		client:     &http.Client{Jar: jar},
		authValues: map[string]string{"csrf_token": "INITIAL"},
	}
	auth := &AuthConfig{
		PreflightURL: srv.URL,
		Extractions: map[string]Extraction{
			"csrf_token": {Pattern: `csrf_token=(\w+)`},
		},
	}

	// Exactly what runPreflight does before entering the cascade.
	pc.authMu.Lock()

	type result struct {
		vals map[string]string
		ok   bool
	}
	done := make(chan result, 1)
	go func() {
		vals, ok := finishEscapeHatch(context.Background(), pc, auth, nil)
		done <- result{vals, ok}
	}()

	select {
	case got := <-done:
		if !got.ok {
			pc.authMu.Unlock()
			t.Fatal("finishEscapeHatch returned false on a clean 2xx preflight")
		}
		if got.vals["csrf_token"] != "FRESH_TOK" {
			pc.authMu.Unlock()
			t.Fatalf("extracted csrf_token = %q, want FRESH_TOK", got.vals["csrf_token"])
		}
		// The caller owns the commit — the whole point of the split.
		commitAuthValuesLocked(pc, got.vals)
		pc.authMu.Unlock()
	case <-time.After(2 * time.Second):
		t.Fatal("DEADLOCK: a recovery tier blocked while the caller held pc.authMu — " +
			"the tiers must not acquire the lock themselves")
	}

	if got := snapshotAuthValues(pc)["csrf_token"]; got != "FRESH_TOK" {
		t.Errorf("committed csrf_token = %q, want FRESH_TOK", got)
	}
}

// TestRunPreflight_SuccessfulRecoveryDoesNotDeadlock drives the whole
// composition — runPreflight taking the write lock, the cascade running under
// it, and the commit — instead of one tier in isolation.
//
// It exists because every test above it could not catch the bug in its real
// shape, which an outside review pointed out and I had missed. Those tests call
// a tier directly and then perform the commit themselves with the caller-locked
// variant, so they hardcode the very thing that has to be right. Swap
// commitAuthValuesLocked for the self-locking commitAuthValues at auth.go:116,
// 124 or 133 and runPreflight deadlocks against the lock it took at auth.go:65
// — while all three tier tests still pass and the structural guard stays quiet,
// because it scans the tier bodies, not runPreflight's.
//
// Verified by sabotage: with that one-token swap at the Tier-3b call site this
// test fails on its timeout; reverted, it passes.
//
// Tier 3b is the tier driven here because it is the only one reachable without
// a real browser: the preflight server answers 202 with a WAF challenge page,
// the JS engine solves it against the fake challenge script, and the retry
// succeeds.
func TestRunPreflight_SuccessfulRecoveryDoesNotDeadlock(t *testing.T) {
	// Pin which tier does the recovering. Tier 3a runs first and reads the
	// machine's real browser cookie stores, so on a developer laptop that
	// happened to hold a cookie for the fixture host, 3a could satisfy the
	// retry and this test would pass having never entered the WAF path it
	// claims to exercise. Declining browser reads makes 3a return false
	// deterministically and leaves Tier 3b — the only tier reachable without a
	// real browser or a terminal — as the sole recovery path.
	t.Setenv(consent.CookiesEnv, "1")

	// A successful recovery persists the session to the on-disk cookie cache,
	// which lives under the home directory. Without this redirect the probe
	// writes its synthetic token into the developer's real cache, next to their
	// live provider sessions — and would overwrite one outright if a fixture host
	// ever collided with a real provider's. A test must not touch state it did
	// not create.
	// One directory for both variables: os.UserHomeDir reads HOME on unix and
	// USERPROFILE on Windows, and the probe must land in the same place either way.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	challenge := `
		window.AwsWafIntegration = {
			getToken: function () {
				return new Promise(function (resolve) {
					setTimeout(function () {
						document.cookie = "aws-waf-token=PREFLIGHT_TOKEN; Path=/";
						resolve("PREFLIGHT_TOKEN");
					}, 5);
				});
			}
		};
	`
	challengeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = fmt.Fprint(w, challenge)
	}))
	defer challengeSrv.Close()

	// The first preflight is challenged; the post-solve retry succeeds. Without
	// the challenge on the first hit the cascade never runs and this test would
	// pass vacuously, so the counter below is load-bearing, not decoration.
	var hits int32
	preflightSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusAccepted)
			_, _ = fmt.Fprint(w, `<html><body>
<script>window.gokuProps={};</script>
<script src="https://fake.awswaf.com/challenge.js"></script>
</body></html>`)
			return
		}
		_, _ = fmt.Fprint(w, `<html>csrf_token=RECOVERED</html>`)
	}))
	defer preflightSrv.Close()

	dir := t.TempDir()
	reg, _ := NewRegistryAt(dir)
	rt := NewRuntime(reg)

	cl := &http.Client{
		Transport: &hostSwitchTransport{
			wafTarget:      challengeSrv.URL + "/challenge.js",
			fallbackTarget: preflightSrv.URL,
		},
		Timeout: 5 * time.Second,
	}
	cl.Jar = newCookieVault()

	pc := &providerClient{
		config: &ProviderConfig{
			ID: "preflight-deadlock", Name: "PD", Category: "hotels",
			Endpoint: preflightSrv.URL,
			Auth: &AuthConfig{
				Type:         "preflight",
				PreflightURL: "https://example.com/",
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
		if res.snap["csrf_token"] != "RECOVERED" {
			t.Fatalf("recovered csrf_token = %q, want RECOVERED", res.snap["csrf_token"])
		}
		// Which tier recovered matters. The retry counter above proves only that
		// *some* tier retried; this proves it was Tier 3b, because the token can
		// only come from running the fake challenge script in the JS engine.
		u, _ := url.Parse("https://example.com/")
		var solved bool
		for _, c := range cl.Jar.Cookies(u) {
			if c.Name == "aws-waf-token" && c.Value == "PREFLIGHT_TOKEN" {
				solved = true
			}
		}
		if !solved {
			t.Fatal("fixture failure: no solved aws-waf-token in the jar, so the WAF tier did not run — " +
				"whatever produced the successful retry, it was not the path this test claims to cover")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("DEADLOCK: runPreflight blocked while committing a successful recovery — " +
			"a tier or the commit is taking pc.authMu that runPreflight already holds")
	}
}

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

	// Substitute the seam. Restoring it is not optional: the var is
	// package-global and a leaked stub would answer for every later test.
	prevResolve := headlessFirstResolve
	t.Cleanup(func() { headlessFirstResolve = prevResolve })

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
	go func() {
		// Tier 4 is gated on the interactive opt-in as well as the config flag;
		// a background context would skip the tier entirely.
		snap, err := rt.runPreflight(WithInteractive(context.Background()), pc, map[string]string{})
		done <- outcome{snap, err}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("runPreflight: %v", res.err)
		}
		if n := atomic.LoadInt32(&resolveCalls); n == 0 {
			t.Fatal("fixture failure: the escape hatch was never entered, so Tier 4 did not run — " +
				"an earlier tier recovered and this test proved nothing about auth.go:133")
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
