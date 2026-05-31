package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadLastSearch_NotFoundV10(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	_, err := loadLastSearch()
	if err == nil {
		t.Error("expected error when no last_search.json")
	}
}

func TestSecureTempPath_ReturnsPath(t *testing.T) {
	tmp := t.TempDir()
	path, err := secureTempPath(tmp, "trvl-test-")
	if err != nil {
		t.Fatalf("secureTempPath: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty secureTempPath")
	}
}

func TestKeysPath_ReturnsPath(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	path, err := keysPath()
	if err != nil {
		t.Fatalf("keysPath: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty keysPath")
	}
}

func TestLoadExistingKeys_NonexistentFile(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	keys := loadExistingKeys()

	_ = keys
}

func TestSaveKeys_WritesFile(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	keys := APIKeys{
		SeatsAero: "test-key",
	}
	if err := saveKeys(keys); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	path, _ := keysPath()
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		t.Error("expected keys file to be written")
	}
}

func TestShouldShowNudge_NotSearchCmdV14(t *testing.T) {
	got := shouldShowNudge("setup", "", os.Getenv, os.Stderr.Fd(), func(int) bool { return true })
	if got {
		t.Error("expected false for non-search command")
	}
}

func TestShouldShowNudge_TrvlNoNudgeEnvV14(t *testing.T) {
	got := shouldShowNudge("flights", "", func(key string) string {
		if key == "TRVL_NO_NUDGE" {
			return "1"
		}
		return ""
	}, os.Stderr.Fd(), func(int) bool { return true })
	if got {
		t.Error("expected false when TRVL_NO_NUDGE=1")
	}
}

func TestLoadNudgeState_InvalidJSONV14(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "nudge.json")
	_ = os.WriteFile(p, []byte("not-json"), 0o600)
	s := loadNudgeState(p)
	if s.SearchCount != 0 || s.Shown {
		t.Errorf("expected zero nudgeState for invalid JSON, got %+v", s)
	}
}

func TestSaveAndLoadNudgeStateV14(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "nudge.json")
	original := nudgeState{SearchCount: 2, Shown: false}
	saveNudgeState(p, original)
	loaded := loadNudgeState(p)
	if loaded.SearchCount != 2 {
		t.Errorf("expected SearchCount=2, got %d", loaded.SearchCount)
	}
}

func TestSaveNudgeState_ShownTrueV14(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "nudge.json")
	s := nudgeState{SearchCount: 5, Shown: true, ShownAt: time.Now()}
	saveNudgeState(p, s)
	loaded := loadNudgeState(p)
	if !loaded.Shown {
		t.Error("expected Shown=true after save")
	}
}

func TestDiscoverCmd_MissingOriginNoPrefs(t *testing.T) {

	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := discoverCmd()
	cmd.SetArgs([]string{"--from", "2026-07-01", "--until", "2026-07-31", "--budget", "500"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no origin and no prefs")
	}
}

func TestPrefsAddFamilyMemberCmd_AddsV16(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsAddFamilyMemberCmd()
	cmd.SetArgs([]string{"family_member", "Father", "--notes", "prefers window seat"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("add family member: %v", err)
	}
}

func TestPrefsAddFamilyMemberCmd_WrongKeyV16(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsAddFamilyMemberCmd()
	cmd.SetArgs([]string{"not_family_member", "Bob"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for wrong first arg")
	}
}

func TestProfileAddCmd_AddsFlightBooking(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	cmd := profileAddCmd()
	cmd.SetArgs([]string{
		"--type", "flight",
		"--provider", "KLM",
		"--from", "HEL",
		"--to", "AMS",
		"--price", "189",
		"--currency", "EUR",
		"--travel-date", "2026-03-15",
	})
	if err := cmd.Execute(); err != nil {
		t.Errorf("profile add: %v", err)
	}
}

func TestProfileAddCmd_AddsHotelBooking(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	cmd := profileAddCmd()
	cmd.SetArgs([]string{
		"--type", "hotel",
		"--provider", "Marriott",
		"--price", "450",
		"--currency", "EUR",
		"--nights", "3",
		"--stars", "4",
		"--travel-date", "2026-03-15",
	})
	if err := cmd.Execute(); err != nil {
		t.Errorf("profile add hotel: %v", err)
	}
}

func TestProfileAddCmd_PrintsFrom_To(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	cmd := profileAddCmd()
	cmd.SetArgs([]string{
		"--type", "ground",
		"--provider", "FlixBus",
		"--from", "Prague",
		"--to", "Vienna",
		"--price", "19",
		"--currency", "EUR",
	})
	if err := cmd.Execute(); err != nil {
		t.Errorf("profile add ground: %v", err)
	}
}

func TestPrefsSetCmd_HomeAirports(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"home_airports", "HEL"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("prefs set home_airports: %v", err)
	}
}

func TestPrefsSetCmd_DisplayCurrency(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"display_currency", "EUR"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("prefs set display_currency: %v", err)
	}
}

func TestPrefsSetCmd_InvalidDisplayCurrency(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"display_currency", "TOOLONG"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid display_currency")
	}
}

