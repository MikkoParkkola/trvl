package hotels

import (
	"sync"
	"testing"
)

// TestFlatioFetcher_ConcurrentNoRace hammers flatioFetcher from many goroutines.
// Before the fix, flatioFetcher read the package-global flatioClient interface on
// an unsynchronized fast path while sync.Once.Do wrote it, racing on the
// interface value (caught here under `go test -race`; it crashed Windows CI).
// All access now flows through the Once, so this is race-free.
//
// The Once is intentionally not reset (copying a sync.Once is a vet error and a
// bug). Nulling flatioClient exercises the cold build path when this test runs
// before any other caller warms the Once; otherwise it exercises concurrent
// reads. Both are race-free under the fix.
func TestFlatioFetcher_ConcurrentNoRace(t *testing.T) {
	prevClient := flatioClient
	t.Cleanup(func() { flatioClient = prevClient })
	flatioClient = nil

	const n = 64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = flatioFetcher() // concurrent read/build; -race flags any tear
		}()
	}
	wg.Wait()
}
