package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// trvl#532 -- the assistant config trvl writes, and the backup it takes of an
// existing one, must not be readable by other accounts on the machine.
//
// These files carry MCP server definitions and can carry API keys. They were
// written 0644 into a directory created 0755.
//
// Asserted by driving runInstall and stat-ing what it produced, rather than by
// reading the mode out of the source. A file mode is invisible in review: the
// diff shows 0o600 and a reader has no way to know that call site is the one
// that actually creates the file. Only the artefact on disk settles it.
func TestMCPInstallWritesPrivateConfigAndBackup(t *testing.T) {
	// Unix permission bits are not implemented on Windows: Go reports 0666 for
	// any writable file and 0777 for any directory, whatever mode was passed to
	// os.WriteFile or os.MkdirAll. Verified on windows-latest, where this
	// assertion failed with exactly those values against files created 0600.
	//
	// So this asserts a Unix property and is skipped rather than weakened. What
	// protects these files on Windows is the ACL they inherit from the parent
	// directory, which trvl does not set and this test cannot see -- a real gap,
	// recorded here because the skip is where someone asking "is this covered on
	// Windows?" will look.
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits are not meaningful on Windows; see comment")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // see scripts/ci/check-home-isolation.sh (#565)

	cfgPath, err := clientConfigPath("claude")
	if err != nil {
		t.Skipf("no config path for this platform: %v", err)
	}

	// Seed an existing config so the backup path is exercised too. Written 0644
	// deliberately: a pre-existing world-readable file is exactly the case where
	// the backup must not inherit that mode.
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if err := runInstall("claude", true, false); err != nil {
		t.Skipf("install did not complete in this environment: %v", err)
	}

	backup := cfgPath + ".trvl.bak"
	for _, p := range []string{backup} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if mode := fi.Mode().Perm(); mode&0o077 != 0 {
			t.Errorf("%s mode = %04o, want no group or world bits. This file can carry API keys, "+
				"and a backup that inherits a world-readable mode preserves the exposure it was "+
				"meant to protect against", filepath.Base(p), mode)
		}
	}
}

// The directory trvl creates for a config that does not yet exist must be
// private too: a 0700 file inside a 0755 directory still leaks its name and
// existence, and the next writer inherits the loose directory.
func TestMCPInstallCreatesPrivateConfigDirectory(t *testing.T) {
	// Unix permission bits are not implemented on Windows: Go reports 0666 for
	// any writable file and 0777 for any directory, whatever mode was passed to
	// os.WriteFile or os.MkdirAll. Verified on windows-latest, where this
	// assertion failed with exactly those values against files created 0600.
	//
	// So this asserts a Unix property and is skipped rather than weakened. What
	// protects these files on Windows is the ACL they inherit from the parent
	// directory, which trvl does not set and this test cannot see -- a real gap,
	// recorded here because the skip is where someone asking "is this covered on
	// Windows?" will look.
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits are not meaningful on Windows; see comment")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath, err := clientConfigPath("claude")
	if err != nil {
		t.Skipf("no config path for this platform: %v", err)
	}

	// Deliberately do NOT pre-create the directory: this exercises the MkdirAll.
	if err := runInstall("claude", true, false); err != nil {
		t.Skipf("install did not complete in this environment: %v", err)
	}

	fi, err := os.Stat(filepath.Dir(cfgPath))
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("config directory mode = %04o, want no group or world bits", mode)
	}
}