func TestPrefsSetCmd_MinHotelStars(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"min_hotel_stars", "3"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("prefs set min_hotel_stars: %v", err)
	}
}

func TestPrefsSetCmd_MinHotelRating(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"min_hotel_rating", "8.5"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("prefs set min_hotel_rating: %v", err)
	}
}

func TestClientConfigPath_WindsurfV19(t *testing.T) {
	p, err := clientConfigPath("windsurf")
	if err != nil {
		t.Fatalf("clientConfigPath(windsurf): %v", err)
	}
	if p == "" {
		t.Error("expected non-empty path for windsurf")
	}
}

func TestClientConfigPath_CodexV19(t *testing.T) {
	p, err := clientConfigPath("codex")
	if err != nil {
		t.Fatalf("clientConfigPath(codex): %v", err)
	}
	if p == "" {
		t.Error("expected non-empty path for codex")
	}
}

func TestClientConfigPath_GeminiV19(t *testing.T) {
	p, err := clientConfigPath("gemini")
	if err != nil {
		t.Fatalf("clientConfigPath(gemini): %v", err)
	}
	if p == "" {
		t.Error("expected non-empty path for gemini")
	}
}

func TestClientConfigPath_AmazonQV19(t *testing.T) {
	p, err := clientConfigPath("amazon-q")
	if err != nil {
		t.Fatalf("clientConfigPath(amazon-q): %v", err)
	}
	if p == "" {
		t.Error("expected non-empty path for amazon-q")
	}
}

func TestClientConfigPath_ZedV19(t *testing.T) {
	p, err := clientConfigPath("zed")
	if err != nil {
		t.Fatalf("clientConfigPath(zed): %v", err)
	}
	if p == "" {
		t.Error("expected non-empty path for zed")
	}
}

func TestClientConfigPath_LMStudioV19(t *testing.T) {
	p, err := clientConfigPath("lm-studio")
	if err != nil {
		t.Fatalf("clientConfigPath(lm-studio): %v", err)
	}
	if p == "" {
		t.Error("expected non-empty path for lm-studio")
	}
}

func TestClientConfigPath_VSCodeV19(t *testing.T) {
	p, err := clientConfigPath("vscode")
	if err != nil {
		t.Fatalf("clientConfigPath(vscode): %v", err)
	}
	if p == "" {
		t.Error("expected non-empty path for vscode")
	}
}

func TestProfileImportEmailCmd_RunsV20(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := profileCmd()
	cmd.SetArgs([]string{"import-email"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("profile import-email: %v", err)
	}
}

func TestLoadLastSearch_MissingFileV20(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	_, err := loadLastSearch()
	if err == nil {
		t.Error("expected error for missing last_search.json")
	}
}

func TestPrefsSetCmd_LocaleV21(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"locale", "en-FI"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("prefs set locale: %v", err)
	}
}

func TestPrefsSetCmd_HomeAirportsMultipleV21(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"home_airports", "HEL,AMS"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("prefs set home_airports multi: %v", err)
	}
}

func TestPrefsSetCmd_LoyaltyAirlinesV21(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"loyalty_airlines", "AY,KL"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("prefs set loyalty_airlines: %v", err)
	}
}

func TestPrefsSetCmd_LoyaltyHotelsV21(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"loyalty_hotels", "Marriott Bonvoy"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("prefs set loyalty_hotels: %v", err)
	}
}

func TestPrefsSetCmd_PreferredDistrictsV21(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"preferred_districts", "Prague=Prague 1,Prague 2"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("prefs set preferred_districts: %v", err)
	}
}

