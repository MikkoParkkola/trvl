package cookies

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestExtractViaNab_BoundedAndDetached is the regression test for the sibling of
// issue #507 found in the same audit.
//
// Cookie extraction is reached from ordinary hotel and rail searches — a
// Booking.com WAF challenge triggers it — and unlike the rail browser fallbacks
// it sits behind no opt-in. It shells out to nab, which needs macOS Keychain
// access to decrypt Chrome or Brave cookies and can therefore raise a
// permission prompt of its own. Unbounded, a wedged nab stalled the search
// forever and the next search started another one: the exact shape of #507.
//
// The fake nab hangs for 30s. A correct build gives up at nabCookieTimeout and
// returns no cookies rather than blocking the caller.
func TestExtractViaNab_BoundedAndDetached(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "nab")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write fake nab: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	start := time.Now()
	got := extractViaNab("brave", "example.com")
	elapsed := time.Since(start)

	if got != "" {
		t.Fatalf("expected no cookies from a hung helper, got %q", got)
	}
	if elapsed > nabCookieTimeout+3*time.Second {
		t.Fatalf("cookie extraction took %v; a search must not wait on a wedged helper (bound is %v)", elapsed, nabCookieTimeout)
	}
}
