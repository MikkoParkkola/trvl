package ground

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// stubFerryhopper points the ferryhopper provider at a local server that
// answers every request with the golden Piraeus to Santorini frame, and
// returns its request counter.
func stubFerryhopper(t *testing.T) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", goldenFerryhopperFrame)
	}))
	t.Cleanup(srv.Close)

	origClient, origURL, origLimiter := ferryhopperClient, ferryhopperMCPURL, ferryhopperLimiter
	ferryhopperClient, ferryhopperMCPURL = srv.Client(), srv.URL
	ferryhopperLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() {
		ferryhopperClient, ferryhopperMCPURL, ferryhopperLimiter = origClient, origURL, origLimiter
	})
	groundBreaker.RecordSuccess("ferryhopper")
	return &n
}

// allButFerryhopper excludes every other provider. An exclude-only filter
// still runs the savings pass, unlike an include filter.
func allButFerryhopper() []string {
	return slices.DeleteFunc(launchProviderIDs(), func(id string) bool { return id == "ferryhopper" })
}

// withComposer swaps in a stand-in savings engine for the test.
func withComposer(t *testing.T, fn func(context.Context, HackComposeRequest) *models.HackSaving) {
	t.Helper()
	prev := composeHackSaving
	composeHackSaving = fn
	t.Cleanup(func() { composeHackSaving = prev })
}

// #810: one unfiltered ground search calls each provider at most once per
// route and date, counting the searches its savings pass makes. The stand-in
// engine repeats the outer route search, as night_transport does. On main the
// nested search keys on savingsoff=true, misses the outer call and queries
// ferryhopper a second time.
func TestSearchByName_SavingsPassReusesOuterRound(t *testing.T) {
	t.Setenv("TRVL_PREFETCH", "false")
	calls := stubFerryhopper(t)
	opts := SearchOptions{Currency: "EUR", ExcludeProviders: allButFerryhopper(), NoCache: true}
	const from, to, date = "Piraeus", "Santorini", "2027-07-15"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Baseline: requests one search makes with no savings pass.
	solo := opts
	solo.NoHacks = true
	if _, err := SearchByName(ctx, from, to, date, solo); err != nil {
		t.Fatalf("baseline search: %v", err)
	}
	perSearch := calls.Swap(0)
	if perSearch == 0 {
		t.Fatal("baseline search never reached the ferryhopper stub")
	}

	var nestedRoutes, nestedErrs atomic.Int32
	withComposer(t, func(ctx context.Context, req HackComposeRequest) *models.HackSaving {
		res, err := SearchByName(ctx, req.Origin, req.Destination, req.Date, SearchOptions{
			Currency:         req.Currency,
			Providers:        req.Providers,
			ExcludeProviders: req.ExcludeProviders,
			NoCache:          req.NoCache,
		})
		if err != nil || res == nil {
			nestedErrs.Add(1)
			return nil
		}
		nestedRoutes.Store(int32(len(res.Routes)))
		return nil
	})

	outer, err := SearchByName(ctx, from, to, date, opts)
	if err != nil || outer == nil || !outer.Success {
		t.Fatalf("outer search: err=%v result=%+v", err, outer)
	}
	if got := calls.Load(); got != perSearch {
		t.Errorf("ferryhopper requests = %d for one search plus its savings pass, want %d (one round)", got, perSearch)
	}
	if nestedErrs.Load() != 0 {
		t.Errorf("nested route search failed inside the pass")
	}
	if got := nestedRoutes.Load(); got != int32(len(outer.Routes)) {
		t.Errorf("nested search saw %d routes, want the outer search's %d", got, len(outer.Routes))
	}
}

// fixedRates converts at 1 EUR = 0.85 GBP, offline.
func fixedRates(t *testing.T) {
	t.Helper()
	prev := convertGroundCurrency
	convertGroundCurrency = func(_ context.Context, amount float64, from, to string) (float64, string) {
		switch {
		case from == to:
			return amount, to
		case from == "EUR" && to == "GBP":
			return amount * 0.85, to
		case from == "GBP" && to == "EUR":
			return amount / 0.85, to
		}
		return amount, from
	}
	t.Cleanup(func() { convertGroundCurrency = prev })
}

