package watch

import (
	"context"
	"testing"
)

// These two tests pin the two guarantees that the release/1.21.0 -> main merge
// had to combine. They were previously enforced by two different
// implementations on two different branches, each of which dropped the other's
// property:
//
//   - UpdateWatchAndRecordPrice (main) wrote the watch, the history purge and
//     the new price point in ONE save, but rewrote a stale whole-struct copy.
//   - Mutate + RecordPrice (release/1.21.0) wrote only caller-owned fields on a
//     freshly reloaded record, but split the history append into a second save.
//
// MutateAndRecordPrice keeps both. A regression that reverts either half passes
// the OTHER branch's tests, which is exactly how one of these would get lost
// again, so both properties are asserted here side by side.
//
// Both drive checkOne -- the entry point the scheduler and the MCP watch_price
// tool use -- rather than poking the store method directly. A test that calls
// the mechanism passes even when the mechanism is no longer on the caller's
// path.

// TRVL.MERGE.ATOMIC.1 -- the purge, the append and the watch update must be
// visible together BEFORE any save, i.e. inside one transaction.
//
// Window-positioned on purpose. Asserting the end state after the check
// returns proves nothing: two sequential saves reach the same end state and
// differ only in what a crash between them would leave behind. stageBeforeSave
// fires while the transaction still holds both locks and has not yet written,
// so the assertion runs INSIDE the window a crash would open.
//
// Sabotage check (run 2026-08-02): replacing the MutateAndRecordPrice call in
// checkOneWithWebhookContext with the old `store.Mutate(...)` +
// `store.RecordPrice(...)` pair makes this fail at "old-currency history still
// present", because at the Mutate transaction's stageBeforeSave the purge and
// the append have not happened yet -- they belong to a later, separate save.
func TestCurrencyChangePurgeAndAppendShareOneTransaction(t *testing.T) {
	if !lockSupported {
		t.Skip("store transactions are not enforced on this platform")
	}

	s := NewStore(t.TempDir())
	id, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		Currency: "JPY", BelowPrice: 20000,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// Two old-currency points, so a purge is observable rather than vacuous.
	if err := s.RecordPrice(id, 19000, "JPY"); err != nil {
		t.Fatalf("seed history: %v", err)
	}
	if err := s.RecordPrice(id, 18500, "JPY"); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	type snapshot struct {
		watchCurrency string
		oldPoints     int
		newPoints     int
	}
	var seen []snapshot
	txnHook = func(st *Store, stage txnStage) {
		if stage != stageBeforeSave {
			return
		}
		snap := snapshot{}
		for _, w := range st.watches {
			if w.ID == id {
				snap.watchCurrency = w.Currency
			}
		}
		for _, p := range st.history {
			if p.WatchID != id {
				continue
			}
			if p.Currency == "JPY" {
				snap.oldPoints++
			} else {
				snap.newPoints++
			}
		}
		seen = append(seen, snap)
	}
	t.Cleanup(func() { txnHook = nil })

	w, _ := s.Get(id)
	res := checkOne(context.Background(), s, &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{{price: 150, currency: "EUR"}}}, w)
	if res.Error != nil {
		t.Fatalf("check: %v", res.Error)
	}

	// The transaction that flipped the currency is the one to inspect.
	var found *snapshot
	for i := range seen {
		if seen[i].watchCurrency == "EUR" {
			found = &seen[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no transaction observed with the new currency applied; saw %+v", seen)
	}
	if found.oldPoints != 0 {
		t.Errorf("old-currency history still present at save time: %d JPY point(s) -- "+
			"the purge is not in the same transaction as the currency update, so a crash "+
			"here leaves an EUR watch beside JPY history", found.oldPoints)
	}
	if found.newPoints != 1 {
		t.Errorf("new price point not appended at save time: got %d, want 1 -- "+
			"the append is in a later save, so a crash here loses this observation",
			found.newPoints)
	}
}

// TRVL.MERGE.TXN.2 -- a concurrent edit made after the check read its copy of
// the watch must survive the check's own write.
//
// The check holds a detached Watch taken BEFORE the provider call. Writing that
// copy back wholesale reverts every field someone else changed meanwhile.
//
// The edit is injected from inside the fake provider call, which is where the
// race genuinely lives: that is the one stretch where the check is in flight
// and holds NO lock, so another process can and does get in. Injecting it
// inside the check's own transaction instead would deadlock -- the store lock
// serialises them by design -- and a test written that way proves only that the
// lock exists, not that the write is field-scoped.
//
// Sabotage check (run 2026-08-02): changing MutateAndRecordPrice's callback
// invocation to instead assign the caller's whole struct (the
// UpdateWatchAndRecordPrice shape) makes this fail at "concurrent webhook edit
// was reverted".
func TestConcurrentEditSurvivesAPriceCheck(t *testing.T) {
	if !lockSupported {
		t.Skip("store transactions are not enforced on this platform")
	}

	dir := t.TempDir()
	s := NewStore(dir)
	id, _, err := s.Add(Watch{
		Type: "flight", Origin: "AMS", Destination: "VLC",
		Currency: "EUR", BelowPrice: 300,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// The stale copy: taken before the check, exactly as the scheduler does.
	w, _ := s.Get(id)

	// A second Store value stands in for the other process. flock is held by
	// the open file description, so two descriptors in one process exclude each
	// other exactly as two processes do -- which is what makes this a faithful
	// model of the multi-process hazard rather than a same-goroutine mock.
	other := NewStore(dir)

	const wantHook = "https://example.invalid/hook"
	var editErr error
	checker := &editingChecker{
		price:    275,
		currency: "EUR",
		duringCall: func() {
			_, editErr = other.Update(id, WatchUpdate{WebhookURL: strPtr(wantHook)})
		},
	}

	res := checkOne(context.Background(), s, checker, w)
	if res.Error != nil {
		t.Fatalf("check: %v", res.Error)
	}
	if editErr != nil {
		t.Fatalf("concurrent edit failed: %v", editErr)
	}
	if !checker.called {
		t.Fatal("provider was never called, so no edit was ever injected")
	}

	reader := NewStore(dir)
	if err := reader.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := reader.Get(id)
	if !ok {
		t.Fatalf("watch %s vanished", id)
	}
	if got.WebhookURL != wantHook {
		t.Errorf("concurrent webhook edit was reverted: WebhookURL = %q, want %q",
			got.WebhookURL, wantHook)
	}
	// The check's own field must still have landed -- otherwise this test would
	// pass against a check that simply wrote nothing.
	if got.LastPrice != 275 {
		t.Errorf("check did not persist its own field: LastPrice = %v, want 275", got.LastPrice)
	}
}

// editingChecker runs duringCall from inside the provider round trip, modelling
// another process editing the watch while this check is in flight.
type editingChecker struct {
	price      float64
	currency   string
	duringCall func()
	called     bool
}

func (c *editingChecker) CheckPrice(_ context.Context, _ Watch) (float64, string, string, error) {
	c.called = true
	if c.duringCall != nil {
		c.duringCall()
	}
	return c.price, c.currency, "", nil
}

func strPtr(s string) *string { return &s }
