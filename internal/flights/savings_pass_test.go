package flights

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/searchctx"
)

// A flight savings pass never starts inside another one, such as a ground
// search's pass whose detectors run flight searches.
func TestAttachFlightHackSaving_NotInsideAnotherPass(t *testing.T) {
	called := false
	withStubComposer(t, func(context.Context, HackComposeRequest) *models.HackSaving {
		called = true
		return nil
	})
	inGroundPass := searchctx.StartSavingsPass(context.Background())
	attachFlightHackSaving(inGroundPass, okFlightResult(), "HEL", "AMS", "2026-12-01", SearchOptions{})
	if called {
		t.Error("flight savings pass started inside another savings pass")
	}
}

// The pass carries the shared marker, so its detectors' own ground and flight
// searches start no pass of their own.
func TestAttachFlightHackSaving_MarksItsPass(t *testing.T) {
	marked := false
	withStubComposer(t, func(ctx context.Context, _ HackComposeRequest) *models.HackSaving {
		marked = searchctx.SavingsPassSuppressed(ctx)
		return nil
	})
	attachFlightHackSaving(context.Background(), okFlightResult(), "HEL", "AMS", "2026-12-01", SearchOptions{})
	if !marked {
		t.Error("the savings pass context does not carry the shared marker")
	}
}

// countingFlightsServer answers every request with the fixture flight body and
// counts them.
func countingFlightsServer(t *testing.T) (*batchexec.Client, *atomic.Int32) {
	t.Helper()
	body := makeFlightResponseBody(t)
	var n atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)
	return batchexec.NewTestClient(ts.URL), &n
}

// heldFlightsServer holds the first request until a second one arrives, so
// two concurrent searches either both reach upstream or provably share one
// call: when no second request comes within the timeout, joined reports true.
// Each search needs its own client: a client sends one Google request at a
// time (batchexec googleMu).
func heldFlightsServer(t *testing.T) (func() *batchexec.Client, func() bool) {
	t.Helper()
	body := makeFlightResponseBody(t)
	var n atomic.Int32
	var joined atomic.Bool
	second := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch n.Add(1) {
		case 1:
			select {
			case <-second:
			case <-time.After(5 * time.Second):
				joined.Store(true)
			}
		case 2:
			close(second)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)
	return func() *batchexec.Client { return batchexec.NewTestClient(ts.URL) }, joined.Load
}

// Two detectors asking the same flight query within one savings pass cause
// one upstream search, even one after the other; each gets its own deep copy.
func TestSearchFlights_SameQueryOncePerSavingsPass(t *testing.T) {
	client, reqs := countingFlightsServer(t)
	pass := searchctx.StartSavingsPass(context.Background())
	first, err := SearchFlightsWithClient(pass, client, "HEL", "NRT", "2026-12-01", SearchOptions{})
	if err != nil || first == nil || len(first.Flights) == 0 {
		t.Fatalf("first search: %+v, %v", first, err)
	}
	second, err := SearchFlightsWithClient(pass, client, "HEL", "NRT", "2026-12-01", SearchOptions{})
	if err != nil || second == nil || len(second.Flights) == 0 {
		t.Fatalf("second search: %+v, %v", second, err)
	}
	if n := reqs.Load(); n != 1 {
		t.Errorf("upstream requests = %d, want 1 within one pass", n)
	}
	if len(first.Flights[0].Legs) > 0 {
		first.Flights[0].Legs[0].FlightNumber = "EDITED"
		if second.Flights[0].Legs[0].FlightNumber == "EDITED" {
			t.Error("callers share flight legs: one detector's edit leaks into another's result")
		}
	}
	if _, err := SearchFlightsWithClient(context.Background(), client, "HEL", "NRT", "2026-12-01", SearchOptions{}); err != nil || reqs.Load() != 2 {
		t.Errorf("a search outside the pass was memoized: %d requests, err %v", reqs.Load(), err)
	}
}

