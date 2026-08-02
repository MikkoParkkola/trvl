package flights

// Runtime self-healing for Wizz Air API version rotations (#430).
//
// Wizz rotates the {version} segment in be.wizzair.com/{version}/Api/... with no
// announcement; the old path then 404s and every Wizz search fails. The original
// design deliberately had NO runtime discovery, on two premises that have since
// been disproven:
//
//   - "No clean server-side endpoint returns the version." False: the
//     be.wizzair.com/<v>/Api/asset/culture route is a deterministic oracle —
//     404 = version path absent, 200/405 = present — over plain HTTP, no JS, no
//     headless browser (so the single-binary principle is intact).
//   - "A guessed version could be wrong." The binary is in fact the BEST verifier:
//     it runs from the user's real (often residential) IP, so after discovering a
//     candidate it confirms end-to-end by retrying the real timetable search —
//     something CI (edge-blocked on that endpoint) fundamentally cannot do.
//
// So on a rotation 404, SearchWizzair discovers the new version, swaps it in,
// caches it to ~/.trvl/wizzair_version.json, and retries once — the rotation is
// invisible to the user. Disabled when WIZZAIR_API_VERSION is set (operator pin)
// or WIZZAIR_NO_AUTOHEAL=1 (opt back into fail-clean behaviour). The CI sentinel
// (.github/workflows/wizzair-version-sentinel.yml) is kept as belt-and-suspenders
// to keep the committed default fresh for cold-start installs.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// wizzBrowserUA is the browser-like User-Agent used for both the timetable
// request and the discovery probe (Wizz's edge is friendlier to it).
const wizzBrowserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

var (
	// wizzVersionMu guards concurrent reads (wizzResolvedVersion) and heal-time
	// writes of wizzVersion and wizzHostVersions, so the aggregate's parallel
	// provider goroutines stay race-free under -race.
	wizzVersionMu sync.RWMutex
	// wizzHostVersions holds healed versions for non-production host overrides
	// (tests, or any explicit SearchOptions.wizzHost), keyed by host. A heal
	// against a non-production host writes here instead of the shared
	// wizzVersion global, so a search against one host can never heal-and-force
	// its discovered version onto a concurrent search against a different host.
	// The production host (wizzDefaultHost) always uses wizzVersion directly, so
	// production behaviour and on-disk persistence are unaffected.
	wizzHostVersions map[string]string
	// wizzHealCacheOnce loads the on-disk cached version exactly once per process.
	wizzHealCacheOnce sync.Once
	// wizzHealLimiter throttles discovery probes so a rotation can't burst the edge.
	wizzHealLimiter = rate.NewLimiter(rate.Every(time.Second), 1)
)

// wizzHealEnabled reports whether runtime self-healing should run. An operator
// pin (WIZZAIR_API_VERSION) or an explicit opt-out (WIZZAIR_NO_AUTOHEAL=1) keeps
// the old fail-clean behaviour.
func wizzHealEnabled() bool {
	if strings.TrimSpace(os.Getenv("WIZZAIR_API_VERSION")) != "" {
		return false
	}
	return os.Getenv("WIZZAIR_NO_AUTOHEAL") != "1"
}

// wizzRealHost reports whether host targets the real Wizz host (not an httptest
// server). Cache load/persist only happen against the real host so unit tests
// stay hermetic.
func wizzRealHost(host string) bool { return host == wizzDefaultHost }

// wizzProbeVersion classifies a version path via the asset/culture oracle:
// "absent" (404), "live" (200/405), or "inconclusive" (transient/edge/transport).
func wizzProbeVersion(ctx context.Context, host, v string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host+"/"+v+"/Api/asset/culture", nil)
	if err != nil {
		return "inconclusive"
	}
	req.Header.Set("User-Agent", wizzBrowserUA)
	resp, err := wizzClient.Do(req)
	if err != nil {
		return "inconclusive"
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	switch resp.StatusCode {
	case http.StatusNotFound:
		return "absent"
	case http.StatusOK, http.StatusMethodNotAllowed:
		return "live"
	default:
		return "inconclusive"
	}
}

