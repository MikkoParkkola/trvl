package flights

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// wizzConfigPage renders a minimal stand-in for the Wizz homepage: the inline
// bootstrap config that names the version-stamped Api base URL the browser
// client is told to use.
func wizzConfigPage(version string) string {
	return `<!doctype html><html><head><script>window.__config={` +
		`apiUrl:"https://be.wizzair.com/` + version + `/Api",locale:"en-gb"};` +
		`</script></head><body>Wizz Air</body></html>`
}

// wizzDiscoveryServer serves the three routes a heal touches: the homepage
// config (advertising configVersion), the asset/culture oracle (live only for
// liveVersion), and 404 for everything else.
func wizzDiscoveryServer(t *testing.T, configVersion, liveVersion string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/en-gb":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(wizzConfigPage(configVersion)))
		case strings.HasSuffix(r.URL.Path, "/Api/asset/culture"):
			if liveVersion != "" && strings.Contains(r.URL.Path, "/"+liveVersion+"/") {
				w.WriteHeader(http.StatusMethodNotAllowed) // oracle: live
				return
			}
			w.WriteHeader(http.StatusNotFound) // oracle: absent
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestWizzHeal_AdoptsVersionBeyondWalkRange is the #506 regression: Wizz rotated
// further than the bounded candidate walk can reach, so the walk exhausts and the
// client stays stuck on a dead version — even though the live one is published in
// plain HTML on the homepage. Reading the config finds it; the walk alone cannot.
func TestWizzHeal_AdoptsVersionBeyondWalkRange(t *testing.T) {
	const stale, live = "29.4.0", "31.2.0" // 31.2.0 is outside every wizzNextCandidates(29.4.0) branch
	for _, c := range wizzNextCandidates(stale) {
		if c == live {
			t.Fatalf("test is void: %q is inside the walk range, pick a version the walk cannot reach", live)
		}
	}
	srv := wizzDiscoveryServer(t, live, live)
	defer setWizzVersionForTest(setWizzVersionForTest(stale))

	got, ok := wizzHeal(context.Background(), srv.URL, stale)
	if !ok {
		t.Fatal("heal failed: the live version was advertised in the homepage config but was never discovered")
	}
	if got != live {
		t.Errorf("healed version = %q, want %q", got, live)
	}
}

// TestWizzHeal_RejectsUnverifiedConfigVersion proves the config value is a claim,
// not a fact: a version the homepage advertises but the oracle says is absent
// must NOT be adopted. The walk fallback still wins.
func TestWizzHeal_RejectsUnverifiedConfigVersion(t *testing.T) {
	const stale, advertised, live = "29.4.0", "31.2.0", "29.5.0" // 29.5.0 = first walk candidate
	srv := wizzDiscoveryServer(t, advertised, live)
	defer setWizzVersionForTest(setWizzVersionForTest(stale))

	got, ok := wizzHeal(context.Background(), srv.URL, stale)
	if !ok {
		t.Fatal("heal should have fallen back to the walk and found the live version")
	}
	if got == advertised {
		t.Fatalf("adopted %q from the homepage config without confirming it live", advertised)
	}
	if got != live {
		t.Errorf("healed version = %q, want %q from the walk fallback", got, live)
	}
}

// TestWizzHeal_LogsAdoption proves a rotation is observable rather than silent:
// the single adoption point reports what changed and which mechanism found it.
func TestWizzHeal_LogsAdoption(t *testing.T) {
	const stale, live = "29.4.0", "31.2.0"
	srv := wizzDiscoveryServer(t, live, live)
	defer setWizzVersionForTest(setWizzVersionForTest(stale))

	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(orig)

	if _, ok := wizzHeal(context.Background(), srv.URL, stale); !ok {
		t.Fatal("heal failed")
	}
	logged := buf.String()
	for _, want := range []string{"from=" + stale, "to=" + live, "source=config"} {
		if !strings.Contains(logged, want) {
			t.Errorf("version adoption log missing %q; an unlogged rotation is an invisible one", want)
		}
	}
}

// TestWizzDiscoverFromConfig covers the parse edges: no config match and a
// non-200 page both yield "" so the caller falls back to the walk.
func TestWizzDiscoverFromConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/nomatch/") {
			_, _ = w.Write([]byte("<html><body>no config here</body></html>"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if got := wizzDiscoverFromConfig(context.Background(), srv.URL+"/nomatch"); got != "" {
		t.Errorf("page without a config apiUrl should yield \"\", got %q", got)
	}
	if got := wizzDiscoverFromConfig(context.Background(), srv.URL); got != "" {
		t.Errorf("non-200 page should yield \"\", got %q", got)
	}
}

// TestWizzDefaultVersionRatchet is a downgrade guard, not a behaviour test: #506
// verified 29.8.0 live, so a later edit that lowers the compiled default below it
// would ship users a known-dead version on first run.
func TestWizzDefaultVersionRatchet(t *testing.T) {
	const verified = "29.8.0"
	if wizzDefaultVersion != verified && !wizzVersionNewer(wizzDefaultVersion, verified) {
		t.Errorf("wizzDefaultVersion = %q, must not be older than the #506-verified %q", wizzDefaultVersion, verified)
	}
}
