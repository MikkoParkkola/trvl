package nab

import (
	"context"
	"errors"
	"os/exec"
	"testing"
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
	t.Setenv(declineEnv, "1")

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
	t.Setenv(declineEnv, "")

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

func TestBrowserCookiesDeclinedParsing(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false}, {"0", false}, {"false", false}, {"FALSE", false},
		{"1", true}, {"true", true}, {"yes", true}, {"no", true}, {"anything", true},
	} {
		t.Setenv(declineEnv, tc.value)
		if got := BrowserCookiesDeclined(); got != tc.want {
			t.Errorf("BrowserCookiesDeclined() with %q = %v, want %v", tc.value, got, tc.want)
		}
	}
}
