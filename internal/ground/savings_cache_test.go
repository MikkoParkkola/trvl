package ground

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/searchctx"
)

// A search inside a savings pass runs without one, so its result carries no
// saving. A concurrent top-level search for the same query must reach the
// provider itself, not share the in-pass call or its cached result.
func TestSearchByName_SuppressedResultNotSharedWithTopLevel(t *testing.T) {
	t.Setenv("TRVL_PREFETCH", "0")
	isolateGroundBreaker(t)
	// The first request waits for a second; if none comes, the two searches
	// shared one call (joined).
	var reqs atomic.Int32
	var joined atomic.Bool
	second := make(chan struct{})
	frame := ferryhopperFrame(goldenFerryhopperFrame)
	withFerryhopperServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch reqs.Add(1) {
		case 1:
			select {
			case <-second:
			case <-time.After(5 * time.Second):
				joined.Store(true)
			}
		case 2:
			close(second)
		}
		frame(w, r)
	})
	withStubComposer(t, func(context.Context, HackComposeRequest) *models.HackSaving {
		return &models.HackSaving{Type: "stub"}
	})
	// Exclude-only, so the savings pass is allowed; only ferryhopper answers.
	var exclude []string
	for _, n := range ValidProviderNames() {
		if n != "ferryhopper" {
			exclude = append(exclude, n)
		}
	}
	opts := SearchOptions{Currency: "EUR", ExcludeProviders: exclude}
	const date = "2026-07-23" // distinct from other tests' cache keys

	suppressed, top := make(chan *models.GroundSearchResult, 1), make(chan *models.GroundSearchResult, 1)
	go func() {
		r, _ := SearchByName(searchctx.StartSavingsPass(context.Background()), "Piraeus", "Santorini", date, opts)
		suppressed <- r
	}()
	go func() {
		r, _ := SearchByName(context.Background(), "Piraeus", "Santorini", date, opts)
		top <- r
	}()
	topRes, supRes := <-top, <-suppressed

	if joined.Load() {
		t.Error("the in-pass search and the top-level search shared one provider call")
	}
	if topRes == nil || topRes.HackSaving == nil {
		t.Errorf("top-level search has no saving: %+v", topRes)
	}
	if supRes != nil && supRes.HackSaving != nil {
		t.Errorf("search inside a savings pass got a saving: %+v", supRes.HackSaving)
	}
}
