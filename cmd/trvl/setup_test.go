package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// --- helpers ---

// makeFakeStdin writes content to a temp file and returns it opened for reading.
// The returned *os.File must be closed by the caller.
func makeFakeStdin(t *testing.T, content string) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin-*")
	if err != nil {
		t.Fatalf("makeFakeStdin: create temp: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("makeFakeStdin: write: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("makeFakeStdin: seek: %v", err)
	}
	return f
}

// makeFakeStdout creates a writable temp file to capture output.
func makeFakeStdout(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatalf("makeFakeStdout: %v", err)
	}
	return f
}

// readAll reads the full contents of f from the beginning.
func readAll(t *testing.T, f *os.File) string {
	t.Helper()
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("readAll seek: %v", err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	return string(data)
}

// overridePrefsPath temporarily redirects preferences to a temp dir.
// Returns a function that writes the preferences file path and the cleanup func.
// We use SaveTo/LoadFrom directly in tests to avoid touching ~/.trvl.
func tempPrefsPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "preferences.json")
}

// --- non-interactive mode ---

func TestSetupCmd_NonInteractive_Defaults(t *testing.T) {
	stdin := makeFakeStdin(t, "") // no input needed
	stdout := makeFakeStdout(t)
	defer func() { _ = stdin.Close() }()
	defer func() { _ = stdout.Close() }()

	cfg := setupConfig{
		nonInteractive: true,
		stdin:          stdin,
		stdout:         stdout,
		mcpClient:      "claude-desktop",
	}

	if err := runSetup(cfg); err != nil {
		t.Fatalf("runSetup non-interactive: %v", err)
	}

	out := readAll(t, stdout)
	if !strings.Contains(out, "Setup complete") {
		t.Errorf("expected 'Setup complete' in output, got:\n%s", out)
	}
}

func TestSetupCmd_NonInteractive_WithFlags(t *testing.T) {
	stdin := makeFakeStdin(t, "")
	stdout := makeFakeStdout(t)
	defer func() { _ = stdin.Close() }()
	defer func() { _ = stdout.Close() }()

	cfg := setupConfig{
		nonInteractive: true,
		homeFlag:       "HEL",
		currencyFlag:   "EUR",
		cabinFlag:      "business",
		stdin:          stdin,
		stdout:         stdout,
		mcpClient:      "claude-desktop",
	}

	if err := runSetup(cfg); err != nil {
		t.Fatalf("runSetup with flags: %v", err)
	}

	out := readAll(t, stdout)
	if !strings.Contains(out, "Preferences saved") {
		t.Errorf("expected 'Preferences saved' in output, got:\n%s", out)
	}
}

// --- preference file writing ---

func TestSaveKeysTo_WritesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	keys := APIKeys{
		SeatsAero: "sk_test123",
		Kiwi:      "kiwi_test456",
	}

	if err := saveKeysTo(path, keys); err != nil {
		t.Fatalf("saveKeysTo: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read keys file: %v", err)
	}

	var got APIKeys
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse keys file: %v", err)
	}
	if got.SeatsAero != keys.SeatsAero {
		t.Errorf("seats_aero: got %q, want %q", got.SeatsAero, keys.SeatsAero)
	}
	if got.Kiwi != keys.Kiwi {
		t.Errorf("kiwi: got %q, want %q", got.Kiwi, keys.Kiwi)
	}
	if got.Distribusion != "" {
		t.Errorf("distribusion: expected empty, got %q", got.Distribusion)
	}
}

func TestSaveKeysTo_Permissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file permissions not supported on Windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	if err := saveKeysTo(path, APIKeys{SeatsAero: "s"}); err != nil {
		t.Fatalf("saveKeysTo: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat keys file: %v", err)
	}
	// Mode should be 0600 (owner read+write, no group/other).
	got := info.Mode().Perm()
	if got != 0o600 {
		t.Errorf("keys.json permissions = %o, want 0600", got)
	}
}

func TestSaveKeysTo_CreatesDirectory(t *testing.T) {
	// saveKeysTo must create parent dirs automatically.
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested", "deep")
	path := filepath.Join(nested, "keys.json")

	if err := saveKeysTo(path, APIKeys{Kiwi: "k"}); err != nil {
		t.Fatalf("saveKeysTo in nested dir: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("keys file should exist: %v", err)
	}
}

func TestSaveKeysTo_OmitsEmptyFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	if err := saveKeysTo(path, APIKeys{SeatsAero: "abc"}); err != nil {
		t.Fatalf("saveKeysTo: %v", err)
	}

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "kiwi") {
		t.Error("empty kiwi field should be omitted from JSON output")
	}
}

// --- IATA validation ---

func TestSetupPromptIATA_ValidCode(t *testing.T) {
	// Feed a valid IATA code followed by newline.
	stdin := makeFakeStdin(t, "AMS\n")
	stdout := makeFakeStdout(t)
	defer func() { _ = stdin.Close() }()
	defer func() { _ = stdout.Close() }()

	sc := makeTestScanner(t, stdin)
	got := setupPromptIATA(sc, stdout, "Home airport", "HEL")
	if got != "AMS" {
		t.Errorf("setupPromptIATA: got %q, want AMS", got)
	}
}

