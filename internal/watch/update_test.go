package watch

import "testing"

// Store.Update is the seam the MCP tool surface calls (TRVL.WATCH.UNSET.5); the
// CLI tests cover the same store through the command. Assertions read a fresh
// store so they see committed bytes, not this test's in-memory copy.
func TestStoreUpdate_ClearsNotificationFieldsOnDisk(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(Watch{
		Type: "hotel", Destination: "Prague", DepartDate: "2026-07-01", ReturnDate: "2026-07-02",
		BelowPrice: 80, Currency: "EUR", WebhookURL: "https://example.test/hook",
		AlertDropPct: 20, AlertDropAbs: 50, LastMinuteMode: true, LastMinuteDropPct: 30,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	empty, zero, off := "", 0.0, false
	if _, err := s.Update(id, WatchUpdate{
		WebhookURL: &empty, AlertDropPct: &zero, AlertDropAbs: &zero,
		LastMinuteMode: &off, LastMinuteDropPct: &zero,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	fresh := NewStore(s.dir)
	if err := fresh.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := fresh.Get(id)
	if !ok {
		t.Fatal("watch disappeared; clearing must not delete the record")
	}
	if got.WebhookURL != "" || got.AlertDropPct != 0 || got.AlertDropAbs != 0 ||
		got.LastMinuteMode || got.LastMinuteDropPct != 0 {
		t.Fatalf("notification settings not cleared on disk: %+v", got)
	}
	if got.BelowPrice != 80 || got.Destination != "Prague" {
		t.Fatalf("identity fields changed: %+v", got)
	}
}

func TestStoreUpdate_NilFieldsAreLeftUntouched(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN", DepartDate: "2026-07-01",
		BelowPrice: 200, WebhookURL: "https://example.test/hook", AlertDropPct: 20,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	zero := 0.0
	if _, err := s.Update(id, WatchUpdate{AlertDropPct: &zero}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	fresh := NewStore(s.dir)
	if err := fresh.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, _ := fresh.Get(id)
	if got.WebhookURL != "https://example.test/hook" {
		t.Fatalf("WebhookURL = %q, want untouched", got.WebhookURL)
	}
	if got.AlertDropPct != 0 {
		t.Fatalf("AlertDropPct = %v, want 0", got.AlertDropPct)
	}
}

func TestStoreUpdate_RejectsInvalidResultWithoutWriting(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", DepartDate: "2026-07-01", BelowPrice: 200})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	on := true
	if _, err := s.Update(id, WatchUpdate{LastMinuteMode: &on}); err == nil {
		t.Fatal("expected an error: last-minute mode is hotel-only")
	}

	fresh := NewStore(s.dir)
	if err := fresh.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, _ := fresh.Get(id); got.LastMinuteMode {
		t.Fatal("rejected update was persisted anyway")
	}
}

func TestStoreUpdate_ErrorsOnUnknownIDAndEmptyUpdate(t *testing.T) {
	s := NewStore(t.TempDir())
	empty := ""
	if _, err := s.Update("nope", WatchUpdate{WebhookURL: &empty}); err == nil {
		t.Fatal("expected an error for an unknown watch ID")
	}
	if _, err := s.Update("nope", WatchUpdate{}); err == nil {
		t.Fatal("expected an error for an empty update")
	}
}

// TRVL.ALERTMARK.1 -- an explicit alert clear must hand the watch back to
// default alerting, not leave it permanently silent.
//
// A currency change can force-zero AlertDropAbs when it is the watch's ONLY
// threshold, and records that as AlertDropAbsClearedByCurrency so the checker
// suspends alerting rather than letting Threshold.effective() substitute the
// built-in default for a policy the user never chose (round 17).
//
// applyIntent clears that marker the instant a re-watch supplies either limb
// (store.go). Store.Update did not: it wrote the threshold values and left the
// marker set. So `trvl watch update --clear-alert-drop` produced
// AlertDropPct == 0, AlertDropAbs == 0, marker still true -- exactly the state
// the checker's guard suppresses. The user clears an override expecting default
// alerting back and gets no alerts at all, with nothing saying so.
func TestUpdateClearingAlertResumesDefaultAlerting(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		Currency: "JPY", AlertDropAbs: 2000,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// A currency change that force-zeroes the sole absolute threshold.
	if _, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		Currency: "EUR",
	}); err != nil {
		t.Fatalf("currency change: %v", err)
	}
	if w, _ := s.Get(id); !w.AlertDropAbsClearedByCurrency {
		t.Fatal("fixture failure: the currency change did not set the pending-reconfirmation marker, " +
			"so this test would pass without exercising anything")
	}

	// The user explicitly clears the override, asking for default behaviour.
	zero := 0.0
	got, err := s.Update(id, WatchUpdate{AlertDropPct: &zero, AlertDropAbs: &zero})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if got.AlertDropAbsClearedByCurrency {
		t.Error("an explicit threshold clear left the pending-reconfirmation marker set, " +
			"so the checker keeps suspending alerts and default alerting never resumes")
	}
}

// TRVL.ALERTMARK.4 -- the marker must still suppress alerting when the user has
// NOT reconfirmed. Without this, TRVL.ALERTMARK.1 could be satisfied by never
// setting the marker at all, which would restore the round-17 defect it exists
// to prevent.
func TestCurrencyChangeStillSuspendsAlertingUntilReconfirmed(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		Currency: "JPY", AlertDropAbs: 2000,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		Currency: "EUR",
	}); err != nil {
		t.Fatalf("currency change: %v", err)
	}

	w, _ := s.Get(id)
	if !w.AlertDropAbsClearedByCurrency {
		t.Error("a currency change that zeroed the sole absolute threshold did not mark the watch " +
			"pending reconfirmation; the checker would substitute the built-in default for a policy " +
			"the user never chose")
	}
	if w.AlertDropAbs != 0 {
		t.Errorf("AlertDropAbs = %v, want 0 -- an absolute threshold cannot survive a currency change", w.AlertDropAbs)
	}
}
