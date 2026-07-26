package cookies

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"testing"
	"time"
)

// ===========================================================================
// BrowserReadPage — exercising non-SkipBrowserRead path on non-darwin
// ===========================================================================

func TestBrowserReadPage_NonDarwinNotSkipped(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("test only runs on non-darwin")
	}
	orig := SkipBrowserRead
	SkipBrowserRead = false
	t.Cleanup(func() { SkipBrowserRead = orig })

	_, err := BrowserReadPage(context.Background(), "https://example.com", 2)
	if err == nil {
		t.Fatal("expected error on non-macOS platform with SkipBrowserRead=false")
	}
	if err.Error() != "browser page reading requires macOS" {
		t.Errorf("unexpected error: %v", err)
	}
}

// ===========================================================================
// BrowserReadPage — darwin with cancelled context exercises the browser loop
// ===========================================================================

func TestBrowserReadPage_DarwinCancelledContext(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("test only runs on darwin")
	}
	orig := SkipBrowserRead
	SkipBrowserRead = false
	t.Cleanup(func() { SkipBrowserRead = orig })

	// Cancel context immediately — osascript should fail fast.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := BrowserReadPage(ctx, "https://example.com/test-page", 1)
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

// ===========================================================================
// BrowserReadPage — darwin with default waitSeconds exercises line 38-39
// ===========================================================================

func TestBrowserReadPage_DarwinDefaultWait(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("test only runs on darwin")
	}
	orig := SkipBrowserRead
	SkipBrowserRead = false
	t.Cleanup(func() { SkipBrowserRead = orig })

	// Very short timeout so osascript fails fast.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// waitSeconds=0 -> defaults to 4, but context expires before osascript runs.
	_, err := BrowserReadPage(ctx, "https://example.com/default-wait", 0)
	if err == nil {
		t.Fatal("expected error with expired context")
	}
}

// ===========================================================================
// BrowserReadPage — darwin with negative waitSeconds exercises defaulting
// ===========================================================================

func TestBrowserReadPage_DarwinNegativeWait(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("test only runs on darwin")
	}
	orig := SkipBrowserRead
	SkipBrowserRead = false
	t.Cleanup(func() { SkipBrowserRead = orig })

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// waitSeconds=-5 -> defaults to 4, but context times out.
	_, err := BrowserReadPage(ctx, "https://example.com/neg-wait", -5)
	if err == nil {
		t.Fatal("expected error with expired context")
	}
}

// ===========================================================================
// BrowserReadPageCached — darwin with cancelled context and expired cache
// ===========================================================================

