package hotels

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestSearchHotelsCall_RecordsEveryPage: page 1 answers, page 2 is refused.
// Both requests land in the run's Google events, in order.
func TestSearchHotelsCall_RecordsEveryPage(t *testing.T) {
	roomLookupWorld(t, googleRoutes{shortEntity, answer(404), func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("start") != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write(fakeHotelPageWithPrices(hotelWithPrice{"Hotel Stefanie", 150}))
	}}, false)

	_, call, err := searchHotelsCall(context.Background(), DefaultClient(), "Vienna", HotelSearchOptions{
		CheckIn: lookupCheckIn, CheckOut: lookupCheckOut, Currency: "USD", MaxPages: 2,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(call.events) < 2 || call.events[0].kind != googleAnswered || call.refusal() == nil {
		t.Fatalf("events = %+v, want page 1 answered, then a later refusal", call.events)
	}
}

// TestDoHotelSearchRun_ThreeCallersOneRun: three concurrent callers join one
// shared run. Two hold different prior outcomes and both merge the run's
// refusal under the precedence rule; the third caller's context ends before
// the run finishes, and it keeps its own prior refusal.
func TestDoHotelSearchRun_ThreeCallersOneRun(t *testing.T) {
	clock := primedRefusal(t) // a cooldown refusal with its retry clock
	plain := fmt.Errorf("hotel search request: %w", batchexec.ErrBlocked)

	var registered sync.WaitGroup
	registered.Add(3)
	t.Cleanup(func() { hotelSearchRegistered = nil })
	hotelSearchRegistered = registered.Done

	var runs atomic.Int32
	release := make(chan struct{})
	fn := func(context.Context) (hotelSearchRun, error) {
		runs.Add(1)
		<-release
		var call googleCall
		call.note(0, plain)
		return hotelSearchRun{result: &models.HotelSearchResult{}, call: call}, nil
	}

	priorOf := func(err error) googleOutcome {
		var o googleOutcome
		if err != nil {
			o.merge(callOf([2]any{0, err}), false)
		}
		return o
	}
	outs := []googleOutcome{priorOf(nil), priorOf(clock), priorOf(clock)}
	cancelCtx, cancel := context.WithCancel(context.Background())
	ctxs := []context.Context{context.Background(), context.Background(), cancelCtx}
	errs := make([]error, 3)
	finished := make([]chan struct{}, 3)
	for i := range outs {
		finished[i] = make(chan struct{})
		go func() {
			defer close(finished[i])
			run, err := doHotelSearchRun(ctxs[i], "three-callers-one-run", fn)
			errs[i] = err
			outs[i].merge(run.call, false) // as trySearchPageFallback does
		}()
	}
	registered.Wait() // all three are in the run
	cancel()          // the third caller's context ends while the run is held
	<-finished[2]     // it has returned with its cancellation
	close(release)
	<-finished[0]
	<-finished[1]

	if n := runs.Load(); n != 1 {
		t.Fatalf("runs = %d, want one shared run", n)
	}
	if !errors.Is(outs[0].Err(), batchexec.ErrBlocked) {
		t.Errorf("caller with no prior: Err() = %v, want the run's 403 refusal", outs[0].Err())
	}
	if !errors.Is(outs[1].Err(), batchexec.ErrRefused) {
		t.Errorf("caller with a clock refusal: Err() = %v, want the clock kept over the 403", outs[1].Err())
	}
	if !errors.Is(errs[2], context.Canceled) || !errors.Is(outs[2].Err(), batchexec.ErrRefused) {
		t.Errorf("cancelled caller: err = %v, Err() = %v; want context.Canceled and its prior refusal", errs[2], outs[2].Err())
	}
}

// TestSharedHotelRun_CallersGetTheirOwnCopies: every caller of a shared run,
// the leader included, receives it through sharedHotelRun, so each gets a
// result and an event slice it may change without touching the others'.
func TestSharedHotelRun_CallersGetTheirOwnCopies(t *testing.T) {
	var call googleCall
	call.note(http.StatusOK, nil)
	shared := hotelSearchRun{
		result: &models.HotelSearchResult{Hotels: []models.HotelResult{{Name: "Hotel Sacher"}}},
		call:   call,
	}
	first, _ := sharedHotelRun(shared, nil)
	second, _ := sharedHotelRun(shared, nil)

	first.result.Hotels[0].Name = "mutated"
	first.call.events[0].kind = googleRefused
	if second.result.Hotels[0].Name != "Hotel Sacher" || second.call.events[0].kind != googleAnswered {
		t.Fatalf("a caller saw another caller's changes: %+v / %+v", second.result.Hotels[0], second.call.events)
	}
	if shared.result.Hotels[0].Name != "Hotel Sacher" || shared.call.events[0].kind != googleAnswered {
		t.Fatal("a caller changed the shared run itself")
	}
}
