package providers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
)

// wiringTarget is a public IP literal with a port: the pre-run destination
// check needs no DNS lookup, and the decision must receive the hostname only.
const wiringTarget = "https://93.184.216.34:8443/searchresults"

type decisionCall struct {
	targetHost string
	cookies    []*network.Cookie
	runErr     error
	refusals   []proxyRefusal
}

// stubHarvest replaces the browser run and the decision. The fake browser
// requests a refused destination through the real policy proxy, so the
// refusal list the call site reads comes from the proxy, not from the test.
func stubHarvest(t *testing.T, runErr error, html string, decide func(decisionCall) ([]*network.Cookie, error)) *[]decisionCall {
	t.Helper()
	t.Setenv(AllowLocalEnv, "")
	t.Setenv("TRVL_NO_TIER2_CDP", "")
	t.Setenv("TRVL_TIER2_CDP", "")
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
	origRun, origDecision := cdpBrowserRun, harvestDecision
	t.Cleanup(func() { cdpBrowserRun, harvestDecision = origRun, origDecision })

	cdpBrowserRun = func(_ context.Context, _, _, proxyAddr string, _ time.Duration, _ pageCapture) ([]*network.Cookie, string, error) {
		proxyURL, _ := url.Parse("http://" + proxyAddr)
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
		if resp, err := client.Get("http://0.0.0.0/consent.js"); err == nil {
			_ = resp.Body.Close()
		}
		return bookingCookies(), html, runErr
	}
	var calls []decisionCall
	harvestDecision = func(host string, cookies []*network.Cookie, err error, refusals []proxyRefusal) ([]*network.Cookie, error) {
		c := decisionCall{host, cookies, err, refusals}
		calls = append(calls, c)
		return decide(c)
	}
	return &calls
}

func requireDecisionInputs(t *testing.T, calls []decisionCall, wantRunErr error) {
	t.Helper()
	if len(calls) != 1 {
		t.Fatalf("harvest decision called %d times, want once from the call site", len(calls))
	}
	c := calls[0]
	if c.targetHost != "93.184.216.34" {
		t.Errorf("decision target host = %q, want the hostname of the target URL", c.targetHost)
	}
	if len(c.cookies) != 2 || !errors.Is(c.runErr, wantRunErr) {
		t.Errorf("decision got %d cookies, run error %v; want the browser's 2 cookies and %v", len(c.cookies), c.runErr, wantRunErr)
	}
	if len(c.refusals) != 1 || c.refusals[0].Host != "0.0.0.0" || !errors.Is(c.refusals[0].Err, ErrDestinationRefused) {
		t.Errorf("decision refusals = %+v, want the proxy's record for host 0.0.0.0", c.refusals)
	}
}

// TestRunCDPCollect_AppliesHarvestDecision: the collect runner hands the
// decision the target hostname, the browser's cookies and error, and the
// proxy's full refusal list, then returns exactly what the decision returns.
func TestRunCDPCollect_AppliesHarvestDecision(t *testing.T) {
	runErr := errors.New("page load failed")
	decided := []*network.Cookie{{Name: "decided"}}
	decidedErr := errors.New("decision error")
	calls := stubHarvest(t, runErr, "", func(decisionCall) ([]*network.Cookie, error) { return decided, decidedErr })

	got, err := runCDPCollect(context.Background(), "test-browser", wiringTarget, time.Millisecond)
	requireDecisionInputs(t, *calls, runErr)
	if !errors.Is(err, decidedErr) || len(got) != 1 || got[0].Name != "decided" {
		t.Errorf("runCDPCollect = %d cookies, %v; want the decision's result", len(got), err)
	}
}

// TestRunCDPChallenge_AppliesHarvestDecision: the challenge runner applies
// the same target-only rule and keeps the page HTML on success.
func TestRunCDPChallenge_AppliesHarvestDecision(t *testing.T) {
	decided := []*network.Cookie{{Name: "decided"}}
	calls := stubHarvest(t, nil, "<html>ok</html>", func(decisionCall) ([]*network.Cookie, error) { return decided, nil })

	got, html, err := runCDPChallenge(context.Background(), "test-browser", wiringTarget, time.Millisecond)
	requireDecisionInputs(t, *calls, nil)
	if err != nil || len(got) != 1 || got[0].Name != "decided" || html != "<html>ok</html>" {
		t.Errorf("runCDPChallenge = %d cookies, html %q, %v; want the decision's cookies and the page HTML", len(got), html, err)
	}
}