// wizzConfigVersionRe extracts the API version from the version-stamped Api base
// URL that the Wizz homepage embeds in its inline bootstrap config
// (apiUrl:"https://be.wizzair.com/<version>/Api").
var wizzConfigVersionRe = regexp.MustCompile(`be\.wizzair\.com/(\d+\.\d+\.\d+)/Api`)

// wizzConfigURL is the page whose inline config names the version. Derived from
// host so tests stay hermetic (an httptest host serves its own /en-gb).
func wizzConfigURL(host string) string {
	if wizzRealHost(host) {
		return "https://www.wizzair.com/en-gb"
	}
	return host + "/en-gb"
}

// wizzDiscoverFromConfig reads the version the real browser client is told to
// use, straight from the homepage bootstrap config. This is the authoritative
// source: the blind candidate walk (wizzNextCandidates) can only find rotations
// inside its bounded range, so a larger jump leaves the client stuck on a dead
// version even though the answer is published in plain HTML, no JS, no cookies.
// Returns "" on any failure — the caller then falls back to the walk, so this is
// strictly additive. The result is a claim, not a fact: the caller MUST confirm
// it with wizzProbeVersion before adopting it.
func wizzDiscoverFromConfig(ctx context.Context, host string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wizzConfigURL(host), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", wizzBrowserUA)
	resp, err := wizzClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	// Bounded read: the homepage is ~2MB and is attacker-adjacent input, so it
	// must never be able to grow this into unbounded memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return ""
	}
	if m := wizzConfigVersionRe.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return ""
}

// wizzNextCandidates returns likely rotation targets from the stale X.Y.Z, most
// likely first: next minors (history shows minor +1 is the common rotation),
// then patches, then the next majors. Bounded; a jump beyond this range is left
// to the operator / CI sentinel.
func wizzNextCandidates(cur string) []string {
	p := strings.Split(cur, ".")
	if len(p) != 3 {
		return nil
	}
	ma, e1 := strconv.Atoi(p[0])
	mi, e2 := strconv.Atoi(p[1])
	pa, e3 := strconv.Atoi(p[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return nil
	}
	out := make([]string, 0, 12)
	for d := 1; d <= 6; d++ {
		out = append(out, itoa3(ma, mi+d, 0))
	}
	for d := 1; d <= 3; d++ {
		out = append(out, itoa3(ma, mi, pa+d))
	}
	out = append(out, itoa3(ma+1, 0, 0), itoa3(ma+1, 1, 0), itoa3(ma+2, 0, 0))
	return out
}

func itoa3(a, b, c int) string {
	return strconv.Itoa(a) + "." + strconv.Itoa(b) + "." + strconv.Itoa(c)
}

// wizzVersionNewer reports whether semver a is strictly newer than b. Returns
// false for equal versions and for any malformed (non X.Y.Z numeric) input — so
// a corrupt cache is never adopted.
func wizzVersionNewer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	if len(pa) != 3 || len(pb) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		if ea != nil || eb != nil {
			return false
		}
		if na != nb {
			return na > nb
		}
	}
	return false
}

// wizzHeal discovers a live version when `stale` has rotated. Serialized via the
// write lock so concurrent searches don't stampede the edge; idempotent — a
// caller arriving after another goroutine already healed gets the new version.
// Returns ("", false) when no candidate in range is live (caller keeps the typed
// rotation error — graceful degradation preserved).
//
// The healed version is scoped to host: a production-host (wizzDefaultHost) heal
// updates the shared wizzVersion global and persists to disk, exactly as before.
// A heal against any other host (a per-call SearchOptions.wizzHost override) only
// ever writes wizzHostVersions[host] — it can never mutate the global, so a
// concurrent search against a different host (production or another override)
// cannot be healed onto a version that was only verified live against this one.
// Discovery order: the homepage config first (authoritative — it is the version
// the browser client is served, and it survives a rotation of any size), then the
// bounded candidate walk as fallback. Either way the version is only adopted
// after wizzProbeVersion confirms it live against this host, and every adoption
// is logged with its source.
func wizzHeal(ctx context.Context, host, stale string) (string, bool) {
	wizzVersionMu.Lock()
	defer wizzVersionMu.Unlock()
	if cur := wizzLockedVersion(host); cur != stale {
		return cur, true // another goroutine already healed
	}
	if c := wizzDiscoverFromConfig(ctx, host); c != "" && c != stale {
		if wizzProbeVersion(ctx, host, c) == "live" {
			wizzAdoptVersion(host, stale, c, "config")
			return c, true
		}
	}
	for _, c := range wizzNextCandidates(stale) {
		_ = wizzHealLimiter.Wait(ctx)
		if wizzProbeVersion(ctx, host, c) == "live" {
			wizzAdoptVersion(host, stale, c, "walk")
			return c, true
		}
	}
	return "", false
}