// A nested search that differs from the outer one in any field of the search
// key is a different query and must not get the outer result. Where the stub
// provider still runs, it costs a second round; excluding the stub too leaves
// nothing to run, and the nested search must come back empty rather than with
// the seeded routes.
func TestSearchByName_SavingsPassOtherQueryStillSearches(t *testing.T) {
	cases := []struct {
		name     string
		vary     func(from, to, date *string, o *SearchOptions)
		noRounds bool    // the varied search runs no provider
		wantGBP  float64 // the varied search's first fare, converted to GBP
	}{
		{name: "date", vary: func(_, _, d *string, _ *SearchOptions) { *d = "2027-07-16" }},
		{name: "origin and destination", vary: func(f, to, _ *string, _ *SearchOptions) { *f, *to = *to, *f }},
		// The stub's EUR 48 ferry at the fixed 0.85 rate.
		{name: "currency", vary: func(_, _, _ *string, o *SearchOptions) { o.Currency = "GBP" }, wantGBP: 40.8},
		{name: "provider filter", vary: func(_, _, _ *string, o *SearchOptions) { o.Providers = []string{"ferryhopper"} }},
		{name: "exclusions", vary: func(_, _, _ *string, o *SearchOptions) {
			o.ExcludeProviders = append(o.ExcludeProviders, "ferryhopper")
		}, noRounds: true},
		{name: "type", vary: func(_, _, _ *string, o *SearchOptions) { o.Type = "ferry" }},
		{name: "price cap", vary: func(_, _, _ *string, o *SearchOptions) { o.MaxPrice = 100 }},
		{name: "browser fallbacks", vary: func(_, _, _ *string, o *SearchOptions) { o.AllowBrowserFallbacks = true }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TRVL_PREFETCH", "false")
			// The environment switch would turn fallbacks on for the outer
			// search too, hiding the option under test.
			t.Setenv("TRVL_ALLOW_BROWSER_FALLBACKS", "false")
			fixedRates(t)
			calls := stubFerryhopper(t)
			opts := SearchOptions{Currency: "EUR", ExcludeProviders: allButFerryhopper(), NoCache: true}
			const from, to, date = "Piraeus", "Santorini", "2027-07-15"
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			solo := opts
			solo.NoHacks = true
			if _, err := SearchByName(ctx, from, to, date, solo); err != nil {
				t.Fatalf("baseline search: %v", err)
			}
			perSearch := calls.Swap(0)
			if perSearch == 0 {
				t.Fatal("baseline search never reached the ferryhopper stub")
			}

			var ran atomic.Bool
			nestedRoutes := -1
			var nestedFirst models.GroundRoute
			withComposer(t, func(ctx context.Context, req HackComposeRequest) *models.HackSaving {
				ran.Store(true)
				f, to, d := req.Origin, req.Destination, req.Date
				nested := SearchOptions{Currency: req.Currency, ExcludeProviders: slices.Clone(req.ExcludeProviders), NoCache: req.NoCache}
				c.vary(&f, &to, &d, &nested)
				if res, err := SearchByName(ctx, f, to, d, nested); err == nil && res != nil {
					nestedRoutes = len(res.Routes)
					if len(res.Routes) > 0 {
						nestedFirst = res.Routes[0]
					}
				}
				return nil
			})
			if _, err := SearchByName(ctx, from, to, date, opts); err != nil {
				t.Fatalf("outer search: %v", err)
			}
			if !ran.Load() {
				t.Fatal("the savings pass never ran")
			}
			if c.wantGBP > 0 && (nestedFirst.Currency != "GBP" || math.Abs(nestedFirst.Price-c.wantGBP) > 0.01) {
				t.Errorf("nested GBP search's first fare = %v %s, want %v GBP", nestedFirst.Price, nestedFirst.Currency, c.wantGBP)
			}
			want := 2 * perSearch
			if c.noRounds {
				want = perSearch
				if nestedRoutes != 0 {
					t.Errorf("nested search with other %s got %d routes, want 0: it reused the outer result", c.name, nestedRoutes)
				}
			}
			if got := calls.Load(); got != want {
				t.Errorf("ferryhopper requests = %d, want %d (the outer query plus the one with another %s)", got, want, c.name)
			}
		})
	}
}

// Detectors searching the same other trip at once inside one pass share a
// single provider round for it.
func TestSearchByName_SavingsPassCoalescesConcurrentSearches(t *testing.T) {
	t.Setenv("TRVL_PREFETCH", "false")
	calls := stubFerryhopper(t)
	opts := SearchOptions{Currency: "EUR", ExcludeProviders: allButFerryhopper(), NoCache: true}
	const from, to, date = "Piraeus", "Santorini", "2027-07-15"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	solo := opts
	solo.NoHacks = true
	if _, err := SearchByName(ctx, from, to, date, solo); err != nil {
		t.Fatalf("baseline search: %v", err)
	}
	perSearch := calls.Swap(0)
	if perSearch == 0 {
		t.Fatal("baseline search never reached the ferryhopper stub")
	}

	var routes [3]int
	withComposer(t, func(ctx context.Context, req HackComposeRequest) *models.HackSaving {
		var wg sync.WaitGroup
		for i := range routes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				nested := SearchOptions{Currency: req.Currency, ExcludeProviders: req.ExcludeProviders, NoCache: req.NoCache}
				if res, err := SearchByName(ctx, req.Origin, req.Destination, "2027-07-16", nested); err == nil && res != nil {
					routes[i] = len(res.Routes)
				}
			}()
		}
		wg.Wait()
		return nil
	})
	if _, err := SearchByName(ctx, from, to, date, opts); err != nil {
		t.Fatalf("outer search: %v", err)
	}
	if got := calls.Load(); got != 2*perSearch {
		t.Errorf("ferryhopper requests = %d, want %d (the outer search plus one shared round for the other date)", got, 2*perSearch)
	}
	for i, n := range routes {
		if n == 0 {
			t.Errorf("concurrent search %d got no routes", i)
		}
	}
}

