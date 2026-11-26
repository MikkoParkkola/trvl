package batchexec

import (
	"bufio"
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/netguard"
	utls "github.com/refraction-networking/utls"
)

// connectProxy is a loopback HTTP proxy that answers CONNECT with reply and,
// on a 200, pipes the tunnel to backend whatever destination was asked for.
// It records the destinations requested.
type connectProxy struct {
	listener net.Listener
	backend  string
	reply    string

	mu      sync.Mutex
	targets []string
}

func startConnectProxy(t *testing.T, backend, reply string) *connectProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	p := &connectProxy{listener: listener, backend: backend, reply: reply}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go p.serve(conn)
		}
	}()
	return p
}

func (p *connectProxy) serve(client net.Conn) {
	defer func() { _ = client.Close() }()
	req, err := http.ReadRequest(bufio.NewReader(client))
	if err != nil || req.Method != http.MethodConnect {
		return
	}
	p.mu.Lock()
	p.targets = append(p.targets, req.Host)
	p.mu.Unlock()
	if _, err := io.WriteString(client, p.reply); err != nil || !strings.HasPrefix(p.reply, "HTTP/1.1 200") || p.backend == "" {
		return
	}
	upstream, err := net.Dial("tcp", p.backend)
	if err != nil {
		return
	}
	defer func() { _ = upstream.Close() }()
	go func() { _, _ = io.Copy(upstream, client) }()
	_, _ = io.Copy(client, upstream)
}

func (p *connectProxy) url() *neturl.URL {
	return &neturl.URL{Scheme: "http", Host: p.listener.Addr().String()}
}

func (p *connectProxy) requested() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.targets...)
}

const tunnelEstablished = "HTTP/1.1 200 Connection established\r\n\r\n"

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// The Chrome handshake itself must run inside the tunnel: the destination
// name never resolves here, so a completed handshake proves the proxy carried it.
func TestDialTLSChromeHTTP1_Success_ThroughEnvironmentProxy(t *testing.T) {
	t.Setenv(netguard.AllowPrivateProxyEnv, "1") // the test proxy is on loopback
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer ts.Close()
	proxy := startConnectProxy(t, ts.Listener.Addr().String(), tunnelEstablished)
	t.Setenv("HTTPS_PROXY", proxy.url().String())
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	conn, err := dialTLSChromeHTTP1WithConfig(testContext(t), "tcp", "example.com:443", &utls.Config{
		ServerName: "example.com",
		RootCAs:    pool,
	})
	if err != nil {
		t.Fatalf("dial through proxy: %v", err)
	}
	_ = conn.Close()

	if got := proxy.requested(); len(got) != 1 || got[0] != "example.com:443" {
		t.Fatalf("proxy saw CONNECT targets %v, want [example.com:443]", got)
	}
}

func TestDialRaw_Success_NoProxyGoesDirect(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer ts.Close()
	proxy := startConnectProxy(t, "", tunnelEstablished)
	t.Setenv("HTTPS_PROXY", proxy.url().String())
	// Loopback destinations are never proxied by the standard environment
	// rules, which is what lets this stay offline.
	conn, err := dialRaw(testContext(t), "tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("direct dial: %v", err)
	}
	_ = conn.Close()
	if got := proxy.requested(); len(got) != 0 {
		t.Fatalf("proxy was used for a destination outside its scope: %v", got)
	}
}

func TestDialRawVia_Error_ProxyRefuses(t *testing.T) {
	t.Setenv(netguard.AllowPrivateProxyEnv, "1")
	proxy := startConnectProxy(t, "", "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
	conn, err := dialRawVia(testContext(t), "tcp", "example.com:443", proxy.url())
	if err == nil {
		_ = conn.Close()
		t.Fatal("expected the proxy refusal to fail the dial")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("error %q does not carry the proxy status", err)
	}
}

func TestDialRawVia_Error_DataAfterProxyResponse(t *testing.T) {
	t.Setenv(netguard.AllowPrivateProxyEnv, "1")
	proxy := startConnectProxy(t, "", tunnelEstablished+"unexpected")
	conn, err := dialRawVia(testContext(t), "tcp", "example.com:443", proxy.url())
	if err == nil {
		_ = conn.Close()
		t.Fatal("expected bytes after the proxy response to fail the dial")
	}
	if !strings.Contains(err.Error(), "unexpected data") {
		t.Fatalf("got %v; want the stray-bytes error", err)
	}
}

func TestDialRawVia_Error_UnsupportedProxy(t *testing.T) {
	for name, proxyURL := range map[string]*neturl.URL{
		"credentials": {Scheme: "http", Host: "127.0.0.1:1", User: neturl.UserPassword("user", "pass")},
		"socks":       {Scheme: "socks5", Host: "127.0.0.1:1"},
		"https":       {Scheme: "https", Host: "127.0.0.1:1"},
	} {
		t.Run(name, func(t *testing.T) {
			conn, err := dialRawVia(testContext(t), "tcp", "example.com:443", proxyURL)
			if err == nil {
				_ = conn.Close()
				t.Fatal("expected an unsupported proxy to be refused")
			}
			if strings.Contains(err.Error(), "pass") {
				t.Fatalf("error %q leaks the proxy credentials", err)
			}
		})
	}
}