// wizzAdoptVersion is the single point at which a discovered version becomes the
// one in use. It logs there rather than at the call sites so a rotation is never
// silent: an adoption by a goroutine whose caller then takes the already-healed
// early return would otherwise leave no trace at all. Callers must hold
// wizzVersionMu for writing.
func wizzAdoptVersion(host, from, to, source string) {
	slog.Info("wizzair api version rotated", "from", from, "to", to, "source", source, "host", host)
	wizzSetLockedVersion(host, to)
}

// wizzLockedVersion returns the currently-known version for host. Callers must
// hold wizzVersionMu (read or write lock).
func wizzLockedVersion(host string) string {
	if !wizzRealHost(host) {
		if v, ok := wizzHostVersions[host]; ok {
			return v
		}
	}
	return wizzVersion
}

// wizzSetLockedVersion records a newly-healed version for host. Callers must
// hold wizzVersionMu for writing. Only the production host updates the shared
// global and on-disk cache; any other host is scoped to wizzHostVersions so it
// can never leak into production or into a different host override.
func wizzSetLockedVersion(host, v string) {
	if wizzRealHost(host) {
		wizzVersion = v
		wizzPersistVersion(host, v)
		return
	}
	if wizzHostVersions == nil {
		wizzHostVersions = make(map[string]string)
	}
	wizzHostVersions[host] = v
}

type wizzVersionCache struct {
	Version      string `json:"version"`
	DiscoveredAt string `json:"discovered_at"`
}

func wizzCachePath() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".trvl", "wizzair_version.json"), true
}

// wizzPersistVersion best-effort writes the discovered version so a restart skips
// rediscovery. No-op against a test host or on any I/O error (caching is an
// optimisation, never load-bearing).
func wizzPersistVersion(host, v string) {
	if !wizzRealHost(host) {
		return
	}
	path, ok := wizzCachePath()
	if !ok {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	b, err := json.Marshal(wizzVersionCache{Version: v, DiscoveredAt: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, b, 0o644)
}

// wizzMaybeLoadCache loads a previously-healed version once per process, so a
// fresh run starts from the last-known-live version rather than the (possibly
// stale) compiled-in default. Skipped under a test host or an operator pin.
func wizzMaybeLoadCache(host string) {
	wizzHealCacheOnce.Do(func() {
		// Skip under a test host or whenever healing is off (operator pin or
		// WIZZAIR_NO_AUTOHEAL): the opt-out must fully restore fail-clean and must
		// not silently adopt a previously-cached healed version.
		if !wizzRealHost(host) || !wizzHealEnabled() {
			return
		}
		path, ok := wizzCachePath()
		if !ok {
			return
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return
		}
		var c wizzVersionCache
		// Only adopt the cache when it is strictly NEWER than the compiled
		// default. Otherwise a release that bumps wizzDefaultVersion past a user's
		// cached value would be silently downgraded back to the stale cache (and
		// could then fail if Wizz had moved beyond the heal walk's range from it).
		// wizzVersionNewer also rejects a malformed/corrupt cache (returns false).
		if json.Unmarshal(b, &c) == nil && wizzVersionNewer(c.Version, wizzDefaultVersion) {
			wizzVersionMu.Lock()
			wizzVersion = c.Version
			wizzVersionMu.Unlock()
		}
	})
}
