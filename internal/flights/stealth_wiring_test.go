package flights

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/stealth"
)

// captureSlog redirects slog to a buffer for the duration of the test and
// restores the previous default logger afterwards.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// runGoogleLeg drives just the Google Flights leg with the given options so the
// stealth gate is exercised deterministically without provider fan-out.
func runGoogleLeg(t *testing.T, opts SearchOptions) {
	t.Helper()
	body := makeFlightResponseBody(t)
	ts := flightsTestServer(t, 200, body)
	defer ts.Close()
	client := batchexec.NewTestClient(ts.URL)
	if _, err := searchGoogleFlightsWithClient(t.Context(), client, "HEL", "NRT", "2026-06-15", opts); err != nil {
		t.Fatalf("google leg error: %v", err)
	}
}

// TestFlightStealth_DefaultOff proves that with Stealth=false the fetch runs the
// normal path and never logs a stealth refusal — i.e. stealth is never even
// consulted (default off).
func TestFlightStealth_DefaultOff(t *testing.T) {
	t.Setenv(stealth.AllowlistEnv, ".google.com") // allowlisted, but flag is off
	buf := captureSlog(t)

	runGoogleLeg(t, SearchOptions{}) // Stealth defaults to false

	if strings.Contains(buf.String(), "stealth not authorized") {
		t.Errorf("stealth refusal logged with Stealth=false; got: %q", buf.String())
	}
}

// TestFlightStealth_RequestedUnauthorized proves Stealth=true for the Google host
// when the allowlist is empty runs the normal path and emits the refusal log —
// proving the flag is genuinely plumbed into the fetch (the prior failure was a
// flag that never reached the fetch path).
func TestFlightStealth_RequestedUnauthorized(t *testing.T) {
	t.Setenv(stealth.AllowlistEnv, "") // refuse by default
	buf := captureSlog(t)

	runGoogleLeg(t, SearchOptions{Stealth: true})

	if !strings.Contains(buf.String(), "stealth not authorized for host www.google.com") {
		t.Errorf("expected refusal log for unauthorized host; got: %q", buf.String())
	}
}

// TestFlightStealth_RequestedAuthorized proves Stealth=true with the Google host
// allowlisted does NOT log a refusal — the gate authorized the host so stealth
// engaged on the fetch path.
func TestFlightStealth_RequestedAuthorized(t *testing.T) {
	t.Setenv(stealth.AllowlistEnv, ".google.com")
	buf := captureSlog(t)

	runGoogleLeg(t, SearchOptions{Stealth: true})

	if strings.Contains(buf.String(), "stealth not authorized") {
		t.Errorf("unexpected refusal log for authorized host; got: %q", buf.String())
	}
}
