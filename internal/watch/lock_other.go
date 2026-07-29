//go:build !(darwin || linux || freebsd || netbsd || openbsd || dragonfly)

package watch

import "os"

// lockSupported is false here: this build has no advisory-lock implementation,
// so the store degrades to in-process serialisation only (s.mu). Concurrent
// *processes* on such a platform remain last-writer-wins, which is the #512
// hazard. Adding Windows support means LockFileEx, which lives in
// golang.org/x/sys/windows; wiring it is a go.mod change and therefore out of
// scope for this fix.
const lockSupported = false

func acquireFileLock(string) (*os.File, error) { return nil, nil }

func releaseFileLock(*os.File) {}
