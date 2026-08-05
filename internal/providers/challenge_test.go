package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/consent"
	"github.com/chromedp/cdproto/network"
)

// ---------------------------------------------------------------------------
// DetectInteractiveCaptcha — pure marker-detection table
// ---------------------------------------------------------------------------

func TestDetectInteractiveCaptcha(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantHuman  bool
		wantVendor string
	}{
		{"clean page", `<html><body>Welcome, here are your flights</body></html>`, false, ""},
		{"empty", ``, false, ""},
		{"datadome iframe", `<iframe src="https://geo.captcha-delivery.com/captcha/?initialCid=x"></iframe>`, true, "datadome"},
		{"datadome bare host", `please solve at captcha-delivery.com`, true, "datadome"},
		{"hcaptcha widget", `<div class="h-captcha" data-sitekey="abc"></div>`, true, "hcaptcha"},
		{"hcaptcha host", `<script src="https://hcaptcha.com/1/api.js"></script>`, true, "hcaptcha"},
		{"recaptcha api", `<script src="https://www.google.com/recaptcha/api.js"></script>`, true, "recaptcha"},
		{"recaptcha container", `<div class="g-recaptcha" data-sitekey="x"></div>`, true, "recaptcha"},
		{"case insensitive", `<DIV CLASS="H-CAPTCHA"></DIV>`, true, "hcaptcha"},
		{"unrelated captcha word", `we use a captcha-free login`, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotHuman, gotVendor := DetectInteractiveCaptcha([]byte(tc.body))
			if gotHuman != tc.wantHuman || gotVendor != tc.wantVendor {
				t.Fatalf("DetectInteractiveCaptcha(%q) = (%v,%q), want (%v,%q)",
					tc.body, gotHuman, gotVendor, tc.wantHuman, tc.wantVendor)
			}
		})
	}
}

