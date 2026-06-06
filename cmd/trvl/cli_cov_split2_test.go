package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/nlsearch"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

func TestFormatWatchDates_DateRangeShowsBest(t *testing.T) {
	w := watch.Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		DepartFrom: "2026-06-01", DepartTo: "2026-06-30",
		CheapestDate: "2026-06-15",
	}
	got := formatWatchDates(w)
	if !strings.Contains(got, "best: 2026-06-15") {
		t.Errorf("expected cheapest date in range, got: %q", got)
	}
}

func TestFormatWatchDates_FixedDates(t *testing.T) {
	w := watch.Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		DepartDate: "2026-06-15",
	}
	got := formatWatchDates(w)
	if got != "2026-06-15" {
		t.Errorf("expected depart date only, got: %q", got)
	}
}

func TestFormatWatchDates_FixedDatesWithReturn(t *testing.T) {
	w := watch.Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		DepartDate: "2026-06-15", ReturnDate: "2026-06-22",
	}
	got := formatWatchDates(w)
	want := "2026-06-15 / 2026-06-22"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// formatLastCheck — renamed to avoid conflicts with watch_test.go
// ---------------------------------------------------------------------------

func TestFormatLastCheck_ZeroTime(t *testing.T) {
	got := formatLastCheck(time.Time{})
	if got != "never" {
		t.Errorf("expected 'never', got %q", got)
	}
}

func TestFormatLastCheck_Recent(t *testing.T) {
	got := formatLastCheck(time.Now().Add(-10 * time.Second))
	if got != "just now" {
		t.Errorf("expected 'just now', got %q", got)
	}
}

func TestFormatLastCheck_FewMinutes(t *testing.T) {
	got := formatLastCheck(time.Now().Add(-15 * time.Minute))
	if !strings.Contains(got, "m ago") {
		t.Errorf("expected minutes ago, got %q", got)
	}
}

func TestFormatLastCheck_FewHours(t *testing.T) {
	got := formatLastCheck(time.Now().Add(-5 * time.Hour))
	if !strings.Contains(got, "h ago") {
		t.Errorf("expected hours ago, got %q", got)
	}
}

func TestFormatLastCheck_MultipleDays(t *testing.T) {
	got := formatLastCheck(time.Now().Add(-72 * time.Hour))
	if !strings.Contains(got, "d ago") {
		t.Errorf("expected days ago, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// printSearchInterpretation
// ---------------------------------------------------------------------------

func TestPrintSearchInterpretation_Full(t *testing.T) {
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = oldStderr }()

	params := nlsearch.Params{
		Intent:      "flight",
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-15",
		ReturnDate:  "2026-06-22",
	}
	printSearchInterpretation("fly HEL BCN 2026-06-15", params)
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()
	if !strings.Contains(output, "intent=flight") {
		t.Errorf("expected intent in output, got: %s", output)
	}
	if !strings.Contains(output, "from=HEL") {
		t.Errorf("expected origin, got: %s", output)
	}
	if !strings.Contains(output, "return=2026-06-22") {
		t.Errorf("expected return date, got: %s", output)
	}
}

func TestPrintSearchInterpretation_Minimal(t *testing.T) {
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = oldStderr }()

	params := nlsearch.Params{Intent: "deals"}
	printSearchInterpretation("deals", params)
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "intent=deals") {
		t.Errorf("expected intent in output, got: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// missingFieldsHint — renamed to avoid conflicts with search_test.go
// ---------------------------------------------------------------------------

func TestMissingFieldsHint_FlightAllMissing(t *testing.T) {
	oldStderr := os.Stderr
	_, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = oldStderr }()

	err := missingFieldsHint(nlsearch.Params{}, "flight", "trvl flights ...")
	_ = w.Close()

	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestMissingFieldsHint_HotelMissingDates(t *testing.T) {
	oldStderr := os.Stderr
	_, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = oldStderr }()

	err := missingFieldsHint(nlsearch.Params{}, "hotel", "trvl hotels ...")
	_ = w.Close()

	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestMissingFieldsHint_DealsNoMissing(t *testing.T) {
	oldStderr := os.Stderr
	_, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = oldStderr }()

	err := missingFieldsHint(nlsearch.Params{}, "deals", "trvl deals")
	_ = w.Close()

	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// loadExistingKeys / saveKeysTo
// ---------------------------------------------------------------------------

func TestLoadExistingKeys_MissingFile(t *testing.T) {
	k := loadExistingKeys()
	_ = k // returns zero struct without panic
}

func TestSaveKeysTo_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	keys := APIKeys{
		SeatsAero: "sa-key",
		Kiwi:      "kiwi-key",
	}
	err := saveKeysTo(path, keys)
	if err != nil {
		t.Fatalf("saveKeysTo failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "sa-key") {
		t.Errorf("expected seats_aero key in file, got: %s", content)
	}
	if !strings.Contains(content, "kiwi-key") {
		t.Errorf("expected kiwi key in file, got: %s", content)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if runtime.GOOS != "windows" {
		perm := info.Mode().Perm()
		if perm&0o077 != 0 {
			t.Errorf("keys file should be owner-only, got permissions %o", perm)
		}
	}
}

// ---------------------------------------------------------------------------
// runInstallCodexTOML
// ---------------------------------------------------------------------------

func TestRunInstallCodexTOML_DryRun(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "codex.toml")

	err := runInstallCodexTOML(cfgPath, "/usr/local/bin/trvl", false, true)
	_ = w.Close()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "Would append") {
		t.Errorf("expected dry-run output, got: %s", buf.String())
	}
}

func TestRunInstallCodexTOML_CreateNew(t *testing.T) {
	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "codex.toml")

	err := runInstallCodexTOML(cfgPath, "/usr/local/bin/trvl", false, false)
	_ = w.Close()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(string(data), "[mcp_servers.trvl]") {
		t.Errorf("expected TOML entry in file, got: %s", string(data))
	}
}

func TestRunInstallCodexTOML_AlreadyInstalled(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "codex.toml")
	_ = os.WriteFile(cfgPath, []byte("[mcp_servers.trvl]\ncommand = \"old\""), 0o644)

	err := runInstallCodexTOML(cfgPath, "/usr/local/bin/trvl", false, false)
	_ = w.Close()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "already installed") {
		t.Errorf("expected already-installed message, got: %s", buf.String())
	}
}

