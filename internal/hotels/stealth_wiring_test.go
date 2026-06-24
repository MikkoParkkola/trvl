package hotels

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/stealth"
)

func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// runHotelPageFetch drives the single-page Google Hotels fetch with the given
// options against a local test server so the stealth gate is exercised
// deterministically. The body parse outcome is irrelevant: the stealth gate is
// resolved when the HTTP request is dispatched (before parsing), so the refusal
// log (or its absence) is the assertion target.
func runHotelPageFetch(t *testing.T, opts HotelSearchOptions, body string) {
	t.Helper()
	opts.CheckIn = "2026-06-15"
	opts.CheckOut = "2026-06-18"
	opts.Guests = 2
	opts.Currency = "EUR"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()
	client := batchexec.NewTestClient(ts.URL)
	// Best-effort: errors from parsing are ignored; the gate already fired.
	_, _ = fetchHotelPageFull(t.Context(), client, "Helsinki", opts, 0, "")
}

// TestHotelStealth_DefaultOff proves Stealth=false never consults the stealth
// gate (no refusal log), even when the host is allowlisted.
func TestHotelStealth_DefaultOff(t *testing.T) {
	t.Setenv(stealth.AllowlistEnv, ".google.com")
	buf := captureSlog(t)

	runHotelPageFetch(t, HotelSearchOptions{}, strings.Repeat("x", 2000))

	if strings.Contains(buf.String(), "stealth not authorized") {
		t.Errorf("stealth refusal logged with Stealth=false; got: %q", buf.String())
	}
}

// TestHotelStealth_RequestedUnauthorized proves Stealth=true with an empty
// allowlist runs the normal path and emits the refusal log — proving opts.Stealth
// is genuinely plumbed into the hotel fetch path.
func TestHotelStealth_RequestedUnauthorized(t *testing.T) {
	t.Setenv(stealth.AllowlistEnv, "") // refuse by default
	buf := captureSlog(t)

	runHotelPageFetch(t, HotelSearchOptions{Stealth: true}, strings.Repeat("x", 2000))

	if !strings.Contains(buf.String(), "stealth not authorized for host www.google.com") {
		t.Errorf("expected refusal log for unauthorized host; got: %q", buf.String())
	}
}

// TestHotelStealth_RequestedAuthorized proves Stealth=true with the host
// allowlisted does NOT log a refusal — the gate authorized stealth on the fetch.
func TestHotelStealth_RequestedAuthorized(t *testing.T) {
	t.Setenv(stealth.AllowlistEnv, ".google.com")
	buf := captureSlog(t)

	runHotelPageFetch(t, HotelSearchOptions{Stealth: true}, strings.Repeat("x", 2000))

	if strings.Contains(buf.String(), "stealth not authorized") {
		t.Errorf("unexpected refusal log for authorized host; got: %q", buf.String())
	}
}
