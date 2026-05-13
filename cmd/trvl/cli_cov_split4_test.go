package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMcpConfigKey_Values(t *testing.T) {
	tests := []struct {
		client string
		want   string
	}{
		{"claude", "mcpServers"},
		{"cursor", "mcpServers"},
		{"vscode", "servers"},
		{"zed", "context_servers"},
	}
	for _, tt := range tests {
		got := mcpConfigKey(tt.client)
		if got != tt.want {
			t.Errorf("mcpConfigKey(%q) = %q, want %q", tt.client, got, tt.want)
		}
	}
}

func TestTrvlBinaryPath_NonEmpty(t *testing.T) {
	p, _ := trvlBinaryPath()
	if p == "" {
		t.Error("expected non-empty binary path")
	}
}

// ---------------------------------------------------------------------------
// clientConfigPath — mcp_install.go
// ---------------------------------------------------------------------------

func TestClientConfigPath_AllKnownClients(t *testing.T) {
	clients := []string{"claude", "claude-code", "cursor", "windsurf", "codex"}
	for _, c := range clients {
		path, err := clientConfigPath(c)
		if err != nil {
			t.Errorf("clientConfigPath(%q) failed: %v", c, err)
		}
		if path == "" {
			t.Errorf("clientConfigPath(%q) returned empty path", c)
		}
	}
}

// ---------------------------------------------------------------------------
// providers status — with temp HOME
// ---------------------------------------------------------------------------

