package flights

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

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

// Two detectors asking the same flight query within one savings pass cause
// one underlying search; each still gets its own copy of the flight list.
func TestSearchFlights_SameQueryOncePerSavingsPass(t *testing.T) {
	var calls atomic.Int32
	opts := SearchOptions{SearchOverride: func(_ context.Context, _, _, _ string, _ SearchOptions) (*models.FlightSearchResult, error) {
		calls.Add(1)
		return &models.FlightSearchResult{Success: true, Count: 1, Flights: []models.FlightResult{{Price: 99, Currency: "EUR"}}}, nil
	}}
	pass := searchctx.StartSavingsPass(context.Background())

	results := make([]*models.FlightSearchResult, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := SearchFlights(pass, "HEL", "BCN", "2026-12-01", opts)
			if err != nil {
				t.Errorf("search: %v", err)
			}
			results[i] = res
		}(i)
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("underlying searches = %d, want 1", n)
	}
	if results[0] == nil || results[1] == nil || len(results[0].Flights) != 1 || len(results[1].Flights) != 1 {
		t.Fatalf("results = %+v, %+v", results[0], results[1])
	}
	results[0].Flights[0].Price = 1
	if results[1].Flights[0].Price != 99 {
		t.Error("callers share one flight list: a detector's edit leaks into another's result")
	}

	if _, err := SearchFlights(pass, "HEL", "BCN", "2026-12-02", opts); err != nil || calls.Load() != 2 {
		t.Errorf("a different query in the same pass did not search: %d calls, err %v", calls.Load(), err)
	}
	if _, err := SearchFlights(context.Background(), "HEL", "BCN", "2026-12-01", opts); err != nil || calls.Load() != 3 {
		t.Errorf("a search outside any pass was memoized: %d calls, err %v", calls.Load(), err)
	}
}
