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
	"net/http"
	"os"
	"path/filepath"
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
	// writes of wizzVersion, so the aggregate's parallel provider goroutines stay
	// race-free under -race.
	wizzVersionMu sync.RWMutex
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

// wizzRealHost reports whether requests target the real Wizz host (not an
// httptest server). Cache load/persist only happen against the real host so unit
// tests stay hermetic.
func wizzRealHost() bool { return wizzHost == "https"+"://"+"be.wizzair.com" }

// wizzProbeVersion classifies a version path via the asset/culture oracle:
// "absent" (404), "live" (200/405), or "inconclusive" (transient/edge/transport).
func wizzProbeVersion(ctx context.Context, v string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wizzHost+"/"+v+"/Api/asset/culture", nil)
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
func wizzHeal(ctx context.Context, stale string) (string, bool) {
	wizzVersionMu.Lock()
	defer wizzVersionMu.Unlock()
	if wizzVersion != stale {
		return wizzVersion, true // another goroutine already healed
	}
	for _, c := range wizzNextCandidates(stale) {
		_ = wizzHealLimiter.Wait(ctx)
		if wizzProbeVersion(ctx, c) == "live" {
			wizzVersion = c
			wizzPersistVersion(c)
			return c, true
		}
	}
	return "", false
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
func wizzPersistVersion(v string) {
	if !wizzRealHost() {
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
func wizzMaybeLoadCache() {
	wizzHealCacheOnce.Do(func() {
		// Skip under a test host or whenever healing is off (operator pin or
		// WIZZAIR_NO_AUTOHEAL): the opt-out must fully restore fail-clean and must
		// not silently adopt a previously-cached healed version.
		if !wizzRealHost() || !wizzHealEnabled() {
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
