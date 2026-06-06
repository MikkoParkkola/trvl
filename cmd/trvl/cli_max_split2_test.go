package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/trips"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

func TestCoalesce_BothEmpty(t *testing.T) {
	if got := coalesce("", ""); got != "" {
		t.Errorf("coalesce('','') = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// setupTimestamp
// ---------------------------------------------------------------------------

func TestSetupTimestamp_RFC3339Max(t *testing.T) {
	ts := setupTimestamp()
	_, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Errorf("setupTimestamp() = %q, not valid RFC3339: %v", ts, err)
	}
}

// ---------------------------------------------------------------------------
// hotelSourceLabels
// ---------------------------------------------------------------------------

func TestHotelSourceLabels_EmptyMax(t *testing.T) {
	h := models.HotelResult{}
	got := hotelSourceLabels(h)
	if got != "" {
		t.Errorf("hotelSourceLabels(empty) = %q, want empty", got)
	}
}

func TestHotelSourceLabels_Multiple(t *testing.T) {
	h := models.HotelResult{
		Sources: []models.PriceSource{
			{Provider: "google_hotels"},
			{Provider: "booking"},
			{Provider: "google_hotels"}, // duplicate
		},
	}
	got := hotelSourceLabels(h)
	if !strings.Contains(got, "Google") {
		t.Error("expected Google in labels")
	}
	if !strings.Contains(got, "Booking") {
		t.Error("expected Booking in labels")
	}
	// Should deduplicate.
	if strings.Count(got, "Google") > 1 {
		t.Error("expected deduplicated Google")
	}
}

// ---------------------------------------------------------------------------
// formatRoomsTable
// ---------------------------------------------------------------------------

func TestFormatRoomsTable_NoRoomsMax(t *testing.T) {
	models.UseColor = false
	out := captureStdoutMax(t, func() {
		_ = formatRoomsTable(&hotels.RoomAvailability{
			HotelID:  "hotel_123",
			CheckIn:  "2026-07-01",
			CheckOut: "2026-07-08",
		})
	})
	if !strings.Contains(out, "No room types found") {
		t.Errorf("expected 'No room types found', got %q", out)
	}
}

func TestFormatRoomsTable_WithRoomsMax(t *testing.T) {
	models.UseColor = false
	out := captureStdoutMax(t, func() {
		_ = formatRoomsTable(&hotels.RoomAvailability{
			Name:     "Beach Resort",
			CheckIn:  "2026-07-01",
			CheckOut: "2026-07-08",
			Rooms: []hotels.RoomType{
				{Name: "Standard", Price: 100, Currency: "EUR", MaxGuests: 2, Provider: "booking", Amenities: []string{"wifi", "AC"}},
				{Name: "Suite", Price: 250, Currency: "EUR", MaxGuests: 4, Provider: "booking"},
			},
		})
	})
	if !strings.Contains(out, "Beach Resort") {
		t.Error("expected hotel name in output")
	}
	if !strings.Contains(out, "Standard") {
		t.Error("expected room name in output")
	}
	if !strings.Contains(out, "Cheapest") {
		t.Error("expected cheapest summary")
	}
}

// ---------------------------------------------------------------------------
// formatTripMarkdown
// ---------------------------------------------------------------------------

func TestFormatTripMarkdown_WithLegs(t *testing.T) {
	tr := &trips.Trip{
		Name: "Summer Trip",
		Legs: []trips.TripLeg{
			{
				Type:      "flight",
				From:      "HEL",
				To:        "BCN",
				StartTime: "2026-07-01",
				EndTime:   "2026-07-01",
				Price:     199,
				Currency:  "EUR",
				Provider:  "Finnair",
			},
			{
				Type:      "hotel",
				From:      "BCN",
				To:        "BCN",
				StartTime: "2026-07-01",
				EndTime:   "2026-07-08",
				Price:     560,
				Currency:  "EUR",
			},
		},
	}
	md := formatTripMarkdown(tr)
	if !strings.Contains(md, "**HEL -> BCN**") {
		t.Error("expected route header")
	}
	if !strings.Contains(md, "7 nights") {
		t.Error("expected nights count")
	}
	if !strings.Contains(md, "Finnair") {
		t.Error("expected provider in table")
	}
	if !strings.Contains(md, "Total") {
		t.Error("expected total row")
	}
}

func TestFormatTripMarkdown_NoLegsMax(t *testing.T) {
	tr := &trips.Trip{Name: "Empty Trip"}
	md := formatTripMarkdown(tr)
	if !strings.Contains(md, "**Empty Trip**") {
		t.Error("expected trip name as header")
	}
}

func TestFormatTripMarkdown_NoPrices(t *testing.T) {
	tr := &trips.Trip{
		Legs: []trips.TripLeg{
			{From: "HEL", To: "BCN", StartTime: "2026-07-01"},
		},
	}
	md := formatTripMarkdown(tr)
	// Should not contain price table.
	if strings.Contains(md, "| Price |") {
		t.Error("should not contain price table when no prices")
	}
}

// ---------------------------------------------------------------------------
// saveLastSearch / loadLastSearch
// ---------------------------------------------------------------------------

func TestSaveLoadLastSearch_RoundTripMax(t *testing.T) {
	// Override HOME to use temp dir.
	dir := t.TempDir()
	setTestHome(t, dir)

	ls := &LastSearch{
		Command:     "flights",
		Origin:      "HEL",
		Destination: "BCN",
	}
	saveLastSearch(ls)

	loaded, err := loadLastSearch()
	if err != nil {
		t.Fatalf("loadLastSearch: %v", err)
	}
	if loaded.Origin != "HEL" {
		t.Errorf("Origin = %q, want HEL", loaded.Origin)
	}
	if loaded.Destination != "BCN" {
		t.Errorf("Destination = %q, want BCN", loaded.Destination)
	}
}

// ---------------------------------------------------------------------------
// runWatchDaemon — additional uncovered branches
// ---------------------------------------------------------------------------

func TestRunWatchDaemon_NilRunCycle(t *testing.T) {
	err := runWatchDaemon(context.Background(), &bytes.Buffer{}, time.Hour, true, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil runCycle")
	}
	if !strings.Contains(err.Error(), "check function") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunWatchDaemon_NilTicker(t *testing.T) {
	// With nil newTicker, should use default.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so daemon exits

	var buf bytes.Buffer
	err := runWatchDaemon(ctx, &buf, time.Hour, false, func(context.Context) (int, error) {
		return 0, nil
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunWatchDaemon_NoRunNow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticker := &stubWatchDaemonTicker{ch: make(chan time.Time, 1)}
	var buf bytes.Buffer
	runs := 0
	done := make(chan error, 1)

	go func() {
		done <- runWatchDaemon(ctx, &buf, time.Hour, false, func(context.Context) (int, error) {
			runs++
			cancel()
			return 0, nil
		}, func(time.Duration) watchDaemonTicker {
			return ticker
		})
	}()

	ticker.ch <- time.Now()

	if err := <-done; err != nil {
		t.Fatalf("runWatchDaemon: %v", err)
	}
	if runs != 1 {
		t.Errorf("runs = %d, want 1 (no runNow)", runs)
	}
}

func TestRunWatchDaemon_ZeroWatches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticker := &stubWatchDaemonTicker{ch: make(chan time.Time, 1)}
	var buf bytes.Buffer
	done := make(chan error, 1)

	go func() {
		done <- runWatchDaemon(ctx, &buf, time.Hour, true, func(context.Context) (int, error) {
			cancel()
			return 0, nil
		}, func(time.Duration) watchDaemonTicker {
			return ticker
		})
	}()

	if err := <-done; err != nil {
		t.Fatalf("runWatchDaemon: %v", err)
	}
	if !strings.Contains(buf.String(), "no active watches") {
		t.Errorf("expected 'no active watches' in output, got %q", buf.String())
	}
}

// ---------------------------------------------------------------------------
// watchDaemonCmd — covers the 50% uncovered cmd setup
// ---------------------------------------------------------------------------

func TestWatchDaemonCmd_FlagsMax(t *testing.T) {
	cmd := watchDaemonCmd()
	if cmd.Use != "daemon" {
		t.Errorf("Use = %q, want 'daemon'", cmd.Use)
	}
	f := cmd.Flags()
	if _, err := f.GetDuration("every"); err != nil {
		t.Errorf("missing --every flag: %v", err)
	}
	if _, err := f.GetBool("run-now"); err != nil {
		t.Errorf("missing --run-now flag: %v", err)
	}
}

// ---------------------------------------------------------------------------
// maybeShowFlightHackTips — deeper path coverage
// ---------------------------------------------------------------------------

func TestMaybeShowFlightHackTips_SingleFlight(t *testing.T) {
	// Not a live test — just exercises the pure logic paths.
	result := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Price: 500, Currency: "EUR", Legs: []models.FlightLeg{
				{AirlineCode: "AY"},
			}},
		},
	}
	// Should not panic.
	out := captureStdoutMax(t, func() {
		maybeShowFlightHackTips(context.Background(), []string{"HEL"}, []string{"NRT"}, "2026-07-01", "2026-07-08", 1, result, false)
	})
	_ = out // Just verify no panic.
}

// ---------------------------------------------------------------------------
// formatHotelsTable — covers the 81.8% to push higher
// ---------------------------------------------------------------------------

func TestFormatHotelsTable_NoHotels(t *testing.T) {
	models.UseColor = false
	out := captureStdoutMax(t, func() {
		_ = formatHotelsTable(context.Background(), "", "", &models.HotelSearchResult{}, false)
	})
	if !strings.Contains(out, "No hotels found") {
		t.Errorf("expected 'No hotels found', got %q", out)
	}
}

func TestFormatHotelsTable_WithHotels(t *testing.T) {
	models.UseColor = false
	result := &models.HotelSearchResult{
		Count:          2,
		TotalAvailable: 5,
		Hotels: []models.HotelResult{
			{
				Name:        "Cheap Hotel",
				Stars:       3,
				Rating:      7.5,
				ReviewCount: 100,
				Price:       50,
				Currency:    "EUR",
				Amenities:   []string{"wifi"},
				Sources:     []models.PriceSource{{Provider: "booking"}},
			},
			{
				Name:           "Fancy Hotel",
				Stars:          5,
				Rating:         9.2,
				ReviewCount:    500,
				Price:          200,
				Currency:       "EUR",
				Amenities:      []string{"pool", "spa", "gym"},
				Sources:        []models.PriceSource{{Provider: "trivago"}},
				Savings:        30,
				CheapestSource: "booking",
			},
		},
	}
	out := captureStdoutMax(t, func() {
		_ = formatHotelsTable(context.Background(), "", "", result, false)
	})
	if !strings.Contains(out, "Showing 2 of 5 hotels") {
		t.Error("expected 'Showing 2 of 5 hotels'")
	}
	if !strings.Contains(out, "Cheap Hotel") {
		t.Error("expected hotel name")
	}
	if !strings.Contains(out, "Cheapest") {
		t.Error("expected cheapest summary")
	}
}

// ---------------------------------------------------------------------------
// applyPreference — cover more preference keys
// ---------------------------------------------------------------------------

func TestApplyPreference_PreferredDistrictsMax(t *testing.T) {
	p := &preferences.Preferences{}
	err := applyPreference(p, "preferred_districts", "Barcelona=Eixample,Born")
	if err != nil {
		t.Fatalf("applyPreference: %v", err)
	}
}

func TestApplyPreference_PreferredDistrictsDelete(t *testing.T) {
	p := &preferences.Preferences{}
	// First add.
	_ = applyPreference((*prefsWrapper)(p), "preferred_districts", "Barcelona=Eixample")
	// Then delete.
	err := applyPreference((*prefsWrapper)(p), "preferred_districts", "Barcelona=")
	if err != nil {
		t.Fatalf("applyPreference delete: %v", err)
	}
}

func TestApplyPreference_InvalidFormat(t *testing.T) {
	p := &preferences.Preferences{}
	err := applyPreference((*prefsWrapper)(p), "preferred_districts", "noequalssign")
	if err == nil {
		t.Error("expected error for invalid format")
	}
}

func TestApplyPreference_EmptyCity(t *testing.T) {
	p := &preferences.Preferences{}
	err := applyPreference((*prefsWrapper)(p), "preferred_districts", "=Eixample")
	if err == nil {
		t.Error("expected error for empty city")
	}
}

func TestApplyPreference_UnknownKey_Max(t *testing.T) {
	p := &preferences.Preferences{}
	err := applyPreference((*prefsWrapper)(p), "nonexistent_key", "value")
	if err == nil {
		t.Error("expected error for unknown key")
	}
}

// ---------------------------------------------------------------------------
// watchHistoryCmd — cover the history display path
// ---------------------------------------------------------------------------

func TestWatchHistoryCmd_Found(t *testing.T) {
	dir := t.TempDir()
	store := watch.NewStore(dir)
	w := watch.Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
	}
	id, err := store.Add(w)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPrice(id, 200, "EUR"); err != nil {
		t.Fatal(err)
	}

	// The watch history command uses DefaultStore so we can't easily test the
	// CLI path, but we can verify the store operations work.
	history := store.History(id)
	if len(history) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(history))
	}
}

