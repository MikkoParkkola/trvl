package main

import (
	"bytes"
	cryptorand "crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
)

func TestRunSignsArtifactAndDoesNotLeakPrivkey(t *testing.T) {
	privHex, pub := generatePrivateKeyHex(t)
	dir := t.TempDir()
	artifact := filepath.Join(dir, "trvl.tar.gz")
	if err := os.WriteFile(artifact, []byte("archive bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	if err := run([]string{"--in", artifact}, &stderr, func(name string) string {
		if name == envPrivkey {
			return privHex
		}
		return ""
	}); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	sigPath := artifact + sigSuffix
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != mldsa65.SignatureSize {
		t.Fatalf("sig bytes = %d, want %d", len(sig), mldsa65.SignatureSize)
	}
	digest, err := sha256File(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if !mldsa65.Verify(pub, digest, nil, sig) {
		t.Fatal("signature does not verify with generated public key")
	}
	if strings.Contains(stderr.String(), privHex[:64]) {
		t.Fatal("stderr leaked private key material")
	}
	if !strings.Contains(stderr.String(), "sign-mldsa:") {
		t.Fatalf("stderr missing success marker:\n%s", stderr.String())
	}
}

func TestRunRejectsMissingPrivateKey(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "trvl.tar.gz")
	if err := os.WriteFile(artifact, []byte("archive bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	err := run([]string{"--in", artifact}, &stderr, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), envPrivkey) {
		t.Fatalf("run() error = %v, want missing env key", err)
	}
	if _, statErr := os.Stat(artifact + sigSuffix); !os.IsNotExist(statErr) {
		t.Fatalf("signature file exists after failed sign: %v", statErr)
	}
}

func TestRunRefusesToOverwriteSignature(t *testing.T) {
	privHex, _ := generatePrivateKeyHex(t)
	dir := t.TempDir()
	artifact := filepath.Join(dir, "trvl.tar.gz")
	out := filepath.Join(dir, "trvl.sig")
	if err := os.WriteFile(artifact, []byte("archive bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	err := run([]string{"--in", artifact, "--out", out}, &stderr, func(name string) string {
		if name == envPrivkey {
			return privHex
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "write sig") {
		t.Fatalf("run() error = %v, want overwrite refusal", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing" {
		t.Fatalf("sig content = %q, want existing", got)
	}
}

func generatePrivateKeyHex(t *testing.T) (string, *mldsa65.PublicKey) {
	t.Helper()
	pk, sk, err := mldsa65.GenerateKey(cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	skb, err := sk.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(skb), pk
}
