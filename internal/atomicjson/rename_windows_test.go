//go:build windows

package atomicjson

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// os.Rename replaces an existing file on Windows, so an ordinary write over an
// existing store succeeds without any delete-first fallback.
func TestWriteReplacesAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	if err := os.WriteFile(path, []byte(`{"v":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteBytes(path, []byte(`{"v":2}`)); err != nil {
		t.Fatalf("replacing an existing file: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != `{"v":2}` {
		t.Fatalf("after replace = %q, %v; want the new contents", got, err)
	}
}

// A failed publishing rename must leave the existing file in place. The temp
// file is held open without FILE_SHARE_DELETE, so MoveFileEx fails with a
// sharing violation while the destination itself stays deletable: a
// remove-then-retry fallback deletes the stored state and then fails anyway.
func TestFailedRenameKeepsTheExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	if err := os.WriteFile(path, []byte(`{"kept":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var held syscall.Handle
	testHookBeforeRename = func(tmp string) {
		name, err := syscall.UTF16PtrFromString(tmp)
		if err != nil {
			t.Fatalf("temp name: %v", err)
		}
		h, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ,
			nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatalf("hold temp file: %v", err)
		}
		held = h
	}
	t.Cleanup(func() {
		testHookBeforeRename = nil
		if held != 0 {
			_ = syscall.CloseHandle(held)
		}
	})

	if err := WriteBytes(path, []byte(`{"kept":false}`)); err == nil {
		t.Fatal("publishing a temp file that is held open must fail")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != `{"kept":true}` {
		t.Fatalf("existing file after a failed rename = %q, %v; want it kept", got, err)
	}
}
