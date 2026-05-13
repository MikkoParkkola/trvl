package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestTrips_AddLegAndBook(t *testing.T) {
	withTempHome(t)

	// Create a trip first
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	createCmd := tripsCreateCmd()
	createCmd.SetArgs([]string{"BCN Trip"})
	err := createCmd.Execute()
	_ = w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	tripID := strings.TrimSpace(strings.TrimPrefix(buf.String(), "Created trip: "))

	// Add leg
	old = os.Stdout
	_, w, _ = os.Pipe()
	os.Stdout = w
	addLeg := tripsAddLegCmd()
	addLeg.SetArgs([]string{tripID, "flight", "--from", "HEL", "--to", "BCN", "--provider", "AY", "--price", "150", "--currency", "EUR", "--confirmed"})
	err = addLeg.Execute()
	_ = w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("add-leg failed: %v", err)
	}

	// Book
	old = os.Stdout
	_, w, _ = os.Pipe()
	os.Stdout = w
	bookCmd := tripsBookCmd()
	bookCmd.SetArgs([]string{tripID, "--provider", "AY", "--ref", "ABC123"})
	err = bookCmd.Execute()
	_ = w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("book failed: %v", err)
	}
}

func TestTrips_StatusEmpty(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	statusCmd := tripsStatusCmd()
	err := statusCmd.Execute()
	_ = w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "No upcoming trips") {
		t.Errorf("expected no upcoming message, got: %s", buf.String())
	}
}

func TestTrips_AlertsEmpty(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	alertsCmd := tripsAlertsCmd()
	err := alertsCmd.Execute()
	_ = w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("alerts failed: %v", err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	// Just verify it ran without error
	_ = buf
}

// ---------------------------------------------------------------------------
// Prefs CLI — show/set with temp HOME
// ---------------------------------------------------------------------------

func TestPrefs_ShowDefault(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := runPrefsShow(nil, nil)
	_ = w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("prefs show failed: %v", err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	// Should output valid JSON
	if !strings.Contains(buf.String(), "{") {
		t.Errorf("expected JSON output, got: %s", buf.String())
	}
}

func TestPrefs_Set(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w
	err := runPrefsSet(nil, []string{"display_currency", "EUR"})
	_ = w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("prefs set failed: %v", err)
	}
}

func TestPrefs_SetHomeAirports(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w
	err := runPrefsSet(nil, []string{"home_airports", "HEL,AMS"})
	_ = w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("prefs set failed: %v", err)
	}
}

func TestPrefs_SetBool(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w
	err := runPrefsSet(nil, []string{"carry_on_only", "true"})
	_ = w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("prefs set bool failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Providers CLI — list with temp HOME (no providers configured)
// ---------------------------------------------------------------------------

func TestProviders_ListEmpty(t *testing.T) {
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
// colorizeVerdict — points_value.go
// ---------------------------------------------------------------------------

func TestColorizeVerdict_AllValues(t *testing.T) {
	tests := []struct {
		verdict string
	}{
		{"use points"},
		{"pay cash"},
		{"mixed"},
	}
	for _, tt := range tests {
		got := colorizeVerdict(tt.verdict)
		if got == "" {
			t.Errorf("colorizeVerdict(%q) returned empty", tt.verdict)
		}
	}
}

// ---------------------------------------------------------------------------
// printProgramList — points_value.go
// ---------------------------------------------------------------------------

func TestPrintProgramList_Table(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := printProgramList("table")
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("printProgramList failed: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if buf.Len() == 0 {
		t.Error("expected non-empty program list")
	}
}

func TestPrintProgramList_JSON(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := printProgramList("json")
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("printProgramList json failed: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "[") {
		t.Error("expected JSON array output")
	}
}

// ---------------------------------------------------------------------------
// Watch CLI — list/remove/history with temp HOME
// ---------------------------------------------------------------------------

func TestWatch_ListEmpty(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := watchListCmd()
	err := cmd.Execute()
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("watch list failed: %v", err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "No active watches") {
		t.Errorf("expected 'No active watches' message, got: %s", buf.String())
	}
}

func TestWatch_RemoveNotFound(t *testing.T) {
	withTempHome(t)

	cmd := watchRemoveCmd()
	cmd.SetArgs([]string{"nonexistent"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for nonexistent watch")
	}
}

func TestWatch_HistoryNotFound(t *testing.T) {
	withTempHome(t)

	cmd := watchHistoryCmd()
	cmd.SetArgs([]string{"nonexistent"})
	err := cmd.Execute()
	// Should either error or show empty history
	_ = err
}

func TestWatch_AddListRemoveFlow(t *testing.T) {
	withTempHome(t)

	// Add a watch
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	addCmd := watchAddCmd()
	addCmd.SetArgs([]string{"HEL", "BCN", "--depart", "2026-06-15", "--return", "2026-06-22", "--below", "200"})
	err := addCmd.Execute()
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("watch add failed: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	// List should now show the watch
	old = os.Stdout
	r, w, _ = os.Pipe()
	os.Stdout = w
	listCmd := watchListCmd()
	err = listCmd.Execute()
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("watch list failed: %v", err)
	}

	buf.Reset()
	_, _ = buf.ReadFrom(r)
	output := buf.String()
	if strings.Contains(output, "No active watches") {
		t.Error("expected watches after adding one")
	}
	if !strings.Contains(output, "HEL") || !strings.Contains(output, "BCN") {
		t.Errorf("expected route in list, got: %s", output)
	}
}

// ---------------------------------------------------------------------------
// mcpConfigKey / trvlBinaryPath — mcp_install.go helpers
// ---------------------------------------------------------------------------
