//go:build !unix

package batchexec

// lockRefusalDir is a no-op where flock is unavailable. The client's mutex
// still stops overlapping Google calls inside one process.
func lockRefusalDir(dir string) func() {
	return func() {}
}
