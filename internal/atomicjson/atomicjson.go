// Package atomicjson writes JSON to disk atomically with restrictive
// permissions. It is the single implementation of the temp-file + fsync + rename
// pattern that was previously copy-pasted across every ~/.trvl store (watch,
// trips, dategrid, probecache, dealquality, providers, ...). Centralising it
// means the security-sensitive bits — 0600 file perms, 0700 dir, atomic rename,
// the Windows rename-over-existing fallback — are written correctly once.
package atomicjson

import (
	"encoding/json"
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

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
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
