//go:build !unix && !windows

package batchexec

import "os"

// tryLockFile always succeeds where no file lock is available. The client's
// in-process mutex still stops overlapping Google calls inside one process.
func tryLockFile(*os.File) (bool, error) { return true, nil }

func unlockFile(*os.File) {}
