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

// TRVL.MERGE.TXN.3 -- a currency migration completed by another process while
// this check was in flight must NOT be re-applied by this check.
//
// This is the defect adversarial review found in the first cut of the merge
// (2026-08-02). The merge reloaded committed state but still DECIDED the
// destructive currency branches from the pre-provider snapshot, then replayed
// them onto the reloaded record. Replaying a migration that committed state has
// already finished zeroes the threshold the other process just re-set and
// purges the history it just wrote in the new currency -- destroying exactly
// the concurrent state the field-scoped write existed to protect.
//
// The window is the provider round trip, where no lock is held. That is the
// only place another process can get in, and it is where the injection happens.
//
// Sabotage check (run 2026-08-02): computing currencyChanged/currencyMismatch
// from the detached `w` instead of from `cur` inside the callback -- i.e.
// restoring the shape this test was written against -- makes it fail at both
// "threshold was wiped" and "new-currency history was purged".
func TestCompletedCurrencyMigrationIsNotReapplied(t *testing.T) {
	if !lockSupported {
		t.Skip("store transactions are not enforced on this platform")
	}

	dir := t.TempDir()
	s := NewStore(dir)
	id, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		Currency: "JPY", BelowPrice: 15000,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.RecordPrice(id, 20000, "JPY"); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	// The stale copy: JPY, with prior observations. Taken before the check.
	w, _ := s.Get(id)

	other := NewStore(dir)
	var migrateErr error
	checker := &editingChecker{
		price:    150,
		currency: "EUR",
		duringCall: func() {
			// Another process performs the whole migration and moves on:
			// re-watch in EUR (purges the JPY history), then re-set a fresh
			// EUR threshold, then observe a real EUR price.
			if _, _, err := other.Add(Watch{
				Type: "flight", Origin: "HEL", Destination: "BCN",
				Currency: "EUR",
			}); err != nil {
				migrateErr = err
				return
			}
			if _, err := other.Update(id, WatchUpdate{AlertDropAbs: f64Ptr(250)}); err != nil {
				migrateErr = err
				return
			}
			if err := other.RecordPrice(id, 160, "EUR"); err != nil {
				migrateErr = err
			}
		},
	}

	res := checkOne(context.Background(), s, checker, w)
	if res.Error != nil {
		t.Fatalf("check: %v", res.Error)
	}
	if migrateErr != nil {
		t.Fatalf("concurrent migration failed: %v", migrateErr)
	}

	reader := NewStore(dir)
	if err := reader.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := reader.Get(id)
	if !ok {
		t.Fatalf("watch %s vanished", id)
	}
	if got.Currency != "EUR" {
		t.Fatalf("currency = %q, want EUR", got.Currency)
	}
	if got.AlertDropAbs != 250 {
		t.Errorf("threshold was wiped: AlertDropAbs = %v, want 250 -- this check "+
			"re-applied a currency migration another process had already completed",
			got.AlertDropAbs)
	}
	if got.AlertDropAbsClearedByCurrency {
		t.Errorf("watch was marked pending currency reconfirmation, but the currency " +
			"had already been reconciled by the other process before this check wrote")
	}
	eur := 0
	jpy := 0
	for _, p := range reader.History(id) {
		switch p.Currency {
		case "EUR":
			eur++
		case "JPY":
			jpy++
		}
	}
	// The other process's EUR observation must survive. This check's own quote
	// does NOT land: the migration changed the watch's poll identity, so the
	// staleness gate discards a result that was requested for the old target.
	if eur < 1 {
		t.Errorf("new-currency history was purged: %d EUR point(s), want >= 1 -- "+
			"a stale currencyChanged purged history written in the CURRENT currency", eur)
	}
	if jpy != 0 {
		t.Errorf("old-currency history survived the migration: %d JPY point(s), want 0", jpy)
	}
	if !res.Stale {
		t.Errorf("the poll was applied rather than discarded: this check was started " +
			"against the pre-migration target, so its answer is stale")
	}
}

