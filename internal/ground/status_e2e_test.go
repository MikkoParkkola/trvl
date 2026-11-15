package ground

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/breaker"
	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// isolateGroundBreaker gives the test a fresh default breaker so failures here
// never leak circuit_broken rows into other tests.
func isolateGroundBreaker(t *testing.T) {
	t.Helper()
	orig := groundBreaker
	groundBreaker = breaker.New()
	t.Cleanup(func() { groundBreaker = orig })
}

func withFerryhopperServer(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	origClient, origURL, origLimiter := ferryhopperClient, ferryhopperMCPURL, ferryhopperLimiter
	ferryhopperClient, ferryhopperMCPURL, ferryhopperLimiter = srv.Client(), srv.URL, rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() {
		srv.Close()
		ferryhopperClient, ferryhopperMCPURL, ferryhopperLimiter = origClient, origURL, origLimiter
	})
}

func ferryhopperFrame(frame string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", frame)
	}
}

const ferryhopperEmptyFrame = `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"Found no itineraries."}],"structuredContent":{"foundDirectItinerariesForTrip":[]}}}`

const ferryhopperNoRouteFrame = `{"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[{"type":"text","text":"No route found for Piraeus - Santorini on 2026-07-15"}]}}`

func runCore(t *testing.T, ctx context.Context, from, to string, providers ...string) *models.GroundSearchResult {
	t.Helper()
	opts := SearchOptions{Currency: "EUR", Providers: providers, NoCache: true, NoHacks: true}
	res, err := searchByNameCore(ctx, from, to, "2026-07-15", opts, "status-e2e", false)
	if err != nil {
		t.Fatalf("searchByNameCore: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	return res
}

// TestSearchByNameCore_DeadlineIsTimeout pins D3 end to end: a provider cut
// off by the deadline is a timeout counted missing, not a skipped provider.
func TestSearchByNameCore_DeadlineIsTimeout(t *testing.T) {
	isolateGroundBreaker(t)
	// Drain the body so the server notices the client hanging up, and cap the
	// wait so a missed cancellation cannot hang srv.Close.
	withFerryhopperServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	res := runCore(t, ctx, "Piraeus", "Santorini", "ferryhopper")
	st := statusByID(res.ProviderStatuses)["ferryhopper"]
	if st.Status != models.StatusTimeout {
		t.Errorf("ferryhopper status = %q, want timeout", st.Status)
	}
	if res.Completeness.State == models.CompletenessComplete || len(res.Completeness.Missing) != 1 {
		t.Errorf("completeness = %+v, want ferryhopper missing", res.Completeness)
	}
}

// TestSearchByNameCore_ValidEmptyIsCheckedNoHit pins that a provider that
// validly answered with nothing is checked_no_hit, not ok with 0 results.
func TestSearchByNameCore_ValidEmptyIsCheckedNoHit(t *testing.T) {
	isolateGroundBreaker(t)
	withFerryhopperServer(t, ferryhopperFrame(ferryhopperEmptyFrame))
	res := runCore(t, context.Background(), "Piraeus", "Santorini", "ferryhopper")
	if st := statusByID(res.ProviderStatuses)["ferryhopper"]; st.Status != models.StatusCheckedNoHit {
		t.Errorf("ferryhopper status = %q, want checked_no_hit", st.Status)
	}
}

// TestSearchFerryhopper_NoRouteFoundIsValidEmpty pins that Ferryhopper's
// tool-level "No route found" for the searched date is an answer, not an error.
func TestSearchFerryhopper_NoRouteFoundIsValidEmpty(t *testing.T) {
	withFerryhopperServer(t, ferryhopperFrame(ferryhopperNoRouteFrame))
	routes, err := SearchFerryhopper(context.Background(), "Piraeus", "Santorini", "2026-07-15", "EUR")
	if err != nil || len(routes) != 0 {
		t.Fatalf("got %d routes, err %v; want 0 routes and nil", len(routes), err)
	}
}

// TestSearchByNameCore_FerryhopperNoRouteIsCheckedNoHit is the same answer
// seen through the merge.
func TestSearchByNameCore_FerryhopperNoRouteIsCheckedNoHit(t *testing.T) {
	isolateGroundBreaker(t)
	withFerryhopperServer(t, ferryhopperFrame(ferryhopperNoRouteFrame))
	res := runCore(t, context.Background(), "Piraeus", "Santorini", "ferryhopper")
	if st := statusByID(res.ProviderStatuses)["ferryhopper"]; st.Status != models.StatusCheckedNoHit {
		t.Errorf("ferryhopper status = %q, want checked_no_hit", st.Status)
	}
}

// TestSearchByNameCore_RouteGateFalseRow pins that a requested provider whose
// route gate is false still gets a row: skipped, reason route_not_served.
func TestSearchByNameCore_RouteGateFalseRow(t *testing.T) {
	res := runCore(t, context.Background(), "Prague", "Vienna", "dfds")
	st, ok := statusByID(res.ProviderStatuses)["dfds"]
	if !ok {
		t.Fatalf("no dfds status row; rows = %+v", res.ProviderStatuses)
	}
	if st.Status != models.StatusSkipped || st.Reason != models.ReasonRouteNotServed {
		t.Errorf("dfds row = %+v, want skipped with reason %q", st, models.ReasonRouteNotServed)
	}
}

// TestSearchByNameCore_DistribusionWithoutKeyIsNotConfigured pins that a
// requested provider missing its API key reports not_configured.
func TestSearchByNameCore_DistribusionWithoutKeyIsNotConfigured(t *testing.T) {
	t.Setenv("DISTRIBUSION_API_KEY", "")
	res := runCore(t, context.Background(), "Helsinki", "Tallinn", "distribusion")
	st, ok := statusByID(res.ProviderStatuses)["distribusion"]
	if !ok {
		t.Fatalf("no distribusion status row; rows = %+v", res.ProviderStatuses)
	}
	if st.Status != models.StatusNotConfigured {
		t.Errorf("distribusion status = %q, want not_configured", st.Status)
	}
}

// TestSearchByNameCore_EurostarLaunchesGatedByOwnName pins that asking for
// "eurostar snap" does not launch regular Eurostar, and the reverse. London to
// Paris is a Snap route, so both names could launch. Both breakers are
// tripped, so the asked-for launch shows up as a circuit_broken row without
// touching the network, and the other name has no row at all.
func TestSearchByNameCore_EurostarLaunchesGatedByOwnName(t *testing.T) {
	cases := []struct{ asked, notLaunched string }{
		{"eurostar snap", "eurostar"},
		{"eurostar", "eurostar snap"},
	}
	for _, c := range cases {
		t.Run(c.asked, func(t *testing.T) {
			swapGroundBreaker(t)
			groundBreaker.RecordFailure("eurostar")
			groundBreaker.RecordFailure("eurostar snap")
			res := runCore(t, context.Background(), "London", "Paris", c.asked)
			rows := statusByID(res.ProviderStatuses)
			if st := rows[c.asked]; st.Status != models.StatusCircuitBroken {
				t.Errorf("asked-for %q row = %+v, want circuit_broken; rows = %+v", c.asked, st, res.ProviderStatuses)
			}
			if st, ok := rows[c.notLaunched]; ok {
				t.Errorf("%q was launched (row %+v) although only %q was asked for", c.notLaunched, st, c.asked)
			}
		})
	}
}

// TestSearchByNameCore_EveryAliasLaunches pins that the non-marketed names the
// filter accepts really launch: each breaker is tripped, so a launch shows up
// as a circuit_broken row without touching the network.
func TestSearchByNameCore_EveryAliasLaunches(t *testing.T) {
	for _, name := range []string{"rome2rio"} {
		t.Run(name, func(t *testing.T) {
			swapGroundBreaker(t)
			groundBreaker.RecordFailure(name)
			res := runCore(t, context.Background(), "London", "Paris", name)
			if st := statusByID(res.ProviderStatuses)[name]; st.Status != models.StatusCircuitBroken {
				t.Errorf("%s row = %+v, want circuit_broken (launched)", name, st)
			}
		})
	}
}
