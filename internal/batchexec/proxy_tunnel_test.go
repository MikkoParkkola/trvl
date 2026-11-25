package batchexec

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// startStallingProxy is a loopback proxy that reads the CONNECT request,
// writes reply (which may be empty) and then holds the connection open
// without another byte until the test ends.
func startStallingProxy(t *testing.T, reply string) *neturl.URL {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		_ = listener.Close()
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
					return
				}
				_, _ = io.WriteString(conn, reply)
				<-done
			}()
		}
	}()
	return &neturl.URL{Scheme: "http", Host: listener.Addr().String()}
}

// dialWithin runs dialRawVia and fails the test if it has not returned within
// limit, so a dial that waits for its deadline fails fast instead of hanging.
func dialWithin(t *testing.T, ctx context.Context, proxyURL *neturl.URL, limit time.Duration) error {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		conn, err := dialRawVia(ctx, "tcp", "example.com:443", proxyURL)
		if conn != nil {
			_ = conn.Close()
		}
		result <- err
	}()
	select {
	case err := <-result:
		return err
	case <-time.After(limit):
		t.Fatalf("dial through the proxy did not return within %v", limit)
		return nil
	}
}

// Cancelling a search must end a CONNECT exchange with a silent proxy at once,
// not when the tunnel deadline expires.
func TestDialRawVia_Error_CancelStopsSilentProxy(t *testing.T) {
	proxyURL := startStallingProxy(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)

	err := dialWithin(t, ctx, proxyURL, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("dial error = %v, want context.Canceled", err)
	}
}

// A refusal is known from its status line; a sized body the proxy never sends
// must not turn it into a wait for the deadline.
func TestDialRawVia_Error_RefusalWithStalledBody(t *testing.T) {
	proxyURL := startStallingProxy(t, "HTTP/1.1 403 Forbidden\r\nContent-Length: 10\r\n\r\n")

	err := dialWithin(t, testContext(t), proxyURL, time.Second)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("dial error = %v, want the proxy's 403", err)
	}
}

// The HTTP/2 dialer has to go through the tunnel as well. Nothing resolves
// example.com in this test and the backend's certificate is self-signed, so
// the handshake is expected to fail; the check is on the CONNECT the proxy got.
func TestDialTLSChromeH2_ThroughEnvironmentProxy(t *testing.T) {
	backend := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(backend.Close)
	tunnel := startConnectProxy(t, backend.Listener.Addr().String(), tunnelEstablished)
	for _, name := range []string{"NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
	t.Setenv("HTTPS_PROXY", tunnel.url().String())

	conn, err := dialTLSChromeH2(testContext(t), "tcp", "example.com:443")
	switch {
	case err == nil:
		_ = conn.Close()
	case !strings.Contains(err.Error(), "utls handshake"):
		t.Fatalf("h2 dial failed before the handshake: %v", err)
	}
	if want := []string{"example.com:443"}; !slices.Equal(tunnel.requested(), want) {
		t.Fatalf("CONNECT targets = %v, want %v", tunnel.requested(), want)
	}
}
