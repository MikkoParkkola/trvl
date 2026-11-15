package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type browserProxyDialFunc func(context.Context, string, string) (net.Conn, error)

// browserPolicyProxy is a short-lived loopback proxy used only by a headless
// browser process. Chrome resolves and redirects outside Go's dialer; routing
// it here brings every navigation, redirect, and subresource back through the
// same destination policy as ordinary provider traffic.
type browserPolicyProxy struct {
	server   *http.Server
	listener net.Listener
	lookup   lookupIPsFunc
	dial     browserProxyDialFunc

	mu       sync.Mutex
	refusals []proxyRefusal
	keep     string // host recorded even past maxProxyRefusals
}

func startBrowserPolicyProxy(lookup lookupIPsFunc, dial browserProxyDialFunc) (*browserPolicyProxy, error) {
	if lookup == nil {
		lookup = lookupHostIPs
	}
	if dial == nil {
		dial = guardedDialer().DialContext
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start browser policy proxy: %w", err)
	}
	proxy := &browserPolicyProxy{listener: listener, lookup: lookup, dial: dial}
	proxy.server = &http.Server{
		Handler:           proxy,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	go func() { _ = proxy.server.Serve(listener) }()
	return proxy, nil
}

func (p *browserPolicyProxy) Address() string { return p.listener.Addr().String() }

func (p *browserPolicyProxy) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return p.server.Shutdown(ctx)
}

// maxProxyRefusals caps how many distinct refused hosts one browser run keeps.
const maxProxyRefusals = 16

// proxyRefusal is one destination the policy proxy refused: the hostname
// (no port) and the policy error behind it.
type proxyRefusal struct {
	Host string
	Err  error
}

// Refusals returns every host the proxy refused, deduplicated and capped.
func (p *browserPolicyProxy) Refusals() []proxyRefusal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]proxyRefusal(nil), p.refusals...)
}

// keepHost names a host whose refusal is recorded even past the cap.
func (p *browserPolicyProxy) keepHost(host string) {
	p.mu.Lock()
	p.keep = normalizeRefusedHost(host)
	p.mu.Unlock()
}

// recordRefusal keeps the first refusal per host and stops at
// maxProxyRefusals, so the earliest refusals (usually the page's own
// requests) survive a page that fans out to many blocked trackers.
func (p *browserPolicyProxy) recordRefusal(host string, err error) {
	if err == nil {
		return
	}
	host = normalizeRefusedHost(host)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.refusals) >= maxProxyRefusals && host != p.keep {
		return
	}
	for _, r := range p.refusals {
		if r.Host == host {
			return
		}
	}
	p.refusals = append(p.refusals, proxyRefusal{Host: host, Err: err})
}

// normalizeRefusedHost lowercases a host and drops any port and trailing dot.
func normalizeRefusedHost(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

func (p *browserPolicyProxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodConnect {
		p.serveConnect(w, req)
		return
	}
	p.serveHTTP(w, req)
}

func (p *browserPolicyProxy) serveHTTP(w http.ResponseWriter, req *http.Request) {
	pinned, _, err := pinHTTPURL(req.Context(), req.URL, p.lookup)
	if err != nil {
		p.recordRefusal(req.URL.Hostname(), err)
		http.Error(w, "destination refused by policy", http.StatusForbidden)
		return
	}

	out := req.Clone(req.Context())
	out.URL = pinned
	out.RequestURI = ""
	if req.Host != "" {
		out.Host = req.Host
	}
	removeProxyHopHeaders(out.Header)

	transport := directGuardedTransport()
	transport.DialContext = p.dial
	resp, err := transport.RoundTrip(out)
	if err != nil {
		if errors.Is(err, ErrDestinationRefused) {
			p.recordRefusal(req.URL.Hostname(), err)
		}
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	removeProxyHopHeaders(resp.Header)
	for name, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *browserPolicyProxy) serveConnect(w http.ResponseWriter, req *http.Request) {
	host := req.Host
	if !strings.Contains(host, ":") {
		host = net.JoinHostPort(host, "443")
	}
	target := &url.URL{Scheme: "https", Host: host}
	pinned, _, err := pinHTTPURL(req.Context(), target, p.lookup)
	if err != nil {
		p.recordRefusal(target.Hostname(), err)
		http.Error(w, "destination refused by policy", http.StatusForbidden)
		return
	}

	upstream, err := p.dial(req.Context(), "tcp", pinned.Host)
	if err != nil {
		if errors.Is(err, ErrDestinationRefused) {
			p.recordRefusal(target.Hostname(), err)
		}
		http.Error(w, "upstream connection failed", http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "proxy connection unavailable", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	if err := buffered.Flush(); err != nil {
		_ = client.Close()
		_ = upstream.Close()
		return
	}

	go func() {
		_, _ = io.Copy(upstream, buffered)
		_ = upstream.Close()
	}()
	_, _ = io.Copy(client, upstream)
	_ = client.Close()
}

func removeProxyHopHeaders(header http.Header) {
	for _, name := range []string{
		"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
		"Proxy-Authorization", "TE", "Trailer", "Transfer-Encoding", "Upgrade",
	} {
		header.Del(name)
	}
}
