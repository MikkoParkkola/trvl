package selfupdate

// GitHub releases API client + on-disk cache for the daily update check.
//
// This file implements ONLY the "is there a newer trvl available?" lookup.
// It does not download archives, does not verify signatures, does not
// touch the binary. Those responsibilities live in sibling files.
//
// Responsibilities:
//   - Hit https://api.github.com/repos/MikkoParkkola/trvl/releases/latest
//     at most once per checkInterval (default 24 h) per machine.
//   - Cache the result at ~/.trvl/update-check.json so subsequent trvl
//     invocations within the interval skip the network call entirely.
//   - Compare semver vs the running binary's Version string.
//   - Fail silent on network / API errors — auto-update is best-effort
//     and must NEVER block or crash trvl. If GitHub is down, the user's
//     trvl session is unaffected.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/upgrade"
)

const (
	// defaultCheckInterval is how long the cache is considered fresh.
	// 24 h matches the cadence of typical release activity for a project
	// of trvl's size and stays well under the unauthenticated GitHub API
	// rate limit (60 requests/hour/IP).
	defaultCheckInterval = 24 * time.Hour

	// defaultReleasesURL is the upstream releases API endpoint. Tests
	// override this via WithReleasesURL.
	defaultReleasesURL = "https://api.github.com/repos/MikkoParkkola/trvl/releases/latest"

	// defaultRequestTimeout caps how long the HTTP call can block. The
	// auto-update path runs in a background goroutine on cold start, so
	// even if the network is slow we don't want a stuck request to keep
	// trvl alive past its normal exit.
	defaultRequestTimeout = 5 * time.Second

	// cacheFilename is the ~/.trvl-relative path to the persisted cache.
	cacheFilename = "update-check.json"

	// userAgent identifies trvl in the GitHub access logs.
	userAgent = "trvl-selfupdate-check/1"
)

// UpdateInfo describes the latest upstream release relative to the
// running binary. Returned by Check; the daily-check goroutine and the
// MCP provider_health tool both read it.
type UpdateInfo struct {
	// CurrentVersion is the version string baked into the running
	// binary at build time (cmd/trvl/main.go's main.Version, set by
	// goreleaser ldflags). Empty or "dev" disables the check.
	CurrentVersion string `json:"current_version"`

	// LatestVersion is the most recent release tag from the upstream
	// repo, with leading "v" stripped (e.g. "1.1.2", not "v1.1.2").
	LatestVersion string `json:"latest_version"`

	// UpdateAvailable is true when LatestVersion is strictly newer than
	// CurrentVersion (per semver comparison). When the running binary is
	// equal-to or ahead of upstream, this is false.
	UpdateAvailable bool `json:"update_available"`

	// ReleaseURL is the human-facing release page on GitHub for the
	// latest version. Notification text shows this so users can read
	// release notes before they upgrade.
	ReleaseURL string `json:"release_url"`

	// CheckedAt is when we last successfully called the GitHub API.
	// Used for cache freshness; if CheckedAt + checkInterval is in the
	// past, we re-fetch.
	CheckedAt time.Time `json:"checked_at"`
}

// Checker coordinates the daily update check. Construct via NewChecker
// to get the production defaults; tests override the HTTP client and
// release URL via the With* options.
type Checker struct {
	httpClient    *http.Client
	releasesURL   string
	cacheDir      string
	checkInterval time.Duration
	currentVer    string
}

// CheckerOption is a functional option for NewChecker.
type CheckerOption func(*Checker)

// WithHTTPClient overrides the HTTP client (test-only).
func WithHTTPClient(c *http.Client) CheckerOption {
	return func(ck *Checker) { ck.httpClient = c }
}

// WithReleasesURL overrides the API endpoint (test-only).
func WithReleasesURL(u string) CheckerOption {
	return func(ck *Checker) { ck.releasesURL = u }
}

// WithCacheDir overrides the on-disk cache location (test-only).
func WithCacheDir(d string) CheckerOption {
	return func(ck *Checker) { ck.cacheDir = d }
}

// WithCheckInterval overrides how long the cache is considered fresh
// (test-only).
func WithCheckInterval(d time.Duration) CheckerOption {
	return func(ck *Checker) { ck.checkInterval = d }
}

