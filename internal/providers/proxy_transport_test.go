package providers

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestGuardedTransportModeIsExplicit(t *testing.T) {
	if got := GuardedTransport().Mode(); got != GuardedTransportProxyAware {
		t.Fatalf("GuardedTransport mode = %q, want %q", got, GuardedTransportProxyAware)
	}
	if got := NewGuardedTransport(GuardedTransportDirect).Mode(); got != GuardedTransportDirect {
		t.Fatalf("direct transport mode = %q, want %q", got, GuardedTransportDirect)
	}
}

func TestProxyAwareTransportReachesPublicDestination(t *testing.T) {
	t.Setenv(AllowLocalEnv, "1")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	var hits atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		if req.URL.Host != "93.184.216.34" {
			t.Errorf("proxy target = %q, want pinned public address", req.URL.Host)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)

	client := &http.Client{Transport: GuardedTransport()}
	resp, err := client.Get("http://93.184.216.34/probe")
	if err != nil {
		t.Fatalf("public request through configured proxy: %v", err)
	}
	_ = resp.Body.Close()
	if hits.Load() != 1 {
		t.Fatalf("proxy received %d requests, want 1", hits.Load())
	}
}

func TestProxyAwareTransportRefusesPrivateDestinationBeforeConnect(t *testing.T) {
	t.Setenv(AllowLocalEnv, "")
	transport := NewGuardedTransport(GuardedTransportProxyAware)
	proxyURL, err := url.Parse("http://93.184.216.34:1")
	if err != nil {
		t.Fatal(err)
	}
	transport.proxy = func(*http.Request) (*url.URL, error) { return proxyURL, nil }

	client := &http.Client{Transport: transport}
	_, err = client.Get("http://127.0.0.1:8080/private")
	if !errors.Is(err, ErrDestinationRefused) {
		t.Fatalf("private destination through proxy failed with %v, want ErrDestinationRefused", err)
	}
}

func TestProxyAwareTransportRefusesPrivateProxy(t *testing.T) {
	t.Setenv(AllowLocalEnv, "")

	var hits atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}

	transport := NewGuardedTransport(GuardedTransportProxyAware)
	transport.proxy = func(*http.Request) (*url.URL, error) { return proxyURL, nil }
	client := &http.Client{Transport: transport}
	_, err = client.Get("http://93.184.216.34/public")
	if !errors.Is(err, ErrDestinationRefused) {
		t.Fatalf("private proxy failed with %v, want ErrDestinationRefused", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("refused proxy received %d requests", hits.Load())
	}
}

func TestPinHTTPURLPinsDNSAndRejectsMixedAnswers(t *testing.T) {
	t.Setenv(AllowLocalEnv, "")
	parsed, err := url.Parse("https://provider.example/search")
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}
	pinned, serverName, err := pinHTTPURL(context.Background(), parsed, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if pinned.Host != "93.184.216.34:443" || serverName != "provider.example" {
		t.Fatalf("pinned URL = %q, server name = %q", pinned.Host, serverName)
	}

	mixed := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("127.0.0.1")}, nil
	}
	if _, _, err := pinHTTPURL(context.Background(), parsed, mixed); !errors.Is(err, ErrDestinationRefused) {
		t.Fatalf("mixed DNS answers failed with %v, want ErrDestinationRefused", err)
	}
}