func TestChallengeStatusString(t *testing.T) {
	cases := map[ChallengeStatus]string{
		ChallengeCleared:    "cleared",
		ChallengeNeedsHuman: "needs_human",
		ChallengeUnresolved: "unresolved",
		ChallengeStatus(99): "unresolved",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", s, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// ResolveChallenge — gating + CLEARED vs NEEDS_HUMAN decision (offline)
// ---------------------------------------------------------------------------

func TestResolveChallenge_DisabledByDefault(t *testing.T) {
	t.Setenv(tier2DisableEnv, "1")
	_, err := ResolveChallenge(context.Background(), "https://example.com/")
	if !errors.Is(err, ErrTier2Disabled) {
		t.Fatalf("err = %v, want ErrTier2Disabled", err)
	}
}

func TestResolveChallenge_NoBrowserFound(t *testing.T) {
	prevExists := fileExists
	fileExists = func(string) bool { return false }
	defer func() { fileExists = prevExists }()

	_, err := ResolveChallenge(context.Background(), "https://example.com/")
	if !errors.Is(err, ErrNoBrowserFound) {
		t.Fatalf("err = %v, want ErrNoBrowserFound", err)
	}
}

func TestResolveChallenge_RunnerError(t *testing.T) {
	prevExists := fileExists
	fileExists = func(string) bool { return true }
	defer func() { fileExists = prevExists }()

	prevRunner := cdpChallengeRunner
	wantErr := errors.New("boom")
	cdpChallengeRunner = func(ctx context.Context, execPath, targetURL string, wait time.Duration) ([]*network.Cookie, string, error) {
		return nil, "", wantErr
	}
	defer func() { cdpChallengeRunner = prevRunner }()

	_, err := ResolveChallenge(context.Background(), "https://example.com/")
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestResolveChallenge_ClearedPersistsCookies(t *testing.T) {
	// os.UserHomeDir reads USERPROFILE on Windows, not HOME — isolate both so
	// the ~/.trvl cookie cache is per-test on every OS.
	//
	// Both must name the SAME directory. t.TempDir() returns a NEW directory on
	// every call, so calling it twice pointed HOME and USERPROFILE at unrelated
	// places: on Windows the test then ran against a directory nothing had
	// populated, while unix used the other one. Capture once (trvl#565).
	challengeHome := t.TempDir()
	t.Setenv("HOME", challengeHome)
	t.Setenv("USERPROFILE", challengeHome)

	prevExists := fileExists
	fileExists = func(string) bool { return true }
	defer func() { fileExists = prevExists }()

	prevRunner := cdpChallengeRunner
	cdpChallengeRunner = func(ctx context.Context, execPath, targetURL string, wait time.Duration) ([]*network.Cookie, string, error) {
		cookies := []*network.Cookie{
			{Name: "cf_clearance", Value: "headless-token", Domain: "example.com", Path: "/", Secure: true, Expires: float64(time.Now().Add(time.Hour).Unix())},
		}
		// Clean page — challenge solved, no captcha markers.
		return cookies, `<html><body>flights</body></html>`, nil
	}
	defer func() { cdpChallengeRunner = prevRunner }()

	target := "https://example.com/"
	res, err := ResolveChallenge(context.Background(), target)
	if err != nil {
		t.Fatalf("ResolveChallenge: %v", err)
	}
	if res.Status != ChallengeCleared {
		t.Fatalf("status = %v, want cleared", res.Status)
	}
	// Cleared path must persist cookies for Tier-1 reuse.
	cached := cachedCookiesForURL(target)
	found := false
	for _, c := range cached {
		if c.Name == "cf_clearance" && c.Value == "headless-token" {
			found = true
		}
	}
	if !found {
		t.Fatalf("cf_clearance not persisted on cleared: %+v", cached)
	}
}

func TestResolveChallenge_NeedsHumanDoesNotPersist(t *testing.T) {
	// os.UserHomeDir reads USERPROFILE on Windows, not HOME — isolate both so a
	// prior test's persisted cookies cannot leak into this absence assertion.
	//
	// Both must name the SAME directory; see the note in
	// TestResolveChallenge_ClearedPersistsCookies. This one matters more than
	// most: it asserts an ABSENCE, and an assertion that nothing was persisted
	// passes trivially when it is looking at the wrong directory.
	needsHumanHome := t.TempDir()
	t.Setenv("HOME", needsHumanHome)
	t.Setenv("USERPROFILE", needsHumanHome)

	prevExists := fileExists
	fileExists = func(string) bool { return true }
	defer func() { fileExists = prevExists }()

	prevRunner := cdpChallengeRunner
	cdpChallengeRunner = func(ctx context.Context, execPath, targetURL string, wait time.Duration) ([]*network.Cookie, string, error) {
		cookies := []*network.Cookie{
			{Name: "datadome", Value: "partial", Domain: "example.com", Path: "/"},
		}
		// Interactive Datadome captcha still present.
		return cookies, `<iframe src="https://geo.captcha-delivery.com/captcha/"></iframe>`, nil
	}
	defer func() { cdpChallengeRunner = prevRunner }()

	target := "https://example.com/"
	res, err := ResolveChallenge(context.Background(), target)
	if err != nil {
		t.Fatalf("ResolveChallenge: %v", err)
	}
	if res.Status != ChallengeNeedsHuman {
		t.Fatalf("status = %v, want needs_human", res.Status)
	}
	if res.Marker != "datadome" {
		t.Fatalf("marker = %q, want datadome", res.Marker)
	}
	// NEEDS_HUMAN must NOT persist cookies (session not authenticated).
	if cached := cachedCookiesForURL(target); len(cached) != 0 {
		t.Fatalf("expected no cookies persisted on needs_human, got %+v", cached)
	}
}

func TestDefaultHeadlessFirstResolve_SkipsInTestBinary(t *testing.T) {
	// Without TRVL_ALLOW_BROWSER_COOKIES the default must refuse to spawn a real
	// browser inside the test binary, so the visible-window fallback can run.
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "")
	_, err := defaultHeadlessFirstResolve(context.Background(), "https://example.com/")
	if !errors.Is(err, ErrTier2Disabled) {
		t.Fatalf("err = %v, want ErrTier2Disabled (skip in test binary)", err)
	}
}

// ---------------------------------------------------------------------------
// tryBrowserEscapeHatch wiring — headless-first vs visible-window fallback
// ---------------------------------------------------------------------------

// withHeadlessResolve swaps the headlessFirstResolve seam for the test duration.
func withHeadlessResolve(t *testing.T, fn func(ctx context.Context, targetURL string) (*ChallengeResult, error)) {
	t.Helper()
	prev := headlessFirstResolve
	headlessFirstResolve = fn
	t.Cleanup(func() { headlessFirstResolve = prev })
}

// TestTryBrowserEscapeHatch_HeadlessClears_NoVisibleWindow proves that when the
// headless pass clears the challenge and the preflight retry succeeds, the
// VISIBLE opener is NEVER called.
func TestTryBrowserEscapeHatch_HeadlessClears_NoVisibleWindow(t *testing.T) {
	// Preflight server that returns a clean 2xx so finishEscapeHatch succeeds.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	// A vault: the escape-hatch tail seeds cookies recovered from the user's
	// own browser window, and those only enter a jar that can revoke them.
	pc := &providerClient{
		config:     &ProviderConfig{ID: "headless-clear", Name: "HeadlessClear"},
		client:     &http.Client{Jar: newCookieVault()},
		authValues: make(map[string]string),
	}
	auth := &AuthConfig{PreflightURL: srv.URL, BrowserEscapeHatch: true}

	withHeadlessResolve(t, func(ctx context.Context, targetURL string) (*ChallengeResult, error) {
		return &ChallengeResult{
			Status:  ChallengeCleared,
			Cookies: []*http.Cookie{{Name: "cf_clearance", Value: "x"}},
		}, nil
	})

	var openerCalls int
	withOpener(t, func(goos, pref, target string) error {
		openerCalls++
		return nil
	})

	if _, got := tryBrowserEscapeHatch(context.Background(), pc, auth); !got {
		t.Fatal("expected true when headless clears and preflight retry succeeds")
	}
	if openerCalls != 0 {
		t.Fatalf("visible opener called %d times, want 0 (headless cleared silently)", openerCalls)
	}
}

// TestTryBrowserEscapeHatch_NeedsHuman_OpensVisibleOnce proves that when an
// interactive captcha remains after the headless pass, the visible opener is
// invoked exactly once.
func TestTryBrowserEscapeHatch_NeedsHuman_OpensVisibleOnce(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	pc := &providerClient{
		config:     &ProviderConfig{ID: "needs-human", Name: "NeedsHuman"},
		client:     &http.Client{Jar: jar},
		authValues: make(map[string]string),
	}
	auth := &AuthConfig{PreflightURL: "https://example.com/page", BrowserEscapeHatch: true}

	withHeadlessResolve(t, func(ctx context.Context, targetURL string) (*ChallengeResult, error) {
		return &ChallengeResult{Status: ChallengeNeedsHuman, Marker: "datadome"}, nil
	})

	var openerCalls int
	withOpener(t, func(goos, pref, target string) error {
		openerCalls++
		return nil
	})
	// No cookie change → waitForFreshCookies times out fast; keep the test quick
	// by cancelling the context shortly.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	// Cookie source returns a stable (unchanged) set so the wait reports no change.
	withCookieSource(t, func(string) []*http.Cookie { return nil })

	_, got := tryBrowserEscapeHatch(ctx, pc, auth)
	if got {
		t.Fatal("expected false: visible path opened but no cookie change observed")
	}
	if openerCalls != 1 {
		t.Fatalf("visible opener called %d times, want exactly 1", openerCalls)
	}
}

// TestTryBrowserEscapeHatch_HeadlessUnresolved_FallsThrough proves that when the
// headless seam errors (e.g. no browser), the visible-window fallback still runs.
func TestTryBrowserEscapeHatch_HeadlessUnresolved_FallsThrough(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	pc := &providerClient{
		config:     &ProviderConfig{ID: "fallthrough", Name: "FallThrough"},
		client:     &http.Client{Jar: jar},
		authValues: make(map[string]string),
	}
	auth := &AuthConfig{PreflightURL: "https://example.com/page", BrowserEscapeHatch: true}

	withHeadlessResolve(t, func(ctx context.Context, targetURL string) (*ChallengeResult, error) {
		return nil, ErrNoBrowserFound
	})

	var openerCalls int
	withOpener(t, func(goos, pref, target string) error {
		openerCalls++
		return nil
	})
	withCookieSource(t, func(string) []*http.Cookie { return nil })

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	_, _ = tryBrowserEscapeHatch(ctx, pc, auth)
	if openerCalls != 1 {
		t.Fatalf("visible opener called %d times, want exactly 1 (headless errored)", openerCalls)
	}
}

// TestTryBrowserEscapeHatch_CookieDecline_NeverOpensBrowser is the consent gate.
//
// The setup is deliberately the one that DOES open a window: the headless seam
// errors, so control reaches the visible-window fallback, which the test above
// proves calls the opener exactly once. The only difference here is the decline.
//
// Gating the cookie reads alone is not enough and that is the point of counting
// opener calls rather than just the return value. browserCookiesForURL already
// returns nil when declined, so a version with no gate in this function still
// returns false — it just does so after opening the user's real browser and
// waiting out the deadline. A test that asserted only on the return value would
// pass against the bug.
func TestTryBrowserEscapeHatch_CookieDecline_NeverOpensBrowser(t *testing.T) {
	t.Setenv(consent.Tier2Env, "")
	t.Setenv(consent.Tier2LegacyEnv, "")
	t.Setenv(consent.CookiesEnv, "1")

	if Tier2Declined() {
		t.Fatalf("precondition: Tier2Declined must be FALSE here, or this test passes for the wrong reason")
	}

	jar, _ := cookiejar.New(nil)
	pc := &providerClient{
		config:     &ProviderConfig{ID: "cookie-declined", Name: "CookieDeclined"},
		client:     &http.Client{Jar: jar},
		authValues: make(map[string]string),
	}
	auth := &AuthConfig{PreflightURL: "https://example.com/page", BrowserEscapeHatch: true}

	// Same seam as the fall-through test: headless cannot resolve, so without the
	// gate the visible path runs.
	withHeadlessResolve(t, func(ctx context.Context, targetURL string) (*ChallengeResult, error) {
		return nil, ErrNoBrowserFound
	})

	var openerCalls int
	withOpener(t, func(goos, pref, target string) error {
		openerCalls++
		return nil
	})
	withCookieSource(t, func(string) []*http.Cookie { return nil })

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	if _, got := tryBrowserEscapeHatch(ctx, pc, auth); got {
		t.Error("the escape hatch reported success after the user declined browser cookie access")
	}
	if openerCalls != 0 {
		t.Fatalf("opener called %d times, want 0: %s declined browser access and the escape hatch "+
			"opened the user's real browser anyway", openerCalls, consent.CookiesEnv)
	}
}
