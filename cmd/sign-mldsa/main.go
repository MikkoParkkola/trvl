// Command sign-mldsa signs a release artifact with the trvl ML-DSA-65
// (NIST FIPS 204) release-signing root key. Invoked by goreleaser's signs
// block as the post-quantum half of the hybrid signing pipeline (the other
// half is cosign keyless via Sigstore + GH OIDC).
//
// The private key is read from the TRVL_MLDSA_PRIVKEY_V1 env var
// (hex-encoded 4032-byte ML-DSA-65 private key, sourced from the GH
// Secret of the same name). The key is never logged or written to disk
// by this tool.
//
// Signature scheme: instead of streaming the entire archive into ML-DSA's
// SignTo, we sign the SHA-256 hash of the archive bytes. This keeps the
// signing operation O(1) regardless of archive size and matches the
// detached-signature convention used by cosign and gpg.
//
// Output: writes <artifact>.mldsa65.sig in raw 3309-byte signature format
// alongside the artifact. The verifier computes SHA-256 of the artifact
// and calls mldsa65.Verify(pubkey, sha256(artifact), nil, sig).
//
// Usage (via goreleaser):
//
//	signs:
//	  - id: mldsa
//	    cmd: go
//	    args: [run, ./cmd/sign-mldsa, --in, "${artifact}"]
//	    artifacts: archive
//
// The companion verifier lives in internal/selfupdate/verify.go and
// embeds the trust-anchor pubkey from cmd/trvl/keys/mldsa65_v1.pubkey.hex.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
)

const (
	// envPrivkey names the env var that holds the hex-encoded ML-DSA-65
	// privkey. The _V1 suffix is intentional: it pins the trust-anchor
	// version this signing tool produces signatures against, and matches
	// the GitHub Actions secret of the same name. Reading the env var
	// directly (rather than via goreleaser's `.Env` template) sidesteps
	// goreleaser's allowlist behavior in older goreleaser-action versions.
	envPrivkey = "TRVL_MLDSA_PRIVKEY_V1"
	sigSuffix  = ".mldsa65.sig"
)

func main() {
	in := flag.String("in", "", "path to the artifact to sign")
	out := flag.String("out", "", "output signature path (default: <in>.mldsa65.sig)")
	flag.Parse()
	if *in == "" {
		die("usage: sign-mldsa --in <artifact> [--out <sig>]")
	}
	outPath := *out
	if outPath == "" {
		outPath = *in + sigSuffix
	}

	privHex := os.Getenv(envPrivkey)
	if privHex == "" {
		die("env %s is empty; cannot sign without privkey", envPrivkey)
	}
	skBytes, err := hex.DecodeString(privHex)
	if err != nil {
		die("decode privkey hex: %v", err)
	}
	if len(skBytes) != mldsa65.PrivateKeySize {
		die("privkey wrong length: got %d, want %d", len(skBytes), mldsa65.PrivateKeySize)
	}
	var sk mldsa65.PrivateKey
	if err := sk.UnmarshalBinary(skBytes); err != nil {
		die("unmarshal privkey: %v", err)
	}

	digest, err := sha256File(*in)
	if err != nil {
		die("sha256 %s: %v", *in, err)
	}

	sig := make([]byte, mldsa65.SignatureSize)
	if err := mldsa65.SignTo(&sk, digest, nil, false, sig); err != nil {
		die("sign: %v", err)
	}

	// Sanity: verify the signature we just produced before writing it.
	// Catches a corrupted privkey-in-secret before it lands in a release.
	pk := sk.Public().(*mldsa65.PublicKey)
	if !mldsa65.Verify(pk, digest, nil, sig) {
		die("self-verify failed; refusing to ship a signature that does not verify")
	}

	if err := writeFileExcl(outPath, sig, 0o644); err != nil {
		die("write sig %s: %v", outPath, err)
	}
	_, _ = fmt.Fprintf(os.Stderr, "sign-mldsa: %s -> %s (sha256=%x, sig=%d bytes)\n",
		*in, outPath, digest[:8], len(sig))
}

func sha256File(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func writeFileExcl(path string, data []byte, mode os.FileMode) error {
	// O_EXCL: never silently overwrite an existing signature.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Write(data)
	return err
}

func die(format string, a ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "sign-mldsa: "+format+"\n", a...)
	os.Exit(1)
}
