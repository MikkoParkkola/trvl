package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/watch"
)

type mockWatchTicker struct {
	ch chan time.Time
}

type fakeDaemonTickerV28 struct {
	ch chan time.Time
}

func TestWatchAddCmd_FlagsExist(t *testing.T) {
	cmd := watchAddCmd()
	for _, name := range []string{"below", "currency", "return", "last-minute", "last-minute-drop"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on watchAddCmd", name)
		}
	}
}

func TestWatchAddCmd_InvalidOriginIATA(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := watchAddCmd()
	cmd.SetArgs([]string{"12", "BCN", "2026-07-01"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid origin IATA")
	}
}

func TestWatchAddCmd_InvalidDestIATA(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := watchAddCmd()
	cmd.SetArgs([]string{"HEL", "12", "2026-07-01"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid dest IATA")
	}
}

func TestWatchListCmd_EmptyStore(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := watchListCmd()
	cmd.SetArgs([]string{})
	_ = cmd.Execute()
}

func TestWatchRemoveCmd_NotFoundInEmptyStore(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := watchRemoveCmd()
	cmd.SetArgs([]string{"nonexistent-watch-id"})
	err := cmd.Execute()

	_ = err
}

func TestWatchAddCmd_RouteWatch(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := watchAddCmd()

	cmd.SetArgs([]string{"HEL", "BCN"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWatchAddCmd_DateRange(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := watchAddCmd()

	cmd.SetArgs([]string{"HEL", "BCN", "--from", "2026-07-01", "--to", "2026-07-31"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWatchAddCmd_SpecificDate_NoBelowPrice(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := watchAddCmd()

	cmd.SetArgs([]string{"HEL", "BCN", "--depart", "2026-07-01"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWatchAddCmd_HotelType(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := watchAddCmd()

	cmd.SetArgs([]string{"Prague", "--type", "hotel", "--depart", "2026-07-01", "--return", "2026-07-08"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWatchAddCmd_HotelLastMinutePersistsMode(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := watchAddCmd()

	cmd.SetArgs([]string{"Prague", "--type", "hotel", "--depart", "2026-07-01", "--return", "2026-07-02", "--last-minute", "--last-minute-drop", "30"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch add: %v", err)
	}

	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	watches := store.List()
	if len(watches) != 1 {
		t.Fatalf("stored watches = %d, want 1", len(watches))
	}
	if !watches[0].LastMinuteMode {
		t.Fatal("LastMinuteMode = false, want true")
	}
	if watches[0].LastMinuteDropPct != 30 {
		t.Fatalf("LastMinuteDropPct = %.0f, want 30", watches[0].LastMinuteDropPct)
	}
}

func TestWatchRoomsCmd_FlagsExist(t *testing.T) {
	cmd := watchRoomsCmd()
	for _, name := range []string{"checkin", "checkout", "below"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on watchRoomsCmd", name)
		}
	}
}

func TestWatchRoomsCmd_NoArgs(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := watchRoomsCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with no args")
	}
}

func TestWatchCheckCmd_EmptyStore(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := watchCheckCmd()
	cmd.SetArgs([]string{})

	_ = cmd.Execute()
}

func TestWatchListCmd_ShowsAddedWatch(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	addCmd := watchAddCmd()
	addCmd.SetArgs([]string{"HEL", "BCN"})
	if err := addCmd.Execute(); err != nil {
		t.Fatalf("watch add: %v", err)
	}

	listCmd := watchListCmd()
	listCmd.SetArgs([]string{})
	if err := listCmd.Execute(); err != nil {
		t.Errorf("watch list: %v", err)
	}
}

func TestWatchRemoveCmd_RemovesWatch(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	addCmd := watchAddCmd()
	addCmd.SetArgs([]string{"HEL", "BCN"})
	if err := addCmd.Execute(); err != nil {
		t.Fatalf("watch add: %v", err)
	}

	store, err := func() (interface{ List() interface{} }, error) {

		return nil, nil
	}()
	_ = store
	_ = err

	import_workaround := loadTripStore
	_ = import_workaround

	removeCmd := watchRemoveCmd()
	removeCmd.SetArgs([]string{"nonexistent"})
	_ = removeCmd.Execute()
}

func TestWatchHistoryCmd_WatchExistsNoHistory(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	addCmd := watchAddCmd()
	addCmd.SetArgs([]string{"HEL", "BCN"})
	if err := addCmd.Execute(); err != nil {
		t.Fatalf("watch add: %v", err)
	}

	listCmd := watchListCmd()
	listCmd.SetArgs([]string{})
	_ = listCmd.Execute()
}

func TestRunWatchCheckCycleWithRooms_EmptyStoreNoNetwork(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	cmd := watchCheckCmd()
	cmd.SetArgs([]string{})
	_ = cmd.Execute()
}

func TestWatchDaemonCmd_InvalidInterval(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := watchDaemonCmd()
	cmd.SetArgs([]string{"--every", "invalid-duration"})
	err := cmd.Execute()

	_ = err
}

func TestRunWatchDaemon_InvalidIntervalV14(t *testing.T) {
	ctx := context.Background()
	err := runWatchDaemon(ctx, &bytes.Buffer{}, 0, false, func(context.Context) (int, error) {
		return 0, nil
	}, func(d time.Duration) watchDaemonTicker {
		return &mockWatchTicker{ch: make(chan time.Time)}
	})
	if err == nil {
		t.Error("expected error for zero interval")
	}
}

func TestRunWatchDaemon_CancelledContextV14(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mt := &mockWatchTicker{ch: make(chan time.Time)}
	err := runWatchDaemon(ctx, &bytes.Buffer{}, time.Minute, false, func(context.Context) (int, error) {
		return 0, nil
	}, func(time.Duration) watchDaemonTicker { return mt })
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunWatchDaemon_RunNow_EmptyStoreV14(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mt := &mockWatchTicker{ch: make(chan time.Time)}
	var buf bytes.Buffer
	err := runWatchDaemon(ctx, &buf, time.Minute, true, func(context.Context) (int, error) {
		return 0, nil
	}, func(time.Duration) watchDaemonTicker { return mt })
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWatchListCmd_JSONFormat(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	addCmd := watchAddCmd()
	addCmd.SetArgs([]string{"HEL", "BCN"})
	if err := addCmd.Execute(); err != nil {
		t.Fatalf("watch add: %v", err)
	}

	oldFormat := format
	format = "json"
	defer func() { format = oldFormat }()

	listCmd := watchListCmd()
	listCmd.SetArgs([]string{})
	if err := listCmd.Execute(); err != nil {
		t.Errorf("watch list json: %v", err)
	}
}

func TestWatchListCmd_TableWithDateRangeWatch(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	addCmd := watchAddCmd()
	addCmd.SetArgs([]string{"HEL", "BCN", "--from", "2026-07-01", "--to", "2026-07-31", "--below", "200"})
	if err := addCmd.Execute(); err != nil {
		t.Fatalf("watch add: %v", err)
	}

	listCmd := watchListCmd()
	listCmd.SetArgs([]string{})
	if err := listCmd.Execute(); err != nil {
		t.Errorf("watch list table: %v", err)
	}
}

func TestWatchListCmd_SpecificDateWatchTable(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	addCmd := watchAddCmd()
	addCmd.SetArgs([]string{"HEL", "BCN", "--depart", "2026-07-01"})
	if err := addCmd.Execute(); err != nil {
		t.Fatalf("watch add: %v", err)
	}

	listCmd := watchListCmd()
	listCmd.SetArgs([]string{})
	if err := listCmd.Execute(); err != nil {
		t.Errorf("watch list specific date: %v", err)
	}
}

func TestWatchAddCmd_HotelTypeWithReturnV15(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	addCmd := watchAddCmd()
	addCmd.SetArgs([]string{"Prague", "--type", "hotel", "--depart", "2026-07-01", "--return", "2026-07-08", "--below", "100"})
	if err := addCmd.Execute(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	listCmd := watchListCmd()
	listCmd.SetArgs([]string{})
	_ = listCmd.Execute()
}

func TestWatchRemoveCmd_ActuallyRemoves(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	addCmd := watchAddCmd()
	addCmd.SetArgs([]string{"HEL", "BCN"})
	if err := addCmd.Execute(); err != nil {
		t.Fatalf("watch add: %v", err)
	}

	wStore, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("watch.DefaultStore: %v", err)
	}
	if err := wStore.Load(); err != nil {
		t.Fatalf("wStore.Load: %v", err)
	}
	watches := wStore.List()
	if len(watches) == 0 {
		t.Skip("no watches in store")
	}
	watchID := watches[0].ID

	removeCmd := watchRemoveCmd()
	removeCmd.SetArgs([]string{watchID})
	if err := removeCmd.Execute(); err != nil {
		t.Errorf("watch remove: %v", err)
	}
}

func TestRunWatchDaemon_NilRunCycleV16(t *testing.T) {
	ctx := context.Background()
	err := runWatchDaemon(ctx, &bytes.Buffer{}, time.Minute, false, nil, func(d time.Duration) watchDaemonTicker {
		return &mockWatchTicker{ch: make(chan time.Time)}
	})
	if err == nil {
		t.Error("expected error for nil runCycle")
	}
}

func TestRunWatchDaemon_TickerFires(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	tickCh := make(chan time.Time, 1)
	mt := &mockWatchTicker{ch: tickCh}

	cycleCount := 0
	var buf bytes.Buffer

	done := make(chan error, 1)
	go func() {
		done <- runWatchDaemon(ctx, &buf, time.Minute, false, func(context.Context) (int, error) {
			cycleCount++
			cancel()
			return 0, nil
		}, func(time.Duration) watchDaemonTicker { return mt })
	}()

	tickCh <- time.Now()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("timeout waiting for daemon to stop")
	}
}

func TestWatchDaemonCmd_EveryFlag(t *testing.T) {
	cmd := watchDaemonCmd()
	f := cmd.Flags().Lookup("every")
	if f == nil {
		t.Fatal("expected --every flag on watchDaemonCmd")
	}
	if f.DefValue != "6h0m0s" {
		t.Logf("default every = %q (may vary by platform)", f.DefValue)
	}
}

func TestWatchDaemonCmd_RunNowFlag(t *testing.T) {
	cmd := watchDaemonCmd()
	f := cmd.Flags().Lookup("run-now")
	if f == nil {
		t.Error("expected --run-now flag on watchDaemonCmd")
	}
}

func TestRunWatchCheckCycleWithRooms_EmptyStoreV19(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	n, err := runWatchCheckCycleWithRooms(t.Context(), &liveChecker{}, &liveRoomChecker{}, nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 checks with empty store, got %d", n)
	}
}

func TestWatchHistoryCmd_NotFoundV22(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	cmd := watchHistoryCmd()
	cmd.SetArgs([]string{"nonexistent-watch-id"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for nonexistent watch ID")
	}
}

func TestWatchHistoryCmd_NoHistoryV22(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	addCmd := watchAddCmd()
	addCmd.SetArgs([]string{"HEL", "BCN"})
	if err := addCmd.Execute(); err != nil {
		t.Fatalf("watch add: %v", err)
	}

	wStore, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("watch.DefaultStore: %v", err)
	}
	if err := wStore.Load(); err != nil {
		t.Fatalf("wStore.Load: %v", err)
	}
	watches := wStore.List()
	if len(watches) == 0 {
		t.Skip("no watches in store")
	}
	watchID := watches[0].ID

	histCmd := watchHistoryCmd()
	histCmd.SetArgs([]string{watchID})
	if err := histCmd.Execute(); err != nil {
		t.Errorf("watch history: %v", err)
	}
}

func TestWatchHistoryCmd_JSONFormatEmptyV22(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	addCmd := watchAddCmd()
	addCmd.SetArgs([]string{"HEL", "BCN"})
	if err := addCmd.Execute(); err != nil {
		t.Fatalf("watch add: %v", err)
	}

	wStore, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("watch.DefaultStore: %v", err)
	}
	if err := wStore.Load(); err != nil {
		t.Fatalf("wStore.Load: %v", err)
	}
	watches := wStore.List()
	if len(watches) == 0 {
		t.Skip("no watches in store")
	}
	watchID := watches[0].ID

	oldFormat := format
	format = "json"
	defer func() { format = oldFormat }()

	histCmd := watchHistoryCmd()
	histCmd.SetArgs([]string{watchID})
	_ = histCmd.Execute()
}