// ---------------------------------------------------------------------------
// maybeShowAccomHackTip — date parsing edge cases
// ---------------------------------------------------------------------------

func TestMaybeShowAccomHackTip_BadDates(t *testing.T) {
	// Should not panic with bad dates.
	maybeShowAccomHackTip(context.Background(), "Helsinki", "invalid", "2026-07-08", "EUR", 2)
	maybeShowAccomHackTip(context.Background(), "Helsinki", "2026-07-01", "invalid", "EUR", 2)
}

func TestMaybeShowAccomHackTip_ShortStayMax(t *testing.T) {
	// 2-night stay — should not trigger tip.
	out := captureStdoutMax(t, func() {
		maybeShowAccomHackTip(context.Background(), "Helsinki", "2026-07-01", "2026-07-03", "EUR", 2)
	})
	if strings.Contains(out, "Tip") {
		t.Error("should not show tip for short stay")
	}
}

func TestMaybeShowAccomHackTip_EmptyDatesMax(t *testing.T) {
	// Should return immediately with empty dates.
	maybeShowAccomHackTip(context.Background(), "Helsinki", "", "", "EUR", 2)
}

// ---------------------------------------------------------------------------
// captureStdout helper (already exists in other test files, but we need it here)
// ---------------------------------------------------------------------------

