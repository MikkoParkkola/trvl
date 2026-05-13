package providers

// Regression test for MIK-3070: race on provider auth cache during city-switch.
//
// Before the fix, two concurrent searches to different cities sharing the same
// providerClient could see each other's auth values: thread A would call
// runPreflight (city=PRG), get back nil, then later read pc.authValues at the
// auth-vars substitution site — but by then thread B (city=PAR) had completed
// its preflight and overwritten pc.authValues with PAR's values. Thread A
// would then send PAR's csrf_token to PRG's endpoint.
//
// The fix changes runPreflight to return an immutable snapshot of authValues
// bound to the URL it just preflighted. The caller uses the snapshot, not
// pc.authValues. This test verifies the snapshot binding under contention.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestRunPreflight_SnapshotIsCityBound (MIK-3070).
//
// Two parallel runPreflight calls to URLs that return distinct tokens. Asserts
// that each call's returned snapshot contains the token from its own URL —
// never the other URL's token, regardless of who finished first. Failure mode
// before the fix: the second-to-finish caller would observe the first caller's
// values via pc.authValues read after the lock window.
func TestRunPreflight_SnapshotIsCityBound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a token derived from the path, so URL=/prg gets csrf=PRG_TOK
		// and URL=/par gets csrf=PAR_TOK.
		city := "OTHER"
		switch {
		case strings.Contains(r.URL.Path, "/prg"):
			city = "PRG"
		case strings.Contains(r.URL.Path, "/par"):
			city = "PAR"
		}
		_, _ = fmt.Fprintf(w, `<html>csrf_token=%s_TOK</html>`, city)
	}))
	defer srv.Close()

	dir := t.TempDir()
	reg, _ := NewRegistryAt(dir)
	rt := NewRuntime(reg)

	// Single config with a city-substituted preflight URL — both goroutines
	// share this config and the underlying providerClient (pc) so they race
	// on pc.authValues / pc.lastPreflightURL.
	cfg := &ProviderConfig{
		ID:       "race-prov",
		Name:     "Race",
		Category: "hotels",
		Endpoint: srv.URL,
		Auth: &AuthConfig{
			Type:         "preflight",
			PreflightURL: srv.URL + "/${city}",
			Extractions: map[string]Extraction{
				"csrf": {Pattern: `csrf_token=([A-Z_0-9]+)`, Variable: "csrf_token"},
			},
		},
	}

	jar, _ := cookiejar.New(nil)
	cl := srv.Client()
	cl.Jar = jar
	pc := &providerClient{
		config:     cfg,
		client:     cl,
		authValues: make(map[string]string),
	}

	const iterations = 50
	var wg sync.WaitGroup
	wg.Add(2 * iterations)
	mismatches := make(chan string, 4*iterations)

	worker := func(city, want string) {
		defer wg.Done()
		vars := map[string]string{"${city}": city}
		snap, err := rt.runPreflight(context.Background(), pc, vars)
		if err != nil {
			mismatches <- fmt.Sprintf("worker(%s): preflight error: %v", city, err)
			return
		}
		got := snap["csrf_token"]
		if got != want {
			mismatches <- fmt.Sprintf("worker(%s): snapshot csrf=%q want %q", city, got, want)
		}
	}

	for i := 0; i < iterations; i++ {
		go worker("prg", "PRG_TOK")
		go worker("par", "PAR_TOK")
	}
	wg.Wait()
	close(mismatches)

	for m := range mismatches {
		t.Error(m)
	}
}
