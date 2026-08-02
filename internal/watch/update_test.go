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
