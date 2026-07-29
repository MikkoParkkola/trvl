package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCookieDomainMatchesHost(t *testing.T) {
	cases := []struct {
		name         string
		cookieDomain string
		host         string
		want         bool
	}{
		{"exact", "booking.com", "booking.com", true},
		{"dot prefix parent", ".booking.com", "www.booking.com", true},
		{"parent no dot", "booking.com", "www.booking.com", true},
		{"unrelated", "example.com", "booking.com", false},
		{"suffix only collision", "oking.com", "booking.com", false},
		{"empty cookie", "", "booking.com", false},
		{"empty host", "booking.com", "", false},
		{"subdomain mismatch", "api.booking.com", "booking.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cookieDomainMatchesHost(tc.cookieDomain, tc.host); got != tc.want {
				t.Errorf("cookieDomainMatchesHost(%q, %q) = %v, want %v", tc.cookieDomain, tc.host, got, tc.want)
			}
		})
	}
}

func TestRegistrableSuffix(t *testing.T) {
	cases := map[string]string{
		"booking.com":          "booking.com",
		"www.booking.com":      "booking.com",
		"a.b.c.booking.com":    "booking.com",
		"localhost":            "localhost",
		".leading.booking.com": "booking.com",
	}
	for in, want := range cases {
		if got := registrableSuffix(in); got != want {
			t.Errorf("registrableSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNeedsBrowserCookieFallback(t *testing.T) {
	extractions := map[string]Extraction{"csrf": {Pattern: `x`, Variable: "csrf"}}
	cases := []struct {
		name       string
		status     int
		extracted  int
		extractors map[string]Extraction
		want       bool
	}{
		{"200 all matched", 200, 1, extractions, false},
		{"200 none matched", 200, 0, extractions, true},
		{"202 challenge", 202, 0, extractions, true},
		{"403 forbidden", 403, 0, extractions, true},
		{"202 but matched", 202, 1, extractions, true},
		{"200 no extractions", 200, 0, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := needsBrowserCookieFallback(tc.status, tc.extracted, tc.extractors)
			if got != tc.want {
				t.Errorf("needsBrowserCookieFallback(%d, %d, %v) = %v, want %v",
					tc.status, tc.extracted, tc.extractors, got, tc.want)
			}
		})
	}
}

// TestApplyBrowserCookies_NilJar ensures the helper fails safely when no
// cookie jar is configured.
func TestApplyBrowserCookies_NilJar(t *testing.T) {
	client := &http.Client{}
	if applyBrowserCookies(&providerClient{config: &ProviderConfig{ID: "t"}, client: client}, "https://example.com", "") {
		t.Error("expected false when client has no jar")
	}
}

// TestApplyBrowserCookies_BadURL ensures the helper fails safely on bad URLs.
func TestApplyBrowserCookies_BadURL(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	if applyBrowserCookies(&providerClient{config: &ProviderConfig{ID: "t"}, client: client}, "::not a url::", "") {
		t.Error("expected false for bad URL")
	}
}

// TestBrowserCookiesForURL_BadURL ensures safe handling of malformed URLs.
func TestBrowserCookiesForURL_BadURL(t *testing.T) {
	if got := browserCookiesForURL("::not a url::"); got != nil {
		t.Errorf("expected nil for bad URL, got %d cookies", len(got))
	}
	if got := browserCookiesForURL(""); got != nil {
		t.Errorf("expected nil for empty URL, got %d cookies", len(got))
	}
}

// TestBrowserCookiesForURL_UnknownDomain ensures we don't crash when no
// browser store has cookies for a random domain.
func TestBrowserCookiesForURL_UnknownDomain(t *testing.T) {
	// Serve a random .invalid domain that no browser will have cookies for.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Just ensure the call returns without panicking. Whether it returns
	// cookies depends on the test environment.
	_ = browserCookiesForURL(u.String())
}

// withOpener installs fn as the process-wide openerFunc for the duration of
// the test and restores the original on cleanup. Tests that touch this must
// NOT run in parallel with each other.
func withOpener(t *testing.T, fn openerFunc) {
	t.Helper()
	prev := currentOpenURL
	currentOpenURL = fn
	t.Cleanup(func() { currentOpenURL = prev })
}

// withCookieSource installs fn as the process-wide cookieSourceFunc for the
// duration of the test and restores the original on cleanup.
func withCookieSource(t *testing.T, fn cookieSourceFunc) {
	t.Helper()
	prev := currentCookieSource
	currentCookieSource = fn
	t.Cleanup(func() { currentCookieSource = prev })
}

// TestOpenURLInBrowser_EmptyURL verifies the top-level guard — an empty URL
// short-circuits before any OS dispatch.
func TestOpenURLInBrowser_EmptyURL(t *testing.T) {
	var called bool
	withOpener(t, func(goos, pref, target string) error {
		called = true
		return nil
	})
	if err := openURLInBrowser("   ", ""); err == nil {
		t.Fatal("expected error for empty URL")
	}
	if called {
		t.Fatal("opener must not be invoked for empty URL")
	}
}

// TestOpenURLInBrowser_BadOS verifies the fallback path returns an error for
// unrecognised OS values. We drive this via the injectable openerFunc so we
// don't actually shell out.
func TestOpenURLInBrowser_BadOS(t *testing.T) {
	withOpener(t, func(goos, pref, target string) error {
		// Simulate what defaultOpenURL does for an unknown OS.
		if goos == "plan9" {
			return errors.New("openURLInBrowser: unsupported OS \"plan9\"")
		}
		return nil
	})
	// The default opener reads runtime.GOOS directly, so to test the "bad OS"
	// path we call the underlying defaultOpenURL with an invented GOOS.
	err := defaultOpenURL("plan9", "", "https://example.com")
	if err == nil {
		t.Fatal("expected error for unsupported OS")
	}
}

// TestOpenURLInBrowser_MacPreference verifies that on darwin we pass the
// browser preference through to the opener.
func TestOpenURLInBrowser_MacPreference(t *testing.T) {
	var gotGOOS, gotPref, gotURL string
	withOpener(t, func(goos, pref, target string) error {
		gotGOOS, gotPref, gotURL = goos, pref, target
		return nil
	})
	if err := openURLInBrowser("https://example.com/challenge", "Firefox"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// gotGOOS reflects the real host, we only assert the other two passed
	// through untouched.
	_ = gotGOOS
	if gotURL != "https://example.com/challenge" {
		t.Errorf("target = %q, want example.com/challenge", gotURL)
	}
	// On non-darwin hosts the preference is still forwarded — the production
	// defaultOpenURL ignores it on Linux/Windows.
	if gotPref != "Firefox" {
		// On darwin we fed it explicitly; on other OSs this is an empty
		// string only if the caller passed empty — we passed "Firefox".
		t.Errorf("pref = %q, want Firefox", gotPref)
	}
}

// TestWaitForFreshCookies_TimesOut verifies that when the cookie source keeps
// returning the same snapshot, the helper returns (prev, false) after maxWait.
func TestWaitForFreshCookies_TimesOut(t *testing.T) {
	prev := []*http.Cookie{{Name: "sid", Value: "abc"}}
	withCookieSource(t, func(string) []*http.Cookie {
		return []*http.Cookie{{Name: "sid", Value: "abc"}} // identical each tick
	})

	start := time.Now()
	got, changed := waitForFreshCookies(context.Background(), "https://example.com",
		prev, 20*time.Millisecond, 100*time.Millisecond)
	elapsed := time.Since(start)

	if changed {
		t.Fatal("expected changed=false when snapshot never differs")
	}
	if len(got) != 1 || got[0].Name != "sid" || got[0].Value != "abc" {
		t.Errorf("returned slice should equal prev snapshot, got %+v", got)
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("returned too quickly: %v < 100ms", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("took too long: %v > 500ms", elapsed)
	}
}

// TestWaitForFreshCookies_DetectsChange verifies that a simulated cookie
// change (swap value) causes the helper to return (fresh, true).
func TestWaitForFreshCookies_DetectsChange(t *testing.T) {
	prev := []*http.Cookie{{Name: "sid", Value: "old"}}
	var calls atomic.Int32
	withCookieSource(t, func(string) []*http.Cookie {
		n := calls.Add(1)
		if n < 3 {
			return []*http.Cookie{{Name: "sid", Value: "old"}}
		}
		return []*http.Cookie{{Name: "sid", Value: "new"}, {Name: "csrf", Value: "xyz"}}
	})

	got, changed := waitForFreshCookies(context.Background(), "https://example.com",
		prev, 15*time.Millisecond, 2*time.Second)

	if !changed {
		t.Fatal("expected changed=true after value swap")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 cookies, got %d", len(got))
	}
	var sidVal, csrfVal string
	for _, c := range got {
		switch c.Name {
		case "sid":
			sidVal = c.Value
		case "csrf":
			csrfVal = c.Value
		}
	}
	if sidVal != "new" || csrfVal != "xyz" {
		t.Errorf("unexpected cookies: %+v", got)
	}
}

// TestWaitForFreshCookies_ContextCancel verifies that cancelling ctx aborts
// the wait cleanly and returns (prev, false) without waiting for maxWait.
func TestWaitForFreshCookies_ContextCancel(t *testing.T) {
	prev := []*http.Cookie{{Name: "sid", Value: "same"}}
	withCookieSource(t, func(string) []*http.Cookie {
		return []*http.Cookie{{Name: "sid", Value: "same"}}
	})

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	var (
		got     []*http.Cookie
		changed bool
		elapsed time.Duration
	)
	go func() {
		defer wg.Done()
		start := time.Now()
		got, changed = waitForFreshCookies(ctx, "https://example.com",
			prev, 20*time.Millisecond, 10*time.Second)
		elapsed = time.Since(start)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()

	if changed {
		t.Fatal("expected changed=false on context cancel")
	}
	if len(got) != 1 || got[0].Value != "same" {
		t.Errorf("expected prev snapshot back, got %+v", got)
	}
	if elapsed >= 5*time.Second {
		t.Errorf("cancel did not abort promptly: elapsed=%v", elapsed)
	}
}

// TestCookieSnapshotKey_OrderIndependent verifies the fingerprint is the same
// regardless of cookie ordering so waitForFreshCookies doesn't spuriously
// report "changed" when the browser just reshuffles cookies.
func TestCookieSnapshotKey_OrderIndependent(t *testing.T) {
	a := []*http.Cookie{{Name: "a", Value: "1"}, {Name: "b", Value: "2"}}
	b := []*http.Cookie{{Name: "b", Value: "2"}, {Name: "a", Value: "1"}}
	if cookieSnapshotKey(a) != cookieSnapshotKey(b) {
		t.Error("cookieSnapshotKey must be order-independent")
	}

	c := []*http.Cookie{{Name: "a", Value: "1"}, {Name: "b", Value: "3"}}
	if cookieSnapshotKey(a) == cookieSnapshotKey(c) {
		t.Error("cookieSnapshotKey must detect value changes")
	}
}

// TestWithInteractive verifies the context marker round-trips and that an
// unset context reports false.
func TestWithInteractive(t *testing.T) {
	if isInteractive(context.Background()) {
		t.Error("plain background ctx must not be interactive")
	}
	if !isInteractive(WithInteractive(context.Background())) {
		t.Error("WithInteractive ctx must report true")
	}
	//lint:ignore SA1012 deliberately testing nil-safety of isInteractive
	if isInteractive(nil) { //nolint:staticcheck
		t.Error("nil ctx must not be interactive")
	}
}

// --- Warm cache tests ---

// resetWarmCache clears the warm cache between tests. Must not run in
// parallel with tests that read the cache.
func resetWarmCache(t *testing.T) {
	t.Helper()
	warmCache.mu.Lock()
	warmCache.entries = make(map[string]*warmCacheEntry)
	warmCache.mu.Unlock()
	t.Cleanup(func() {
		warmCache.mu.Lock()
		warmCache.entries = make(map[string]*warmCacheEntry)
		warmCache.mu.Unlock()
	})
}

// TestWarmBrowserCookies_CachedResultServedInstantly verifies that
// WarmBrowserCookies starts a background read and warmBrowserCookiesResult
// returns the cached cookies without hitting kooky again.
func TestWarmBrowserCookies_CachedResultServedInstantly(t *testing.T) {
	resetWarmCache(t)

	targetURL := "https://www.booking.com/searchresults.html"
	hint := "brave"

	// Manually populate the warm cache with synthetic cookies to avoid
	// hitting the real Keychain during tests.
	entry := &warmCacheEntry{done: make(chan struct{})}
	entry.cookies = []*http.Cookie{
		{Name: "sid", Value: "test123", Domain: ".booking.com"},
		{Name: "bkng_sso_ses", Value: "session456", Domain: ".booking.com"},
	}
	close(entry.done)

	key := warmCacheKey(targetURL, hint)
	warmCache.mu.Lock()
	warmCache.entries[key] = entry
	warmCache.mu.Unlock()

	// warmBrowserCookiesResult should return the cached cookies instantly.
	start := time.Now()
	got := warmBrowserCookiesResult(targetURL, hint, time.Second)
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Errorf("warm cache lookup took %v, expected < 50ms", elapsed)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 cookies from warm cache, got %d", len(got))
	}
	if got[0].Name != "sid" || got[0].Value != "test123" {
		t.Errorf("first cookie = %s=%s, want sid=test123", got[0].Name, got[0].Value)
	}
}

// TestWarmBrowserCookies_DeduplicatesCalls verifies that calling
// WarmBrowserCookies multiple times for the same URL only creates one entry.
func TestWarmBrowserCookies_DeduplicatesCalls(t *testing.T) {
	resetWarmCache(t)

	targetURL := "https://www.example.com/page"
	hint := "chrome"

	// Pre-populate to simulate an existing entry.
	entry := &warmCacheEntry{done: make(chan struct{})}
	close(entry.done)

	key := warmCacheKey(targetURL, hint)
	warmCache.mu.Lock()
	warmCache.entries[key] = entry
	warmCache.mu.Unlock()

	// Calling WarmBrowserCookies again should not overwrite the entry.
	WarmBrowserCookies(targetURL, hint)

	warmCache.mu.Lock()
	got := warmCache.entries[key]
	warmCache.mu.Unlock()

	if got != entry {
		t.Error("WarmBrowserCookies replaced existing entry instead of deduplicating")
	}
}

// TestWarmBrowserCookiesResult_NoEntry verifies that warmBrowserCookiesResult
// returns nil when no warm-up was started for the URL.
func TestWarmBrowserCookiesResult_NoEntry(t *testing.T) {
	resetWarmCache(t)

	got := warmBrowserCookiesResult("https://unknown.example.com", "", 50*time.Millisecond)
	if got != nil {
		t.Errorf("expected nil for un-warmed URL, got %d cookies", len(got))
	}
}

// TestWarmBrowserCookiesResult_TimesOut verifies that warmBrowserCookiesResult
// returns nil when the warm-up is still in progress and the timeout expires.
func TestWarmBrowserCookiesResult_TimesOut(t *testing.T) {
	resetWarmCache(t)

	targetURL := "https://slow.example.com"
	// Create an entry whose done channel is never closed (simulates slow read).
	entry := &warmCacheEntry{done: make(chan struct{})}

	key := warmCacheKey(targetURL, "")
	warmCache.mu.Lock()
	warmCache.entries[key] = entry
	warmCache.mu.Unlock()

	start := time.Now()
	got := warmBrowserCookiesResult(targetURL, "", 50*time.Millisecond)
	elapsed := time.Since(start)

	if got != nil {
		t.Error("expected nil when warm-up times out")
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("returned too quickly: %v, expected >= 50ms", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("took too long: %v, expected ~50ms", elapsed)
	}
}

// TestInvalidateWarmCache verifies that InvalidateWarmCache removes the
// cached entry so a subsequent read falls through to kooky.
func TestInvalidateWarmCache(t *testing.T) {
	resetWarmCache(t)

	targetURL := "https://www.booking.com/searchresults.html"
	hint := "brave"

	// Populate cache.
	entry := &warmCacheEntry{done: make(chan struct{})}
	entry.cookies = []*http.Cookie{{Name: "old", Value: "stale"}}
	close(entry.done)

	key := warmCacheKey(targetURL, hint)
	warmCache.mu.Lock()
	warmCache.entries[key] = entry
	warmCache.mu.Unlock()

	// Invalidate.
	InvalidateWarmCache(targetURL, hint)

	// Result should be nil now.
	got := warmBrowserCookiesResult(targetURL, hint, 50*time.Millisecond)
	if got != nil {
		t.Errorf("expected nil after invalidation, got %d cookies", len(got))
	}
}

// TestWarmCacheKey_DifferentHints verifies that different browser hints
// produce different cache keys for the same URL.
func TestWarmCacheKey_DifferentHints(t *testing.T) {
	url := "https://www.booking.com"
	k1 := warmCacheKey(url, "brave")
	k2 := warmCacheKey(url, "chrome")
	k3 := warmCacheKey(url, "")

	if k1 == k2 {
		t.Error("brave and chrome hints must produce different keys")
	}
	if k1 == k3 {
		t.Error("brave and empty hints must produce different keys")
	}
	if k2 == k3 {
		t.Error("chrome and empty hints must produce different keys")
	}
}

// TestIsAkamaiChallenge verifies detection of Akamai/AWS WAF challenge pages.
func TestIsAkamaiChallenge(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"200 normal", 200, `{"results":[]}`, false},
		{"202 JSON accepted", 202, `{"job_id":"abc123"}`, false},
		{"202 challenge.js", 202, `<html><script src="https://example.com/challenge.js"></script></html>`, true},
		{"202 window.aws", 202, `<html><script>window.aws = {token:"x"}</script></html>`, true},
		{"202 reportChallengeError", 202, `<html><script>reportChallengeError("fail")</script></html>`, true},
		{"202 awswaf", 202, `<html><script src="https://1234.awswaf.com/challenge.js"></script></html>`, true},
		{"202 plain HTML no markers", 202, `<html><body>Please wait...</body></html>`, false},
		{"403 with challenge markers", 403, `<html><script src="challenge.js"></script></html>`, false},
		{"202 empty body", 202, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isAkamaiChallenge(tc.status, []byte(tc.body))
			if got != tc.want {
				t.Errorf("isAkamaiChallenge(%d, %q) = %v, want %v",
					tc.status, tc.body[:min(len(tc.body), 40)], got, tc.want)
			}
		})
	}
}

// TestCookieTargetPermitted pins browser cookies to the endpoint domain the
// consent elicitation displayed. The preflight URL travels in the same
// configure_provider call, is never shown to the user, and used to be handed
// straight to the browser cookie reader.
func TestCookieTargetPermitted(t *testing.T) {
	const endpoint = "https://www.booking.com/dml/graphql"

	cases := []struct {
		name     string
		endpoint string
		target   string
		want     bool
	}{
		{"endpoint itself", endpoint, endpoint, true},
		{"sibling subdomain", endpoint, "https://secure.booking.com/login", true},
		{"apex", endpoint, "https://booking.com/", true},
		{"foreign host holding a live session", endpoint, "https://mail.google.com/mail/u/0/", false},
		{"suffix lookalike", endpoint, "https://www.booking.com.evil.com/", false},
		{"userinfo prefix", endpoint, "https://www.booking.com@evil.com/", false},
		{"site only in the query", endpoint, "https://evil.com/?x=www.booking.com", false},
		{"loopback", endpoint, "http://localhost:9200/_search", false},
		{"link-local metadata", endpoint, "http://169.254.169.254/latest/meta-data/", false},
		{"plaintext on the right site", endpoint, "http://www.booking.com/dml/graphql", false},
		{"ipv6 zone smuggling", endpoint, "https://[::1%25.booking.com]/", false},
		{"empty endpoint fails closed", "", "https://www.booking.com/", false},
		{"hostless endpoint fails closed", "file:///etc/passwd", "https://www.booking.com/", false},

		// A self-hosted or on-LAN endpoint reaches a provider config only by the
		// user typing it, so plaintext there is their choice and refusing it
		// would break the config while closing nothing: such an endpoint is
		// same-site with its own preflight. Same-site is still enforced, by
		// exact host — a bare literal has no registrable site to be a subdomain
		// of, and a suffix rule over one is how host smuggling returns.
		{"self-hosted endpoint, its own preflight", "http://127.0.0.1:8080/api", "http://127.0.0.1:8080/preflight", true},
		{"self-hosted endpoint, foreign preflight", "http://127.0.0.1:8080/api", "https://mail.google.com/mail/u/0/", false},
		{"self-hosted endpoint, neighbour on the LAN", "http://127.0.0.1:8080/api", "http://192.168.1.4/admin", false},
		{"self-hosted endpoint, metadata service", "http://127.0.0.1:8080/api", "http://169.254.169.254/latest/meta-data/", false},
		{"localhost by name, its own preflight", "http://localhost:8080/api", "http://localhost:8080/preflight", true},
		{"localhost by name, loopback literal is a different host", "http://localhost:8080/api", "http://127.0.0.1:8080/preflight", false},
		// No downgrade: cookies approved for a TLS endpoint may not be replayed
		// over plaintext, not even back to the same host.
		{"self-hosted https endpoint, plaintext preflight", "https://127.0.0.1:8443/api", "http://127.0.0.1:8443/preflight", false},
		{"self-hosted endpoint, non-web scheme", "http://127.0.0.1:8080/api", "file:///etc/passwd", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &ProviderConfig{ID: "t", Endpoint: tc.endpoint}
			if got := cookieTargetPermitted(cfg, tc.target); got != tc.want {
				t.Errorf("cookieTargetPermitted(%q, %q) = %v, want %v",
					tc.endpoint, tc.target, got, tc.want)
			}
		})
	}

	if cookieTargetPermitted(nil, "https://www.booking.com/") {
		t.Error("cookieTargetPermitted(nil, ...) = true, want false")
	}
}

// TestApplyBrowserCookies_OffSitePreflightGetsNoCookies is the end-to-end form:
// cookies are available for the caller-named host, and the jar must still come
// out empty because that host is not the provider the user approved.
func TestApplyBrowserCookies_OffSitePreflightGetsNoCookies(t *testing.T) {
	resetWarmCache(t)

	const preflightURL = "https://mail.google.com/mail/u/0/"
	entry := &warmCacheEntry{done: make(chan struct{})}
	entry.cookies = []*http.Cookie{
		{Name: "SID", Value: "live-session", Domain: ".google.com"},
	}
	close(entry.done)

	warmCache.mu.Lock()
	warmCache.entries[warmCacheKey(preflightURL, "")] = entry
	warmCache.mu.Unlock()

	vault := newCookieVault()
	pc := &providerClient{
		config: &ProviderConfig{ID: "t", Endpoint: "https://www.booking.com/dml/graphql"},
		client: &http.Client{Jar: vault},
	}

	if applyBrowserCookies(pc, preflightURL, "") {
		t.Error("applyBrowserCookies sent browser cookies to an off-site preflight URL, want refusal")
	}
	u, _ := url.Parse(preflightURL)
	if got := vault.Cookies(u); len(got) != 0 {
		t.Errorf("jar holds %d cookies for %s, want 0", len(got), preflightURL)
	}
	if vault.isBrowserSeeded() {
		t.Error("vault marked browser-seeded after a refused off-site read")
	}
}
