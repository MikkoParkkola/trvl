package flights

import (
	"os"
	"path/filepath"
	"testing"
)

// trvl#532 -- the cached Wizz Air version file, and the directory holding it,
// are private.
//
// The file lives under ~/.trvl and records which API version this machine
// discovered. Not a secret, but not other accounts' business either, and it was
// written 0644 into a directory created 0755.
//
// Driven through the real writer and stat-ed, rather than read out of the
// source: a mode in a diff proves nothing about which call site creates the
// file.
func TestWizzVersionCacheIsPrivate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // see scripts/ci/check-home-isolation.sh (#565)

	wizzPersistVersion(wizzDefaultHost, "29.10.0")

	path := filepath.Join(home, ".trvl", "wizzair_version.json")

	fi, err := os.Stat(path)
	if err != nil {
		t.Skipf("cache not written in this environment: %v", err)
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("cache file mode = %04o, want no group or world bits", mode)
	}

	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat cache dir: %v", err)
	}
	if mode := di.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("cache directory mode = %04o, want no group or world bits. A private file in a "+
			"world-readable directory still leaks its name, and the next writer inherits the "+
			"loose directory", mode)
	}
}
