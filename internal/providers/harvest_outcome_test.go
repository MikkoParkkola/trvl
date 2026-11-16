package providers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/network"
)

// sinkholeLookup answers every host with 0.0.0.0, what a DNS-level ad or
// tracker blocker returns for hosts such as cdn.cookielaw.org.
func sinkholeLookup(context.Context, string) ([]net.IP, error) {
	return []net.IP{net.IPv4zero}, nil
}

// realRefusal produces the policy error the proxy records for host, built by
// the real pinHTTPURL so assertions about its text are not vacuous.
func realRefusal(t *testing.T, host string) proxyRefusal {
	t.Helper()
	t.Setenv(AllowLocalEnv, "")
	_, _, err := pinHTTPURL(context.Background(), &url.URL{Scheme: "https", Host: host}, sinkholeLookup)
	if !errors.Is(err, ErrDestinationRefused) {
		t.Fatalf("pinHTTPURL(%s) = %v, want ErrDestinationRefused", host, err)
	}
	if !strings.Contains(err.Error(), AllowLocalEnv) {
		t.Fatalf("precondition: real refusal %q no longer carries the %s hint", err, AllowLocalEnv)
	}
	return proxyRefusal{Host: strings.Split(host, ":")[0], Err: err}
}

func bookingCookies() []*network.Cookie {
	return []*network.Cookie{
		{Name: "aws-waf-token", Value: "tok", Domain: ".booking.com"},
		{Name: "bkng", Value: "sess", Domain: ".booking.com"},
	}
}

// TestHarvestOutcome_ThirdPartyRefusalKeepsCookies: Booking.com's page pulls
// a consent script from cdn.cookielaw.org. A sinkholed third-party host is
// not the harvest target, so a run that produced the WAF token succeeds.
func TestHarvestOutcome_ThirdPartyRefusalKeepsCookies(t *testing.T) {
	refusals := []proxyRefusal{realRefusal(t, "cdn.cookielaw.org")}

	got, err := harvestOutcome("www.booking.com", bookingCookies(), nil, refusals)
	if err != nil {
		t.Fatalf("third-party refusal failed the harvest: %v", err)
	}
	var token bool
	for _, c := range got {
		if c.Name == "aws-waf-token" {
			token = true
		}
	}
	if len(got) != 2 || !token {
		t.Fatalf("cookies = %d (aws-waf-token present: %v), want both harvested cookies", len(got), token)
	}
}

// TestHarvestOutcome_TargetRefusalFails: when the target host itself was
// refused, the harvest fails, whatever port or case the record carries. The
// error names the host and keeps ErrDestinationRefused, but no longer tells
// the user to set TRVL_ALLOW_LOCAL_PROVIDERS=1: a sinkholed public host is
// not a local provider, and that switch also unlocks private destinations.
func TestHarvestOutcome_TargetRefusalFails(t *testing.T) {
	for _, recorded := range []string{"www.booking.com", "www.booking.com:443", "WWW.Booking.com"} {
		t.Run(recorded, func(t *testing.T) {
			refusal := realRefusal(t, "www.booking.com")
			refusal.Host = recorded
			refusals := []proxyRefusal{realRefusal(t, "cdn.cookielaw.org"), refusal}

			got, err := harvestOutcome("www.booking.com", bookingCookies(), nil, refusals)
			if err == nil {
				t.Fatalf("target-host refusal returned %d cookies and no error", len(got))
			}
			if got != nil {
				t.Errorf("cookies = %d, want none on a failed harvest", len(got))
			}
			if !errors.Is(err, ErrDestinationRefused) {
				t.Errorf("err = %q, want it to wrap ErrDestinationRefused", err)
			}
			if !strings.Contains(err.Error(), "www.booking.com") {
				t.Errorf("err = %q, want it to name the refused target host", err)
			}
			if strings.Contains(err.Error(), AllowLocalEnv) {
				t.Errorf("err = %q still suggests %s", err, AllowLocalEnv)
			}
		})
	}
}

// TestHarvestOutcome_RunErrorCarriesRefusals: a browser run error is the
// failure; refusals are context on it, not a replacement for it.
func TestHarvestOutcome_RunErrorCarriesRefusals(t *testing.T) {
	runErr := errors.New("chromedp: page load failed")
	refusals := []proxyRefusal{realRefusal(t, "cdn.cookielaw.org")}

	got, err := harvestOutcome("www.booking.com", bookingCookies(), runErr, refusals)
	if err == nil || got != nil {
		t.Fatalf("run error: got %d cookies, err %v; want no cookies and an error", len(got), err)
	}
	if !errors.Is(err, runErr) {
		t.Errorf("err = %q does not wrap the run error", err)
	}
	if !strings.Contains(err.Error(), "cdn.cookielaw.org") {
		t.Errorf("err = %q, want the refused hosts as context", err)
	}
	if strings.Contains(err.Error(), AllowLocalEnv) {
		t.Errorf("err = %q still suggests %s", err, AllowLocalEnv)
	}
}

