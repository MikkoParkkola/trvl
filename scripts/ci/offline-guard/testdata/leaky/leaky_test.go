package leaky

import (
	"net"
	"testing"
	"time"

	"golang.org/x/net/ipv4"
)

// Reaches outside addresses (TEST-NET-1, never routed) in each way the guard
// counts, and ignores every failure, the way a test that relies on a short
// timeout does. It passes, so only the guard can see it.
func TestLeaks(t *testing.T) {
	// TCP connect.
	if c, err := net.DialTimeout("tcp", "192.0.2.1:443", time.Second); err == nil {
		_ = c.Close()
	}
	// Write on a connected UDP socket.
	if c, err := net.Dial("udp", "192.0.2.1:53"); err == nil {
		_, _ = c.Write([]byte{0})
		_ = c.Close()
	}
	// sendmmsg batch with the outside destination behind a loopback one.
	if c, err := net.ListenPacket("udp4", "127.0.0.1:0"); err == nil {
		msgs := []ipv4.Message{
			{Buffers: [][]byte{{0}}, Addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}},
			{Buffers: [][]byte{{0}}, Addr: &net.UDPAddr{IP: net.IPv4(192, 0, 2, 3), Port: 123}},
		}
		_, _ = ipv4.NewPacketConn(c).WriteBatch(msgs, 0)
		_ = c.Close()
	}
}
