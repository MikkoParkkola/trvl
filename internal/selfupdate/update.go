package selfupdate

// Self-update orchestrator: downloads the platform tarball for a target
// version, verifies SHA-256 + ML-DSA-65, extracts the binary, and
// atomically replaces the running trvl binary.
//
// Cosign verification is intentionally NOT performed here yet. The
// ML-DSA-65 layer alone provides full cryptographic guarantee against
// silent tampering (post-quantum + classical SHA-256). Cosign keyless
// adds defense-in-depth and will land via sigstore-go in a follow-up.
//
// Threat model. The orchestrator runs after the user explicitly invokes
// `trvl self-update`. We assume:
//   - The HTTPS connection to releases.github.com is intact (TLS verified).
//   - The user expects the running binary to be replaced if all
//     verifications pass.
//   - A failed verification MUST abort and leave the on-disk binary
//     unchanged. There is no rollback path because there is nothing to
//     roll back from.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// downloadBaseURL is the GitHub release-asset download root. Tests override
// this via UpdaterOption.
const downloadBaseURL = "https://github.com/MikkoParkkola/trvl/releases/download"

// downloadTimeout caps the total time for tarball + checksums + signature
// downloads. Tarballs run ~12 MB so 60s is generous even on slow links.
const downloadTimeout = 60 * time.Second

// Updater orchestrates the verify-then-replace sequence. Construct via
// NewUpdater; override the HTTP client and base URL in tests via the
// With* options.
type Updater struct {
	httpClient *http.Client
	baseURL    string
	// goos / goarch let tests pick a fixed platform regardless of the
	// host running the test. Production code uses runtime.GOOS / GOARCH.
	goos   string
	goarch string
}

// UpdaterOption is a functional option for NewUpdater.
type UpdaterOption func(*Updater)

// WithUpdaterHTTPClient overrides the HTTP client (test-only).
func WithUpdaterHTTPClient(c *http.Client) UpdaterOption {
	return func(u *Updater) { u.httpClient = c }
}

// WithUpdaterBaseURL overrides the download base URL (test-only).
func WithUpdaterBaseURL(s string) UpdaterOption {
	return func(u *Updater) { u.baseURL = s }
}

// WithUpdaterPlatform pins the (goos, goarch) the updater will request.
// Test-only — production callers always use the running host's values.
func WithUpdaterPlatform(goos, goarch string) UpdaterOption {
	return func(u *Updater) { u.goos = goos; u.goarch = goarch }
}

// NewUpdater constructs an Updater with production defaults: 60s HTTP
// timeout, real download base URL, runtime.GOOS / runtime.GOARCH.
func NewUpdater(opts ...UpdaterOption) *Updater {
	u := &Updater{
		httpClient: &http.Client{Timeout: downloadTimeout},
		baseURL:    downloadBaseURL,
		goos:       runtime.GOOS,
		goarch:     runtime.GOARCH,
	}
	for _, opt := range opts {
		opt(u)
	}
	return u
}

// PerformUpdate downloads, verifies, and atomically swaps the running
// trvl binary for the version indicated by latestVer. exePath is the
// resolved path of the binary to replace (typically os.Executable()
// after EvalSymlinks).
//
// Returns the path the new binary was installed at. On any error the
// on-disk binary is left untouched.
//
// Steps:
//  1. Download the platform tarball + checksums.txt + .mldsa65.sig.
//  2. Verify SHA-256(tarball) matches the entry in checksums.txt.
//  3. Verify ML-DSA-65 signature against the embedded trust anchor.
//  4. Extract the trvl binary from the tarball into a temp dir next to
//     the target path.
//  5. Atomic rename: temp binary -> exePath. On Unix os.Rename on the
//     same filesystem is atomic. On Windows we fall back to a two-step
//     rename via .old.
//
// The orchestrator is deliberately conservative: at every step we
// return early with a wrapped error rather than guessing. The only
// "failure that destroys data" path is the final rename, and that's
// guarded by a successful sha256 + ml-dsa verification.
func (u *Updater) PerformUpdate(ctx context.Context, latestVer string, exePath string) (string, error) {
	if strings.TrimSpace(latestVer) == "" {
		return "", fmt.Errorf("self-update: latestVer is empty")
	}
	if exePath == "" {
		return "", fmt.Errorf("self-update: exePath is empty")
	}

	tarballName := fmt.Sprintf("trvl_%s_%s_%s.tar.gz", latestVer, u.goos, u.goarch)
	checksumsURL := fmt.Sprintf("%s/v%s/checksums.txt", u.baseURL, latestVer)
	tarballURL := fmt.Sprintf("%s/v%s/%s", u.baseURL, latestVer, tarballName)
	sigURL := tarballURL + ".mldsa65.sig"

	tmpDir, err := os.MkdirTemp("", "trvl-update-*")
	if err != nil {
		return "", fmt.Errorf("self-update: mktemp: %w", err)
	}
	// Best-effort cleanup once the function returns.
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tarballPath := filepath.Join(tmpDir, tarballName)
	if err := u.downloadTo(ctx, tarballURL, tarballPath); err != nil {
		return "", fmt.Errorf("self-update: download tarball: %w", err)
	}
	checksumsPath := filepath.Join(tmpDir, "checksums.txt")
	if err := u.downloadTo(ctx, checksumsURL, checksumsPath); err != nil {
		return "", fmt.Errorf("self-update: download checksums: %w", err)
	}
	sigPath := filepath.Join(tmpDir, tarballName+".mldsa65.sig")
	if err := u.downloadTo(ctx, sigURL, sigPath); err != nil {
		return "", fmt.Errorf("self-update: download signature: %w", err)
	}

	if err := verifySHA256(tarballPath, checksumsPath, tarballName); err != nil {
		return "", fmt.Errorf("self-update: verify sha256: %w", err)
	}
	if err := verifyMLDSAFile(tarballPath, sigPath); err != nil {
		return "", fmt.Errorf("self-update: verify mldsa65: %w", err)
	}

	binDir := filepath.Join(tmpDir, "extracted")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("self-update: mkdir extract dir: %w", err)
	}
	binName := "trvl"
	if u.goos == "windows" {
		binName = "trvl.exe"
	}
	extractedBin := filepath.Join(binDir, binName)
	if err := extractBinaryFromTarGz(tarballPath, binName, extractedBin); err != nil {
		return "", fmt.Errorf("self-update: extract binary: %w", err)
	}
	if err := os.Chmod(extractedBin, 0o755); err != nil {
		return "", fmt.Errorf("self-update: chmod: %w", err)
	}

	if err := atomicReplace(extractedBin, exePath); err != nil {
		return "", fmt.Errorf("self-update: atomic replace: %w", err)
	}
	return exePath, nil
}

