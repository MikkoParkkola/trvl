package ground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/cache"
	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

func freshNegCache(t *testing.T) {
	t.Helper()
	t.Setenv("TRVL_NEGCACHE", "1")
	t.Setenv("TRVL_PREFETCH", "0")
	orig := groundNegCache
	groundNegCache = cache.NewNegativeCache(groundNegCacheTTL)
	t.Cleanup(func() { groundNegCache = orig })
}

func negKeyFor(t *testing.T, from, to, date string, opts SearchOptions) string {
	t.Helper()
	k, ok := groundNegKey(from, to, date, opts, groundProviderKey(opts))
	if !ok {
		t.Fatalf("search %s>%s %s %+v not eligible for the negative cache", from, to, date, opts)
	}
	return k
}

// TestGroundNegKey_ExactDate pins that ground "no service" is cached per
// date: a ferry that does not sail on the 15th may sail on the 16th.
func TestGroundNegKey_ExactDate(t *testing.T) {
	a := negKeyFor(t, "Helsinki", "Tallinn", "2026-07-15", SearchOptions{})
	b := negKeyFor(t, "Helsinki", "Tallinn", "2026-07-16", SearchOptions{})
	if a == b {
		t.Errorf("keys for 2026-07-15 and 2026-07-16 are equal (%q); want one key per date", a)
	}
}

// TestGroundNegKey_IncludesScope pins that the exclusion list and the
// browser-fallback mode are part of the key: either can change the answer.
func TestGroundNegKey_IncludesScope(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_FALLBACKS", "")
	base := negKeyFor(t, "Helsinki", "Tallinn", "2026-07-15", SearchOptions{})
	if k := negKeyFor(t, "Helsinki", "Tallinn", "2026-07-15", SearchOptions{ExcludeProviders: []string{"vikingline"}}); k == base {
		t.Error("ExcludeProviders does not change the negative-cache key")
	}
	if k := negKeyFor(t, "Helsinki", "Tallinn", "2026-07-15", SearchOptions{AllowBrowserFallbacks: true}); k == base {
		t.Error("browser-fallback mode does not change the negative-cache key")
	}
}

func runCoreCached(t *testing.T, from, to string, providers ...string) (*models.GroundSearchResult, string) {
	t.Helper()
	opts := SearchOptions{Currency: "EUR", Providers: providers, NoHacks: true}
	res, err := searchByNameCore(context.Background(), from, to, "2026-07-15", opts, "negcache-"+strings.Join(providers, ","), false)
	if err != nil {
		t.Fatalf("searchByNameCore: %v", err)
	}
	return res, negKeyFor(t, from, to, "2026-07-15", opts)
}

// TestNegCache_NotMarkedWhenNothingQueried pins that a search that queried no
// provider at all (every route gate false) proves nothing and is not cached.
func TestNegCache_NotMarkedWhenNothingQueried(t *testing.T) {
	freshNegCache(t)
	_, key := runCoreCached(t, "Prague", "Vienna", "dfds")
	if groundNegCache.Seen(key) {
		t.Error("negative cache marked although no provider was queried")
	}
}

// TestNegCache_CheckedNoHitNotCached pins that a date-specific empty answer
// (sailing exists, none that day) is never cached as "no service".
func TestNegCache_CheckedNoHitNotCached(t *testing.T) {
	freshNegCache(t)
	isolateGroundBreaker(t)
	withFerryhopperServer(t, ferryhopperFrame(ferryhopperEmptyFrame))
	_, key := runCoreCached(t, "Piraeus", "Santorini", "ferryhopper")
	if groundNegCache.Seen(key) {
		t.Error("negative cache marked on a checked_no_hit answer")
	}
}

// withEmptyFlixBusAutocomplete serves an empty FlixBus city list, so FlixBus
// answers "never served" for any pair.
func withEmptyFlixBusAutocomplete(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "autocomplete") {
			_ = json.NewEncoder(w).Encode([]flixbusAutocompleteItem{})
			return
		}
		http.NotFound(w, r)
	}))
	origClient, origLimiter := httpClient, flixbusLimiter
	httpClient = &http.Client{Transport: &urlRewriter{base: srv.URL}, Timeout: 5 * time.Second}
	flixbusLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() {
		srv.Close()
		httpClient, flixbusLimiter = origClient, origLimiter
	})
}

// TestNegCache_MarkedWhenEveryQueriedProviderNeverServes guards the positive
// case: a provider was queried and answered "never served", so it is cached.
func TestNegCache_MarkedWhenEveryQueriedProviderNeverServes(t *testing.T) {
	freshNegCache(t)
	isolateGroundBreaker(t)
	withEmptyFlixBusAutocomplete(t)
	_, key := runCoreCached(t, "Atlantis", "El Dorado", "flixbus")
	if !groundNegCache.Seen(key) {
		t.Error("negative cache not marked although every queried provider never serves the pair")
	}
}

// TestNegCache_HitSynthesisesStatusRows pins that a negative-cache hit still
// reports one row per requested provider: skipped, reason route_not_served.
// The shared client points at a local 404 server so a missed hit stays offline.
func TestNegCache_HitSynthesisesStatusRows(t *testing.T) {
	freshNegCache(t)
	isolateGroundBreaker(t)
	withEmptyFlixBusAutocomplete(t)
	opts := SearchOptions{Currency: "EUR", Providers: []string{"flixbus", "regiojet"}, NoHacks: true}
	groundNegCache.Mark(negKeyFor(t, "Atlantis", "El Dorado", "2026-07-15", opts))
	res, err := SearchByName(context.Background(), "Atlantis", "El Dorado", "2026-07-15", opts)
	if err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	rows := statusByID(res.ProviderStatuses)
	for _, id := range []string{"flixbus", "regiojet"} {
		st, ok := rows[id]
		if !ok {
			t.Errorf("no %s row on a negative-cache hit; rows = %+v", id, res.ProviderStatuses)
			continue
		}
		if st.Status != models.StatusSkipped || st.Reason != models.ReasonRouteNotServed {
			t.Errorf("%s row = %+v, want skipped with reason %q", id, st, models.ReasonRouteNotServed)
		}
	}
}
