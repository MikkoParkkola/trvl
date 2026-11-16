package flights

import (
	"context"
	"net/http"
	"net/http/httptest"
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
// counts them; the first request waits for gate when gate is non-nil.
func countingFlightsServer(t *testing.T, gate <-chan struct{}, firstIn chan<- struct{}) (*batchexec.Client, *atomic.Int32) {
	t.Helper()
	body := makeFlightResponseBody(t)
	var n atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 && gate != nil {
			close(firstIn)
			<-gate
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)
	return batchexec.NewTestClient(ts.URL), &n
}

// Two detectors asking the same flight query within one savings pass cause
// one upstream search, even one after the other; each gets its own deep copy.
func TestSearchFlights_SameQueryOncePerSavingsPass(t *testing.T) {
	client, reqs := countingFlightsServer(t, nil, nil)
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

// A round-trip leg suppresses the one-way Skiplagged call, so its query is not
// the plain one-way query and must not share its memo entry.
func TestFlightMemoKey_SeparatesSkiplaggedSuppressedLegs(t *testing.T) {
	ctx := context.Background()
	plain := flightMemoKey(ctx, nil, "HEL", "BCN", "2026-12-01", SearchOptions{})
	leg := flightMemoKey(disableSkiplaggedOneWay(ctx), nil, "HEL", "BCN", "2026-12-01", SearchOptions{})
	if plain == leg {
		t.Errorf("memo key ignores the Skiplagged suppression: %q", plain)
	}
}

// A search inside a savings pass runs without one. A concurrent top-level
// search for the same query must not join it and lose its own saving.
func TestSearchFlights_SuppressedSearchNotSharedWithTopLevel(t *testing.T) {
	withStubComposer(t, func(context.Context, HackComposeRequest) *models.HackSaving {
		return &models.HackSaving{Type: "stub"}
	})
	gate, firstIn := make(chan struct{}), make(chan struct{})
	client, _ := countingFlightsServer(t, gate, firstIn)

	suppressed := make(chan *models.FlightSearchResult, 1)
	go func() {
		r, _ := SearchFlightsWithClient(searchctx.StartSavingsPass(context.Background()), client, "HEL", "NRT", "2026-12-03", SearchOptions{})
		suppressed <- r
	}()
	<-firstIn
	top := make(chan *models.FlightSearchResult, 1)
	go func() {
		r, _ := SearchFlightsWithClient(context.Background(), client, "HEL", "NRT", "2026-12-03", SearchOptions{})
		top <- r
	}()
	time.Sleep(50 * time.Millisecond) // let the top-level search reach the in-flight call
	close(gate)

	if r := <-top; r == nil || r.HackSaving == nil {
		t.Errorf("top-level search got the in-pass result without a saving: %+v", r)
	}
	if r := <-suppressed; r != nil && r.HackSaving != nil {
		t.Errorf("search inside a savings pass got a saving: %+v", r.HackSaving)
	}
}