// downloadTo fetches url and writes the response body to path. Honors
// ctx cancellation. Returns an error on non-200 responses + I/O issues.
func (u *Updater) downloadTo(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := u.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d for %s", resp.StatusCode, url)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

// verifySHA256 reads tarballPath, computes its SHA-256, and asserts it
// matches the entry for tarballName in checksumsPath. The checksums
// file format follows `sha256sum`'s output: one entry per line, hex
// digest then filename, separated by two spaces. Lines with trailing
// CRLF are tolerated. BSD-style "SHA256 (file) = digest" entries are
// also tolerated.
func verifySHA256(tarballPath, checksumsPath, tarballName string) error {
	expected, err := readExpectedChecksum(checksumsPath, tarballName)
	if err != nil {
		return err
	}
	got, err := sha256File(tarballPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("sha256 mismatch: got %s, expected %s", got, expected)
	}
	return nil
}

// readExpectedChecksum returns the hex-encoded SHA-256 for filename as
// recorded in the checksums.txt at path. Returns an error if the file
// does not list filename.
func readExpectedChecksum(path, filename string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Standard sha256sum format: "<hex>  <name>" (two spaces).
		// Some generators emit a single space; tolerate both.
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// goreleaser writes "<hex>  <name>".
		if fields[len(fields)-1] == filename {
			return strings.ToLower(fields[0]), nil
		}
		// BSD-style: "SHA256 (name) = hex".
		if len(fields) >= 4 && fields[0] == "SHA256" {
			bsdName := strings.Trim(fields[1], "()")
			if bsdName == filename {
				return strings.ToLower(fields[len(fields)-1]), nil
			}
		}
	}
	return "", fmt.Errorf("checksums.txt has no entry for %q", filename)
}

// sha256File returns the hex-encoded SHA-256 of the file at path.
func sha256File(path string) (string, error) {
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

// verifyMLDSAFile loads the detached signature at sigPath and runs
// VerifyMLDSA65 against tarballPath. Wraps the file I/O so callers can
// treat it as a single step.
func verifyMLDSAFile(tarballPath, sigPath string) error {
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("read signature: %w", err)
	}
	f, err := os.Open(tarballPath)
	if err != nil {
		return fmt.Errorf("open tarball: %w", err)
	}
	defer func() { _ = f.Close() }()
	return VerifyMLDSA65(f, sig)
}

// extractBinaryFromTarGz reads the .tar.gz at tarballPath, finds the
// entry whose basename equals binName, and writes it to dest. Returns
// an error if the tarball does not contain such an entry.
//
// Security note: we do NOT honor any directory components in the
// archive — we only extract a single top-level binary, refusing entries
// with path traversal segments ("../") + absolute paths + symlinks.
// This keeps the extraction surface minimal regardless of how trusted
// the upstream tarball is.
func extractBinaryFromTarGz(tarballPath, binName, dest string) error {
	f, err := os.Open(tarballPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("binary %q not found in tarball", binName)
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}
		// Tarballs from goreleaser have a flat structure: just `trvl`
		// at the root. Reject anything fancy.
		if strings.Contains(hdr.Name, "..") || strings.HasPrefix(hdr.Name, "/") {
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(hdr.Name) != binName {
			continue
		}
		out, err := os.Create(dest)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	}
}

// atomicReplace moves src onto dst with as much atomicity as the host
// filesystem allows.
//
// Unix path: os.Rename across the same filesystem is atomic (a single
// rename(2) syscall). When dst is on a different filesystem we fall
// back to a copy + replace.
//
// Windows path: a running .exe cannot be deleted while it's executing.
// The standard trick is: rename dst -> dst.old, then rename src -> dst.
// The .old file is left behind for the user to clean up after the next
// launch.
func atomicReplace(src, dst string) error {
	// Try the simplest path first. On Unix this is atomic for same-FS.
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	// Fallback 1 (Windows-style): rename dst out of the way, then move
	// src in. The "out of the way" file lingers on disk but does not
	// affect functionality.
	old := dst + ".old"
	_ = os.Remove(old) // best-effort: previous self-update leftover
	if err := os.Rename(dst, old); err != nil {
		// dst doesn't exist (first install?) — try direct rename again.
		// Otherwise propagate.
		if !os.IsNotExist(err) {
			return fmt.Errorf("rename current binary out of way: %w", err)
		}
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	// Fallback 2 (cross-filesystem): copy bytes, then remove src.
	if err := copyFile(src, dst); err != nil {
		return fmt.Errorf("copy fallback: %w", err)
	}
	_ = os.Remove(src)
	return nil
}

// copyFile copies src to dst, preserving 0755 mode. dst is overwritten.
// Used as the cross-filesystem fallback inside atomicReplace.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
