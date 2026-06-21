package tier1

import (
	"context"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/cfprobe"
	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/probecache"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

var now = time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

func newCache(t *testing.T) *probecache.Store {
	t.Helper()
	return probecache.NewStore(t.TempDir())
}

func TestProbeOneCachesSavings(t *testing.T) {
	cache := newCache(t)
	engine := cfprobe.NewEngine(5, 0, nil)
	active := []watch.Watch{
		{Type: "flight", Origin: "AMS", Destination: "VLC", Currency: "EUR", LastPrice: 200},
	}
	detect := func(_ context.Context, in hacks.DetectorInput) []hacks.Hack {
		return []hacks.Hack{{Type: "positioning", Title: "Fly from BGY", Savings: 40, Currency: "EUR"}}
	}
	if !ProbeOne(context.Background(), active, 0, engine, cache, now, detect) {
		t.Fatalf("probe should have run")
	}
	e, ok := cache.Get(probecache.RouteKey("AMS", "VLC"))
	if !ok || len(e.Savings) != 1 || e.Savings[0].Amount != 40 {
		t.Fatalf("savings not cached: %+v ok=%v", e, ok)
	}
	if !e.Savings[0].CallFree {
		t.Fatalf("cached savings must be stamped call-free for read-time serving")
	}
}

// Budget exhausted -> no probe, nothing cached (no silent unbounded fan-out).
func TestProbeOneBudgetExhaustedCachesNothing(t *testing.T) {
	cache := newCache(t)
	engine := cfprobe.NewEngine(1, 0, nil) // one token
	active := []watch.Watch{{Type: "flight", Origin: "AMS", Destination: "VLC", Currency: "EUR", LastPrice: 200}}
	calls := 0
	detect := func(_ context.Context, _ hacks.DetectorInput) []hacks.Hack { calls++; return nil }

	ProbeOne(context.Background(), active, 0, engine, cache, now, detect) // spends the token
	ran := ProbeOne(context.Background(), active, 1, engine, cache, now, detect)
	if ran {
		t.Fatalf("second probe must be refused (budget exhausted)")
	}
	if calls != 1 {
		t.Fatalf("detect should run exactly once (first probe), got %d", calls)
	}
}

func TestProbeOneSkipsNonFlightAndZeroPrice(t *testing.T) {
	cache := newCache(t)
	engine := cfprobe.NewEngine(5, 0, nil)
	active := []watch.Watch{
		{Type: "hotel", Origin: "AMS", Destination: "VLC", LastPrice: 200},
		{Type: "flight", Origin: "AMS", Destination: "VLC", LastPrice: 0}, // no price
	}
	detect := func(_ context.Context, _ hacks.DetectorInput) []hacks.Hack { return nil }
	if ProbeOne(context.Background(), active, 0, engine, cache, now, detect) {
		t.Fatalf("no eligible flight candidate -> must not run")
	}
}

func TestProbeOneRotates(t *testing.T) {
	cache := newCache(t)
	engine := cfprobe.NewEngine(10, 0, nil)
	active := []watch.Watch{
		{Type: "flight", Origin: "AMS", Destination: "VLC", Currency: "EUR", LastPrice: 200},
		{Type: "flight", Origin: "HEL", Destination: "BCN", Currency: "EUR", LastPrice: 300},
	}
	detect := func(_ context.Context, in hacks.DetectorInput) []hacks.Hack {
		return []hacks.Hack{{Title: "x", Savings: 10, Currency: in.Currency}}
	}
	ProbeOne(context.Background(), active, 0, engine, cache, now, detect)
	ProbeOne(context.Background(), active, 1, engine, cache, now, detect)
	if _, ok := cache.Get(probecache.RouteKey("AMS", "VLC")); !ok {
		t.Fatalf("tick 0 should probe AMS-VLC")
	}
	if _, ok := cache.Get(probecache.RouteKey("HEL", "BCN")); !ok {
		t.Fatalf("tick 1 should probe HEL-BCN")
	}
}