// A search narrowed by type, price cap and browser fallbacks hands that scope
// to the pass, so a detector repeating it builds the same key and still costs
// no second provider round.
func TestSearchByName_SavingsPassReusesScopedRound(t *testing.T) {
	t.Setenv("TRVL_PREFETCH", "false")
	calls := stubFerryhopper(t)
	// The golden route is a EUR 48 ferry, so these filters keep it.
	opts := SearchOptions{
		Currency: "EUR", ExcludeProviders: allButFerryhopper(), NoCache: true,
		Type: "ferry", MaxPrice: 100, AllowBrowserFallbacks: true,
	}
	const from, to, date = "Piraeus", "Santorini", "2027-07-15"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	solo := opts
	solo.NoHacks = true
	if _, err := SearchByName(ctx, from, to, date, solo); err != nil {
		t.Fatalf("baseline search: %v", err)
	}
	perSearch := calls.Swap(0)

	var nestedRoutes atomic.Int32
	nestedRoutes.Store(-1)
	withComposer(t, func(ctx context.Context, req HackComposeRequest) *models.HackSaving {
		res, err := SearchByName(ctx, req.Origin, req.Destination, req.Date, SearchOptions{
			Currency:              req.Currency,
			Providers:             req.Providers,
			ExcludeProviders:      req.ExcludeProviders,
			NoCache:               req.NoCache,
			Type:                  req.Type,
			MaxPrice:              req.MaxPrice,
			AllowBrowserFallbacks: req.AllowBrowserFallbacks,
		})
		if err == nil && res != nil {
			nestedRoutes.Store(int32(len(res.Routes)))
		}
		return nil
	})
	outer, err := SearchByName(ctx, from, to, date, opts)
	if err != nil || outer == nil || len(outer.Routes) == 0 {
		t.Fatalf("outer search found nothing, so the pass never ran: err=%v result=%+v", err, outer)
	}
	if got := nestedRoutes.Load(); got != int32(len(outer.Routes)) {
		t.Errorf("nested search saw %d routes, want the outer search's %d", got, len(outer.Routes))
	}
	if got := calls.Load(); got != perSearch {
		t.Errorf("ferryhopper requests = %d for one scoped search plus its savings pass, want %d", got, perSearch)
	}
}

// Routes handed out by the pass are the caller's own copy: a detector that
// edits them does not change what the next detector or the outer search sees.
func TestSearchByName_SavingsPassRoutesAreCopies(t *testing.T) {
	t.Setenv("TRVL_PREFETCH", "false")
	stubFerryhopper(t)
	opts := SearchOptions{Currency: "EUR", ExcludeProviders: allButFerryhopper(), NoCache: true}
	const from, to, date = "Piraeus", "Santorini", "2027-07-15"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var first, second float64
	withComposer(t, func(ctx context.Context, req HackComposeRequest) *models.HackSaving {
		nested := SearchOptions{Currency: req.Currency, ExcludeProviders: req.ExcludeProviders, NoCache: req.NoCache}
		a, err := SearchByName(ctx, req.Origin, req.Destination, req.Date, nested)
		if err != nil || a == nil || len(a.Routes) == 0 {
			return nil
		}
		first = a.Routes[0].Price
		a.Routes[0].Price = -1
		b, err := SearchByName(ctx, req.Origin, req.Destination, req.Date, nested)
		if err == nil && b != nil && len(b.Routes) > 0 {
			second = b.Routes[0].Price
		}
		return nil
	})
	outer, err := SearchByName(ctx, from, to, date, opts)
	if err != nil || outer == nil || len(outer.Routes) == 0 {
		t.Fatalf("outer search: err=%v result=%+v", err, outer)
	}
	if first <= 0 || second != first {
		t.Errorf("second nested search saw price %v after the first caller edited its copy, want %v", second, first)
	}
	if outer.Routes[0].Price != first {
		t.Errorf("outer route price %v changed by a detector's edit, want %v", outer.Routes[0].Price, first)
	}
}
