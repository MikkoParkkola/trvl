package batchexec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
)

// proxyTunnelTimeout bounds the CONNECT exchange when the context carries no
// deadline of its own.
const proxyTunnelTimeout = 15 * time.Second

// dialRaw opens the TCP connection the Chrome TLS handshake runs on.
//
// This client performs its own handshake, so net/http never applies
// HTTPS_PROXY to it. Without this step the Google client dials the destination
// directly even when the process is configured with a proxy, and fails wherever
// the proxy is the only way out. When the proxy environment names a proxy for
// addr the connection is an HTTP CONNECT tunnel through it; otherwise it is the
// direct connection it has always been.
func dialRaw(ctx context.Context, network, addr string) (net.Conn, error) {
	proxyURL, err := httpproxy.FromEnvironment().ProxyFunc()(&neturl.URL{Scheme: "https", Host: addr})
	if err != nil {
		return nil, fmt.Errorf("resolve proxy configuration: %w", err)
	}
	return dialRawVia(ctx, network, addr, proxyURL)
}

// dialRawVia is dialRaw with the proxy decision handed in.
func dialRawVia(ctx context.Context, network, addr string, proxyURL *neturl.URL) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if proxyURL == nil {
		return dialer.DialContext(ctx, network, addr)
	}
	// Same limits as the guarded provider transport, so one proxy setting
	// behaves the same way across the binary.
	if proxyURL.User != nil {
		return nil, errors.New("authenticated proxies are not supported")
	}
	if !strings.EqualFold(proxyURL.Scheme, "http") {
		return nil, fmt.Errorf("proxy scheme %q is not supported", proxyURL.Scheme)
	}
	proxyAddr := proxyURL.Host
	if proxyURL.Port() == "" {
		proxyAddr = net.JoinHostPort(proxyURL.Hostname(), "80")
	}

	conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("dial proxy: %w", err)
	}
	if err := connectTunnel(ctx, conn, addr); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// connectTunnel asks the proxy on conn for a tunnel to addr and returns once
// conn carries the destination's bytes.
func connectTunnel(ctx context.Context, conn net.Conn, addr string) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(proxyTunnelTimeout)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("proxy connect: %w", err)
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", addr, addr); err != nil {
		return fmt.Errorf("proxy connect: %w", err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return fmt.Errorf("proxy connect: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("proxy connect: %s", resp.Status)
	}
	// The client speaks first in TLS, so nothing may follow the proxy's
	// answer. Bytes here would be lost with the reader and corrupt the
	// handshake; refuse instead.
	if reader.Buffered() > 0 {
		return errors.New("proxy connect: unexpected data after the proxy response")
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fmt.Errorf("proxy connect: %w", err)
	}
	return nil
}
