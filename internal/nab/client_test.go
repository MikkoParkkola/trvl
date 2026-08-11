package nab

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// skipOnWindows short-circuits tests that rely on a POSIX-shell mock nab
// binary (#!/bin/sh). The mock script is not executable on Windows and the
// CLI contract tested here is exercised identically on Linux and macOS.
func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-shell mock nab binary is not portable to Windows")
	}
}

func TestNewReturnsErrNotAvailableWhenNabMissing(t *testing.T) {
	origLookPath := lookPath
	lookPath = func(string) (string, error) {
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() {
		lookPath = origLookPath
	})

	client, err := New()
	if client != nil {
		t.Fatalf("client = %#v, want nil", client)
	}
	if !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("err = %v, want ErrNotAvailable", err)
	}
}

func TestClientFetchHTMLDefaultsToAutoCookies(t *testing.T) {
	skipOnWindows(t)
	script := writeMockNab(t, `#!/bin/sh
[ "$1" = "fetch" ] || { echo "bad subcommand" >&2; exit 11; }
[ "$2" = "https://example.com" ] || { echo "bad url" >&2; exit 12; }
[ "$3" = "--format" ] || { echo "missing --format" >&2; exit 13; }
[ "$4" = "json" ] || { echo "missing json format" >&2; exit 14; }
[ "$5" = "--raw-html" ] || { echo "missing --raw-html" >&2; exit 15; }
[ "$6" = "--cookies" ] || { echo "missing --cookies" >&2; exit 16; }
[ "$7" = "auto" ] || { echo "expected auto cookies" >&2; exit 17; }
[ "$8" = "--no-save" ] || { echo "missing --no-save" >&2; exit 18; }
printf '%s' '{"status":200,"markdown":"<html>ok</html>"}'
`)

	client := &Client{path: script}
	html, err := client.FetchHTML(context.Background(), "https://example.com", "")
	if err != nil {
		t.Fatalf("FetchHTML returned error: %v", err)
	}
	if got := string(html); got != "<html>ok</html>" {
		t.Fatalf("html = %q, want %q", got, "<html>ok</html>")
	}
}

func TestClientFetchHTMLRejectsNon200(t *testing.T) {
	skipOnWindows(t)
	script := writeMockNab(t, `#!/bin/sh
printf '%s' '{"status":403,"markdown":"blocked"}'
`)

	client := &Client{path: script}
	_, err := client.FetchHTML(context.Background(), "https://example.com", "auto")
	if err == nil || !strings.Contains(err.Error(), "status 403") {
		t.Fatalf("err = %v, want status 403 error", err)
	}
}

func TestClientFetchSupportsMethodBodyAndHeaders(t *testing.T) {
	skipOnWindows(t)
	script := writeMockNab(t, `#!/bin/sh
[ "$1" = "fetch" ] || { echo "bad subcommand" >&2; exit 11; }
[ "$2" = "https://api.example.com/search" ] || { echo "bad url" >&2; exit 12; }
[ "$3" = "--format" ] || { echo "missing --format" >&2; exit 13; }
[ "$4" = "json" ] || { echo "missing json format" >&2; exit 14; }
[ "$5" = "--raw-html" ] || { echo "missing --raw-html" >&2; exit 15; }
[ "$6" = "--cookies" ] || { echo "missing --cookies" >&2; exit 16; }
[ "$7" = "auto" ] || { echo "expected auto cookies" >&2; exit 17; }
[ "$8" = "--no-save" ] || { echo "missing --no-save" >&2; exit 18; }
[ "$9" = "-X" ] || { echo "missing -X" >&2; exit 19; }
[ "${10}" = "POST" ] || { echo "missing POST method" >&2; exit 20; }
[ "${11}" = "-d" ] || { echo "missing -d" >&2; exit 21; }
[ "${12}" = '{"ping":true}' ] || { echo "missing request body" >&2; exit 22; }
[ "${13}" = "--add-header" ] || { echo "missing first header flag" >&2; exit 23; }
[ "${14}" = "Content-Type: application/json" ] || { echo "missing content-type header" >&2; exit 24; }
[ "${15}" = "--add-header" ] || { echo "missing second header flag" >&2; exit 25; }
[ "${16}" = "X-Test: 1" ] || { echo "missing x-test header" >&2; exit 26; }
printf '%s' '{"status":200,"markdown":"{\"ok\":true}"}'
`)

	client := &Client{path: script}
	body, err := client.Fetch(context.Background(), "https://api.example.com/search", FetchOptions{
		Method: "POST",
		Body:   `{"ping":true}`,
		Headers: []string{
			"Content-Type: application/json",
			"X-Test: 1",
		},
	})
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if got := string(body); got != `{"ok":true}` {
		t.Fatalf("body = %q, want %q", got, `{"ok":true}`)
	}
}

func TestClientFetchForbidsKeychainInteractionInChild(t *testing.T) {
	skipOnWindows(t)
	t.Setenv("NAB_KEYCHAIN_INTERACTION", "allow")
	script := writeMockNab(t, `#!/bin/sh
[ "$NAB_KEYCHAIN_INTERACTION" = "never" ] || { echo "keychain interaction was not disabled" >&2; exit 31; }
printf '%s' '{"status":200,"markdown":"non-interactive"}'
`)

	client := &Client{path: script}
	body, err := client.Fetch(context.Background(), "https://example.com", FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if got := string(body); got != "non-interactive" {
		t.Fatalf("body = %q, want non-interactive", got)
	}
}

func writeMockNab(t *testing.T, script string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "nab")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write mock nab: %v", err)
	}
	return path
}
