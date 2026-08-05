package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// trvl#532 (G110) -- extraction is bounded, and refuses rather than truncating.
//
// This is defence in depth: extraction already runs AFTER verifySHA256 against
// the signed checksums file, so reaching it with a hostile archive means an
// attacker who can forge that signature -- and they would ship a malicious
// binary rather than fill a disk. What the cap buys is removing the "what if
// verification is ever moved or skipped" branch from the reasoning.
//
// A guard that is never exercised is a guard nobody knows works, which is why
// the limit is injectable and asserted here rather than trusted by reading it.
func writeTarGz(t *testing.T, name string, size int) string {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o755, Size: int64(size), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(bytes.Repeat([]byte("x"), size)); err != nil {
		t.Fatalf("tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	path := filepath.Join(t.TempDir(), "release.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return path
}

func TestExtractRefusesAnOversizedBinary(t *testing.T) {
	prev := maxExtractedBinaryBytes
	maxExtractedBinaryBytes = 1024
	defer func() { maxExtractedBinaryBytes = prev }()

	src := writeTarGz(t, "trvl", 4096) // four times the injected cap
	dest := filepath.Join(t.TempDir(), "trvl")

	err := extractBinaryFromTarGz(src, "trvl", dest)
	if err == nil {
		t.Fatal("an archive four times the limit extracted without error; the cap is not " +
			"enforced, and a tarball claiming an implausible size would be written to disk in full")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error does not say the limit was exceeded, so an operator cannot tell this "+
			"from an ordinary extraction failure: %v", err)
	}

	// Refusing, not truncating. A silently truncated binary is worse than either
	// outcome: it would be installed, and it would not run.
	if fi, statErr := os.Stat(dest); statErr == nil && fi.Size() > maxExtractedBinaryBytes {
		t.Errorf("a truncated %d-byte file was left at the destination; refusal must not leave a "+
			"partial binary behind", fi.Size())
	}
}

// The control: an ordinary release must still extract. Without this, a cap set
// to zero would pass the refusal test while breaking every update.
func TestExtractAcceptsANormalBinary(t *testing.T) {
	prev := maxExtractedBinaryBytes
	maxExtractedBinaryBytes = 1 << 20
	defer func() { maxExtractedBinaryBytes = prev }()

	src := writeTarGz(t, "trvl", 4096)
	dest := filepath.Join(t.TempDir(), "trvl")

	if err := extractBinaryFromTarGz(src, "trvl", dest); err != nil {
		t.Fatalf("a normally-sized binary failed to extract: %v -- the cap must refuse the absurd, "+
			"not the ordinary", err)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat extracted binary: %v", err)
	}
	if fi.Size() != 4096 {
		t.Errorf("extracted %d bytes, want 4096", fi.Size())
	}
}
