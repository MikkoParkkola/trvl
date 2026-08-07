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
	"time"

	"github.com/chromedp/cdproto/network"
)

func publicTestLookup(context.Context, string) ([]net.IP, error) {
	return []net.IP{net.ParseIP("93.184.216.34")}, nil
}

func TestResolveChallengeRefusesBeforeBrowserRunner(t *testing.T) {
	t.Setenv(AllowLocalEnv, "")
	t.Setenv("TRVL_NO_TIER2_CDP", "")
	t.Setenv("TRVL_TIER2_CDP", "")

	previous := cdpChallengeRunner
	var calls atomic.Int64
	cdpChallengeRunner = func(context.Context, string, string, time.Duration) ([]*network.Cookie, string, error) {
		calls.Add(1)
		return nil, "", nil
	}
	t.Cleanup(func() { cdpChallengeRunner = previous })

	_, err := ResolveChallenge(context.Background(), "http://127.0.0.1:8080/private", WithTier2ExecPath("test-browser"))
	if !errors.Is(err, ErrDestinationRefused) {
		t.Fatalf("ResolveChallenge failed with %v, want ErrDestinationRefused", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("browser runner called %d times for a refused destination", calls.Load())
	}
}

func TestResolveChallengeHonoursLocalProviderOptIn(t *testing.T) {
	t.Setenv(AllowLocalEnv, "1")
	t.Setenv("TRVL_NO_TIER2_CDP", "")
	t.Setenv("TRVL_TIER2_CDP", "")

	previous := cdpChallengeRunner
	var calls atomic.Int64
	cdpChallengeRunner = func(context.Context, string, string, time.Duration) ([]*network.Cookie, string, error) {
		calls.Add(1)
		return nil, "<html></html>", nil
	}
	t.Cleanup(func() { cdpChallengeRunner = previous })

	result, err := ResolveChallenge(context.Background(), "http://127.0.0.1:8080/local", WithTier2ExecPath("test-browser"))
	if err != nil {
		t.Fatalf("ResolveChallenge with local opt-in: %v", err)
	}
	if result.Status != ChallengeCleared || calls.Load() != 1 {
		t.Fatalf("status=%v runner calls=%d, want cleared and one call", result.Status, calls.Load())
	}
}

func TestBrowserPolicyProxyRefusesRedirectToLoopback(t *testing.T) {
	t.Setenv(AllowLocalEnv, "")

	var witnessHits atomic.Int64
	witness := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		witnessHits.Add(1)
	}))
	defer witness.Close()

	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, witness.URL, http.StatusFound)
	}))
	defer public.Close()
	publicAddress := public.Listener.Addr().String()

	lookup := func(_ context.Context, host string) ([]net.IP, error) {
		if host == "public.example" {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		}
		return net.DefaultResolver.LookupIP(context.Background(), "ip", host)
	}
	dial := func(ctx context.Context, networkName, address string) (net.Conn, error) {
		if address == "93.184.216.34:80" {
			return (&net.Dialer{}).DialContext(ctx, networkName, publicAddress)
		}
		return (&net.Dialer{Control: dialControl}).DialContext(ctx, networkName, address)
	}
	policyProxy, err := startBrowserPolicyProxy(lookup, dial)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = policyProxy.Close() }()

	proxyURL, err := url.Parse("http://" + policyProxy.Address())
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := client.Get("http://public.example/start")
	if err != nil {
		t.Fatalf("request through browser policy proxy: %v", err)
	}
	_ = resp.Body.Close()
	if witnessHits.Load() != 0 {
		t.Fatalf("loopback redirect reached witness %d times", witnessHits.Load())
	}
	if refusal := policyProxy.Refusal(); !errors.Is(refusal, ErrDestinationRefused) {
		t.Fatalf("proxy refusal = %v, want ErrDestinationRefused", refusal)
	}
}
