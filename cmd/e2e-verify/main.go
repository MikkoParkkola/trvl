// e2e-verify is a one-off harness that proves the production trust
// anchor embedded in the running trvl binary accepts signatures
// produced by the real release pipeline.
//
// It reads:
//
//	/tmp/trvl-e2e/tarball.tar.gz
//	/tmp/trvl-e2e/tarball.tar.gz.mldsa65.sig
//	/tmp/trvl-e2e/checksums.txt
//
// Computes SHA-256 of the tarball, asserts it appears in checksums.txt
// (production goreleaser format), then runs VerifyMLDSA65 with the
// embedded trust anchor.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/selfupdate"
)

type verifyFunc func(io.Reader, []byte) error

type verifyConfig struct {
	TarballName  string
	TarballPath  string
	SigPath      string
	ChecksumPath string
}

func main() {
	const dir = "/tmp/trvl-e2e"
	cfg := verifyConfig{
		TarballName:  "trvl_1.2.0_darwin_arm64.tar.gz",
		TarballPath:  dir + "/tarball.tar.gz",
		SigPath:      dir + "/tarball.tar.gz.mldsa65.sig",
		ChecksumPath: dir + "/checksums.txt",
	}
	if err := run(cfg, os.Stdout, os.Stderr, selfupdate.VerifyMLDSA65); err != nil {
		os.Exit(1)
	}
}

func run(cfg verifyConfig, stdout, stderr io.Writer, verify verifyFunc) error {
	// 1. SHA-256 verify against checksums.txt.
	got, err := sha256OfFile(cfg.TarballPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "FAIL: sha256 read: %v\n", err)
		return err
	}
	checksumsBytes, err := os.ReadFile(cfg.ChecksumPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "FAIL: read checksums: %v\n", err)
		return err
	}
	expected := ""
	for _, line := range strings.Split(string(checksumsBytes), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[len(fields)-1] == cfg.TarballName {
			expected = strings.ToLower(fields[0])
			break
		}
	}
	if expected == "" {
		err := fmt.Errorf("%s not found in checksums.txt", cfg.TarballName)
		_, _ = fmt.Fprintf(stderr, "FAIL: %v\n", err)
		return err
	}
	if !strings.EqualFold(got, expected) {
		err := fmt.Errorf("sha256 mismatch: got %s, want %s", got, expected)
		_, _ = fmt.Fprintf(stderr, "FAIL: %v\n", err)
		return err
	}
	_, _ = fmt.Fprintf(stdout, "OK   sha256: %s matches checksums.txt entry for %s\n", got, cfg.TarballName)

	// 2. ML-DSA-65 verify against embedded trust anchor.
	sig, err := os.ReadFile(cfg.SigPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "FAIL: read sig: %v\n", err)
		return err
	}
	tarball, err := os.Open(cfg.TarballPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "FAIL: open tarball: %v\n", err)
		return err
	}
	defer func() { _ = tarball.Close() }()
	if verify == nil {
		verify = func(io.Reader, []byte) error { return errors.New("missing verifier") }
	}
	if err := verify(tarball, sig); err != nil {
		_, _ = fmt.Fprintf(stderr, "FAIL: ml-dsa: %v\n", err)
		return err
	}
	_, _ = fmt.Fprintf(stdout, "OK   ml-dsa-65: signature verified against trust anchor %s\n",
		selfupdate.MLDSA65PubkeyFingerprint())

	_, _ = fmt.Fprintln(stdout, "\nE2E VERIFY PASSED — production v1.2.0 artifacts pass the same chain trvl self-update would run.")
	return nil
}

func sha256OfFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
