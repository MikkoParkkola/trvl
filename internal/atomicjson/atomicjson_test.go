package atomicjson

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type sample struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func TestWriteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.json")
	in := sample{Name: "a", Value: 7}
	if err := Write(path, in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var out sample
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round-trip mismatch: %+v != %+v", out, in)
	}
}

func TestWritePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits not meaningful on windows")
	}
	path := filepath.Join(t.TempDir(), "secret.json")
	if err := Write(path, sample{Name: "s"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file perm = %o, want 600", perm)
	}
}

func TestWriteOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.json")
	if err := Write(path, sample{Value: 1}); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, sample{Value: 2}); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	b, _ := os.ReadFile(path)
	var out sample
	_ = json.Unmarshal(b, &out)
	if out.Value != 2 {
		t.Fatalf("overwrite failed: got %d want 2", out.Value)
	}
}

func TestWriteCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deep", "x.json")
	if err := Write(path, sample{Name: "n"}); err != nil {
		t.Fatalf("Write into missing dir: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestWriteBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raw.json")
	if err := WriteBytes(path, []byte(`{"k":1}`)); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != `{"k":1}` {
		t.Fatalf("got %q", b)
	}
}

func TestWriteMarshalError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := Write(path, make(chan int)); err == nil {
		t.Fatal("expected marshal error for non-serializable value")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("no file should be written on marshal error")
	}
}

// TestWriteNoTempLeftover verifies that a successful write leaves exactly the
// target file behind — no ".tmp-*" artifacts from the O_EXCL temp file.
func TestWriteNoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	if err := Write(path, sample{Name: "a", Value: 1}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := Write(path, sample{Name: "b", Value: 2}); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "x.json" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("expected only x.json, got %v", names)
	}
}

// TestWriteBytesMkdirAllError exercises the dir-creation failure branch: when a
// parent path component is an existing regular file, MkdirAll cannot create the
// directory under it and WriteBytes must surface the error rather than panic or
// silently drop the write.
func TestWriteBytesMkdirAllError(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file, then try to write "under" it as if it were a dir.
	blocker := filepath.Join(dir, "afile")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(blocker, "nested", "out.json")
	if err := WriteBytes(target, []byte("{}")); err == nil {
		t.Fatal("expected error when a parent component is a regular file, got nil")
	}
}

// TestWriteBytesRenameError exercises the rename-failure branch on non-Windows:
// when the destination path is an existing directory, os.Rename of a file over
// it fails, and WriteBytes must return that error while cleaning up its temp
// file (no leftover .tmp-* in the directory).
func TestWriteBytesRenameError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("rename-over-directory semantics differ on Windows")
	}
	dir := t.TempDir()
	// Make the target path itself a directory so rename(file -> dir) fails.
	target := filepath.Join(dir, "iam-a-dir")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteBytes(target, []byte("{}")); err == nil {
		t.Fatal("expected error renaming over an existing directory, got nil")
	}
	// The temp file must have been cleaned up despite the failure.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "iam-a-dir" {
			t.Errorf("expected no temp leftover, found %q", e.Name())
		}
	}
}
