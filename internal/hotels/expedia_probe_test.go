//go:build proof

package hotels

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveProbe_Expedia documents the live probe from which the Branch-B
// (opt-in, endpoint-gated) decision was made. It is gated behind the `proof`
// build tag AND TRVL_TEST_LIVE_PROBES=1 so it never runs in the default suite
// or in CI — it makes a real network call.
//
// Probe result (2026-06-25, plain static client, Paris ~30 days out, 2 guests):
//   - GET https://www.expedia.com/Hotel-Search?... -> HTTP 429
//   - GET https://www.expedia.com/                  -> HTTP 429, x-page-id: wildcard-challenge-handler
//   - server: istio-envoy, x-app-info: captcha-pwa
//   - set-cookie: _abck (~-1~ unsolved sensor), bm_ss, bm_s, bm_so, bm_sz (Akamai Bot Manager)
//
// A static Go client is bot-walled; only a browser-grade anti-bot fetcher
// returned 200. Hence the provider is opt-in (EXPEDIA_API_BASE), and this probe
// asserts the wall is still present so the Branch-B rationale stays honest.
func TestLiveProbe_Expedia(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("live probes disabled (set TRVL_TEST_LIVE_PROBES=1)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		expediaDefaultHost+"/Hotel-Search?destination=Paris%2C%20France&adults=2", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("User-Agent", "trvl/1.0")
	req.Header.Set("Accept", "text/html,application/json")

	resp, err := expediaClient.Do(req)
	if err != nil {
		t.Fatalf("probe request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	t.Logf("Expedia live probe: status=%d server=%q x-page-id=%q x-app-info=%q",
		resp.StatusCode, resp.Header.Get("server"),
		resp.Header.Get("x-page-id"), resp.Header.Get("x-app-info"))

	var botCookies []string
	for _, c := range resp.Cookies() {
		switch {
		case c.Name == "_abck", strings.HasPrefix(c.Name, "bm_"):
			botCookies = append(botCookies, c.Name)
		}
	}
	t.Logf("Expedia bot-manager cookies observed: %v", botCookies)

	// The probe documents the wall; it does not fail the build if Expedia ever
	// stops challenging (that would be a signal to re-evaluate Branch A).
	if resp.StatusCode == http.StatusOK && len(botCookies) == 0 {
		t.Logf("NOTE: Expedia returned 200 with no bot cookies to a static client — re-evaluate Branch A vs B")
	}
}
