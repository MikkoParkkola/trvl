package upgrade

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TRVL.HARDEN.3 -- a version stamp must be validated before it reaches a path.
//
// backupPreferences builds "preferences.json.bak.<version>" by concatenation.
// The version does NOT come from the compiled-in Version constant: it comes
// from ReadStamp, which returns the trimmed contents of a file on disk. A
// corrupted, hand-edited, or externally-written stamp therefore chooses part of
// a filename.
//
// The escape asserted here is the one that matters: a stamp containing "../"
// walks out of the preferences directory entirely, so the backup lands
// somewhere nobody intended and may overwrite an unrelated file.
func TestBackupPreferencesRefusesUnsafeVersionStamp(t *testing.T) {
	for _, stamp := range []string{
		"../../escaped",
		"../sibling",
		"..",
		"a/b",
		`a\b`,
		"",
		"has space",
		"semi;colon",
	} {
		t.Run(stamp, func(t *testing.T) {
			dir := t.TempDir()
			// A preferences file must exist, or backupPreferences returns early
			// for an unrelated reason and the test would pass without
			// exercising the guard at all.
			if err := os.WriteFile(prefsPathIn(dir), []byte(`{"currency":"EUR"}`), 0o600); err != nil {
				t.Fatalf("seed prefs: %v", err)
			}

			backupPreferences(dir, stamp)

			// Two properties, and the second was missing until 2026-08-06.
			//
			// (a) The stamp must never reach a filename. Walking the PARENT of dir
			// catches an escape that a check confined to dir would miss entirely.
			//
			// (b) The backup must still happen. The original version returned
			// silently on a bad stamp, so the migration rewrote the user's
			// preferences with no backup and no message -- safe against a
			// traversal filename, unsafe against the thing a backup is for. This
			// test asserted (a) by requiring that NOTHING was written, which also
			// locked in (b)'s absence.
			//
			// Asserted on the backup's SUFFIX rather than by searching filenames
			// for the stamp text. The first attempt did the latter and failed on
			// stamp "a/b": filepath.Base gives "b", and "preferences.json.bak."
			// contains a b. A one-character needle matches everything.
			parent := filepath.Dir(dir)
			var strays []string
			var suffixes []string
			_ = filepath.Walk(parent, func(path string, info os.FileInfo, err error) error {
				if err != nil || info == nil || info.IsDir() {
					return nil //nolint:nilerr // an unreadable sibling is not this test's business
				}
				if path == prefsPathIn(dir) {
					return nil
				}
				// Anything created outside dir is an escape, full stop.
				if filepath.Dir(path) != dir {
					if filepath.Dir(path) == parent {
						strays = append(strays, path)
					}
					return nil
				}
				base := filepath.Base(path)
				suffix, isBackup := strings.CutPrefix(base, "preferences.json.bak.")
				if !isBackup {
					strays = append(strays, path)
					return nil
				}
				suffixes = append(suffixes, suffix)
				return nil
			})
			if len(strays) > 0 {
				t.Errorf("stamp %q produced %v -- an unvalidated version stamp reached a filename",
					stamp, strays)
			}
			if len(suffixes) != 1 {
				t.Errorf("stamp %q produced %d backups, want exactly 1: refusing the stamp as a "+
					"filename must not also cancel the backup, or a user with a corrupted version "+
					"stamp has their preferences migrated with no copy kept and nothing said",
					stamp, len(suffixes))
			}
			for _, suffix := range suffixes {
				if !strings.HasPrefix(suffix, "unknown-") {
					t.Errorf("fallback backup suffix %q must be generated here, never derived from "+
						"the rejected stamp %q", suffix, stamp)
				}
				if strings.ContainsAny(suffix, `/\`) || strings.Contains(suffix, "..") {
					t.Errorf("fallback backup suffix %q contains path syntax", suffix)
				}
			}
		})
	}
}

// The control that makes the assertion above mean something: a well-formed
// stamp must still produce its backup. Without this, deleting backupPreferences
// entirely would pass the refusal test.
func TestBackupPreferencesStillBacksUpAValidStamp(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(prefsPathIn(dir), []byte(`{"currency":"EUR"}`), 0o600); err != nil {
		t.Fatalf("seed prefs: %v", err)
	}

	backupPreferences(dir, "1.20.3")

	want := prefsPathIn(dir) + ".bak.1.20.3"
	if _, err := os.Stat(want); err != nil {
		t.Errorf("a valid version stamp produced no backup at %s: %v -- the guard must refuse "+
			"unsafe stamps, not disable the feature", want, err)
	}
}

// Shapes real versions take must all be accepted, so the guard cannot quietly
// disable backups for a release naming scheme this repo already uses.
func TestSafeVersionStampAcceptsRealVersions(t *testing.T) {
	for _, v := range []string{"1.21.0", "v1.21.0", "1.21.0-rc.1", "1.21.0+build.5", "0.0.1", "dev_snapshot"} {
		if !safeVersionStamp(v) {
			t.Errorf("safeVersionStamp(%q) = false, want true", v)
		}
	}
}