func TestOpenBrowser_EmptyURLMax(t *testing.T) {
	err := openBrowser("")
	if err == nil {
		t.Error("expected error for empty URL")
	}
}

// ---------------------------------------------------------------------------
// saveLastSearch edge case — nil pointer
// ---------------------------------------------------------------------------

// TestSaveLastSearch_NilDoesNotPanic removed: saveLastSearch(nil) intentionally
// panics because nil *LastSearch has no meaningful state to persist.

// ---------------------------------------------------------------------------
// formatWatchDates (extra branches to push coverage)
// ---------------------------------------------------------------------------

func TestFormatWatchDates_FixedDepartOnly(t *testing.T) {
	w := watch.Watch{DepartDate: "2026-07-01"}
	got := formatWatchDates(w)
	if got != "2026-07-01" {
		t.Errorf("formatWatchDates = %q, want %q", got, "2026-07-01")
	}
}

// ---------------------------------------------------------------------------
// secureTempPath
// ---------------------------------------------------------------------------

func TestSecureTempPath_Unique(t *testing.T) {
	dir := t.TempDir()
	p1, err := secureTempPath(dir, "test-")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := secureTempPath(dir, "test-")
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Error("expected unique paths")
	}
}

// ---------------------------------------------------------------------------
// upgradeCmd branches
// ---------------------------------------------------------------------------

