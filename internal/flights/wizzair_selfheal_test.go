package flights

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWizzNextCandidates checks the rotation-walk order: next minors first
// (the common rotation), then patches, then the next majors.
func TestWizzNextCandidates(t *testing.T) {
	got := wizzNextCandidates("29.3.0")
	if len(got) == 0 {
		t.Fatal("no candidates generated")
	}
	if got[0] != "29.4.0" {
		t.Errorf("first candidate = %q, want 29.4.0 (minor +1 is most likely)", got[0])
	}
	want := map[string]bool{"29.4.0": false, "29.5.0": false, "29.3.1": false, "30.0.0": false}
	for _, c := range got {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for v, seen := range want {
		if !seen {
			t.Errorf("expected candidate %q not in walk %v", v, got)
		}
	}
	if c := wizzNextCandidates("not-a-version"); c != nil {
		t.Errorf("malformed input should yield nil candidates, got %v", c)
	}
}

// TestWizzProbeVersion classifies the asset/culture oracle responses.
func TestWizzProbeVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/29.4.0/"):
			w.WriteHeader(http.StatusMethodNotAllowed) // live: route exists, GET not allowed
		case strings.Contains(r.URL.Path, "/29.9.9/"):
			w.WriteHeader(http.StatusForbidden) // transient edge block
		default:
			w.WriteHeader(http.StatusNotFound) // absent
		}
	}))
	defer srv.Close()
	orig := wizzHost
	wizzHost = srv.URL
	defer func() { wizzHost = orig }()

	cases := map[string]string{"29.4.0": "live", "29.3.0": "absent", "29.9.9": "inconclusive"}
	for v, want := range cases {
		if got := wizzProbeVersion(context.Background(), v); got != want {
			t.Errorf("probe(%s) = %q, want %q", v, got, want)
		}
	}
}

// TestSearchWizzair_SelfHealsOnRotation is the integration proof: the stale
// configured version 404s on the timetable endpoint, the asset/culture oracle
// reveals the rotated-to version is live, and SearchWizzair transparently
// rediscovers it and returns results — without the operator touching anything.
func TestSearchWizzair_SelfHealsOnRotation(t *testing.T) {
	const stale, live = "29.3.0", "29.4.0"
	fixture := loadFixture(t, "wizzair_timetable.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		liveVersion := strings.Contains(r.URL.Path, "/"+live+"/")
		switch {
		case strings.HasSuffix(r.URL.Path, "/Api/asset/culture"):
			if liveVersion {
				w.WriteHeader(http.StatusMethodNotAllowed) // oracle: live
			} else {
				w.WriteHeader(http.StatusNotFound) // oracle: absent
			}
		case strings.HasSuffix(r.URL.Path, "/Api/search/timetable"):
			if liveVersion {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(fixture)
			} else {
				w.WriteHeader(http.StatusNotFound) // stale version: rotation 404
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	origHost, origVer := wizzHost, wizzVersion
	wizzHost = srv.URL
	wizzVersion = stale
	defer func() { wizzHost, wizzVersion = origHost, origVer }()
	t.Setenv("WIZZAIR_API_VERSION", "")  // no operator pin
	t.Setenv("WIZZAIR_NO_AUTOHEAL", "")  // healing enabled

	out, err := SearchWizzair(context.Background(), "BUD", "BCN", "2026-07-07", "EUR", SearchOptions{Adults: 1})
	if err != nil {
		t.Fatalf("self-heal search should succeed, got error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 result after self-heal, got %d", len(out))
	}
	if got := wizzResolvedVersion(); got != live {
		t.Errorf("version after heal = %q, want %q", got, live)
	}
}

// TestSearchWizzair_NoHealWhenPinned proves an operator pin (WIZZAIR_API_VERSION)
// disables self-healing: the rotation 404 surfaces as the typed sentinel instead
// of silently rediscovering — the operator stays in control.
func TestSearchWizzair_NoHealWhenPinned(t *testing.T) {
	probed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/Api/asset/culture") {
			probed = true
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	origHost, origVer := wizzHost, wizzVersion
	wizzHost = srv.URL
	wizzVersion = "10.1.0"
	defer func() { wizzHost, wizzVersion = origHost, origVer }()
	t.Setenv("WIZZAIR_API_VERSION", "29.3.0") // operator pin -> healing disabled

	_, err := SearchWizzair(context.Background(), "BUD", "BCN", "2026-07-07", "EUR", SearchOptions{Adults: 1})
	if err == nil {
		t.Fatal("want rotation error when pinned, got nil")
	}
	if probed {
		t.Error("self-heal discovery ran despite an operator pin; it must not")
	}
}

// TestWizzHealEnabled covers the enable/disable matrix.
func TestWizzHealEnabled(t *testing.T) {
	t.Setenv("WIZZAIR_API_VERSION", "")
	t.Setenv("WIZZAIR_NO_AUTOHEAL", "")
	if !wizzHealEnabled() {
		t.Error("healing should be enabled by default")
	}
	t.Setenv("WIZZAIR_API_VERSION", "29.3.0")
	if wizzHealEnabled() {
		t.Error("operator pin must disable healing")
	}
	t.Setenv("WIZZAIR_API_VERSION", "")
	t.Setenv("WIZZAIR_NO_AUTOHEAL", "1")
	if wizzHealEnabled() {
		t.Error("WIZZAIR_NO_AUTOHEAL=1 must disable healing")
	}
}
