package selfupdate

import (
	"bytes"
	"strings"
	"testing"
)

// TestIsUpdateAvailable_GitDescribe verifies the git-describe-aware update
// gate: a build N commits ahead of its base tag must NOT be told to "update"
// to the release it already contains, while genuinely older builds and real
// pre-releases still behave per semver.
func TestIsUpdateAvailable_GitDescribe(t *testing.T) {
	cases := []struct {
		name    string
		latest  string // release tag from GitHub, "v"-stripped as the checker stores it
		current string // running binary's main.Version (git-describe, carries "v")
		want    bool
	}{
		{
			name:    "ahead of tag: 3 commits past v1.5.0 is not an update",
			latest:  "1.5.0",
			current: "v1.5.0-3-gfb1d4e4",
			want:    false,
		},
		{
			name:    "ahead of tag, dirty tree: still not an update",
			latest:  "1.5.0",
			current: "v1.5.0-3-gfb1d4e4-dirty",
			want:    false,
		},
		{
			name:    "ahead of OLD tag but newer release exists: update IS available",
			latest:  "1.6.0",
			current: "v1.5.0-3-gfb1d4e4",
			want:    true,
		},
		{
			name:    "clean older release: update available",
			latest:  "1.5.0",
			current: "v1.4.1",
			want:    true,
		},
		{
			name:    "clean equal release: no update",
			latest:  "1.5.0",
			current: "v1.5.0",
			want:    false,
		},
		{
			name:    "real pre-release is older than its release: update available",
			latest:  "1.5.0",
			current: "v1.5.0-rc.1",
			want:    true,
		},
		{
			name:    "local ahead of latest release: no update",
			latest:  "1.4.0",
			current: "v1.5.0",
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUpdateAvailable(tc.latest, tc.current); got != tc.want {
				t.Fatalf("isUpdateAvailable(%q, %q) = %v, want %v",
					tc.latest, tc.current, got, tc.want)
			}
		})
	}
}

// TestNotifyAvailable_NoDoubleV ensures the notice never prints a doubled
// "v" prefix even when CurrentVersion already carries one (main.Version is
// stamped as "v1.5.0-..." by git-describe / goreleaser).
func TestNotifyAvailable_NoDoubleV(t *testing.T) {
	var buf bytes.Buffer
	NotifyAvailable(&buf, UpdateInfo{
		LatestVersion:   "1.6.0",
		CurrentVersion:  "v1.5.0",
		UpdateAvailable: true,
		ReleaseURL:      "https://example.test/releases/v1.6.0",
	})
	out := buf.String()
	if strings.Contains(out, "vv") {
		t.Fatalf("notice contains doubled v prefix: %q", out)
	}
	if !strings.Contains(out, "v1.6.0 available") {
		t.Fatalf("notice missing latest version: %q", out)
	}
	if !strings.Contains(out, "you have v1.5.0") {
		t.Fatalf("notice missing/garbled current version: %q", out)
	}
}

// TestNotifyAvailable_NoDoubleV_LatestWithV guards the symmetric case where
// LatestVersion unexpectedly carries a "v" too (defensive normalization).
func TestNotifyAvailable_NoDoubleV_LatestWithV(t *testing.T) {
	var buf bytes.Buffer
	NotifyAvailable(&buf, UpdateInfo{
		LatestVersion:   "v1.6.0",
		CurrentVersion:  "v1.5.0",
		UpdateAvailable: true,
		ReleaseURL:      "https://example.test/releases/v1.6.0",
	})
	if out := buf.String(); strings.Contains(out, "vv") {
		t.Fatalf("notice contains doubled v prefix: %q", out)
	}
}
