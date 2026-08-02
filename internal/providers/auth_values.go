package providers

import (
	"context"
	"net/http"
)

// copyAuthValues returns a defensive copy of m. Always called under either
// pc.authMu read or write lock to avoid concurrent-map-iteration. Returns a
// non-nil map even when m is empty/nil so callers can iterate freely.
func copyAuthValues(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// snapshotAuthValues takes the read lock itself and returns a defensive copy.
// Used in the no-preflight code path where callers expected pc.authValues
// directly; the snapshot eliminates the cross-call race in MIK-3070.
//
// Note the naming split in this file, because getting it wrong is what caused
// the deadlock this file exists to prevent: a "Locked" suffix here means "the
// caller must already hold the lock" (commitAuthValuesLocked), never "this
// function locks for you". Functions that lock for you carry no suffix.
func snapshotAuthValues(pc *providerClient) map[string]string {
	pc.authMu.RLock()
	defer pc.authMu.RUnlock()
	return copyAuthValues(pc.authValues)
}

// extractAuthValues builds a fresh auth-value map from a recovered response.
// It takes no lock, and the map it returns is owned solely by the caller until
// it is committed. It does use pc.client — applyURLExtractions performs network
// I/O through it — so it is not free of shared state; the client is internally
// synchronized, and pc.authValues is what must not be touched here.
//
// Splitting extraction from the commit is load-bearing, not cosmetic. The
// recovery tiers run under two different lock disciplines — runPreflight holds
// pc.authMu for writing across the whole cascade, while the search-path
// recovery in runtime_provider.go holds nothing — so a tier that reached for
// the mutex itself was correct for exactly one of its two callers. It
// deadlocked the first (sync.RWMutex is not reentrant) and had to keep locking
// for the second to stay race-free against readers holding RLock.
func extractAuthValues(ctx context.Context, pc *providerClient, auth *AuthConfig, resp *http.Response, body []byte) map[string]string {
	fresh := map[string]string{}
	applyExtractions(auth.Extractions, resp, body, fresh)
	applyURLExtractions(ctx, pc.client, auth.Extractions, fresh)
	return fresh
}

// commitAuthValuesLocked clears pc.authValues and installs fresh in its place.
// The caller MUST already hold pc.authMu for writing.
func commitAuthValuesLocked(pc *providerClient, fresh map[string]string) {
	for k := range pc.authValues {
		delete(pc.authValues, k)
	}
	for k, v := range fresh {
		pc.authValues[k] = v
	}
}

// commitAuthValues is the self-locking commit, for callers that do not already
// hold pc.authMu. Readers hold pc.authMu.RLock, so an unsynchronized
// clear+write here would race concurrent searches sharing the providerClient.
func commitAuthValues(pc *providerClient, fresh map[string]string) {
	pc.authMu.Lock()
	defer pc.authMu.Unlock()
	commitAuthValuesLocked(pc, fresh)
}
