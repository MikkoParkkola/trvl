package batchexec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetWithHeaders_ForwardsHeaders is the same-package regression test for
// the Booking WAF rebuild (fix/booking-waf-token-apollo): the Booking provider
// relies on GetWithHeaders to send a real Chrome UA + Accept + the
// aws-waf-token Cookie. If those headers are not forwarded, Booking's WAF
// returns 202 and the provider silently yields zero results.
func TestGetWithHeaders_ForwardsHeaders(t *testing.T) {
	var gotUA, gotAccept, gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAccept = r.Header.Get("Accept")
		gotCookie = r.Header.Get("Cookie")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok-body"))
	}))
	defer srv.Close()

	c := NewClient()
	status, body, err := c.GetWithHeaders(context.Background(), srv.URL, map[string]string{
		"User-Agent": "Mozilla/5.0 (Macintosh) Chrome/120.0.0.0",
		"Accept":     "text/html",
		"Cookie":     "aws-waf-token=sample",
	})
	if err != nil {
		t.Fatalf("GetWithHeaders: %v", err)
	}
	if status != http.StatusOK || string(body) != "ok-body" {
		t.Fatalf("status=%d body=%q", status, body)
	}
	if gotUA != "Mozilla/5.0 (Macintosh) Chrome/120.0.0.0" {
		t.Errorf("User-Agent not forwarded: %q", gotUA)
	}
	if gotAccept != "text/html" {
		t.Errorf("Accept not forwarded: %q", gotAccept)
	}
	if gotCookie != "aws-waf-token=sample" {
		t.Errorf("Cookie not forwarded: %q", gotCookie)
	}
}