func TestProviders_StatusEmpty(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := providersStatusCmd()
	err := cmd.Execute()
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("providers status failed: %v", err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	_ = buf
}

// ---------------------------------------------------------------------------
// Trips with legs — status command exercises
// ---------------------------------------------------------------------------

func TestTrips_StatusWithTrip(t *testing.T) {
	withTempHome(t)

	// Create trip with a leg that has a future date
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	createCmd := tripsCreateCmd()
	createCmd.SetArgs([]string{"Upcoming"})
	_ = createCmd.Execute()
	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	tripID := strings.TrimSpace(strings.TrimPrefix(buf.String(), "Created trip: "))

	// Add a leg with future date
	futureDate := time.Now().AddDate(0, 0, 10).Format("2006-01-02T15:04")
	old = os.Stdout
	_, w, _ = os.Pipe()
	os.Stdout = w
	addLeg := tripsAddLegCmd()
	addLeg.SetArgs([]string{tripID, "flight", "--from", "HEL", "--to", "BCN", "--start", futureDate})
	_ = addLeg.Execute()
	_ = w.Close()
	os.Stdout = old

	// Now status should show it
	old = os.Stdout
	r, w, _ = os.Pipe()
	os.Stdout = w
	statusCmd := tripsStatusCmd()
	_ = statusCmd.Execute()
	_ = w.Close()
	os.Stdout = old

	buf.Reset()
	_, _ = buf.ReadFrom(r)
	// Should show the upcoming trip
	if !strings.Contains(buf.String(), "Upcoming") {
		t.Errorf("expected trip name in status, got: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// saveKeys — setup.go (exercises the keysPath() + saveKeysTo chain)
// ---------------------------------------------------------------------------

func TestSaveKeys_RoundTrip(t *testing.T) {
	withTempHome(t)

	keys := APIKeys{Kiwi: "test-kiwi-key"}
	err := saveKeys(keys)
	if err != nil {
		t.Fatalf("saveKeys failed: %v", err)
	}

	// Verify file was created
	loaded := loadExistingKeys()
	if loaded.Kiwi != "test-kiwi-key" {
		t.Errorf("expected kiwi key, got: %+v", loaded)
	}
}

// ---------------------------------------------------------------------------
// newRealWatchDaemonTicker / Chan — coverage for trivial methods
// ---------------------------------------------------------------------------

func TestNewRealWatchDaemonTicker(t *testing.T) {
	ticker := newRealWatchDaemonTicker(time.Hour)
	defer ticker.Stop()

	ch := ticker.Chan()
	if ch == nil {
		t.Error("expected non-nil channel")
	}
}

// ---------------------------------------------------------------------------
// shareTrip / shareLastSearch — with temp HOME
// ---------------------------------------------------------------------------

func TestShareTrip_Success(t *testing.T) {
	withTempHome(t)

	// Create a trip first
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	createCmd := tripsCreateCmd()
	createCmd.SetArgs([]string{"Share Test"})
	_ = createCmd.Execute()
	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	tripID := strings.TrimSpace(strings.TrimPrefix(buf.String(), "Created trip: "))

	// Add a leg
	old = os.Stdout
	_, w, _ = os.Pipe()
	os.Stdout = w
	addLeg := tripsAddLegCmd()
	addLeg.SetArgs([]string{tripID, "flight", "--from", "HEL", "--to", "BCN"})
	_ = addLeg.Execute()
	_ = w.Close()
	os.Stdout = old

	// Share
	old = os.Stdout
	r, w, _ = os.Pipe()
	os.Stdout = w
	err := shareTrip(tripID, "markdown")
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("shareTrip failed: %v", err)
	}

	buf.Reset()
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "HEL") || !strings.Contains(buf.String(), "BCN") {
		t.Errorf("expected route in share output, got: %s", buf.String())
	}
}

func TestShareLastSearch_Success(t *testing.T) {
	withTempHome(t)

	// Save a last search
	ls := &LastSearch{
		Command:        "flights",
		Origin:         "HEL",
		Destination:    "BCN",
		DepartDate:     "2026-06-15",
		FlightPrice:    150,
		FlightCurrency: "EUR",
	}
	saveLastSearch(ls)

	// Share
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := shareLastSearch("markdown")
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("shareLastSearch failed: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "HEL") {
		t.Errorf("expected origin in share output, got: %s", buf.String())
	}
}

func TestShareLastSearch_NoData(t *testing.T) {
	withTempHome(t)

	err := shareLastSearch("markdown")
	if err == nil {
		t.Error("expected error when no last search saved")
	}
}

// ---------------------------------------------------------------------------
// runProvidersList — with temp HOME
// ---------------------------------------------------------------------------

func TestRunProvidersList_EmptyWithTempHome(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := providersListCmd()
	err := cmd.Execute()
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("providers list failed: %v", err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "No providers") {
		t.Errorf("expected 'No providers' message, got: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// runProvidersStatus — with temp HOME
// ---------------------------------------------------------------------------

func TestRunProvidersStatus_Empty(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := providersStatusCmd()
	err := cmd.Execute()
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("providers status failed: %v", err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	_ = buf
}

// ---------------------------------------------------------------------------
// watchCheckCmd — runs check cycle with no watches
// ---------------------------------------------------------------------------

func TestWatch_CheckEmpty(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := watchCheckCmd()
	err := cmd.Execute()
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("watch check failed: %v", err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "No active watches") {
		t.Errorf("expected no-watches message, got: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// watchRoomsCmd — exercises the room watch add path
// ---------------------------------------------------------------------------

func TestWatch_AddRoomWatch(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })

	// Drain the pipe in a goroutine to prevent deadlock if output exceeds
	// the OS pipe buffer.
	outCh := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		outCh <- buf.String()
	}()

	cmd := watchRoomsCmd()
	cmd.SetArgs([]string{"Hotel Lutetia", "--checkin", "2026-06-15", "--checkout", "2026-06-18", "--keywords", "suite"})
	execErr := cmd.Execute()
	_ = w.Close()
	os.Stdout = old

	if execErr != nil {
		t.Fatalf("watch rooms failed: %v", execErr)
	}

	out := <-outCh
	if !strings.Contains(out, "watch") {
		t.Errorf("expected output to contain %q, got: %s", "watch", out)
	}
}

// ---------------------------------------------------------------------------
// Command validation paths — exercise cobra RunE error returns
// ---------------------------------------------------------------------------

func TestFlightsCmd_InvalidCabin(t *testing.T) {
	cmd := flightsCmd()
	cmd.SetArgs([]string{"HEL", "BCN", "2026-06-15", "--cabin", "invalid"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid cabin")
	}
}

func TestFlightsCmd_InvalidStops(t *testing.T) {
	cmd := flightsCmd()
	cmd.SetArgs([]string{"HEL", "BCN", "2026-06-15", "--stops", "invalid"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid stops")
	}
}

func TestFlightsCmd_InvalidSort(t *testing.T) {
	cmd := flightsCmd()
	cmd.SetArgs([]string{"HEL", "BCN", "2026-06-15", "--sort", "invalid"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid sort")
	}
}

func TestVisaCmd_MissingPassport(t *testing.T) {
	cmd := visaCmd()
	cmd.SetArgs([]string{"--destination", "JP"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when passport is missing")
	}
}

func TestVisaCmd_ListAll(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := visaCmd()
	cmd.SetArgs([]string{"--list"})
	err := cmd.Execute()
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("visa --list failed: %v", err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if buf.Len() < 100 {
		t.Errorf("expected country list, got too short output: %d bytes", buf.Len())
	}
}

func TestVisaCmd_Lookup(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := visaCmd()
	cmd.SetArgs([]string{"--passport", "FI", "--destination", "JP"})
	err := cmd.Execute()
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("visa lookup failed: %v", err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "Visa") {
		t.Errorf("expected visa info in output, got: %s", buf.String())
	}
}

func TestExploreCmd_InvalidOrigin(t *testing.T) {
	cmd := exploreCmd()
	cmd.SetArgs([]string{"XX"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid 2-letter IATA")
	}
}

func TestBaggageCmd_AllFlag(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	cmd := baggageCmd()
	cmd.SetArgs([]string{"--all"})
	err := cmd.Execute()
	_ = w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("baggage --all failed: %v", err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if buf.Len() == 0 {
		t.Error("expected non-empty baggage list")
	}
}

func TestBaggageCmd_SingleAirline(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	cmd := baggageCmd()
	cmd.SetArgs([]string{"KL"})
	err := cmd.Execute()
	_ = w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("baggage KL failed: %v", err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if buf.Len() == 0 {
		t.Error("expected non-empty baggage detail")
	}
}

func TestWeatherCmd_RequiresArg(t *testing.T) {
	cmd := weatherCmd()
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing arg")
	}
}

func TestLoungesCmd_RequiresArg(t *testing.T) {
	cmd := loungesCmd()
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing arg")
	}
}

func TestHacksCmd_RequiresOriginDest(t *testing.T) {
	cmd := hacksCmd()
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing args")
	}
}

func TestGridCmd_NoArgsFails(t *testing.T) {
	cmd := gridCmd()
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing args")
	}
}

func TestCalendarCmd_RequiresArgs(t *testing.T) {
	cmd := calendarCmd()
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing args")
	}
}

func TestAccomHackCmd_RequiresArg(t *testing.T) {
	cmd := accomHackCmd()
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing arg")
	}
}

func TestUpgradeCmd_Runs(t *testing.T) {
	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	cmd := upgradeCmd()
	cmd.SetArgs([]string{"--dry-run"})
	_ = cmd.Execute()
	_ = w.Close()
	os.Stdout = old
}

func TestUpgradeCmd_Quiet(t *testing.T) {
	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	cmd := upgradeCmd()
	cmd.SetArgs([]string{"--quiet"})
	_ = cmd.Execute()
	_ = w.Close()
	os.Stdout = old
}

// Import anchors to ensure all imports are used.
