// Package atomicjson writes JSON to disk atomically with restrictive
// permissions. It is the single implementation of the temp-file + fsync + rename
// pattern that was previously copy-pasted across every ~/.trvl store (watch,
// trips, dategrid, probecache, dealquality, providers, ...). Centralising it
// means the security-sensitive bits — 0600 file perms, 0700 dir, atomic rename,
// the Windows rename-over-existing fallback — are written correctly once.
package atomicjson

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Write marshals v as indented JSON and writes it to path atomically: it is
// rendered to a temp file in the same directory (0600), fsynced, then renamed
// over path so a reader never observes a partial file. The parent directory is
// created (0700) if absent.
func Write(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteBytes(path, b)
}

// WriteBytes writes raw bytes to path atomically (same guarantees as Write).
func WriteBytes(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	// Open the temp file with O_CREATE|O_EXCL at mode 0600 directly, rather than
	// os.CreateTemp followed by Chmod. CreateTemp inherits the process umask, so
	// the file can be momentarily group/world-readable between creation and the
	// Chmod — a TOCTOU window. With a crypto-random name and O_EXCL the file is
	// owner-only from the instant it exists, which is what lets even secret
	// stores (API keys, preferences) rely on this single implementation.
	rnd := make([]byte, 8)
	if _, err := rand.Read(rnd); err != nil {
		return fmt.Errorf("atomicjson: generate temp name: %w", err)
	}
	tmpPath := filepath.Join(dir, filepath.Base(path)+".tmp-"+hex.EncodeToString(rnd))
	//nolint:gosec // mode 0600 is intentional — store files must be owner-only
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	tmpPath = tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		// Windows cannot rename over an existing file; remove then retry.
		if runtime.GOOS == "windows" {
			_ = os.Remove(path)
			if err2 := os.Rename(tmpPath, path); err2 == nil {
				cleanup = false
				return nil
			}
		}
		return err
	}
	cleanup = false
	return nil
}
