package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/providers"
)

func seedDashboardHealthLog(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	dir := filepath.Join(tmp, ".trvl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "health.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	entry := providers.HealthEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Provider:  "kiwi",
		Operation: "search",
		Status:    "ok",
		LatencyMs: 95,
		Results:   12,
	}
	line, _ := json.Marshal(entry)
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
}

func TestHandleDashboard_RendersHTML(t *testing.T) {
	seedDashboardHealthLog(t)

	hs := NewHTTPServer(0) // no auth configured => loopback-only => served
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)

	hs.handleDashboard(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body := rr.Body.String()
	for _, want := range []string{"<!DOCTYPE html>", "trvl status", "kiwi", "healthy"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard body missing %q", want)
		}
	}
}

func TestHandleDashboard_LoopbackServesWithoutToken(t *testing.T) {
	seedDashboardHealthLog(t)

	// Loopback bind with a token configured: the dashboard is still served
	// token-free (read-only, secret-redacted, local-only — same as /health),
	// so it opens in a browser without an Authorization header.
	hs := NewHTTPServerWithOptions(HTTPServerOptions{Host: "127.0.0.1", Port: 0, Token: "secret-token"})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	hs.handleDashboard(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("loopback no-token status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "trvl status") {
		t.Error("loopback dashboard body missing title")
	}
}

func TestHandleDashboard_RemoteBindRequiresToken(t *testing.T) {
	seedDashboardHealthLog(t)

	// A non-loopback bind must require a valid read token.
	hs := NewHTTPServerWithOptions(HTTPServerOptions{Host: "0.0.0.0", Port: 0, Token: "secret-token"})

	// No token => unauthorized.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	hs.handleDashboard(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("remote no-token status = %d, want 401", rr.Code)
	}

	// Valid token => served.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	hs.handleDashboard(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("remote valid-token status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "trvl status") {
		t.Error("authorized dashboard body missing title")
	}
}