func TestPrefsSetCmd_CarryOnOnlyV21(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"carry_on_only", "true"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("prefs set carry_on_only: %v", err)
	}
}

func TestPrefsSetCmd_PreferDirectV21(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"prefer_direct", "false"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("prefs set prefer_direct: %v", err)
	}
}

func TestPrefsSetCmd_UnknownKeyV21(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"unknown_key_xyz", "value"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for unknown preference key")
	}
}

func TestWhenCmd_MissingOriginNoPrefsV22(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	// Disable geo-IP so origin resolution can't silently succeed off the
	// network; with no home airport and no explicit --origin it must error.
	t.Setenv("TRVL_NO_GEO", "1")
	cmd := whenCmd()
	cmd.SetArgs([]string{"--to", "BCN", "--from", "2026-07-01", "--until", "2026-07-31"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no origin and no prefs")
	}
}

func TestPrefsSetCmd_EnsuitOnlyV23(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"ensuite_only", "true"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("prefs set ensuite_only: %v", err)
	}
}

func TestPrefsSetCmd_NoDormitoriesV23(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"no_dormitories", "true"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("prefs set no_dormitories: %v", err)
	}
}

func TestPrefsSetCmd_FastWifiV23(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"fast_wifi_needed", "true"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("prefs set fast_wifi_needed: %v", err)
	}
}

func TestPrefsSetCmd_HomeCitiesV23(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := prefsSetCmd()
	cmd.SetArgs([]string{"home_cities", "Helsinki,Amsterdam"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("prefs set home_cities: %v", err)
	}
}

func TestShouldShowNudge_NotSearchCommandV24(t *testing.T) {
	got := shouldShowNudge("prefs", "", os.Getenv, 2, func(int) bool { return true })
	if got {
		t.Error("expected false for non-search command")
	}
}

func TestShouldShowNudge_NoNudgeEnvV24(t *testing.T) {
	t.Setenv("TRVL_NO_NUDGE", "1")
	got := shouldShowNudge("flights", "", os.Getenv, 2, func(int) bool { return true })
	if got {
		t.Error("expected false when TRVL_NO_NUDGE=1")
	}
}

func TestShouldShowNudge_JSONFormatV24(t *testing.T) {
	got := shouldShowNudge("flights", "json", os.Getenv, 2, func(int) bool { return true })
	if got {
		t.Error("expected false when format=json")
	}
}

func TestShouldShowNudge_NotTerminalV24(t *testing.T) {
	got := shouldShowNudge("flights", "", os.Getenv, 2, func(int) bool { return false })
	if got {
		t.Error("expected false when not a terminal")
	}
}

func TestShouldShowNudge_ReturnsTrueV24(t *testing.T) {
	t.Setenv("TRVL_NO_NUDGE", "")
	got := shouldShowNudge("hotels", "", func(key string) string {
		if key == "TRVL_NO_NUDGE" {
			return ""
		}
		return ""
	}, 2, func(int) bool { return true })
	if !got {
		t.Error("expected true for search command with terminal and no suppression")
	}
}

func TestNudgePath_ReturnsPathV24(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	p, err := nudgePath()
	if err != nil {
		t.Fatalf("nudgePath: %v", err)
	}
	if !strings.HasSuffix(p, "nudge.json") {
		t.Errorf("expected path ending in nudge.json, got %s", p)
	}
}

func TestSaveAndLoadNudgeState_V24(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "nudge.json")

	s := nudgeState{SearchCount: 3, Shown: true}
	saveNudgeState(path, s)

	loaded := loadNudgeState(path)
	if loaded.SearchCount != 3 {
		t.Errorf("expected SearchCount=3, got %d", loaded.SearchCount)
	}
	if !loaded.Shown {
		t.Error("expected Shown=true")
	}
}

func TestLoadNudgeState_MissingFileV24(t *testing.T) {
	s := loadNudgeState("/tmp/nonexistent-nudge-xyz.json")
	if s.SearchCount != 0 || s.Shown {
		t.Errorf("expected zero state for missing file, got %+v", s)
	}
}

