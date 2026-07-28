package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"
)

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
//
// NOTE: this file should be renamed to auth_deadlock_test.go. It began as a
// throwaway probe; the rename is pending because deleting it was blocked by a
// safety guard mid-session, and routing around that guard is not something to
// do for a filename.
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

	if got := snapshotAuthValuesLocked(pc)["csrf_token"]; got != "FRESH_TOK" {
		t.Errorf("committed csrf_token = %q, want FRESH_TOK", got)
	}
}
