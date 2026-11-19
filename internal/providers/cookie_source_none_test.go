package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

// stubBrowserReader replaces the browser cookie store reader with one that
// counts its calls and hands back a cookie for host, so a test can tell "never
// read" from "read, then dropped".
func stubBrowserReader(t *testing.T, domain string) *atomic.Int32 {
	t.Helper()
	resetWarmCache(t)
	var reads atomic.Int32
	original := browserCookieReader
	browserCookieReader = func(string, string) []*http.Cookie {
		reads.Add(1)
		return []*http.Cookie{{Name: "sid", Value: "syn123", Domain: domain, Path: "/"}}
	}
	t.Cleanup(func() { browserCookieReader = original })
	return &reads
}

// A provider that declares cookies.source "none" must never get the user's
// browser cookies, not even from a challenge-recovery retry (#840). An empty
// source and "preflight" keep reading them: configure_provider fills an empty
// source with "preflight", and Booking's skeleton-built config relies on the
// recovery read.
func TestApplyBrowserCookies_HonoursSourceNone(t *testing.T) {
	const targetURL = "https://www.testprovider-synth.com/search"
	for source, want := range map[string]bool{
		"none":      false,
		"":          true,
		"preflight": true,
		"browser":   true,
	} {
		t.Run("source="+source, func(t *testing.T) {
			reads := stubBrowserReader(t, ".testprovider-synth.com")
			vault := newCookieVault()
			pc := &providerClient{
				config: &ProviderConfig{ID: "t", Endpoint: "https://www.testprovider-synth.com/api", Cookies: CookieConfig{Source: source, Browser: "chrome"}},
				client: &http.Client{Jar: vault},
			}
			if got := applyBrowserCookies(pc, targetURL, "chrome"); got != want {
				t.Fatalf("applyBrowserCookies = %v, want %v", got, want)
			}
			if got := reads.Load(); (got > 0) != want {
				t.Errorf("browser store read %d times, want reads only when allowed", got)
			}
			u, _ := url.Parse(targetURL)
			if got := len(vault.Cookies(u)); (got > 0) != want {
				t.Errorf("jar holds %d cookies, want cookies only when the read is allowed", got)
			}
		})
	}
}

// The recovery path end to end: a WAF challenge on a provider that declares
// cookies.source "none" must not read the browser store, while the same
// challenge on a provider with no declared source still does.
func TestChallengeRecoveryHonoursSourceNone(t *testing.T) {
	t.Setenv(AllowLocalEnv, "1")
	// Both challenge bodies carry no challenge.js URL, so the WAF solver gives
	// up without a network fetch. The empty one is Booking's answer (#837).
	for _, body := range []string{`<html><script>window.aws = {}</script></html>`, ""} {
		for source, wantRead := range map[string]bool{"none": false, "": true} {
			t.Run(fmt.Sprintf("source=%q/body=%dB", source, len(body)), func(t *testing.T) {
				testChallengeRecoveryReads(t, source, body, wantRead)
			})
		}
	}
}

func testChallengeRecoveryReads(t *testing.T, source, body string, wantRead bool) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	rt, cfg := newCanaryProvider(t, srv.URL)
	cfg.Cookies = CookieConfig{Source: source, Browser: "chrome"}
	u, _ := url.Parse(srv.URL)
	reads := stubBrowserReader(t, u.Hostname())

	if _, err := rt.searchProvider(context.Background(), cfg, "Kyoto", 35.0, 135.7, "2026-06-01", "2026-06-03", "EUR", 2, nil); err == nil {
		t.Fatal("searchProvider succeeded on a challenge page")
	}
	if got := reads.Load(); (got > 0) != wantRead {
		t.Errorf("browser store read %d times during challenge recovery, want read=%v", got, wantRead)
	}
}
