//go:build !(darwin || linux || freebsd || netbsd || openbsd || dragonfly) && !windows

package watch

import "os"

// lockSupported is false here: this build has no advisory-lock implementation,
// so the store degrades to in-process serialisation only (s.mu). Concurrent
// *processes* on such a platform remain last-writer-wins, which is the #512 /
// #553 hazard. Both unix (lock_unix.go, flock) and windows (lock_windows.go,
// LockFileEx) have real implementations; this fallback only applies to
// platforms outside both build tags.
//
// The tags on all three lock_*.go files MUST stay mutually exclusive. This
// file's list previously omitted "&& !windows" from its own negation, so on
// GOOS=windows this file co-compiled with lock_windows.go and redeclared
// acquireFileLock/releaseFileLock/lockSupported -- a build break (#553 review
// round 2). Do NOT "fix" that by switching lock_unix.go to Go's generic
// "unix" build tag instead -- that tag also matches aix/illumos/solaris,
// where flock(2) (lock_unix.go's syscall.Flock) is not part of the Go
// syscall package's surface, which breaks GOOS=solaris in the other
// direction. The explicit BSD-family list is deliberate: it is exactly the
// set of GOOS values where lock_unix.go's syscall.Flock calls compile.
// Verify with `GOOS=<target> go build ./internal/watch/...` for windows,
// solaris, and at least one non-unix/non-windows GOOS (e.g. js) after
// touching any of the three files.
const lockSupported = false

func acquireFileLock(string) (*os.File, error) { return nil, nil }

func releaseFileLock(*os.File) {}

// tryLockFile / unlockFile back the scheduler's non-blocking singleton lock
// (lock.go), a separate primitive from acquireFileLock/releaseFileLock above.
// lock_unix.go and lock_windows.go together are not exhaustive (see comment
// above), so platforms outside both -- e.g. js/wasm, solaris -- need these
// stubbed here too, or the build fails with "undefined: tryLockFile".
func tryLockFile(*os.File) (bool, error) { return true, nil }

func unlockFile(*os.File) error { return nil }