// TestHarvestOutcome_NoRefusalsPassesThrough guards the plain paths.
func TestHarvestOutcome_NoRefusalsPassesThrough(t *testing.T) {
	got, err := harvestOutcome("www.booking.com", bookingCookies(), nil, nil)
	if err != nil || len(got) != 2 {
		t.Fatalf("clean run: got %d cookies, err %v", len(got), err)
	}
	runErr := errors.New("boom")
	if _, err := harvestOutcome("www.booking.com", nil, runErr, nil); !errors.Is(err, runErr) {
		t.Fatalf("run error without refusals = %v, want the run error", err)
	}
}

// TestBrowserPolicyProxyRecordsEveryRefusedHost drives the real proxy with a
// sinkhole resolver. Every refused host is recorded once, by hostname, over
// both CONNECT and plain HTTP, and the list stops at maxProxyRefusals. No
// request leaves the machine: every host is refused before any dial.
func TestBrowserPolicyProxyRecordsEveryRefusedHost(t *testing.T) {
	t.Setenv(AllowLocalEnv, "")
	dial := func(context.Context, string, string) (net.Conn, error) {
		t.Error("policy proxy dialed a refused destination")
		return nil, errors.New("dial disabled in test")
	}
	policyProxy, err := startBrowserPolicyProxy(sinkholeLookup, dial)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = policyProxy.Close() }()
	proxyURL, err := url.Parse("http://" + policyProxy.Address())
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	get := func(target string) {
		if resp, err := client.Get(target); err == nil {
			_ = resp.Body.Close()
		}
	}

	get("https://cdn.cookielaw.org/consent.js") // CONNECT
	get("https://cdn.cookielaw.org/again.js")   // duplicate host
	get("http://tracker.example/pixel")         // plain HTTP
	get("http://tracker.example/pixel2")        // duplicate host

	refusals := policyProxy.Refusals()
	count := map[string]int{}
	for _, r := range refusals {
		count[r.Host]++
		if !errors.Is(r.Err, ErrDestinationRefused) {
			t.Errorf("refusal for %q = %v, want ErrDestinationRefused", r.Host, r.Err)
		}
	}
	for _, host := range []string{"cdn.cookielaw.org", "tracker.example"} {
		if count[host] != 1 {
			t.Errorf("host %q recorded %d times, want exactly once (records: %+v)", host, count[host], refusals)
		}
	}

	for i := range maxProxyRefusals + 8 {
		get(fmt.Sprintf("http://h%d.example/", i))
	}
	if n := len(policyProxy.Refusals()); n != maxProxyRefusals {
		t.Errorf("recorded %d refusals after %d distinct hosts, want the cap %d", n, maxProxyRefusals+10, maxProxyRefusals)
	}
	kept := map[string]bool{}
	for _, r := range policyProxy.Refusals() {
		kept[r.Host] = true
	}
	for _, host := range []string{"cdn.cookielaw.org", "tracker.example", "h0.example"} {
		if !kept[host] {
			t.Errorf("cap dropped early host %q; the first refusals must be kept", host)
		}
	}
	if kept[fmt.Sprintf("h%d.example", maxProxyRefusals+7)] {
		t.Errorf("cap kept the last host instead of the earliest ones")
	}
}

// TestHarvestOutcome_TargetRefusalWinsOverRunError: a refused target usually
// also breaks the page load. The target refusal is the cause, so it must be
// the error (ErrDestinationRefused) with the run error kept as context.
func TestHarvestOutcome_TargetRefusalWinsOverRunError(t *testing.T) {
	runErr := errors.New("chromedp: net::ERR_TUNNEL_CONNECTION_FAILED")
	refusals := []proxyRefusal{realRefusal(t, "cdn.cookielaw.org"), realRefusal(t, "www.booking.com")}

	got, err := harvestOutcome("www.booking.com", bookingCookies(), runErr, refusals)
	if got != nil || !errors.Is(err, ErrDestinationRefused) {
		t.Fatalf("got %d cookies, err %v; want no cookies and ErrDestinationRefused", len(got), err)
	}
	if !errors.Is(err, runErr) || !strings.Contains(err.Error(), "www.booking.com") {
		t.Errorf("err = %q, want the target host named and the run error kept", err)
	}
	if strings.Contains(err.Error(), AllowLocalEnv) {
		t.Errorf("err = %q still suggests %s", err, AllowLocalEnv)
	}
}

// TestBrowserPolicyProxyAlwaysKeepsTargetRefusal: a page can fill the cap
// with refused trackers before the target itself is refused. The target's
// refusal decides the harvest, so it is kept past the cap.
func TestBrowserPolicyProxyAlwaysKeepsTargetRefusal(t *testing.T) {
	t.Setenv(AllowLocalEnv, "")
	policyProxy, err := startBrowserPolicyProxy(sinkholeLookup, func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("dial disabled in test")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = policyProxy.Close() }()
	policyProxy.keepHost("www.booking.com")
	proxyURL, _ := url.Parse("http://" + policyProxy.Address())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	for i := range maxProxyRefusals + 4 {
		if resp, err := client.Get(fmt.Sprintf("http://t%d.example/", i)); err == nil {
			_ = resp.Body.Close()
		}
	}
	if resp, err := client.Get("http://www.booking.com/searchresults"); err == nil {
		_ = resp.Body.Close()
	}

	var kept bool
	for _, r := range policyProxy.Refusals() {
		kept = kept || r.Host == "www.booking.com"
	}
	if !kept {
		t.Fatalf("target refusal dropped by the cap; refusals = %d", len(policyProxy.Refusals()))
	}
}
