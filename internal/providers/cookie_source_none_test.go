package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
		"nnoe":      false, // a typo fails closed (#844)
		"None":      false,
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

// A config file on disk skips Validate (NewRegistryAt runs only Migrate), so a
// misspelled cookies.source reaches the runtime as is. It must still fail
// closed: no browser-store read during challenge recovery (#844).
func TestDiskConfigWithMisspelledSourceNeverReadsCookies(t *testing.T) {
	t.Setenv(AllowLocalEnv, "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	dir := t.TempDir()
	data, _ := json.Marshal(&ProviderConfig{
		ID: "typo", Name: "Typo", Category: "hotels", Endpoint: srv.URL + "/search", Method: "GET",
		ResponseMapping: ResponseMapping{ResultsPath: "results", Fields: map[string]string{"name": "name"}},
		Cookies:         CookieConfig{Source: "nnoe", Browser: "chrome"},
	})
	if err := os.WriteFile(filepath.Join(dir, "typo.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	cfg := reg.Get("typo")
	if cfg == nil || cfg.Cookies.Source != "nnoe" {
		t.Fatalf("disk config not loaded with its typo intact: %+v", cfg)
	}
	u, _ := url.Parse(srv.URL)
	reads := stubBrowserReader(t, u.Hostname())

	_, err = NewRuntime(reg).searchProvider(context.Background(), cfg, "Kyoto", 35.0, 135.7, "2026-06-01", "2026-06-03", "EUR", 2, nil)
	// The zero below only means something if the search reached the challenge
	// recovery, so prove it did before reading the count.
	if err == nil || !strings.Contains(err.Error(), "WAF/JS challenge page") {
		t.Fatalf("searchProvider err = %v, want the challenge-recovery error", err)
	}
	if got := reads.Load(); got != 0 {
		t.Errorf("browser store read %d times for cookies.source %q, want 0", got, cfg.Cookies.Source)
	}
}

// Validate names a cookies.source outside the allowed set at entry time, and
// every shipped definition still passes.
func TestValidateRefusesOutOfSetCookieSource(t *testing.T) {
	base := ProviderConfig{
		ID: "v", Name: "V", Category: "hotels", Endpoint: "https://api.testprovider-synth.com/search",
		ResponseMapping: ResponseMapping{ResultsPath: "results"},
	}
	for source, wantErr := range map[string]bool{
		"": false, "none": false, "preflight": false, "browser": false,
		"nnoe": true, "None": true, "browsr": true,
	} {
		cfg := base
		cfg.Cookies.Source = source
		err := cfg.Validate()
		if (err != nil) != wantErr {
			t.Errorf("source %q: Validate() = %v, want error=%v", source, err, wantErr)
		}
		if err != nil && !strings.Contains(err.Error(), source) {
			t.Errorf("source %q: error %q does not name the bad value", source, err)
		}
	}
	err := fs.WalkDir(providerDefinitions, "definitions", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := providerDefinitions.ReadFile(path)
		if err != nil {
			return err
		}
		var cfg ProviderConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("shipped definition %s no longer validates: %v", path, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
