package upgrade

import (
	"os"
	"path/filepath"
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
