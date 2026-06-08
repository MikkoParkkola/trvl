package livecheck

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/watch"
)

// TestChecker_UnsupportedType is deterministic and hits no network: an
// unsupported watch type must return an error rather than a fabricated price.
func TestChecker_UnsupportedType(t *testing.T) {
	t.Parallel()
	price, _, _, err := Checker{}.CheckPrice(context.Background(), watch.Watch{Type: "bus"})
	if err == nil {
		t.Fatal("expected error for unsupported watch type")
	}
	if price != 0 {
		t.Errorf("price = %f, want 0 on error", price)
	}
}
