package leaky

import (
	"net/http"
	"testing"
)

// Reaches for an outside host and ignores the failure, the way a test that
// relies on a short timeout does. It passes, so only the guard can see it.
func TestLeaks(t *testing.T) {
	resp, err := http.Get("https://offline-guard.invalid/")
	if err == nil {
		_ = resp.Body.Close()
	}
}
