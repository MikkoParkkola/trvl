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
// saving. A concurrent or later top-level search for the same query must not
// get that result through the shared in-flight call or the result cache.
func TestSearchByName_SuppressedResultNotSharedWithTopLevel(t *testing.T) {
	t.Setenv("TRVL_PREFETCH", "0")
	isolateGroundBreaker(t)
	var reqs atomic.Int32
	firstIn, release := make(chan struct{}), make(chan struct{})
	frame := ferryhopperFrame(goldenFerryhopperFrame)
	withFerryhopperServer(t, func(w http.ResponseWriter, r *http.Request) {
		if reqs.Add(1) == 1 {
			close(firstIn)
			<-release
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

	suppressed := make(chan *models.GroundSearchResult, 1)
	go func() {
		r, _ := SearchByName(searchctx.StartSavingsPass(context.Background()), "Piraeus", "Santorini", date, opts)
		suppressed <- r
	}()
	<-firstIn
	top := make(chan *models.GroundSearchResult, 1)
	go func() {
		r, _ := SearchByName(context.Background(), "Piraeus", "Santorini", date, opts)
		top <- r
	}()
	time.Sleep(50 * time.Millisecond) // let the top-level search reach the in-flight call
	close(release)

	if r := <-top; r == nil || r.HackSaving == nil {
		t.Errorf("top-level search got the in-pass result without a saving: %+v", r)
	}
	if r := <-suppressed; r != nil && r.HackSaving != nil {
		t.Errorf("search inside a savings pass got a saving: %+v", r.HackSaving)
	}
}
