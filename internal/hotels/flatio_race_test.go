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
func TestFlatioFetcher_ConcurrentNoRace(t *testing.T) {
	// Reset the lazy-init state so this test exercises the build path, not a
	// value a prior test injected. Save and restore to avoid cross-test leakage.
	prevClient := flatioClient
	prevOnce := flatioClientOnce
	flatioClient = nil
	flatioClientOnce = sync.Once{}
	t.Cleanup(func() {
		flatioClient = prevClient
		flatioClientOnce = prevOnce
	})

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