// TRVL.MERGE.TXN.4 -- a poll that returns the OLD currency after another
// process migrated the watch must be DISCARDED, not applied.
//
// This is the direction TXN.3 does not reach, and the one that actually loses
// data. livecheck selects results in the SNAPSHOT's currency
// (cheapestByCurrency(..., w.Currency)), so a poll started against a JPY watch
// comes back holding a JPY quote. If EUR was committed meanwhile, the returning
// check reads committed-EUR-versus-JPY-quote as a currency change and wipes the
// fresh EUR threshold, purges the EUR history and drags the watch back to JPY --
// on the strength of a quote that predates the re-watch.
//
// Deciding inside the transaction does not help here: the decision is correct
// about the state, but the INPUT is stale. Only comparing the poll identity
// catches it. Found by GPT second-opinion review, 2026-08-02, which noted
// TXN.3's fake provider returns the NEW currency and therefore never exercises
// this.
//
// Sabotage check: passing "" as expectPollKey (disabling the gate) fails this
// at the threshold and history assertions.
func TestStalePollInOldCurrencyIsDiscarded(t *testing.T) {
	if !lockSupported {
		t.Skip("store transactions are not enforced on this platform")
	}

	dir := t.TempDir()
	s := NewStore(dir)
	id, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		Currency: "JPY", BelowPrice: 15000,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.RecordPrice(id, 20000, "JPY"); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	// The stale copy: JPY. The poll below is made FOR this target.
	w, _ := s.Get(id)

	other := NewStore(dir)
	var migrateErr error
	checker := &editingChecker{
		// The provider answers in the currency the poll asked for -- JPY --
		// because livecheck selects on the snapshot's currency.
		price:    19000,
		currency: "JPY",
		duringCall: func() {
			if _, _, err := other.Add(Watch{
				Type: "flight", Origin: "HEL", Destination: "BCN",
				Currency: "EUR",
			}); err != nil {
				migrateErr = err
				return
			}
			if _, err := other.Update(id, WatchUpdate{AlertDropAbs: f64Ptr(250)}); err != nil {
				migrateErr = err
				return
			}
			if err := other.RecordPrice(id, 160, "EUR"); err != nil {
				migrateErr = err
			}
		},
	}

	res := checkOne(context.Background(), s, checker, w)
	if res.Error != nil {
		t.Fatalf("check: %v", res.Error)
	}
	if migrateErr != nil {
		t.Fatalf("concurrent migration failed: %v", migrateErr)
	}
	if !res.Stale {
		t.Errorf("a poll for a re-targeted watch was applied instead of discarded")
	}
	if res.PriceDropAlert || res.BelowGoal {
		t.Errorf("a discarded poll still fired an alert: PriceDropAlert=%v BelowGoal=%v",
			res.PriceDropAlert, res.BelowGoal)
	}

	reader := NewStore(dir)
	if err := reader.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := reader.Get(id)
	if !ok {
		t.Fatalf("watch %s vanished", id)
	}
	if got.Currency != "EUR" {
		t.Errorf("a stale JPY quote dragged the watch back to %q; the committed currency was EUR",
			got.Currency)
	}
	if got.AlertDropAbs != 250 {
		t.Errorf("a stale quote wiped the freshly re-set threshold: AlertDropAbs = %v, want 250",
			got.AlertDropAbs)
	}
	eur, jpy := 0, 0
	for _, p := range reader.History(id) {
		switch p.Currency {
		case "EUR":
			eur++
		case "JPY":
			jpy++
		}
	}
	if eur < 1 {
		t.Errorf("a stale quote purged the new-currency history: %d EUR point(s), want >= 1", eur)
	}
	if jpy != 0 {
		t.Errorf("a stale JPY quote was recorded anyway: %d JPY point(s), want 0", jpy)
	}
}

func f64Ptr(v float64) *float64 { return &v }