// NewChecker constructs a Checker. currentVer is the running binary's
// Version string (typically main.Version). An empty or "dev" version
// disables checking entirely (Check returns the empty UpdateInfo).
func NewChecker(currentVer string, opts ...CheckerOption) (*Checker, error) {
	cacheDir, err := defaultCacheDir()
	if err != nil {
		return nil, fmt.Errorf("selfupdate: resolve cache dir: %w", err)
	}
	c := &Checker{
		httpClient:    &http.Client{Timeout: defaultRequestTimeout},
		releasesURL:   defaultReleasesURL,
		cacheDir:      cacheDir,
		checkInterval: defaultCheckInterval,
		currentVer:    currentVer,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Check returns the UpdateInfo for the running binary. It reads the
// on-disk cache when fresh; falls through to the GitHub API otherwise;
// fails silently (returning the empty UpdateInfo with err == nil) on
// network / API problems so callers can use the result without nil
// checks. The only non-nil error path is fundamental misconfiguration
// (cache dir not creatable / writable).
//
// disableForCI: when true, the check is skipped regardless of cache
// state. Used by the cmd/trvl entry point to honor CI / sandbox env
// detection without baking that detection into this package.
func (c *Checker) Check(ctx context.Context, disableForCI bool) (UpdateInfo, error) {
	// Disable: dev / unstamped binaries (we have no version to compare
	// against) and CI environments (auto-update is hostile in CI).
	if c.currentVer == "" || c.currentVer == "dev" || disableForCI {
		return UpdateInfo{CurrentVersion: c.currentVer}, nil
	}

	if cached, ok := c.readCache(); ok && c.fresh(cached) {
		// Recompute UpdateAvailable in case the running binary version
		// changed since the cache was written (user upgraded out of
		// band — e.g. brew — and the cache hasn't expired yet).
		cached.CurrentVersion = c.currentVer
		cached.UpdateAvailable = upgrade.CompareSemver(cached.LatestVersion, c.currentVer) > 0
		return cached, nil
	}

	info, err := c.fetchLatest(ctx)
	if err != nil {
		// Fail silent: return whatever the cache had, even if stale,
		// so the caller can show "v1.1.2 available (cache 5d old)"
		// rather than nothing.
		if cached, ok := c.readCache(); ok {
			cached.CurrentVersion = c.currentVer
			cached.UpdateAvailable = upgrade.CompareSemver(cached.LatestVersion, c.currentVer) > 0
			return cached, nil
		}
		return UpdateInfo{CurrentVersion: c.currentVer}, nil
	}
	// Best-effort persist: cache write failure does not fail the check.
	_ = c.writeCache(info)
	return info, nil
}

// fetchLatest hits the GitHub releases API and returns the parsed
// UpdateInfo. Honors ctx cancellation and the configured timeout.
func (c *Checker) fetchLatest(ctx context.Context) (UpdateInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.releasesURL, nil)
	if err != nil {
		return UpdateInfo{}, err
	}
	// Accept the v3 API explicitly; saves bytes vs the default and
	// avoids surprises if GitHub starts returning newer formats.
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return UpdateInfo{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return UpdateInfo{}, fmt.Errorf("selfupdate: github api status %d", resp.StatusCode)
	}

	var raw struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return UpdateInfo{}, err
	}
	latest := strings.TrimPrefix(strings.TrimSpace(raw.TagName), "v")
	if latest == "" {
		return UpdateInfo{}, fmt.Errorf("selfupdate: empty tag_name in api response")
	}
	return UpdateInfo{
		CurrentVersion:  c.currentVer,
		LatestVersion:   latest,
		UpdateAvailable: upgrade.CompareSemver(latest, c.currentVer) > 0,
		ReleaseURL:      raw.HTMLURL,
		CheckedAt:       time.Now().UTC(),
	}, nil
}

// fresh reports whether the cached info is still inside checkInterval.
func (c *Checker) fresh(info UpdateInfo) bool {
	if info.CheckedAt.IsZero() {
		return false
	}
	return time.Since(info.CheckedAt) < c.checkInterval
}

// readCache loads the cached UpdateInfo. Returns (zero, false) on any
// read or parse error — callers treat this as "no cache".
func (c *Checker) readCache() (UpdateInfo, bool) {
	path := filepath.Join(c.cacheDir, cacheFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		return UpdateInfo{}, false
	}
	var info UpdateInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return UpdateInfo{}, false
	}
	return info, true
}

// writeCache persists info to ~/.trvl/update-check.json with 0600 perms.
// Best-effort: any error is returned but the caller is expected to
// ignore it (the in-memory result is correct regardless).
func (c *Checker) writeCache(info UpdateInfo) error {
	if err := os.MkdirAll(c.cacheDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(c.cacheDir, cacheFilename)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// defaultCacheDir returns ~/.trvl, matching the directory the rest of
// trvl uses for per-user state (preferences, providers, version stamp,
// trust-anchor copy, etc.).
func defaultCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".trvl"), nil
}
