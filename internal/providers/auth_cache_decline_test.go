package providers

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// TestAuthCacheDiscardsBrowserSeededStateAfterADecline pins the seventh path of
// this family, and the first that never touches disk or a browser process.
//
// The auth cache is in-memory and `trvl mcp` is long-lived, so a jar seeded
// from the user's browser survives the moment it was permitted. Every later
// call for the same URL returns from the cache above the guarded readers, and
// the browser-derived cookies keep going out on the wire — the setting looks
// applied and nothing about the traffic changes. That is the shape of the disk
// cache bypass, moved into memory.
//
// The test is ordered around a CONTROL: it asserts the cached auth IS served
// while nothing is declined, so the assertion afterwards is about the decline
// rather than about a cache that was never warm.
func TestAuthCacheDiscardsBrowserSeededStateAfterADecline(t *testing.T) {
	const preflight = "https://example.test/preflight"

	newPC := func() *providerClient {
		vault := newCookieVault()
		if vault == nil {
			t.Fatal("cookie vault")
		}
		u, err := url.Parse(preflight)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		// Seeded through the browser path, so the jar records the provenance
		// itself. This is the material that must stop being sent.
		if !vault.seedFromBrowser(u, []*http.Cookie{{Name: "datadome", Value: "from-the-users-browser"}}) {
			t.Fatal("fixture did not seed browser cookies")
		}

		pc := &providerClient{
			config: &ProviderConfig{
				ID:   "test-auth-cache-decline",
				Auth: &AuthConfig{PreflightURL: preflight},
			},
			client:           &http.Client{Jar: vault},
			authValues:       map[string]string{"token": "seeded-from-browser"},
			authExpiry:       time.Now().Add(time.Hour),
			lastPreflightURL: preflight,
		}
		return pc
	}

	rt := NewRuntime(nil)

	// CONTROL. Warm, unexpired, same URL: the cache must be served. Without
	// this the assertion below could pass against a cache that never hit — for
	// instance if the fixture's expiry or URL were wrong.
	t.Run("served while nothing is declined", func(t *testing.T) {
		pc := newPC()
		got, err := rt.runPreflight(context.Background(), pc, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["token"] != "seeded-from-browser" {
			t.Fatalf("the fixture did not produce a cache hit, so the decline case would prove nothing: got %v", got)
		}
	})

	t.Run("discarded once the user declines", func(t *testing.T) {
		pc := newPC()
		t.Setenv(consent.CookiesEnv, "1")

		// runPreflight will now try a real preflight against a host that does
		// not resolve, and fail. The error is not what is being tested: the
		// state left behind is. Reaching the network at all is itself part of
		// the evidence, because a served cache would have returned before it.
		_, _ = rt.runPreflight(context.Background(), pc, nil)

		pc.authMu.RLock()
		defer pc.authMu.RUnlock()

		if pc.isBrowserSeeded() {
			t.Error("the client is still marked browser-seeded after the decline")
		}
		if len(pc.authValues) != 0 {
			t.Errorf("browser-derived auth values survived the decline: %v", pc.authValues)
		}
		if !pc.authExpiry.IsZero() {
			t.Errorf("the auth cache is still live after the decline: expiry = %v", pc.authExpiry)
		}
		u, err := url.Parse(preflight)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if pc.client.Jar == nil {
			t.Fatal("the jar was removed rather than replaced, which silently drops Set-Cookie")
		}
		if cs := pc.client.Jar.Cookies(u); len(cs) != 0 {
			t.Errorf("browser-derived cookies are still in the jar and would still be sent: %v", cs)
		}
	})
}

// TestAuthCacheKeepsNonBrowserStateAfterADecline is the other half: a session
// that was never seeded from the browser is none of the setting's business.
// Discarding it would punish a user for a control that says nothing about it,
// and would turn the opt-out into a general cache-buster.
func TestAuthCacheKeepsNonBrowserStateAfterADecline(t *testing.T) {
	const preflight = "https://example.test/preflight"

	// A vault, but one no browser ever touched: cookies arrived the ordinary
	// way, via Set-Cookie. The vault must tell the two apart.
	vault := newCookieVault()
	if vault == nil {
		t.Fatal("cookie vault")
	}
	u, err := url.Parse(preflight)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	vault.SetCookies(u, []*http.Cookie{{Name: "session", Value: "from-an-ordinary-preflight"}})
	pc := &providerClient{
		config: &ProviderConfig{
			ID:   "test-auth-cache-decline-keep",
			Auth: &AuthConfig{PreflightURL: preflight},
		},
		client:           &http.Client{Jar: vault},
		authValues:       map[string]string{"token": "from-an-ordinary-preflight"},
		authExpiry:       time.Now().Add(time.Hour),
		lastPreflightURL: preflight,
	}

	t.Setenv(consent.CookiesEnv, "1")

	got, err := (NewRuntime(nil)).runPreflight(context.Background(), pc, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["token"] != "from-an-ordinary-preflight" {
		t.Errorf("a session that never touched the browser was discarded by the browser opt-out: got %v", got)
	}
}
