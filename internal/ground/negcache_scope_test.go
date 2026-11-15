package ground

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// TestGroundNegKey_IncludesScope pins that the provider filter, the exclusion
// list and the browser-fallback mode are part of the key: each can change the
// answer.
func TestGroundNegKey_IncludesScope(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_FALLBACKS", "")
	base := negKeyFor(t, "Helsinki", "Tallinn", "2026-07-15", SearchOptions{})
	if k := negKeyFor(t, "Helsinki", "Tallinn", "2026-07-15", SearchOptions{ExcludeProviders: []string{"vikingline"}}); k == base {
		t.Error("ExcludeProviders does not change the negative-cache key")
	}
	if k := negKeyFor(t, "Helsinki", "Tallinn", "2026-07-15", SearchOptions{AllowBrowserFallbacks: true}); k == base {
		t.Error("browser-fallback mode does not change the negative-cache key")
	}
	if negKeyFor(t, "Helsinki", "Tallinn", "2026-07-15", SearchOptions{Providers: []string{"vikingline"}}) ==
		negKeyFor(t, "Helsinki", "Tallinn", "2026-07-15", SearchOptions{Providers: []string{"tallink"}}) {
		t.Error("different Providers lists share a negative-cache key")
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

// withNeverServedBusLookups serves empty FlixBus and RegioJet city lists, so
// both answer "never served" for any pair, and counts the requests it gets.
func withNeverServedBusLookups(t *testing.T) *atomic.Int64 {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "autocomplete"):
			_ = json.NewEncoder(w).Encode([]flixbusAutocompleteItem{})
		case strings.Contains(r.URL.Path, "/consts/locations"):
			_ = json.NewEncoder(w).Encode([]regiojetCountry{})
		default:
			http.NotFound(w, r)
		}
	}))
	origClient, origFlix, origRJ := httpClient, flixbusLimiter, regiojetLimiter
	httpClient = &http.Client{Transport: &urlRewriter{base: srv.URL}, Timeout: 5 * time.Second}
	flixbusLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	regiojetLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	regiojetLocationCacheMu.Lock()
	origCache := regiojetLocationCache
	regiojetLocationCache = nil
	regiojetLocationCacheMu.Unlock()
	t.Cleanup(func() {
		srv.Close()
		httpClient, flixbusLimiter, regiojetLimiter = origClient, origFlix, origRJ
		regiojetLocationCacheMu.Lock()
		regiojetLocationCache = origCache
		regiojetLocationCacheMu.Unlock()
	})
	return &hits
}

// TestNegCache_HitSynthesisesStatusRows pins that a real negative-cache hit
// skips the fan-out and still reports one row per requested provider:
// skipped, reason route_not_served. The first search really queries both
// providers and they really answer "never served".
func TestNegCache_HitSynthesisesStatusRows(t *testing.T) {
	freshNegCache(t)
	isolateGroundBreaker(t)
	hits := withNeverServedBusLookups(t)
	opts := SearchOptions{Currency: "EUR", Providers: []string{"flixbus", "regiojet"}, NoHacks: true}
	if _, err := SearchByName(context.Background(), "Atlantis", "El Dorado", "2026-07-15", opts); err != nil {
		t.Fatalf("first SearchByName: %v", err)
	}
	if !groundNegCache.Seen(negKeyFor(t, "Atlantis", "El Dorado", "2026-07-15", opts)) {
		t.Fatal("first search did not mark the negative cache; the hit below would not be a hit")
	}
	before := hits.Load()
	res, err := SearchByName(context.Background(), "Atlantis", "El Dorado", "2026-07-15", opts)
	if err != nil {
		t.Fatalf("second SearchByName: %v", err)
	}
	if n := hits.Load() - before; n != 0 {
		t.Errorf("negative-cache hit still sent %d provider requests", n)
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

// withHangingFinnlines points Finnlines at a server that never answers and
// gives its client a 20ms budget, so Finnlines times out on its own while the
// other providers finish normally.
func withHangingFinnlines(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// Drain the body so the server notices the client hanging up.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	origClient, origLimiter := finnlinesClient, finnlinesLimiter
	finnlinesClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 20 * time.Millisecond}
	finnlinesLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() {
		srv.Close()
		finnlinesClient, finnlinesLimiter = origClient, origLimiter
	})
}

// TestNegCache_NotMarkedOnMixedOutcomes pins that "no service" is cached only
// when every queried provider never serves the pair. One never-served answer
// next to a date-specific empty answer, or next to a timeout, proves nothing
// about the pair and must not be cached.
func TestNegCache_NotMarkedOnMixedOutcomes(t *testing.T) {
	cases := []struct {
		name      string
		providers []string
		timeout   bool
	}{
		{"not served + checked_no_hit", []string{"flixbus", "ferryhopper"}, false},
		{"not served + checked_no_hit + timeout", []string{"flixbus", "ferryhopper", "finnlines"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			freshNegCache(t)
			isolateGroundBreaker(t)
			withEmptyFlixBusAutocomplete(t)
			withFerryhopperServer(t, ferryhopperFrame(ferryhopperEmptyFrame))
			if c.timeout {
				withHangingFinnlines(t)
			}
			_, key := runCoreCached(t, "Helsinki", "Travemünde", c.providers...)
			if groundNegCache.Seen(key) {
				t.Errorf("negative cache marked after a mixed outcome from %v", c.providers)
			}
		})
	}
}
