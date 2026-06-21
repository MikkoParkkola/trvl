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
