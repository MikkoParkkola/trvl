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
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/selfupdate"
)

func main() {
	const (
		dir          = "/tmp/trvl-e2e"
		tarballName  = "trvl_1.2.0_darwin_arm64.tar.gz"
		tarballPath  = dir + "/tarball.tar.gz"
		sigPath      = dir + "/tarball.tar.gz.mldsa65.sig"
		checksumPath = dir + "/checksums.txt"
	)

	// 1. SHA-256 verify against checksums.txt.
	got, err := sha256OfFile(tarballPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "FAIL: sha256 read: %v\n", err)
		os.Exit(1)
	}
	checksumsBytes, err := os.ReadFile(checksumPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "FAIL: read checksums: %v\n", err)
		os.Exit(1)
	}
	expected := ""
	for _, line := range strings.Split(string(checksumsBytes), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[len(fields)-1] == tarballName {
			expected = strings.ToLower(fields[0])
			break
		}
	}
	if expected == "" {
		_, _ = fmt.Fprintf(os.Stderr, "FAIL: %s not found in checksums.txt\n", tarballName)
		os.Exit(1)
	}
	if !strings.EqualFold(got, expected) {
		_, _ = fmt.Fprintf(os.Stderr, "FAIL: sha256 mismatch: got %s, want %s\n", got, expected)
		os.Exit(1)
	}
	fmt.Printf("OK   sha256: %s matches checksums.txt entry for %s\n", got, tarballName)

	// 2. ML-DSA-65 verify against embedded trust anchor.
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "FAIL: read sig: %v\n", err)
		os.Exit(1)
	}
	tarball, err := os.Open(tarballPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "FAIL: open tarball: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = tarball.Close() }()
	if err := selfupdate.VerifyMLDSA65(tarball, sig); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "FAIL: ml-dsa: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK   ml-dsa-65: signature verified against trust anchor %s\n",
		selfupdate.MLDSA65PubkeyFingerprint())

	fmt.Println("\nE2E VERIFY PASSED — production v1.2.0 artifacts pass the same chain trvl self-update would run.")
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
