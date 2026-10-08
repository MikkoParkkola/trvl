package leaky

import (
	"net"
	"testing"
	"time"
)

// Dials an outside address (TEST-NET-1, never routed) and ignores the
// failure, the way a test that relies on a short timeout does. It passes, so
// only the guard can see it.
func TestLeaks(t *testing.T) {
	if c, err := net.DialTimeout("tcp", "192.0.2.1:443", time.Second); err == nil {
		_ = c.Close()
	}
}