func TestBrowserReadPageCached_DarwinExpiredCacheCancelledCtx(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("test only runs on darwin")
	}
	orig := SkipBrowserRead
	SkipBrowserRead = false
	t.Cleanup(func() { SkipBrowserRead = orig })

	const testURL = "https://darwin-expired-cancelled.example.com"

	// Put an expired entry in the cache.
	browserPageCache.Lock()
	browserPageCache.entries[testURL] = browserCacheEntry{
		text:    "old content",
		expires: time.Now().Add(-1 * time.Second),
	}
	browserPageCache.Unlock()
	t.Cleanup(func() {
		browserPageCache.Lock()
		delete(browserPageCache.entries, testURL)
		browserPageCache.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := BrowserReadPageCached(ctx, testURL, 1, 5*time.Minute)
	if err == nil {
		t.Fatal("expected error with cancelled context and expired cache")
	}
}

// ===========================================================================
// BrowserReadPage — multiple wait values with skip enabled
// ===========================================================================

func TestBrowserReadPage_VariousWaitSeconds(t *testing.T) {
	orig := SkipBrowserRead
	SkipBrowserRead = true
	t.Cleanup(func() { SkipBrowserRead = orig })

	for _, ws := range []int{-100, -1, 0, 1, 10, 100} {
		_, err := BrowserReadPage(context.Background(), "https://example.com", ws)
		if err == nil {
			t.Errorf("waitSeconds=%d: expected error when SkipBrowserRead=true", ws)
		}
	}
}

// ===========================================================================
// BrowserReadPageCached — cache entry exactly at expiry boundary
// ===========================================================================

func TestBrowserReadPageCached_ExactExpiry(t *testing.T) {
	const testURL = "https://max-exact-expiry.example.com"

	// Entry with expires = now (exactly at boundary).
	browserPageCache.Lock()
	browserPageCache.entries[testURL] = browserCacheEntry{
		text:    "boundary content",
		expires: time.Now(),
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

	// time.Now().Before(entry.expires) is false when expires == now,
	// so cache should be treated as expired -> falls through to BrowserReadPage -> error.
	_, err := BrowserReadPageCached(context.Background(), testURL, 1, 5*time.Minute)
	if err == nil {
		t.Fatal("expected error when cache entry is at exact expiry boundary")
	}
}

func TestBrowserReadPageCached_MultipleFreshEntries(t *testing.T) {
	const url1 = "https://max-fresh1.example.com"
	const url2 = "https://max-fresh2.example.com"

	browserPageCache.Lock()
	browserPageCache.entries[url1] = browserCacheEntry{
		text:    "content one",
		expires: time.Now().Add(10 * time.Minute),
	}
	browserPageCache.entries[url2] = browserCacheEntry{
		text:    "content two",
		expires: time.Now().Add(10 * time.Minute),
	}
	browserPageCache.Unlock()
	t.Cleanup(func() {
		browserPageCache.Lock()
		delete(browserPageCache.entries, url1)
		delete(browserPageCache.entries, url2)
		browserPageCache.Unlock()
	})

	orig := SkipBrowserRead
	SkipBrowserRead = true
	t.Cleanup(func() { SkipBrowserRead = orig })

	got1, err := BrowserReadPageCached(context.Background(), url1, 1, 5*time.Minute)
	if err != nil {
		t.Fatalf("url1: %v", err)
	}
	if got1 != "content one" {
		t.Errorf("url1 = %q, want 'content one'", got1)
	}

	got2, err := BrowserReadPageCached(context.Background(), url2, 1, 5*time.Minute)
	if err != nil {
		t.Fatalf("url2: %v", err)
	}
	if got2 != "content two" {
		t.Errorf("url2 = %q, want 'content two'", got2)
	}
}

// ===========================================================================
// OpenBrowserForAuth — edge cases for domain extraction
// ===========================================================================

func TestOpenBrowserForAuth_URLWithoutPath(t *testing.T) {
	origNow := browserAuthNow
	origStart := browserAuthStart
	now := time.Unix(1_700_000_000, 0)
	browserAuthNow = func() time.Time { return now }
	t.Cleanup(func() {
		browserAuthNow = origNow
		browserAuthStart = origStart
		browserAuthOpened.mu.Lock()
		delete(browserAuthOpened.domains, "nopath.example.com")
		browserAuthOpened.mu.Unlock()
	})

	browserAuthStart = func(name string, args ...string) error { return nil }

	// URL with scheme but no path.
	err := OpenBrowserForAuth("https://nopath.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	browserAuthOpened.mu.Lock()
	_, ok := browserAuthOpened.domains["nopath.example.com"]
	browserAuthOpened.mu.Unlock()
	if !ok {
		t.Error("expected cooldown recorded for 'nopath.example.com'")
	}
}

func TestOpenBrowserForAuth_EmptyURL(t *testing.T) {
	origNow := browserAuthNow
	origStart := browserAuthStart
	now := time.Unix(1_700_000_000, 0)
	browserAuthNow = func() time.Time { return now }
	t.Cleanup(func() {
		browserAuthNow = origNow
		browserAuthStart = origStart
		browserAuthOpened.mu.Lock()
		delete(browserAuthOpened.domains, "")
		browserAuthOpened.mu.Unlock()
	})

	browserAuthStart = func(name string, args ...string) error { return nil }

	// Empty URL: domain becomes "".
	err := OpenBrowserForAuth("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenBrowserForAuth_CooldownThenFailure(t *testing.T) {
	origNow := browserAuthNow
	origStart := browserAuthStart
	now := time.Unix(1_700_000_000, 0)
	browserAuthNow = func() time.Time { return now }
	t.Cleanup(func() {
		browserAuthNow = origNow
		browserAuthStart = origStart
		browserAuthOpened.mu.Lock()
		delete(browserAuthOpened.domains, "cf.example.com")
		browserAuthOpened.mu.Unlock()
	})

	calls := 0
	browserAuthStart = func(name string, args ...string) error {
		calls++
		return nil
	}

	// First call succeeds.
	if err := OpenBrowserForAuth("https://cf.example.com/login"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}

	// Second call suppressed by cooldown.
	if err := OpenBrowserForAuth("https://cf.example.com/login"); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls after suppression = %d, want 1", calls)
	}

	// Advance past cooldown, but now launch fails.
	now = now.Add(25 * time.Hour)
	browserAuthStart = func(name string, args ...string) error {
		calls++
		return errors.New("browser not found")
	}

	err := OpenBrowserForAuth("https://cf.example.com/login")
	if err == nil {
		t.Fatal("expected error from failed launch after cooldown")
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}

	// Failed launch should NOT record cooldown, so next attempt tries again.
	browserAuthStart = func(name string, args ...string) error {
		calls++
		return nil
	}
	if err := OpenBrowserForAuth("https://cf.example.com/login"); err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

// ===========================================================================
// IsCaptchaResponse — additional edge cases
// ===========================================================================

func TestIsCaptchaResponse_403NoCaptchaMarker(t *testing.T) {
	body := []byte(`{"error": "access denied", "reason": "rate limited"}`)
	isCaptcha, captchaURL := IsCaptchaResponse(403, body)
	if isCaptcha {
		t.Error("403 without captcha-delivery.com should not be detected as captcha")
	}
	if captchaURL != "" {
		t.Errorf("captchaURL should be empty, got %q", captchaURL)
	}
}

func TestIsCaptchaResponse_403CaptchaNoURLField(t *testing.T) {
	// Has captcha-delivery.com but no "url":"..." field.
	body := []byte(`{"blocked": true, "reason": "captcha-delivery.com challenge required"}`)
	isCaptcha, captchaURL := IsCaptchaResponse(403, body)
	if !isCaptcha {
		t.Error("expected captcha detected (has captcha-delivery.com)")
	}
	if captchaURL != "" {
		t.Errorf("captchaURL should be empty when no url field, got %q", captchaURL)
	}
}

func TestIsCaptchaResponse_403CaptchaWithEmptyURL(t *testing.T) {
	body := []byte(`captcha-delivery.com "url":""`)
	isCaptcha, captchaURL := IsCaptchaResponse(403, body)
	if !isCaptcha {
		t.Error("expected captcha detected")
	}
	// Empty URL between quotes -> end=0 -> won't match the end > 0 check.
	if captchaURL != "" {
		t.Errorf("captchaURL should be empty for empty url value, got %q", captchaURL)
	}
}

// ===========================================================================
// parseNetscapeCookies — additional edge cases
// ===========================================================================

func TestParseNetscapeCookies_EmptyNameSkipped(t *testing.T) {
	// Entry with empty name at index 5 should be skipped.
	data := ".example.com\tTRUE\t/\tFALSE\t0\t\tvalue_only\n"
	got := parseNetscapeCookies(data)
	if got != "" {
		t.Errorf("expected empty result for cookie with empty name, got %q", got)
	}
}

func TestParseNetscapeCookies_TooFewFields(t *testing.T) {
	// Only 6 fields (need 7).
	data := ".example.com\tTRUE\t/\tFALSE\t0\tname_only\n"
	got := parseNetscapeCookies(data)
	if got != "" {
		t.Errorf("expected empty result for line with only 6 fields, got %q", got)
	}
}

func TestParseNetscapeCookies_EmptyInput(t *testing.T) {
	got := parseNetscapeCookies("")
	if got != "" {
		t.Errorf("expected empty result for empty input, got %q", got)
	}
}

func TestParseNetscapeCookies_AllComments(t *testing.T) {
	data := "# Netscape HTTP Cookie File\n# This is a comment\n# Another comment\n"
	got := parseNetscapeCookies(data)
	if got != "" {
		t.Errorf("expected empty result for all-comments input, got %q", got)
	}
}

func TestParseNetscapeCookies_LargeNumberOfCookies(t *testing.T) {
	// Build 10 cookies.
	var data string
	for i := 0; i < 10; i++ {
		data += ".example.com\tTRUE\t/\tFALSE\t0\tcookie" + string(rune('A'+i)) + "\tvalue" + string(rune('0'+i)) + "\n"
	}
	got := parseNetscapeCookies(data)
	// Count "; " separators — 10 cookies produces 9 separators.
	sepCount := 0
	for i := 0; i < len(got)-1; i++ {
		if got[i] == ';' && got[i+1] == ' ' {
			sepCount++
		}
	}
	if sepCount != 9 {
		t.Errorf("expected 9 separators (10 cookies), got %d: %q", sepCount, got)
	}
}

// ===========================================================================
// sanitizeURL — edge cases
// ===========================================================================

func TestSanitizeURL_EmptyString(t *testing.T) {
	got := sanitizeURL("")
	if got != "" {
		t.Errorf("expected empty result for empty input, got %q", got)
	}
}

func TestSanitizeURL_OnlyQuotes(t *testing.T) {
	got := sanitizeURL(`""""`)
	if got != "" {
		t.Errorf("expected empty result for all-quotes input, got %q", got)
	}
}

func TestSanitizeURL_OnlyBackslashes(t *testing.T) {
	got := sanitizeURL(`\\\\`)
	if got != "" {
		t.Errorf("expected empty result for all-backslashes input, got %q", got)
	}
}

// ===========================================================================
// ApplyCookies — exercises function with different request methods
// ===========================================================================

func TestApplyCookies_PostRequest(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.invalid/api", nil)
	if err != nil {
		t.Fatal(err)
	}
	ApplyCookies(req, "example.invalid")
	// No panic is the main assertion; when nab is absent, Cookie header is not set.
}

func TestApplyCookies_EmptyDomain(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	ApplyCookies(req, "")
	// Should not panic with empty domain.
}

// ===========================================================================
// BrowserCookies — exercises the full function
// ===========================================================================

func TestBrowserCookies_NonexistentDomainReturnsEmpty(t *testing.T) {
	got := BrowserCookies("this-domain-does-not-exist-anywhere.invalid")
	if got != "" {
		t.Errorf("expected empty for nonexistent domain, got %q", got)
	}
}

// ===========================================================================
// extractViaNab — exercises when nab is not available
// ===========================================================================

func TestExtractViaNab_NoNab(t *testing.T) {
	// When nab is not on PATH, extractViaNab returns "".
	got := extractViaNab(context.Background(), "brave", "example.invalid")
	// Don't assert the value since it depends on whether nab is installed.
	// The main goal is ensuring no panic.
	_ = got
}

func TestExtractViaNab_EmptyDomain(t *testing.T) {
	got := extractViaNab(context.Background(), "chrome", "")
	// With empty domain, should not panic.
	_ = got
}

func TestExtractViaNab_UnknownBrowser(t *testing.T) {
	got := extractViaNab(context.Background(), "firefox", "example.com")
	// With unknown browser, nab may or may not support it.
	_ = got
}
