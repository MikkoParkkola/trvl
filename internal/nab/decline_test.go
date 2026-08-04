package nab

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// TestFetchRefusesWhenBrowserCookiesDeclined covers the bypass a third
// adversarial review found in #521.
//
// Every Fetch passes `--cookies <browser>` (defaulting to "auto") to the nab
// binary, which reads the user's browser cookie stores. The rail providers call
// this as a fallback AFTER the gated in-process extractor has already refused --
// internal/ground/trainline.go, eurostar.go and sncf.go all do -- so declining
// browser cookie reads stopped the reader trvl controls and left running the
// helper that does the same thing in another process. The README promised "no
// nab, no Keychain, nothing", and this was the "no nab" part being untrue.
//
// The gate is asserted at the command seam: if it leaks, commandContext runs and
// the test sees it, rather than the test passing because no binary was found.
func TestFetchRefusesWhenBrowserCookiesDeclined(t *testing.T) {
	t.Setenv(consent.CookiesEnv, "1")

	invoked := false
	prev := commandContext
	commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		invoked = true
		return prev(ctx, name, args...)
	}
	defer func() { commandContext = prev }()

	c := &Client{path: "/nonexistent/nab"}

	if _, err := c.Fetch(context.Background(), "https://www.thetrainline.com/", FetchOptions{}); !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("Fetch: err = %v, want ErrNotAvailable", err)
	}
	if _, err := c.FetchHTML(context.Background(), "https://www.thetrainline.com/", "auto"); !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("FetchHTML: err = %v, want ErrNotAvailable", err)
	}
	if invoked {
		t.Fatal("nab was invoked despite an explicit decline")
	}
}

// TestFetchRunsWithoutADecline is the other half: the gate must refuse a decline
// and only a decline, so an absent opt-out still reaches the command seam.
func TestFetchRunsWithoutADecline(t *testing.T) {
	t.Setenv(consent.CookiesEnv, "")

	invoked := false
	prev := commandContext
	commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		invoked = true
		return prev(ctx, name, args...)
	}
	defer func() { commandContext = prev }()

	c := &Client{path: "/nonexistent/nab"}
	_, _ = c.Fetch(context.Background(), "https://www.thetrainline.com/", FetchOptions{})

	if !invoked {
		t.Fatal("nab was not invoked without a decline; the gate refuses more than it should")
	}
}

// TestDeclineLogDoesNotLeakTheURL pins the fix for a defect adversarial review
// found in the debug line added by the consent refactor: it logged the whole
// URL. This codebase's URLs carry the user's journey in their query parameters
// -- dates, origin, destination -- so the line that exists to explain a privacy
// decision was itself disclosing what the user was looking up. To a debug log,
// but that is where a privacy control least belongs.
//
// Three rounds then failed to reduce the URL to something provably harmless:
// the query string, then the IPv6 zone identifier that survives in url.URL.Host,
// then the hostname itself, which is only non-sensitive because this repo's
// callers happen to pass hardcoded provider hosts. The line now logs no part of
// the URL at all, so this test asserts absence of the host as well.
//
// It asserts on the emitted record rather than on a helper, because a helper
// unit test passes whether or not the call site uses it -- the same
// caller-instead-of-seam mistake the four earlier rounds each found.
func TestDeclineLogDoesNotLeakTheURL(t *testing.T) {
	t.Setenv(consent.CookiesEnv, "1")

	var buf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prevLogger)

	const (
		host      = "www.thetrainline.com"
		sensitive = "from=PARIS&to=BERLIN&date=2026-08-14&passenger=M+Parkkola"
	)
	c := &Client{path: "/nonexistent/nab"}
	_, _ = c.Fetch(context.Background(), "https://"+host+"/search?"+sensitive, FetchOptions{})

	logged := buf.String()
	// Without this the rest is vacuous: a line that is never emitted leaks nothing.
	// Anchored on the variable name, which is the line's whole actionable content.
	if !strings.Contains(logged, consent.CookiesEnv) {
		t.Fatalf("expected the decline to name the variable that refused it, got %q", logged)
	}
	for _, leak := range []string{sensitive, "PARIS", "BERLIN", "2026-08-14", "Parkkola", "/search", host} {
		if strings.Contains(logged, leak) {
			t.Errorf("decline log leaked %q; full record: %s", leak, logged)
		}
	}
}

func TestBrowserCookiesDeclinedParsing(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false}, {"0", false}, {"false", false}, {"FALSE", false},
		{"1", true}, {"true", true}, {"yes", true}, {"no", true}, {"anything", true},
	} {
		t.Setenv(consent.CookiesEnv, tc.value)
		if got := BrowserCookiesDeclined(); got != tc.want {
			t.Errorf("BrowserCookiesDeclined() with %q = %v, want %v", tc.value, got, tc.want)
		}
	}
}
