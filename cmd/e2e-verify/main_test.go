package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunVerifiesChecksumAndSignature(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "tarball.tar.gz")
	sigPath := filepath.Join(dir, "tarball.tar.gz.mldsa65.sig")
	checksums := filepath.Join(dir, "checksums.txt")
	tarballName := "trvl_1.2.0_darwin_arm64.tar.gz"
	body := []byte("release archive bytes")
	sig := []byte("signature bytes")

	if err := os.WriteFile(tarball, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sigPath, sig, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if err := os.WriteFile(checksums, []byte(fmt.Sprintf("%x  %s\n", sum, tarballName)), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := run(verifyConfig{
		TarballName:  tarballName,
		TarballPath:  tarball,
		SigPath:      sigPath,
		ChecksumPath: checksums,
	}, &stdout, &stderr, func(r io.Reader, gotSig []byte) error {
		gotBody, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		if !bytes.Equal(gotBody, body) {
			t.Fatalf("verifier read body %q, want %q", gotBody, body)
		}
		if !bytes.Equal(gotSig, sig) {
			t.Fatalf("verifier got sig %q, want %q", gotSig, sig)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "OK   sha256:") || !strings.Contains(out, "E2E VERIFY PASSED") {
		t.Fatalf("stdout missing success markers:\n%s", out)
	}
}

func TestRunRejectsChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "tarball.tar.gz")
	sigPath := filepath.Join(dir, "tarball.tar.gz.mldsa65.sig")
	checksums := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(tarball, []byte("release archive bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sigPath, []byte("signature"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checksums, []byte("deadbeef  trvl_1.2.0_darwin_arm64.tar.gz\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := run(verifyConfig{
		TarballName:  "trvl_1.2.0_darwin_arm64.tar.gz",
		TarballPath:  tarball,
		SigPath:      sigPath,
		ChecksumPath: checksums,
	}, &stdout, &stderr, func(io.Reader, []byte) error {
		t.Fatal("verifier should not run after checksum mismatch")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("run() error = %v, want checksum mismatch", err)
	}
	if !strings.Contains(stderr.String(), "FAIL: sha256 mismatch") {
		t.Fatalf("stderr missing mismatch:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestSha256OfFileMissingFile(t *testing.T) {
	if _, err := sha256OfFile(filepath.Join(t.TempDir(), "missing.tar.gz")); err == nil {
		t.Fatal("sha256OfFile() error = nil, want missing file error")
	}
}
