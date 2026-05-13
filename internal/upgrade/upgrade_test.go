package upgrade

import (
	"os"
	"path/filepath"
	"testing"
)

// --- CompareSemver ---

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.5.0", "0.6.0", -1},
		{"0.6.0", "0.5.0", 1},
		{"0.6.0", "0.6.0", 0},
		{"1.0.0", "0.99.99", 1},
		{"0.6.0", "0.6.1", -1},
		{"v0.6.0", "0.6.0", 0},
		{"v1.2.3", "v1.2.3", 0},
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0", "1.0.0-alpha", 1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.2", -1},
		{"1.0.0-1", "1.0.0-2", -1},
		// Non-semver fallback.
		{"dev", "dev", 0},
		{"abc", "def", -1},
	}

	for _, tt := range tests {
		got := CompareSemver(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("CompareSemver(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// --- ReadStamp / WriteStamp ---

func TestStampRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := stampPathIn(dir)

	// Missing file returns empty string.
	v, err := ReadStamp(path)
	if err != nil {
		t.Fatalf("ReadStamp missing: %v", err)
	}
	if v != "" {
		t.Errorf("ReadStamp missing: got %q, want empty", v)
	}

	// Write and read back.
	if err := WriteStamp(path, "0.6.0"); err != nil {
		t.Fatalf("WriteStamp: %v", err)
	}

	v, err = ReadStamp(path)
	if err != nil {
		t.Fatalf("ReadStamp: %v", err)
	}
	if v != "0.6.0" {
		t.Errorf("ReadStamp: got %q, want 0.6.0", v)
	}
}

// --- CheckUpgrade ---

func TestCheckUpgrade_FreshInstall(t *testing.T) {
	dir := t.TempDir()

	r, err := CheckUpgrade("0.6.0", dir)
	if err != nil {
		t.Fatalf("CheckUpgrade fresh: %v", err)
	}
	if !r.FreshInstall {
		t.Error("expected FreshInstall=true")
	}

	// Stamp should now exist.
	v, _ := ReadStamp(stampPathIn(dir))
	if v != "0.6.0" {
		t.Errorf("stamp after fresh install: got %q, want 0.6.0", v)
	}
}

func TestCheckUpgrade_SameVersion(t *testing.T) {
	dir := t.TempDir()
	if err := WriteStamp(stampPathIn(dir), "0.6.0"); err != nil {
		t.Fatalf("WriteStamp: %v", err)
	}

	r, err := CheckUpgrade("0.6.0", dir)
	if err != nil {
		t.Fatalf("CheckUpgrade same: %v", err)
	}
	if r.FreshInstall || r.Downgrade || r.MigrationsApplied != 0 {
		t.Errorf("same version should be no-op, got %+v", r)
	}
}

func TestCheckUpgrade_Upgrade(t *testing.T) {
	dir := t.TempDir()
	if err := WriteStamp(stampPathIn(dir), "0.5.0"); err != nil {
		t.Fatalf("WriteStamp: %v", err)
	}

	r, err := CheckUpgrade("0.6.0", dir)
	if err != nil {
		t.Fatalf("CheckUpgrade upgrade: %v", err)
	}
	if r.OldVersion != "0.5.0" {
		t.Errorf("OldVersion: got %q, want 0.5.0", r.OldVersion)
	}
	if r.NewVersion != "0.6.0" {
		t.Errorf("NewVersion: got %q, want 0.6.0", r.NewVersion)
	}

	// Stamp should be updated.
	v, _ := ReadStamp(stampPathIn(dir))
	if v != "0.6.0" {
		t.Errorf("stamp after upgrade: got %q, want 0.6.0", v)
	}
}

func TestCheckUpgrade_Downgrade(t *testing.T) {
	dir := t.TempDir()
	if err := WriteStamp(stampPathIn(dir), "0.7.0"); err != nil {
		t.Fatalf("WriteStamp: %v", err)
	}

	r, err := CheckUpgrade("0.6.0", dir)
	if err != nil {
		t.Fatalf("CheckUpgrade downgrade: %v", err)
	}
	if !r.Downgrade {
		t.Error("expected Downgrade=true")
	}

	// Stamp should NOT be modified.
	v, _ := ReadStamp(stampPathIn(dir))
	if v != "0.7.0" {
		t.Errorf("stamp after downgrade: got %q, want 0.7.0 (unchanged)", v)
	}
}

func TestCheckUpgrade_DevVersion(t *testing.T) {
	dir := t.TempDir()

	r, err := CheckUpgrade("dev", dir)
	if err != nil {
		t.Fatalf("CheckUpgrade dev: %v", err)
	}
	if r.FreshInstall || r.Downgrade {
		t.Error("dev version should be no-op")
	}
}

// --- RunUpgrade with dry-run ---

func TestRunUpgrade_DryRun(t *testing.T) {
	dir := t.TempDir()
	if err := WriteStamp(stampPathIn(dir), "0.5.0"); err != nil {
		t.Fatalf("WriteStamp: %v", err)
	}

	r, err := RunUpgrade("0.6.0", dir, true)
	if err != nil {
		t.Fatalf("RunUpgrade dry-run: %v", err)
	}
	if r.OldVersion != "0.5.0" || r.NewVersion != "0.6.0" {
		t.Errorf("unexpected result: %+v", r)
	}

	// Stamp should NOT be modified in dry-run.
	v, _ := ReadStamp(stampPathIn(dir))
	if v != "0.5.0" {
		t.Errorf("stamp after dry-run: got %q, want 0.5.0 (unchanged)", v)
	}
}

// --- Backup preferences ---

func TestBackupPreferences(t *testing.T) {
	dir := t.TempDir()
	prefsPath := prefsPathIn(dir)

	// Write a dummy prefs file.
	if err := os.WriteFile(prefsPath, []byte(`{"locale":"en"}`), 0o600); err != nil {
		t.Fatalf("write prefs: %v", err)
	}

	backupPreferences(dir, "0.5.0")

	bakPath := prefsPath + ".bak.0.5.0"
	data, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(data) != `{"locale":"en"}` {
		t.Errorf("backup content: got %q", string(data))
	}
}

func TestBackupPreferences_NoFile(t *testing.T) {
	dir := t.TempDir()
	// Should not panic when no prefs file exists.
	backupPreferences(dir, "0.5.0")

	bakPath := filepath.Join(dir, "preferences.json.bak.0.5.0")
	if _, err := os.Stat(bakPath); !os.IsNotExist(err) {
		t.Error("backup should not be created when no prefs file exists")
	}
}

// --- Migration registry ---

func TestMigrationRegistry(t *testing.T) {
	// Save and restore global state.
	old := migrations
	defer func() { migrations = old }()
	resetMigrations()

	applied := false
	RegisterMigration(Migration{
		FromVersion: "0.5.0",
		Description: "test migration",
		Apply:       func() error { applied = true; return nil },
	})

	dir := t.TempDir()
	if err := WriteStamp(stampPathIn(dir), "0.4.0"); err != nil {
		t.Fatalf("WriteStamp: %v", err)
	}

	r, err := CheckUpgrade("0.6.0", dir)
	if err != nil {
		t.Fatalf("CheckUpgrade with migration: %v", err)
	}
	if r.MigrationsApplied != 1 {
		t.Errorf("MigrationsApplied: got %d, want 1", r.MigrationsApplied)
	}
	if !applied {
		t.Error("migration Apply func was not called")
	}
}

func TestMigrationRegistry_NotApplicable(t *testing.T) {
	old := migrations
	defer func() { migrations = old }()
	resetMigrations()

	RegisterMigration(Migration{
		FromVersion: "0.7.0",
		Description: "future migration",
		Apply:       func() error { t.Fatal("should not be called"); return nil },
	})

	dir := t.TempDir()
	if err := WriteStamp(stampPathIn(dir), "0.5.0"); err != nil {
		t.Fatalf("WriteStamp: %v", err)
	}

	r, err := CheckUpgrade("0.6.0", dir)
	if err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}
	if r.MigrationsApplied != 0 {
		t.Errorf("MigrationsApplied: got %d, want 0", r.MigrationsApplied)
	}
}

func TestMigrationRegistry_DryRunDoesNotApply(t *testing.T) {
	old := migrations
	defer func() { migrations = old }()
	resetMigrations()

	RegisterMigration(Migration{
		FromVersion: "0.5.0",
		Description: "test migration",
		Apply:       func() error { t.Fatal("should not be called in dry-run"); return nil },
	})

	dir := t.TempDir()
	_ = WriteStamp(stampPathIn(dir), "0.4.0")

	r, err := RunUpgrade("0.6.0", dir, true)
	if err != nil {
		t.Fatalf("RunUpgrade dry-run: %v", err)
	}
	if r.MigrationsApplied != 1 {
		t.Errorf("MigrationsApplied: got %d, want 1 (counted but not applied)", r.MigrationsApplied)
	}
}

// --- WhatsNew ---

func TestWhatsNew(t *testing.T) {
	tests := []struct {
		name string
		r    *Result
		want string
	}{
		{
			name: "fresh install — no output",
			r:    &Result{NewVersion: "0.6.0", FreshInstall: true},
			want: "",
		},
		{
			name: "downgrade",
			r:    &Result{OldVersion: "0.7.0", NewVersion: "0.6.0", Downgrade: true},
			want: "Warning: running older version 0.6.0 (stamp is 0.7.0). Stamp not modified.",
		},
		{
			name: "upgrade with whats-new entries",
			r:    &Result{OldVersion: "0.5.0", NewVersion: "0.6.0", MigrationsApplied: 0},
			want: "What's new since v0.5.0:\n" +
				"  - New `upgrade` command with version stamp and migration framework\n" +
				"  - Agent-first install: tell your AI to read the README\n" +
				"  - 10 MCP client auto-install targets (gemini, amazon-q, lm-studio added)\n" +
				"trvl upgraded v0.5.0 → v0.6.0",
		},
		{
			name: "upgrade without whats-new entries",
			r:    &Result{OldVersion: "0.6.0", NewVersion: "0.7.0"},
			want: "trvl upgraded v0.6.0 → v0.7.0",
		},
		{
			name: "same version",
			r:    &Result{OldVersion: "0.6.0", NewVersion: "0.6.0"},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WhatsNew(tt.r)
			if got != tt.want {
				t.Errorf("WhatsNew:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

// --- whatsNewSince ---

func TestWhatsNewSince(t *testing.T) {
	// 0.5.0 → 0.6.0 should include all 0.6.0 entries.
	items := whatsNewSince("0.5.0", "0.6.0")
	if len(items) != 3 {
		t.Errorf("whatsNewSince(0.5.0, 0.6.0): got %d items, want 3", len(items))
	}

	// 0.6.0 → 0.7.0 should include nothing (no 0.7.0 entries).
	items = whatsNewSince("0.6.0", "0.7.0")
	if len(items) != 0 {
		t.Errorf("whatsNewSince(0.6.0, 0.7.0): got %d items, want 0", len(items))
	}

	// 0.4.0 → 0.6.0 should include 0.6.0 entries.
	items = whatsNewSince("0.4.0", "0.6.0")
	if len(items) != 3 {
		t.Errorf("whatsNewSince(0.4.0, 0.6.0): got %d items, want 3", len(items))
	}
}
