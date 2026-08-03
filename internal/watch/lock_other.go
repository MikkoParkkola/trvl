//go:build !(darwin || linux || freebsd || netbsd || openbsd || dragonfly)

package watch

import "os"

// lockSupported is false here: this build has no advisory-lock implementation,
// so the store degrades to in-process serialisation only (s.mu). Concurrent
// *processes* on such a platform remain last-writer-wins, which is the #512 /
// #553 hazard. Both unix (lock_unix.go, flock) and windows (lock_windows.go,
// LockFileEx) have real implementations; this fallback only applies to
// platforms outside both build tags.
const lockSupported = false

func acquireFileLock(string) (*os.File, error) { return nil, nil }

func releaseFileLock(*os.File) {}
