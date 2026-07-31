package watch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestFireWebhook_DoesNotFollowRedirect: round 25 (GPT second-opinion review,
// 2026-07-31). Before this round, CheckRedirect only capped hop count, so a
// receiver could 302/307/308 the notifier to an attacker-controlled host and
// (a) receive a Referer header built from the FULL original URL -- exactly
// where Slack/Discord-style webhook tokens live in the path/query -- and
// (b) on 307/308, receive a replayed copy of the JSON payload body. Neither
// server test cases below the round-25 fix should ever see a second request.
//
// httptest servers bind loopback, which newSafeWebhookClient's SSRF dialer
// correctly refuses to dial -- so this test swaps in a client that shares
// production's CheckRedirect policy but not its dial-time SSRF guard (that
// guard has its own dedicated tests). It exercises the redirect-refusal
// behavior specifically.
func TestFireWebhook_DoesNotFollowRedirect(t *testing.T) {
	var captureHits int
	var capturedReferer, capturedBody string
	capture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captureHits++
		capturedReferer = r.Header.Get("Referer")
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		capturedBody = string(buf[:n])
		w.WriteHeader(http.StatusOK)
	}))
	defer capture.Close()

	// origin carries a bearer-style token in its own path/query -- what a
	// Referer leak would disclose to capture -- and redirects every request
	// there with a 307 (which also replays the POST body per HTTP semantics).
	var originHits int
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits++
		http.Redirect(w, r, capture.URL+"/webhook-relay", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	// Built from the SAME webhookCheckRedirect func value newSafeWebhookClient
	// uses in production, not a hand-copied duplicate -- if the production
	// policy ever regresses (e.g. back to a hop-count cap), this test breaks
	// with it instead of silently testing a stale copy (Grok second-opinion
	// review, round 25, optional finding 3). The SSRF dial-time guard itself
	// is exercised by its own dedicated tests; loopback httptest servers
	// would fail that guard, so it's intentionally not part of this client.
	oldClient := webhookHTTPClient
	webhookHTTPClient = &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: webhookCheckRedirect,
	}
	t.Cleanup(func() { webhookHTTPClient = oldClient })

	result := CheckResult{
		Watch: Watch{
			ID:         "w-round25",
			WebhookURL: origin.URL + "/hook?token=super-secret-webhook-token",
		},
		NewPrice:  100,
		PrevPrice: 200,
		Currency:  "EUR",
		PriceDrop: -100,
	}

	fireWebhook(context.Background(), result)

	if originHits != 1 {
		t.Fatalf("origin should receive exactly the initial POST, got %d hits", originHits)
	}
	if captureHits != 0 {
		t.Fatalf("redirect target must never be contacted: got %d hits, referer=%q body=%q", captureHits, capturedReferer, capturedBody)
	}
}
