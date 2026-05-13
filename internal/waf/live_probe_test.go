package waf

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveProbe_Booking hits booking.com for real, captures the WAF
// interstitial, and asserts that SolveAWSWAF returns a plausibly shaped
// aws-waf-token. Gated on TRVL_TEST_LIVE_PROBES=1 and always skipped under
// -short to match the rest of the repo's live-probe conventions.
func TestLiveProbe_Booking(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live probe under -short")
	}
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("set TRVL_TEST_LIVE_PROBES=1 to run this probe")
	}

	client := &http.Client{Timeout: 20 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	target := "https://www.booking.com/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("User-Agent", defaultUA)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET booking.com: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), ".awswaf.com") {
		t.Skipf("page did not include an AWS WAF challenge (status=%d) — nothing to solve", resp.StatusCode)
	}

	cookie, err := SolveAWSWAF(ctx, client, target, string(body), &Options{
		Budget: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("SolveAWSWAF: %v", err)
	}
	if cookie.Name != "aws-waf-token" {
		t.Errorf("expected Name=aws-waf-token, got %q", cookie.Name)
	}
	if len(cookie.Value) < 20 {
		t.Errorf("token too short: %q", cookie.Value)
	}
}
