package upgrade

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// trvl#532 -- the version stamp is written 0600.
//
// It lives under ~/.trvl and belongs to one user. There was never a reason for
// every account on the machine to read it, and the file-mode is asserted here
// rather than assumed because a mode is invisible in review: the diff shows
// 0o600 and the reader has no way to know whether that call site is the one
// that actually creates the file.
func TestWriteStampIsNotWorldReadable(t *testing.T) {
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
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "version")

	if err := WriteStamp(path, "1.21.0"); err != nil {
		t.Fatalf("WriteStamp: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("version stamp mode = %04o, want no group or world bits (0600). Anything under "+
			"~/.trvl belongs to one user", mode)
	}
}