func TestRunInstallCodexTOML_ForceOverwrite(t *testing.T) {
	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "codex.toml")
	_ = os.WriteFile(cfgPath, []byte("[mcp_servers.trvl]\ncommand = \"old\""), 0o644)

	err := runInstallCodexTOML(cfgPath, "/usr/local/bin/trvl", true, false)
	_ = w.Close()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(string(data), "trvl") {
		t.Errorf("expected trvl config after force, got: %s", string(data))
	}
}

// ---------------------------------------------------------------------------
// maybeShowFlightHackTips — exercise early returns
// ---------------------------------------------------------------------------

func TestMaybeShowFlightHackTips_NilResult(t *testing.T) {
	maybeShowFlightHackTips(context.Background(), nil, nil, "", "", 1, nil, false)
}

func TestMaybeShowFlightHackTips_EmptyFlights(t *testing.T) {
	result := &models.FlightSearchResult{Success: true, Flights: nil}
	maybeShowFlightHackTips(context.Background(), []string{"HEL"}, []string{"BCN"}, "2026-06-15", "", 1, result, false)
}

func TestMaybeShowFlightHackTips_FailedResult(t *testing.T) {
	result := &models.FlightSearchResult{Success: false}
	maybeShowFlightHackTips(context.Background(), []string{"HEL"}, []string{"BCN"}, "2026-06-15", "", 1, result, false)
}

// ---------------------------------------------------------------------------
// resolveString — extra cases (renamed to avoid conflicts with setup_test.go)
// ---------------------------------------------------------------------------

func TestResolveString_NonInteractiveWithExisting(t *testing.T) {
	got := resolveString(true, "", "existing", "fallback")
	if got != "existing" {
		t.Errorf("expected existing even in non-interactive, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// isCodexTOML
// ---------------------------------------------------------------------------

func TestIsCodexTOML_Various(t *testing.T) {
	tests := []struct {
		client string
		want   bool
	}{
		{"codex", true},
		{"Codex", true},
		{"CODEX", true},
		{"claude", false},
		{"cursor", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isCodexTOML(tt.client)
		if got != tt.want {
			t.Errorf("isCodexTOML(%q) = %v, want %v", tt.client, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// starRating — additional edge cases
// ---------------------------------------------------------------------------

func TestStarRating_FractionalRounding(t *testing.T) {
	got := starRating(3.5)
	fullCount := strings.Count(got, "\u2605")
	if fullCount != 3 {
		t.Errorf("expected 3 filled stars for 3.5, got %d in %q", fullCount, got)
	}
}

func TestStarRating_ExactInt(t *testing.T) {
	got := starRating(4.0)
	fullCount := strings.Count(got, "\u2605")
	if fullCount != 4 {
		t.Errorf("expected 4 filled stars for 4.0, got %d in %q", fullCount, got)
	}
}

// ---------------------------------------------------------------------------
// looksLikeGoogleHotelID — colon format
// ---------------------------------------------------------------------------

func TestLooksLikeGoogleHotelID_ColonFormat(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"abc:123", true},
		{"foo:bar baz", false},
		{"no-colon-here", false},
		{"two:colons:here", false},
	}
	for _, tt := range tests {
		got := looksLikeGoogleHotelID(tt.input)
		if got != tt.want {
			t.Errorf("looksLikeGoogleHotelID(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// colorizeVisaStatus — extra statuses
// ---------------------------------------------------------------------------

func TestColorizeVisaStatus_EvisaAndUnknown(t *testing.T) {
	got := colorizeVisaStatus("e-visa", "E-visa")
	if got == "" {
		t.Error("expected non-empty output for e-visa")
	}

	got2 := colorizeVisaStatus("unknown-status", "Unknown")
	if got2 != "Unknown" {
		t.Errorf("expected passthrough for unknown status, got %q", got2)
	}
}

// ---------------------------------------------------------------------------
// trvlFooter / capitalizeFirst / formatDateCompact — share.go helpers
// ---------------------------------------------------------------------------

func TestTrvlFooter_ContainsURL(t *testing.T) {
	f := trvlFooter()
	if !strings.Contains(f, "trvl") {
		t.Errorf("expected trvl in footer, got %q", f)
	}
}

func TestCapitalizeFirst_Table(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"hello", "Hello"},
		{"H", "H"},
		{"", ""},
		{"already", "Already"},
	}
	for _, tt := range tests {
		got := capitalizeFirst(tt.in)
		if got != tt.want {
			t.Errorf("capitalizeFirst(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatDateCompact_Valid(t *testing.T) {
	got := formatDateCompact("2026-06-15")
	if !strings.Contains(got, "Jun") || !strings.Contains(got, "15") {
		t.Errorf("expected formatted date, got %q", got)
	}
}
