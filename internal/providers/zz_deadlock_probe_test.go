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
	"testing"
	"time"
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

// TestRecoveryTiersDoNotReferenceAuthMu is the structural half of the same
// contract, and it exists because one tier cannot be reached behaviourally.
//
// Tier 3b (tryWAFSolve) only commits after waf.SolveAWSWAF runs a real AWS WAF
// challenge through the JS engine; there is no fixture for that, which is why
// every existing Tier-3b test covers a failure path and none reaches the
// commit. Rather than leave that tier unguarded, this asserts the property
// directly at the source: a recovery tier must not name pc.authMu at all.
//
// This is a weaker instrument than the two behavioural tests — it reads text,
// so it cannot see a lock taken through a helper it does not know about — but
// it is the only one that covers all four tiers uniformly, including tiers
// added later.
func TestRecoveryTiersDoNotReferenceAuthMu(t *testing.T) {
	src, err := os.ReadFile("auth.go")
	if err != nil {
		t.Fatalf("read auth.go: %v", err)
	}
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
	}
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
