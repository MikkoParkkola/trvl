package providers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

// TRVL.PROXYSPLIT.1 -- reaching a proxy on a private address must not require
// switching off the destination guard.
//
// These were one switch. A corporate HTTP_PROXY is almost always RFC1918, so
// the only way to route through one was TRVL_ALLOW_LOCAL_PROVIDERS=1 -- which
// also unlocks private, loopback and link-local DESTINATIONS, including the
// cloud metadata address the refusal list exists to keep out. Obeying your
// employer's egress policy meant disabling the guard against server-side
// request forgery, and nothing said so.
//
// Raised by adversarial review of #587, which was precise about the shape: the
// ORDERING was already correct, with the destination checked before the proxy.
// The control plane was not.
func TestPrivateProxyOptInDoesNotUnlockPrivateDestinations(t *testing.T) {
	t.Setenv(AllowLocalEnv, "")
	t.Setenv(AllowPrivateProxyEnv, "1")

	var hits atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}

	transport := NewGuardedTransport(GuardedTransportProxyAware)
	transport.proxy = func(*http.Request) (*url.URL, error) { return proxyURL, nil }
	client := &http.Client{Transport: transport}

	// The private PROXY is now reachable: the request gets as far as it.
	if _, err := client.Get("http://93.184.216.34/public"); errors.Is(err, ErrDestinationRefused) {
		t.Errorf("a private proxy was refused with %s set. That is the whole point of the "+
			"separate opt-in: a corporate proxy is almost always on a private address.", AllowPrivateProxyEnv)
	}
	if hits.Load() == 0 {
		t.Errorf("the proxy received no requests, so the opt-in did not actually reach the "+
			"transport; %s alone must be enough to use a private proxy", AllowPrivateProxyEnv)
	}

	// And a private DESTINATION is still refused, which is the half that
	// matters. If this ever passes, the split has silently become the old
	// single switch again.
	if _, err := client.Get("http://127.0.0.1:8080/private"); !errors.Is(err, ErrDestinationRefused) {
		t.Fatalf("a private DESTINATION was allowed by the proxy opt-in (err = %v). %s must relax "+
			"the proxy hop only; the destination is what server-side request forgery targets, and "+
			"the cloud metadata address is a private destination.", err, AllowPrivateProxyEnv)
	}
}

// The default stays strict on both. Without either variable a private proxy is
// refused, which is the behaviour the separate opt-in exists to make optional
// rather than to remove.
func TestPrivateProxyIsRefusedByDefault(t *testing.T) {
	t.Setenv(AllowLocalEnv, "")
	t.Setenv(AllowPrivateProxyEnv, "")

	proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}

	transport := NewGuardedTransport(GuardedTransportProxyAware)
	transport.proxy = func(*http.Request) (*url.URL, error) { return proxyURL, nil }
	client := &http.Client{Transport: transport}

	if _, err := client.Get("http://93.184.216.34/public"); !errors.Is(err, ErrDestinationRefused) {
		t.Fatalf("a private proxy was reachable with no opt-in at all (err = %v)", err)
	}
}

// The destination opt-in still implies the proxy one. Someone who has already
// allowed private destinations cannot be protected by refusing a private proxy,
// and making them set two variables would be ceremony rather than safety.
func TestLocalDestinationOptInImpliesPrivateProxy(t *testing.T) {
	t.Setenv(AllowLocalEnv, "1")
	t.Setenv(AllowPrivateProxyEnv, "")

	if !privateProxyAllowed() {
		t.Errorf("%s=1 did not imply the proxy opt-in; a user who has allowed private "+
			"destinations gains nothing from a refused private proxy", AllowLocalEnv)
	}
}
