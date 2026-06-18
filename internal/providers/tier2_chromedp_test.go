package providers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
)

func TestTier2Enabled(t *testing.T) {
	t.Setenv(tier2EnableEnv, "")
	if Tier2Enabled() {
		t.Fatal("expected disabled when env unset")
	}
	t.Setenv(tier2EnableEnv, "1")
	if !Tier2Enabled() {
		t.Fatal("expected enabled when env=1")
	}
}

func TestRefreshCookiesViaCDP_DisabledByDefault(t *testing.T) {
	t.Setenv(tier2EnableEnv, "")
	_, err := RefreshCookiesViaCDP(context.Background(), "https://example.com/")
	if !errors.Is(err, ErrTier2Disabled) {
		t.Fatalf("err = %v, want ErrTier2Disabled", err)
	}
}

func TestRefreshCookiesViaCDP_NoBrowserFound(t *testing.T) {
	// Force-enable but make detection find nothing.
	prevExists := fileExists
	fileExists = func(string) bool { return false }
	defer func() { fileExists = prevExists }()

	_, err := RefreshCookiesViaCDP(context.Background(), "https://example.com/", WithTier2Force())
	if !errors.Is(err, ErrNoBrowserFound) {
		t.Fatalf("err = %v, want ErrNoBrowserFound", err)
	}
}

func TestRefreshCookiesViaCDP_HarvestsAndCaches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Pretend a browser exists.
	prevExists := fileExists
	fileExists = func(string) bool { return true }
	defer func() { fileExists = prevExists }()

	// Stub the actual browser drive to return a Cloudflare clearance cookie.
	prevRunner := cdpRunner
	cdpRunner = func(ctx context.Context, execPath, targetURL string, wait time.Duration) ([]*network.Cookie, error) {
		return []*network.Cookie{
			{Name: "cf_clearance", Value: "tier2-token", Domain: "example.com", Path: "/", Secure: true, Expires: float64(time.Now().Add(time.Hour).Unix())},
			{Name: "session", Value: "xyz", Domain: "example.com", Path: "/"},
		}, nil
	}
	defer func() { cdpRunner = prevRunner }()

	target := "https://example.com/"
	cookies, err := RefreshCookiesViaCDP(context.Background(), target, WithTier2Force())
	if err != nil {
		t.Fatalf("RefreshCookiesViaCDP: %v", err)
	}
	if len(cookies) != 2 {
		t.Fatalf("got %d cookies, want 2", len(cookies))
	}

	// The harvested cookies must have been persisted to the ~/.trvl/cookies cache
	// so Tier-1 can reuse them.
	u, _ := url.Parse(target)
	jar := cachedCookiesForURL(target)
	if len(jar) == 0 {
		t.Fatalf("expected cookies persisted to cache for %s", u.Host)
	}
	found := false
	for _, c := range jar {
		if c.Name == "cf_clearance" && c.Value == "tier2-token" {
			found = true
		}
	}
	if !found {
		t.Fatalf("cf_clearance not persisted to cache: %+v", jar)
	}
}

func TestRefreshCookiesViaCDP_RunnerError(t *testing.T) {
	prevExists := fileExists
	fileExists = func(string) bool { return true }
	defer func() { fileExists = prevExists }()

	prevRunner := cdpRunner
	wantErr := errors.New("boom")
	cdpRunner = func(ctx context.Context, execPath, targetURL string, wait time.Duration) ([]*network.Cookie, error) {
		return nil, wantErr
	}
	defer func() { cdpRunner = prevRunner }()

	_, err := RefreshCookiesViaCDP(context.Background(), "https://example.com/", WithTier2Force())
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestConvertNetworkCookies(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	in := []*network.Cookie{
		nil, // must be skipped
		{Name: "a", Value: "1", Domain: "x.com", Path: "/", Secure: true, HTTPOnly: true, Expires: float64(exp)},
		{Name: "b", Value: "2", Domain: "x.com", Path: "/p"},
	}
	out := convertNetworkCookies(in)
	if len(out) != 2 {
		t.Fatalf("got %d, want 2", len(out))
	}
	if out[0].Name != "a" || !out[0].Secure || !out[0].HttpOnly {
		t.Fatalf("cookie a malformed: %+v", out[0])
	}
	if out[0].Expires.Unix() != exp {
		t.Fatalf("expires = %v, want %v", out[0].Expires.Unix(), exp)
	}
	if out[1].Expires.IsZero() == false {
		t.Fatalf("cookie b should have zero expiry, got %v", out[1].Expires)
	}
}

func TestDetectInstalledBrowser(t *testing.T) {
	prevExists := fileExists
	defer func() { fileExists = prevExists }()

	candidates := browserCandidatePaths()
	if len(candidates) == 0 {
		t.Skip("no candidate paths for this OS")
	}
	want := candidates[len(candidates)-1] // pick the last so earlier ones miss
	fileExists = func(p string) bool { return p == want }

	got, ok := detectInstalledBrowser()
	if !ok || got != want {
		t.Fatalf("detect = (%q,%v), want (%q,true)", got, ok, want)
	}

	fileExists = func(string) bool { return false }
	if _, ok := detectInstalledBrowser(); ok {
		t.Fatal("expected no browser detected")
	}
}

func TestPersistCookiesToCache_NoopOnEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Should not panic / should be a no-op.
	persistCookiesToCache("https://example.com/", nil)
	persistCookiesToCache("://bad", []*http.Cookie{{Name: "a", Value: "b"}})
}
