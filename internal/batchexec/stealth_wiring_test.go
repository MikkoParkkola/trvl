package batchexec

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/stealth"
)

// captureSlog redirects slog output to a buffer for the duration of the test so
// the refusal log line can be asserted. It restores the previous default logger.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestTransportForStealth_NotRequested proves that without the stealth flag the
// normal transport is used and stealth is never engaged, regardless of allowlist.
func TestTransportForStealth_NotRequested(t *testing.T) {
	t.Setenv(stealth.AllowlistEnv, "www.google.com")
	c := NewClient()

	got, engaged := c.transportForStealth(false, FlightsURL)
	if engaged {
		t.Error("engaged = true with stealth not requested; want false")
	}
	if got != c.http {
		t.Error("transportForStealth returned non-default client when stealth not requested")
	}
}

// TestTransportForStealth_RequestedUnauthorized proves the fail-safe scope fence:
// stealth requested for a non-allowlisted host runs the normal path, engages no
// stealth, and emits exactly one refusal log line naming the host.
func TestTransportForStealth_RequestedUnauthorized(t *testing.T) {
	t.Setenv(stealth.AllowlistEnv, "") // empty => refuse by default
	buf := captureSlog(t)
	c := NewClient()

	got, engaged := c.transportForStealth(true, FlightsURL)
	if engaged {
		t.Error("engaged = true for unauthorized host; want false")
	}
	if got != c.http {
		t.Error("unauthorized host did not fall back to the normal transport")
	}
	logged := buf.String()
	if !strings.Contains(logged, "stealth not authorized for host www.google.com") {
		t.Errorf("missing refusal log line; got: %q", logged)
	}
	if strings.Count(logged, "stealth not authorized for host") != 1 {
		t.Errorf("expected exactly one refusal log line; got: %q", logged)
	}
}

// TestTransportForStealth_RequestedAuthorized proves that when stealth is
// requested AND the host is on the operator allowlist, stealth is engaged with a
// distinct (non-default), non-nil transport.
func TestTransportForStealth_RequestedAuthorized(t *testing.T) {
	t.Setenv(stealth.AllowlistEnv, ".google.com")
	buf := captureSlog(t)
	c := NewClient()

	got, engaged := c.transportForStealth(true, FlightsURL)
	if !engaged {
		t.Fatal("engaged = false for authorized host; want true")
	}
	if got == nil {
		t.Fatal("authorized stealth returned nil client")
	}
	if got == c.http {
		t.Error("authorized stealth used the default transport; want the stealth transport")
	}
	if strings.Contains(buf.String(), "stealth not authorized") {
		t.Errorf("unexpected refusal log for authorized host: %q", buf.String())
	}
}

// TestPostFormStealth_AuthorizedReachesServer proves the authorized stealth path
// is wired end-to-end through PostFormStealth: the request still completes (no
// abort) and the stealth transport is engaged. A test client reuses its redirect
// transport for stealth so the call stays offline and deterministic.
func TestPostFormStealth_AuthorizedReachesServer(t *testing.T) {
	t.Setenv(stealth.AllowlistEnv, ".google.com")
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewTestClient(srv.URL)
	status, body, err := c.PostFormStealth(context.Background(), FlightsURL, "f.req=x", true)
	if err != nil {
		t.Fatalf("PostFormStealth error: %v", err)
	}
	if status != 200 || string(body) != "ok" {
		t.Fatalf("unexpected response: status=%d body=%q", status, body)
	}
	if hits != 1 {
		t.Fatalf("expected exactly one request; got %d", hits)
	}
}

// TestPostFormStealth_UnauthorizedReachesServerNormalPath proves stealth
// requested for a non-allowlisted host does NOT abort: it runs the normal path
// and the request still reaches the server.
func TestPostFormStealth_UnauthorizedReachesServerNormalPath(t *testing.T) {
	t.Setenv(stealth.AllowlistEnv, "") // refuse by default
	buf := captureSlog(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewTestClient(srv.URL)
	status, body, err := c.PostFormStealth(context.Background(), FlightsURL, "f.req=x", true)
	if err != nil {
		t.Fatalf("PostFormStealth error: %v", err)
	}
	if status != 200 || string(body) != "ok" {
		t.Fatalf("unauthorized stealth should run normal path; status=%d body=%q", status, body)
	}
	if !strings.Contains(buf.String(), "stealth not authorized for host www.google.com") {
		t.Errorf("missing refusal log line; got: %q", buf.String())
	}
}

func TestHostFromURL(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{FlightsURL, "www.google.com"},
		{"https://example.com:8443/path", "example.com"},
		{"://bad-url", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := hostFromURL(tt.in); got != tt.want {
			t.Errorf("hostFromURL(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}

// TestGetStealth_DefaultIsByteIdenticalToGet proves GetStealth with
// stealthRequested=false is equivalent to the normal Get path.
func TestGetStealth_DefaultIsByteIdenticalToGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewTestClient(srv.URL)
	status, body, err := c.GetStealth(context.Background(), "https://www.google.com/travel/hotels", false)
	if err != nil || status != 200 || string(body) != "ok" {
		t.Fatalf("GetStealth(false) mismatch: status=%d body=%q err=%v", status, body, err)
	}
}
