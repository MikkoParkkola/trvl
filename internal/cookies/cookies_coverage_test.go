package cookies

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// ==========================================================================
// browserReadPageWith — exercising the Safari/Chrome branches on darwin.
// Uses a very short context timeout to avoid actually waiting for osascript.
// ==========================================================================

func TestBrowserReadPageWith_ChromeBranch(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires darwin for osascript")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	_, err := browserReadPageWith(ctx, "Google Chrome", "https://coverage-chrome.test/page", 1)
	if err == nil {
		t.Fatal("expected error from expired context")
	}
}

func TestBrowserReadPageWith_SafariBranch(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires darwin for osascript")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	_, err := browserReadPageWith(ctx, "Safari", "https://coverage-safari.test/page", 1)
	if err == nil {
		t.Fatal("expected error from expired context")
	}
}

func TestBrowserReadPageWith_SanitizesInjection(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires darwin for osascript")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// Injection attempt: quotes and backslashes should be stripped by sanitizeURL.
	_, err := browserReadPageWith(ctx, "Google Chrome", `https://coverage.test";\do shell script "whoami`, 1)
	if err == nil {
		t.Fatal("expected error from expired context")
	}
}

// ==========================================================================
// BrowserReadPage — exercising the browser iteration loop on darwin.
// Both Chrome and Safari fail (short context), hitting the final error return.
// ==========================================================================

func TestBrowserReadPage_AllBrowsersFail(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires darwin for osascript")
	}
	orig := SkipBrowserRead
	SkipBrowserRead = false
	t.Cleanup(func() { SkipBrowserRead = orig })

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	_, err := BrowserReadPage(ctx, "https://coverage-loop.test/page", 1)
	if err == nil {
		t.Fatal("expected error when all browsers fail")
	}
}

// ==========================================================================
// BrowserReadPageCached — cache write path verification.
// ==========================================================================

func TestBrowserReadPageCached_WritesToCacheOnSuccess(t *testing.T) {
	const testURL = "https://coverage-cache-write.test/page"

	browserPageCache.Lock()
	delete(browserPageCache.entries, testURL)
	browserPageCache.Unlock()

	// Simulate a successful cache write then verify read-back.
	browserPageCache.Lock()
	browserPageCache.entries[testURL] = browserCacheEntry{
		text:    "freshly cached content from browser",
		expires: time.Now().Add(10 * time.Minute),
	}
	browserPageCache.Unlock()
	t.Cleanup(func() {
		browserPageCache.Lock()
		delete(browserPageCache.entries, testURL)
		browserPageCache.Unlock()
	})

	orig := SkipBrowserRead
	SkipBrowserRead = true
	t.Cleanup(func() { SkipBrowserRead = orig })

	got, err := BrowserReadPageCached(context.Background(), testURL, 1, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "freshly cached content from browser" {
		t.Errorf("got %q, want cached content", got)
	}
}

// ==========================================================================
// IsCaptchaResponse — exercising the URL extraction edge cases.
// ==========================================================================

func TestIsCaptchaResponse_URLExtractionEdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantCap   bool
		wantEmpty bool
	}{
		{
			name:      "marker present, no closing quote after start",
			body:      `captcha-delivery.com "url":"https://no-end-quote`,
			wantCap:   true,
			wantEmpty: true,
		},
		{
			name:      "marker present, empty url value",
			body:      `captcha-delivery.com "url":""`,
			wantCap:   true,
			wantEmpty: true,
		},
		{
			name:    "marker present, valid url",
			body:    `captcha-delivery.com "url":"https://captcha.test/solve"`,
			wantCap: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isCaptcha, url := IsCaptchaResponse(403, []byte(tt.body))
			if isCaptcha != tt.wantCap {
				t.Errorf("isCaptcha = %v, want %v", isCaptcha, tt.wantCap)
			}
			if tt.wantEmpty && url != "" {
				t.Errorf("url = %q, want empty", url)
			}
			if !tt.wantEmpty && url == "" {
				t.Error("expected non-empty URL")
			}
		})
	}
}

// ==========================================================================
// OpenBrowserForAuth — cooldown exact boundary and domain-only URL.
// ==========================================================================

func TestOpenBrowserForAuth_CooldownExactBoundary(t *testing.T) {
	origNow := browserAuthNow
	origStart := browserAuthStart
	now := time.Unix(1_700_000_000, 0)
	browserAuthNow = func() time.Time { return now }
	t.Cleanup(func() {
		browserAuthNow = origNow
		browserAuthStart = origStart
		browserAuthOpened.mu.Lock()
		delete(browserAuthOpened.domains, "exact-boundary.test")
		browserAuthOpened.mu.Unlock()
	})

	calls := 0
	browserAuthStart = func(name string, args ...string) error {
		calls++
		return nil
	}

	if err := OpenBrowserForAuth("https://exact-boundary.test/page"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}

	// Advance exactly to the cooldown duration boundary.
	// Sub(last) == browserAuthCooldown which is NOT < browserAuthCooldown.
	now = now.Add(browserAuthCooldown)
	if err := OpenBrowserForAuth("https://exact-boundary.test/page"); err != nil {
		t.Fatalf("boundary call: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls at exact boundary = %d, want 2 (cooldown expired)", calls)
	}
}

func TestOpenBrowserForAuth_DomainOnlyNoSlash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("browser-opener command differs on Windows; tracked in #45")
	}
	origNow := browserAuthNow
	origStart := browserAuthStart
	now := time.Unix(1_700_000_000, 0)
	browserAuthNow = func() time.Time { return now }
	t.Cleanup(func() {
		browserAuthNow = origNow
		browserAuthStart = origStart
		browserAuthOpened.mu.Lock()
		delete(browserAuthOpened.domains, "domain-only.test")
		browserAuthOpened.mu.Unlock()
	})

	var capturedArgs []string
	browserAuthStart = func(name string, args ...string) error {
		capturedArgs = args
		return nil
	}

	url := "https://domain-only.test"
	if err := OpenBrowserForAuth(url); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(capturedArgs) == 0 || capturedArgs[0] != url {
		t.Errorf("expected URL %q in args, got %v", url, capturedArgs)
	}
}
