package providers

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TRVL.FIXHINT.1 -- a fix hint must not prescribe a tool that always fails.
//
// #538 made provider definitions source-only, so configure_provider now returns
// an error unconditionally. Three FixHints still told the operator (and, more
// often, an agent acting on ProviderStatus.FixHint) to call it: on a WAF block,
// on an expired cookie, and on a changed response shape.
//
// That is worse than a stale doc. These strings are returned on the failure
// path the trust-boundary change made MORE common, and an agent that treats
// them as remediation gets a guaranteed error loop with nothing to learn from.
//
// Found by adversarial review of the release delta, after the CHANGELOG,
// LEGAL.md, DESIGN.md and the MCP setup prompt had all been corrected. The
// runtime hints were a layer below the docs I went looking for.
func TestFixHintsDoNotPrescribeConfigureProvider(t *testing.T) {
	// Messages chosen to hit each branch of classifyProviderError that used to
	// name the tool, plus the preflight branch next to them.
	for _, tc := range []struct {
		probe string
		code  FixHintCode
	}{
		{"waf: 403 forbidden", FixHintAkamaiBlock},
		{"access denied by akamai", FixHintAkamaiBlock},
		{"cookie rejected: 401 unauthorized", FixHintCookieExpired},
		{"csrf token mismatch", FixHintCookieExpired},
		{"results_path produced no match", FixHintResponseShapeChanged},
		{"response shape changed: unexpected end of json", FixHintResponseShapeChanged},
		{"preflight failed", FixHintPreflightFailed},
	} {
		code, hint := classifyProviderError(errString(tc.probe))
		if code != tc.code {
			t.Errorf("classifyProviderError(%q) code = %q, want %q", tc.probe, code, tc.code)
		}
		if strings.Contains(hint, "configure_provider") {
			t.Errorf("the fix hint for %q tells the caller to use configure_provider, which "+
				"returns an error unconditionally since #538. An agent following it loops on a "+
				"refusal it cannot fix.\n  hint: %s", tc.probe, hint)
		}
	}
}

// The hint says the next search re-reads browser cookies. Prove the behavior,
// not just the prose: a completed warm entry supplies a stale cookie, an auth
// failure invalidates both that entry and the seeded jar, and the next lookup
// reaches the direct reader and receives the fresh cookie.
func TestCookieExpiredHintForcesNextBrowserCookieRead(t *testing.T) {
	target := "https://www.example.com/search"
	browser := "chrome"
	InvalidateWarmCacheForSite(target, browser)
	t.Cleanup(func() { InvalidateWarmCacheForSite(target, browser) })

	originalReader := browserCookieReader
	reads := 0
	browserCookieReader = func(string, string) []*http.Cookie {
		reads++
		value := "stale"
		if reads > 1 {
			value = "fresh"
		}
		return []*http.Cookie{{Name: "session", Value: value, Domain: ".example.com", Path: "/"}}
	}
	t.Cleanup(func() { browserCookieReader = originalReader })

	WarmBrowserCookies(target, browser)
	stale := warmBrowserCookiesResult(target, browser, time.Second)
	if len(stale) != 1 || stale[0].Value != "stale" || reads != 1 {
		t.Fatalf("warm fixture = %#v, reads=%d", stale, reads)
	}

	cfg := &ProviderConfig{ID: "example", Endpoint: target}
	cfg.Cookies.Source = "browser"
	cfg.Cookies.Browser = browser
	vault := newCookieVault()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	if !vault.seedFromBrowser(u, stale) {
		t.Fatal("failed to seed stale browser session")
	}
	pc := &providerClient{
		config:     cfg,
		client:     &http.Client{Jar: vault},
		authValues: map[string]string{"csrf": "stale"},
		authExpiry: time.Now().Add(time.Hour),
	}
	rt := &Runtime{clients: map[string]*providerClient{cfg.ID: pc}}

	rt.invalidateBrowserSessionAfterAuthFailure(cfg, FixHintCookieExpired)
	if pc.isBrowserSeeded() {
		t.Fatal("failed browser session remained in the provider jar")
	}
	if len(pc.authValues) != 0 || !pc.authExpiry.IsZero() {
		t.Fatalf("failed auth cache survived: values=%v expiry=%v", pc.authValues, pc.authExpiry)
	}

	fresh := browserCookiesForURLWithHint(target, browser)
	if len(fresh) != 1 || fresh[0].Value != "fresh" {
		t.Fatalf("next lookup reused stale warm cookies: %#v", fresh)
	}
	if reads != 2 {
		t.Fatalf("direct browser reads = %d, want 2 (warm read plus forced refresh)", reads)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