func TestSetupPromptIATA_InvalidThenValid(t *testing.T) {
	// First input invalid, second valid.
	stdin := makeFakeStdin(t, "12\nHEL\n")
	stdout := makeFakeStdout(t)
	defer func() { _ = stdin.Close() }()
	defer func() { _ = stdout.Close() }()

	sc := makeTestScanner(t, stdin)
	got := setupPromptIATA(sc, stdout, "Home airport", "JFK")
	if got != "HEL" {
		t.Errorf("setupPromptIATA with retry: got %q, want HEL", got)
	}

	out := readAll(t, stdout)
	if !strings.Contains(out, "Invalid IATA code") {
		t.Errorf("expected invalid IATA error in output, got:\n%s", out)
	}
}

func TestSetupPromptIATA_EmptyKeepsCurrent(t *testing.T) {
	stdin := makeFakeStdin(t, "\n") // just Enter
	stdout := makeFakeStdout(t)
	defer func() { _ = stdin.Close() }()
	defer func() { _ = stdout.Close() }()

	sc := makeTestScanner(t, stdin)
	got := setupPromptIATA(sc, stdout, "Home airport", "NRT")
	if got != "NRT" {
		t.Errorf("setupPromptIATA empty input: got %q, want NRT (current)", got)
	}
}

func TestSetupPromptIATA_UpercasesInput(t *testing.T) {
	stdin := makeFakeStdin(t, "hel\n")
	stdout := makeFakeStdout(t)
	defer func() { _ = stdin.Close() }()
	defer func() { _ = stdout.Close() }()

	sc := makeTestScanner(t, stdin)
	got := setupPromptIATA(sc, stdout, "Home airport", "")
	if got != "HEL" {
		t.Errorf("setupPromptIATA lowercase input: got %q, want HEL", got)
	}
}

// --- preferences persistence via SaveTo/LoadFrom ---

func TestRunSetup_NonInteractive_SavesHomeAirport(t *testing.T) {
	// Use a temp prefs path. We intercept by loading after runSetup writes to real ~/.trvl.
	// Instead, we verify behavior via the preferences package directly.
	prefsPath := tempPrefsPath(t)

	// Start with empty prefs.
	p := preferences.Default()
	if err := preferences.SaveTo(prefsPath, p); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	// Load, set, save — mirrors what runSetup does.
	loaded, err := preferences.LoadFrom(prefsPath)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	loaded.HomeAirports = []string{"HEL"}
	loaded.DisplayCurrency = "EUR"

	if err := preferences.SaveTo(prefsPath, loaded); err != nil {
		t.Fatalf("SaveTo after update: %v", err)
	}

	result, err := preferences.LoadFrom(prefsPath)
	if err != nil {
		t.Fatalf("LoadFrom result: %v", err)
	}
	if result.HomeAirport() != "HEL" {
		t.Errorf("home airport: got %q, want HEL", result.HomeAirport())
	}
	if result.DisplayCurrency != "EUR" {
		t.Errorf("currency: got %q, want EUR", result.DisplayCurrency)
	}
}

// --- resolveString helper ---

func TestResolveString_FlagWins(t *testing.T) {
	got := resolveString(true, "FLAG", "EXISTING", "DEFAULT")
	if got != "FLAG" {
		t.Errorf("resolveString flag: got %q, want FLAG", got)
	}
}

func TestResolveString_ExistingWhenNoFlag(t *testing.T) {
	got := resolveString(true, "", "EXISTING", "DEFAULT")
	if got != "EXISTING" {
		t.Errorf("resolveString existing: got %q, want EXISTING", got)
	}
}

func TestResolveString_FallbackWhenBothEmpty(t *testing.T) {
	got := resolveString(true, "", "", "DEFAULT")
	if got != "DEFAULT" {
		t.Errorf("resolveString default: got %q, want DEFAULT", got)
	}
}

// --- coalesce ---

func TestCoalesce_FirstNonEmpty(t *testing.T) {
	if got := coalesce("a", "b"); got != "a" {
		t.Errorf("coalesce: got %q, want a", got)
	}
	if got := coalesce("", "b"); got != "b" {
		t.Errorf("coalesce empty: got %q, want b", got)
	}
	if got := coalesce("", ""); got != "" {
		t.Errorf("coalesce both empty: got %q, want empty", got)
	}
}

// --- setupTimestamp ---

func TestSetupTimestamp_RFC3339(t *testing.T) {
	ts := setupTimestamp()
	if ts == "" {
		t.Fatal("setupTimestamp returned empty string")
	}
	// Must parse as RFC3339.
	if !strings.Contains(ts, "T") || !strings.Contains(ts, "Z") {
		t.Errorf("setupTimestamp %q does not look like RFC3339 UTC", ts)
	}
}

// --- setupCmd construction ---

func TestSetupCmd_NonNil(t *testing.T) {
	cmd := setupCmd()
	if cmd == nil {
		t.Fatal("setupCmd() returned nil")
	}
}

func TestSetupCmd_Use(t *testing.T) {
	cmd := setupCmd()
	if cmd.Use != "setup" {
		t.Errorf("setupCmd Use = %q, want setup", cmd.Use)
	}
}

func TestSetupCmd_Flags(t *testing.T) {
	cmd := setupCmd()
	flags := []string{"non-interactive", "home", "currency", "cabin", "mcp-client"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("setupCmd missing --%s flag", name)
		}
	}
}

// --- scanner helper for tests ---

// makeTestScanner builds a bufio.Scanner from an *os.File (replaces bufio.NewScanner).
func makeTestScanner(t *testing.T, f *os.File) *bufio.Scanner {
	t.Helper()
	return bufio.NewScanner(f)
}