// A test override replaces the whole search, so it is never memoized.
func TestSearchFlights_OverrideBypassesMemo(t *testing.T) {
	var calls atomic.Int32
	opts := SearchOptions{SearchOverride: func(context.Context, string, string, string, SearchOptions) (*models.FlightSearchResult, error) {
		calls.Add(1)
		return &models.FlightSearchResult{Success: true}, nil
	}}
	pass := searchctx.StartSavingsPass(context.Background())
	for range 2 {
		if _, err := SearchFlights(pass, "HEL", "BCN", "2026-12-01", opts); err != nil {
			t.Fatal(err)
		}
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("override ran %d times, want 2 (no memo)", n)
	}
}

// skiplaggedRow returns the Skiplagged provider status of a result.
func skiplaggedRow(r *models.FlightSearchResult) models.ProviderStatus {
	if r != nil {
		for _, st := range r.ProviderStatuses {
			if st.ID == "skiplagged" {
				return st
			}
		}
	}
	return models.ProviderStatus{}
}

// A round-trip leg (one-way Skiplagged suppressed) and a plain one-way for the
// same route, run concurrently in one savings pass, each reach upstream and
// keep their own Skiplagged answer instead of sharing one call.
func TestSearchFlights_SkiplaggedSuppressedLegNotSharedWithOneWay(t *testing.T) {
	newClient, joined := heldFlightsServer(t)
	pass := searchctx.StartSavingsPass(context.Background())

	leg, oneWay := make(chan *models.FlightSearchResult, 1), make(chan *models.FlightSearchResult, 1)
	go func() {
		r, _ := SearchFlightsWithClient(disableSkiplaggedOneWay(pass), newClient(), "HEL", "NRT", "2026-12-04", SearchOptions{})
		leg <- r
	}()
	go func() {
		r, _ := SearchFlightsWithClient(pass, newClient(), "HEL", "NRT", "2026-12-04", SearchOptions{})
		oneWay <- r
	}()
	legRes, oneWayRes := <-leg, <-oneWay

	if joined() {
		t.Error("the suppressed leg and the one-way shared one upstream call")
	}
	const suppressedMsg = "one-way Skiplagged skipped"
	if row := skiplaggedRow(legRes); !strings.Contains(row.Error, suppressedMsg) {
		t.Errorf("leg Skiplagged row = %+v, want the round-trip suppression", row)
	}
	if row := skiplaggedRow(oneWayRes); strings.Contains(row.Error, suppressedMsg) {
		t.Errorf("one-way Skiplagged row = %+v: it got the leg's suppression", row)
	}
}

// A search inside a savings pass runs without one. A concurrent top-level
// search for the same query must reach upstream itself and keep its saving.
func TestSearchFlights_SuppressedSearchNotSharedWithTopLevel(t *testing.T) {
	withStubComposer(t, func(context.Context, HackComposeRequest) *models.HackSaving {
		return &models.HackSaving{Type: "stub"}
	})
	newClient, joined := heldFlightsServer(t)

	suppressed, top := make(chan *models.FlightSearchResult, 1), make(chan *models.FlightSearchResult, 1)
	go func() {
		r, _ := SearchFlightsWithClient(searchctx.StartSavingsPass(context.Background()), newClient(), "HEL", "NRT", "2026-12-03", SearchOptions{})
		suppressed <- r
	}()
	go func() {
		r, _ := SearchFlightsWithClient(context.Background(), newClient(), "HEL", "NRT", "2026-12-03", SearchOptions{})
		top <- r
	}()
	topRes, supRes := <-top, <-suppressed

	if joined() {
		t.Error("the in-pass search and the top-level search shared one upstream call")
	}
	if topRes == nil || topRes.HackSaving == nil {
		t.Errorf("top-level search has no saving: %+v", topRes)
	}
	if supRes != nil && supRes.HackSaving != nil {
		t.Errorf("search inside a savings pass got a saving: %+v", supRes.HackSaving)
	}
}
