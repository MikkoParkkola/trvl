package batchexec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	neturl "net/url"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/http/httpproxy"

	"github.com/MikkoParkkola/trvl/internal/netguard"
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
	// Refuse the proxy forms the guarded provider transport refuses, and apply
	// its private-address rule to the proxy hop (proxyDialControl).
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

	dialer.Control = proxyDialControl
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

// proxyDialControl refuses a proxy on a loopback, private or link-local
// address unless TRVL_ALLOW_PRIVATE_PROXY is set: the rule the provider
// transport applies to its proxy hop. It runs on the resolved address being
// dialled, so a proxy hostname cannot resolve its way around it.
func proxyDialControl(_, address string, _ syscall.RawConn) error {
	if netguard.PrivateProxyAllowed() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("%w: proxy address %q cannot be classified", netguard.ErrDestinationRefused, host)
	}
	if reason := netguard.RefusedIP(net.IP(ip.WithZone("").AsSlice())); reason != "" {
		return fmt.Errorf("%w: proxy %s is a %s (set %s=1 to allow it)", netguard.ErrDestinationRefused, host, reason, netguard.AllowPrivateProxyEnv)
	}
	return nil
}

// connectTunnel asks the proxy on conn for a tunnel to addr and returns once
// conn carries the destination's bytes.
//
// The exchange ends at proxyTunnelTimeout or the context's deadline, whichever
// is sooner, and at once when the context is cancelled.
func connectTunnel(ctx context.Context, conn net.Conn, addr string) error {
	limit := time.Now().Add(proxyTunnelTimeout)
	if ctxLimit, set := ctx.Deadline(); set && ctxLimit.Before(limit) {
		limit = ctxLimit
	}
	// fail reports the cancellation itself when the context ended the
	// exchange, rather than the i/o timeout it was turned into.
	fail := func(cause error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			cause = ctxErr
		}
		return fmt.Errorf("proxy connect: %w", cause)
	}
	if err := conn.SetDeadline(limit); err != nil {
		return fail(err)
	}
	// Cancelling the context pulls the deadline to now, which unblocks the
	// pending write or read below.
	unwatch := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer unwatch()
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", addr, addr); err != nil {
		return fail(err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return fail(err)
	}
	// Decide on the status alone and leave the body unread: closing it would
	// drain a sized body, so a proxy that announces one and stalls would hold
	// an already known refusal until the deadline. The caller closes conn.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("proxy connect: %s", resp.Status)
	}
	// The client speaks first in TLS, so nothing may follow the proxy's
	// answer. Bytes here would be lost with the reader and corrupt the
	// handshake; refuse instead.
	if reader.Buffered() > 0 {
		return errors.New("proxy connect: unexpected data after the proxy response")
	}
	// A cancel that fired after the read has queued a deadline of now, which
	// would land on the TLS handshake once the reset below is done.
	if !unwatch() {
		return fail(ctx.Err())
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fail(err)
	}
	return nil
}
