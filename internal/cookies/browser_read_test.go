package cookies

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestSkipBrowserRead(t *testing.T) {
	orig := SkipBrowserRead
	SkipBrowserRead = true
	defer func() { SkipBrowserRead = orig }()

	_, err := BrowserReadPage(context.Background(), "https://example.com", 1)
	if err == nil {
		t.Fatal("expected error when SkipBrowserRead=true, got nil")
	}
}

func TestBrowserReadPageCachedHit(t *testing.T) {
	// Populate the cache manually then verify we get the cached value back
	// without BrowserReadPage being called (which would fail in CI).
	const testURL = "https://example.com/cached-test"
	const testText = "cached page content"

	browserPageCache.Lock()
	browserPageCache.entries[testURL] = browserCacheEntry{
		text:    testText,
		expires: time.Now().Add(5 * time.Minute),
	}
	browserPageCache.Unlock()
	defer func() {
		browserPageCache.Lock()
		delete(browserPageCache.entries, testURL)
		browserPageCache.Unlock()
	}()

	// Also ensure SkipBrowserRead=true so any accidental fallthrough to
	// the real BrowserReadPage fails loudly rather than opening a browser.
	orig := SkipBrowserRead
	SkipBrowserRead = true
	defer func() { SkipBrowserRead = orig }()

	got, err := BrowserReadPageCached(context.Background(), testURL, 1, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error from cache hit: %v", err)
	}
	if got != testText {
		t.Errorf("got %q, want %q", got, testText)
	}
}

func TestBrowserReadPage_NonDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("test only runs on non-darwin")
	}
	orig := SkipBrowserRead
	SkipBrowserRead = false
	defer func() { SkipBrowserRead = orig }()

	_, err := BrowserReadPage(context.Background(), "https://example.com", 1)
	if err == nil {
		t.Fatal("expected error on non-macOS platform")
	}
}

func TestBrowserReadPage_DefaultWaitSeconds(t *testing.T) {
	// With SkipBrowserRead=true, the function returns before reaching waitSeconds logic.
	// This test verifies the SkipBrowserRead short-circuit.
	orig := SkipBrowserRead
	SkipBrowserRead = true
	defer func() { SkipBrowserRead = orig }()

	_, err := BrowserReadPage(context.Background(), "https://example.com", 0)
	if err == nil {
		t.Fatal("expected error when SkipBrowserRead=true")
	}
}

func TestWaitForBrowserRenderHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := waitForBrowserRender(ctx, time.Second)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("waitForBrowserRender ignored cancellation and slept too long")
	}
}

func TestURLSanitization(t *testing.T) {
	// Verify that browserReadPageWith sanitizes quotes and backslashes.
	// We can't call it directly without osascript, but we can verify the
	// sanitization logic by testing the strings package behavior.
	tests := []struct {
		input string
		clean string
	}{
		{`https://example.com"`, `https://example.com`},
		{`https://example.com\`, `https://example.com`},
		{`https://example.com" ; do shell script "whoami`, `https://example.com ; do shell script whoami`},
		{`https://normal.com/path?q=1`, `https://normal.com/path?q=1`},
	}

	for _, tt := range tests {
		got := sanitizeURL(tt.input)
		if got != tt.clean {
			t.Errorf("sanitizeURL(%q) = %q, want %q", tt.input, got, tt.clean)
		}
	}
}

func TestBrowserReadPageCachedExpiry(t *testing.T) {
	// Populate the cache with an already-expired entry.
	const testURL = "https://example.com/expired-test"
	const staleText = "stale content"

	browserPageCache.Lock()
	browserPageCache.entries[testURL] = browserCacheEntry{
		text:    staleText,
		expires: time.Now().Add(-1 * time.Second), // already expired
	}
	browserPageCache.Unlock()
	defer func() {
		browserPageCache.Lock()
		delete(browserPageCache.entries, testURL)
		browserPageCache.Unlock()
	}()

	// SkipBrowserRead=true ensures BrowserReadPage returns an error (rather than
	// opening a browser) so we can confirm the cache was bypassed.
	orig := SkipBrowserRead
	SkipBrowserRead = true
	defer func() { SkipBrowserRead = orig }()

	_, err := BrowserReadPageCached(context.Background(), testURL, 1, 5*time.Minute)
	if err == nil {
		t.Fatal("expected error because cache entry expired and browser read is disabled, got nil")
	}
}