func TestUpgradeCmd_DryRun(t *testing.T) {
	cmd := upgradeCmd()
	if cmd.Use != "upgrade" {
		t.Errorf("Use = %q, want 'upgrade'", cmd.Use)
	}
	// Check flags exist.
	if _, err := cmd.Flags().GetBool("dry-run"); err != nil {
		t.Errorf("missing --dry-run flag: %v", err)
	}
	if _, err := cmd.Flags().GetBool("quiet"); err != nil {
		t.Errorf("missing --quiet flag: %v", err)
	}
}

// ---------------------------------------------------------------------------
// mcpConfigKey
// ---------------------------------------------------------------------------

func TestMcpConfigKey_VSCode(t *testing.T) {
	if got := mcpConfigKey("vscode"); got != "servers" {
		t.Errorf("mcpConfigKey(vscode) = %q, want %q", got, "servers")
	}
}

func TestMcpConfigKey_Zed(t *testing.T) {
	if got := mcpConfigKey("zed"); got != "context_servers" {
		t.Errorf("mcpConfigKey(zed) = %q, want %q", got, "context_servers")
	}
}

func TestMcpConfigKey_Default(t *testing.T) {
	if got := mcpConfigKey("claude-desktop"); got != "mcpServers" {
		t.Errorf("mcpConfigKey(claude-desktop) = %q, want %q", got, "mcpServers")
	}
}

