package main

import (
	"bytes"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
)

func TestRunWritesKeypairWithSafeModes(t *testing.T) {
	dir := t.TempDir()
	priv := filepath.Join(dir, "mldsa.priv.hex")
	pub := filepath.Join(dir, "mldsa.pub.hex")
	var stderr bytes.Buffer

	if err := run([]string{"--priv", priv, "--pub", pub}, &stderr, cryptorand.Reader); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	privHex := readHexFile(t, priv)
	pubHex := readHexFile(t, pub)
	if len(privHex) != mldsa65.PrivateKeySize {
		t.Fatalf("priv key bytes = %d, want %d", len(privHex), mldsa65.PrivateKeySize)
	}
	if len(pubHex) != mldsa65.PublicKeySize {
		t.Fatalf("pub key bytes = %d, want %d", len(pubHex), mldsa65.PublicKeySize)
	}
	assertMode(t, priv, 0o600)
	assertMode(t, pub, 0o644)

	out := stderr.String()
	if !strings.Contains(out, "roundtrip: sign + verify OK") {
		t.Fatalf("stderr missing roundtrip marker:\n%s", out)
	}
	if strings.Contains(out, hex.EncodeToString(privHex[:32])) {
		t.Fatal("stderr leaked private key material")
	}
}

func TestRunRejectsMissingPaths(t *testing.T) {
	var stderr bytes.Buffer
	err := run(nil, &stderr, cryptorand.Reader)
	var usage usageError
	if !errors.As(err, &usage) {
		t.Fatalf("run() error = %T %[1]v, want usageError", err)
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	if !strings.Contains(stderr.String(), "usage: keygen-mldsa") {
		t.Fatalf("stderr missing usage:\n%s", stderr.String())
	}
}

func TestWriteFileExclusiveRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileExclusive(path, []byte("new"), 0o600); err == nil {
		t.Fatal("writeFileExclusive() error = nil, want overwrite refusal")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing" {
		t.Fatalf("file content = %q, want existing", got)
	}
}

func readHexFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return decoded
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX file modes through os.FileMode")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
