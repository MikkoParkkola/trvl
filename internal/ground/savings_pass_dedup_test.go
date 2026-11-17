package ground

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
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

// A nested search for a different date is a different trip: it must still
// query the provider (the shared round covers only the outer route and date).
func TestSearchByName_SavingsPassOtherDateStillSearches(t *testing.T) {
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

	withComposer(t, func(ctx context.Context, req HackComposeRequest) *models.HackSaving {
		_, _ = SearchByName(ctx, req.Origin, req.Destination, "2027-07-16", SearchOptions{
			Currency:         req.Currency,
			ExcludeProviders: req.ExcludeProviders,
			NoCache:          req.NoCache,
		})
		return nil
	})
	if _, err := SearchByName(ctx, from, to, date, opts); err != nil {
		t.Fatalf("outer search: %v", err)
	}
	if got := calls.Load(); got != 2*perSearch {
		t.Errorf("ferryhopper requests = %d, want %d (outer date plus the other date)", got, 2*perSearch)
	}
}
