// Command keygen-mldsa generates the trvl release-signing root-of-trust
// ML-DSA-65 (NIST FIPS 204) keypair. One-time use per key version. The
// resulting public key becomes the trust anchor embedded in every trvl
// binary; the private key signs every release archive and lives in a
// GitHub Actions secret + 1Password backup.
//
// Run:
//
//	go run ./cmd/keygen-mldsa --priv ~/.trvl/mldsa65-vN.privkey.hex \
//	                          --pub  ~/.trvl/mldsa65-vN.pubkey.hex
//
// Output files: privkey 0600, pubkey 0644. Private key NEVER touches
// stdout or argv. Roundtrip (marshal -> unmarshal -> sign -> verify) is
// performed before write so a corrupted keypair is detected here, not at
// release time.
//
// After successful keygen, push the privkey to the GitHub Secret and to
// 1Password, embed the pubkey in trvl, then shred the privkey file. See
// docs/security/release-signing.md for the full runbook.
package main

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
)

func main() {
	if err := run(os.Args[1:], os.Stderr, cryptorand.Reader); err != nil {
		if _, ok := err.(usageError); !ok {
			_, _ = fmt.Fprintf(os.Stderr, "keygen-mldsa: %v\n", err)
		}
		os.Exit(exitCode(err))
	}
}

type usageError struct {
	msg string
}

func (e usageError) Error() string { return e.msg }
func (e usageError) ExitCode() int { return 2 }

type exitCoder interface {
	ExitCode() int
}

func exitCode(err error) int {
	if e, ok := err.(exitCoder); ok {
		return e.ExitCode()
	}
	return 1
}

func run(args []string, stderr io.Writer, random io.Reader) error {
	fs := flag.NewFlagSet("keygen-mldsa", flag.ContinueOnError)
	fs.SetOutput(stderr)
	priv := fs.String("priv", "", "path to write privkey hex (0600)")
	pub := fs.String("pub", "", "path to write pubkey hex (0644)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *priv == "" || *pub == "" {
		_, _ = fmt.Fprintln(stderr, "usage: keygen-mldsa --priv <path> --pub <path>")
		return usageError{msg: "missing required --priv or --pub"}
	}
	if random == nil {
		random = cryptorand.Reader
	}

	pk, sk, err := mldsa65.GenerateKey(random)
	if err != nil {
		return fmt.Errorf("keygen: %w", err)
	}
	pkb, err := pk.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal pub: %w", err)
	}
	skb, err := sk.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal priv: %w", err)
	}

	// Roundtrip: ensure the serialized bytes re-parse and sign+verify
	// before we hand the privkey off to durable storage.
	var pk2 mldsa65.PublicKey
	if err := pk2.UnmarshalBinary(pkb); err != nil {
		return fmt.Errorf("pub roundtrip: %w", err)
	}
	var sk2 mldsa65.PrivateKey
	if err := sk2.UnmarshalBinary(skb); err != nil {
		return fmt.Errorf("priv roundtrip: %w", err)
	}
	canary := []byte("trvl-keygen-roundtrip-canary")
	sig := make([]byte, mldsa65.SignatureSize)
	if err := mldsa65.SignTo(&sk2, canary, nil, false, sig); err != nil {
		return fmt.Errorf("roundtrip sign: %w", err)
	}
	if !mldsa65.Verify(&pk2, canary, nil, sig) {
		return fmt.Errorf("roundtrip verify: failed")
	}

	if err := writeFileExclusive(*priv, []byte(hex.EncodeToString(skb)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write priv: %w", err)
	}
	if err := writeFileExclusive(*pub, []byte(hex.EncodeToString(pkb)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write pub: %w", err)
	}

	_, _ = fmt.Fprintf(stderr, "ML-DSA-65 keypair written.\n")
	_, _ = fmt.Fprintf(stderr, "  pubkey:  %s (%d bytes binary, %d hex chars)\n", *pub, len(pkb), len(pkb)*2)
	_, _ = fmt.Fprintf(stderr, "  privkey: %s (mode 0600, %d bytes binary, %d hex chars)\n", *priv, len(skb), len(skb)*2)
	_, _ = fmt.Fprintf(stderr, "  roundtrip: sign + verify OK\n")
	return nil
}

func writeFileExclusive(path string, data []byte, mode os.FileMode) error {
	// O_EXCL refuses to overwrite an existing file — we never want to
	// silently clobber a previous keypair.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Write(data)
	return err
}