// ---------------------------------------------------------------------------
// clientConfigPath — cover more clients
// ---------------------------------------------------------------------------

func TestClientConfigPath_AllClients(t *testing.T) {
	clients := []string{
		"cursor", "claude-code", "windsurf", "vscode", "gemini",
		"amazon-q", "lm-studio",
	}
	for _, c := range clients {
		t.Run(c, func(t *testing.T) {
			path, err := clientConfigPath(c)
			if err != nil {
				t.Errorf("clientConfigPath(%q) error: %v", c, err)
			}
			if path == "" {
				t.Errorf("clientConfigPath(%q) = empty", c)
			}
		})
	}
}

func TestClientConfigPath_UnknownMax(t *testing.T) {
	_, err := clientConfigPath("unknown-editor")
	if err == nil {
		t.Error("expected error for unknown client")
	}
}

func TestClientConfigPath_Codex(t *testing.T) {
	path, err := clientConfigPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(path, "config.toml") {
		t.Errorf("codex path should contain config.toml, got %q", path)
	}
}

func TestClientConfigPath_Zed(t *testing.T) {
	path, err := clientConfigPath("zed")
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Error("expected non-empty path for zed")
	}
}

// ---------------------------------------------------------------------------
// convertRoundedDisplayAmounts
// ---------------------------------------------------------------------------

func TestConvertRoundedDisplayAmounts_SameCurrency(t *testing.T) {
	val := 100.0
	got := convertRoundedDisplayAmounts(context.Background(), "EUR", "EUR", 0, &val)
	if got != "EUR" {
		t.Errorf("same currency should return source, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// saveKeysTo — permission and round-trip
// ---------------------------------------------------------------------------

func TestSaveKeysTo_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := fmt.Sprintf("%s/deep/nested/keys.json", dir)

	keys := APIKeys{Kiwi: "test-key"}
	if err := saveKeysTo(path, keys); err != nil {
		t.Fatalf("saveKeysTo: %v", err)
	}

	// Verify file permissions.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtime.GOOS != "windows" {
		if info.Mode().Perm() != 0o600 {
			t.Errorf("file mode = %o, want 0600", info.Mode().Perm())
		}
	}
}
