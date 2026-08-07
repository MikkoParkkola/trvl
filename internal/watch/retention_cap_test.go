package watch

import "testing"

const testMaxPointsPerWatch = 20

// newCappedStore keeps boundary tests meaningful without making every
// RecordPrice call rewrite the production-sized retention corpus. Load is
// required because retention overrides are deliberately validated at store
// startup rather than read ad hoc by mutation paths.
func newCappedStore(t *testing.T) (*Store, int) {
	t.Helper()
	t.Setenv(EnvMaxPointsPerWatch, "20")

	store := NewStore(t.TempDir())
	if err := store.Load(); err != nil {
		t.Fatalf("load capped store: %v", err)
	}
	return store, testMaxPointsPerWatch
}

func TestNewCappedStoreReallyLowersTheCap(t *testing.T) {
	store, want := newCappedStore(t)
	if got := store.retentionOrDefault().MaxPointsPerWatch; got != want {
		t.Fatalf("per-watch retention cap = %d, want %d", got, want)
	}
}