func TestRunSetup_NonInteractiveV24(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	cfg := setupConfig{
		nonInteractive: true,
		homeFlag:       "HEL",
		currencyFlag:   "EUR",
		cabinFlag:      "economy",
		stdin:          os.Stdin,
		stdout:         os.Stdout,
	}
	if err := runSetup(cfg); err != nil {
		t.Errorf("runSetup non-interactive: %v", err)
	}
}

func TestRunSetup_NonInteractiveBusinessClassV24(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	cfg := setupConfig{
		nonInteractive: true,
		homeFlag:       "JFK",
		currencyFlag:   "USD",
		cabinFlag:      "business",
		stdin:          os.Stdin,
		stdout:         os.Stdout,
	}
	if err := runSetup(cfg); err != nil {
		t.Errorf("runSetup non-interactive business: %v", err)
	}
}

func TestSecureTempPath_V24(t *testing.T) {
	tmp := t.TempDir()
	p, err := secureTempPath(tmp, "keys.json.tmp-")
	if err != nil {
		t.Fatalf("secureTempPath: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(p), "keys.json.tmp-") {
		t.Errorf("unexpected prefix in %s", p)
	}
}

func TestKeysPath_V24(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	p, err := keysPath()
	if err != nil {
		t.Fatalf("keysPath: %v", err)
	}
	if !strings.HasSuffix(p, "keys.json") {
		t.Errorf("expected keys.json suffix, got %s", p)
	}
}

func TestSaveKeysTo_V24(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, ".trvl", "keys.json")
	keys := APIKeys{SeatsAero: "test-key", Kiwi: "kiwi-key"}
	if err := saveKeysTo(path, keys); err != nil {
		t.Fatalf("saveKeysTo: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("keys.json not created: %v", err)
	}
}

func TestMcpConfigKey_V24(t *testing.T) {
	cases := []struct {
		client string
		want   string
	}{
		{"vscode", "servers"},
		{"vs-code", "servers"},
		{"copilot", "servers"},
		{"zed", "context_servers"},
		{"claude-desktop", "mcpServers"},
		{"windsurf", "mcpServers"},
		{"codex", "mcpServers"},
	}
	for _, tc := range cases {
		got := mcpConfigKey(tc.client)
		if got != tc.want {
			t.Errorf("mcpConfigKey(%q) = %q, want %q", tc.client, got, tc.want)
		}
	}
}

func TestTrvlBinaryPath_V24(t *testing.T) {
	p, err := trvlBinaryPath()
	if err != nil {
		t.Fatalf("trvlBinaryPath: %v", err)
	}
	if p == "" {
		t.Error("expected non-empty binary path")
	}
}

func TestPrefsEditCmd_NonNilV24(t *testing.T) {
	cmd := prefsEditCmd()
	if cmd == nil {
		t.Error("expected non-nil prefsEditCmd")
	}
}

func TestPrefsInitCmd_NonNilV24(t *testing.T) {
	cmd := prefsInitCmd()
	if cmd == nil {
		t.Error("expected non-nil prefsInitCmd")
	}
}

func TestLoadExistingKeys_MissingFileV24(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	keys := loadExistingKeys()

	if keys.SeatsAero != "" || keys.Kiwi != "" {
		t.Errorf("expected empty keys for missing file, got %+v", keys)
	}
}

func TestShouldShowNudge_SearchCommand_Terminal_V28(t *testing.T) {
	got := shouldShowNudge("flights", "table", func(string) string { return "" }, 0, func(int) bool { return true })
	if !got {
		t.Error("expected true for search command on terminal")
	}
}

func TestShouldShowNudge_NonSearch_V28(t *testing.T) {
	got := shouldShowNudge("version", "table", func(string) string { return "" }, 0, func(int) bool { return true })
	if got {
		t.Error("expected false for non-search command")
	}
}

func TestShouldShowNudge_EnvVarSuppressed_V28(t *testing.T) {
	got := shouldShowNudge("flights", "table", func(k string) string {
		if k == "TRVL_NO_NUDGE" {
			return "1"
		}
		return ""
	}, 0, func(int) bool { return true })
	if got {
		t.Error("expected false when TRVL_NO_NUDGE=1")
	}
}

func TestLoadNudgeState_MissingFile_V28(t *testing.T) {
	s := loadNudgeState("/tmp/trvl-nonexistent-nudge-v28-xyz.json")
	if s.SearchCount != 0 || s.Shown {
		t.Errorf("expected zero state for missing file, got %+v", s)
	}
}
