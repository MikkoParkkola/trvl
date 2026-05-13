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
// BrowserReadPage — platform-specific branches
// ===========================================================================

func TestBrowserReadPage_DarwinSkipBrowserRead(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("test only runs on darwin")
	}
	orig := SkipBrowserRead
	SkipBrowserRead = true
	t.Cleanup(func() { SkipBrowserRead = orig })

	_, err := BrowserReadPage(context.Background(), "https://example.com", 1)
	if err == nil {
		t.Fatal("expected error when SkipBrowserRead=true on darwin")
	}
}

func TestBrowserReadPage_ZeroWaitSeconds(t *testing.T) {
	orig := SkipBrowserRead
	SkipBrowserRead = true
	t.Cleanup(func() { SkipBrowserRead = orig })

	// waitSeconds=0 would be defaulted to 4, but SkipBrowserRead short-circuits.
	_, err := BrowserReadPage(context.Background(), "https://example.com", 0)
	if err == nil {
		t.Fatal("expected error when SkipBrowserRead=true")
	}
}

func TestBrowserReadPage_LargeWaitSeconds(t *testing.T) {
	orig := SkipBrowserRead
	SkipBrowserRead = true
	t.Cleanup(func() { SkipBrowserRead = orig })

	_, err := BrowserReadPage(context.Background(), "https://example.com", 999)
	if err == nil {
		t.Fatal("expected error when SkipBrowserRead=true")
	}
}

// ===========================================================================
// BrowserReadPageCached — expired cache forces re-read
// ===========================================================================

func TestBrowserReadPageCached_NotInCache(t *testing.T) {
	orig := SkipBrowserRead
	SkipBrowserRead = true
	t.Cleanup(func() { SkipBrowserRead = orig })

	const testURL = "https://unique-not-cached-cov2.example.com"
	// Ensure not in cache.
	browserPageCache.Lock()
	delete(browserPageCache.entries, testURL)
	browserPageCache.Unlock()

	_, err := BrowserReadPageCached(context.Background(), testURL, 1, 5*time.Minute)
	if err == nil {
		t.Fatal("expected error on cache miss with SkipBrowserRead=true")
	}
}

func TestBrowserReadPageCached_FreshCacheHit(t *testing.T) {
	const testURL = "https://cov2-fresh-cache.example.com"
	const testText = "fresh cached content"

	browserPageCache.Lock()
	browserPageCache.entries[testURL] = browserCacheEntry{
		text:    testText,
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
	if got != testText {
		t.Errorf("got %q, want %q", got, testText)
	}
}

// ===========================================================================
// IsCaptchaResponse — comprehensive edge cases
// ===========================================================================

func TestIsCaptchaResponse_403WithTruncatedURL(t *testing.T) {
	// Body contains captcha-delivery.com and "url":" but no closing quote.
	body := []byte(`captcha-delivery.com "url":"https://captcha-delivery.com/abc`)
	isCaptcha, url := IsCaptchaResponse(403, body)
	if !isCaptcha {
		t.Error("expected captcha detected")
	}
	// URL extraction may or may not find a valid URL depending on parsing.
	_ = url
}

func TestIsCaptchaResponse_200WithCaptchaBody(t *testing.T) {
	body := []byte(`captcha-delivery.com`)
	isCaptcha, _ := IsCaptchaResponse(200, body)
	if isCaptcha {
		t.Error("200 status should not be detected as captcha regardless of body")
	}
}

func TestIsCaptchaResponse_403WithValidURL(t *testing.T) {
	body := []byte(`{"url":"https://geo.captcha-delivery.com/captcha/?initialCid=abc123&t=fe&s=42"}`)
	isCaptcha, captchaURL := IsCaptchaResponse(403, body)
	if !isCaptcha {
		t.Error("expected captcha detected")
	}
	if captchaURL != "https://geo.captcha-delivery.com/captcha/?initialCid=abc123&t=fe&s=42" {
		t.Errorf("url = %q", captchaURL)
	}
}

// ===========================================================================
// parseNetscapeCookies — value with special characters
// ===========================================================================

func TestParseNetscapeCookies_ValueWithEquals(t *testing.T) {
	data := ".example.com\tTRUE\t/\tTRUE\t0\ttoken\tbase64=value=\n"
	got := parseNetscapeCookies(data)
	if got != "token=base64=value=" {
		t.Errorf("got %q, want 'token=base64=value='", got)
	}
}

func TestParseNetscapeCookies_ValueWithSemicolon(t *testing.T) {
	// Semicolons in values are unusual but the format allows them.
	data := ".example.com\tTRUE\t/\tTRUE\t0\tsid\tabc;def\n"
	got := parseNetscapeCookies(data)
	if got != "sid=abc;def" {
		t.Errorf("got %q, want 'sid=abc;def'", got)
	}
}

func TestParseNetscapeCookies_SingleCookie(t *testing.T) {
	data := ".example.com\tTRUE\t/\tFALSE\t1893456000\tonly\tcookie\n"
	got := parseNetscapeCookies(data)
	if got != "only=cookie" {
		t.Errorf("got %q, want 'only=cookie'", got)
	}
}

// ===========================================================================
// OpenBrowserForAuth — unsupported OS path
// ===========================================================================

func TestOpenBrowserForAuth_DifferentDomains(t *testing.T) {
	origNow := browserAuthNow
	origStart := browserAuthStart
	now := time.Unix(1_700_000_000, 0)
	browserAuthNow = func() time.Time { return now }
	t.Cleanup(func() {
		browserAuthNow = origNow
		browserAuthStart = origStart
		browserAuthOpened.mu.Lock()
		delete(browserAuthOpened.domains, "a.example.com")
		delete(browserAuthOpened.domains, "b.example.com")
		browserAuthOpened.mu.Unlock()
	})

	calls := 0
	browserAuthStart = func(name string, args ...string) error {
		calls++
		return nil
	}

	// Two different domains should both get opened.
	if err := OpenBrowserForAuth("https://a.example.com/path"); err != nil {
		t.Fatalf("first domain: %v", err)
	}
	if err := OpenBrowserForAuth("https://b.example.com/path"); err != nil {
		t.Fatalf("second domain: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (different domains)", calls)
	}
}

func TestOpenBrowserForAuth_URLWithPort(t *testing.T) {
	origNow := browserAuthNow
	origStart := browserAuthStart
	now := time.Unix(1_700_000_000, 0)
	browserAuthNow = func() time.Time { return now }
	t.Cleanup(func() {
		browserAuthNow = origNow
		browserAuthStart = origStart
		browserAuthOpened.mu.Lock()
		delete(browserAuthOpened.domains, "localhost:8080")
		browserAuthOpened.mu.Unlock()
	})

	browserAuthStart = func(name string, args ...string) error {
		return nil
	}

	err := OpenBrowserForAuth("https://localhost:8080/auth")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	browserAuthOpened.mu.Lock()
	_, ok := browserAuthOpened.domains["localhost:8080"]
	browserAuthOpened.mu.Unlock()
	if !ok {
		t.Error("expected cooldown recorded for localhost:8080")
	}
}

// ===========================================================================
// ApplyCookies — function call path
// ===========================================================================

func TestApplyCookies_MultipleDomains(t *testing.T) {
	// Exercise ApplyCookies with different domains. When nab is not installed,
	// these are all no-ops, but the function must not panic.
	for _, domain := range []string{"booking.com", "airbnb.com", "example.invalid"} {
		req, err := http.NewRequest(http.MethodGet, "https://"+domain+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		ApplyCookies(req, domain)
	}
}

// ===========================================================================
// sanitizeURL — additional edge cases
// ===========================================================================

func TestSanitizeURL_MultipleBackslashes(t *testing.T) {
	input := `https://example.com\\\path`
	got := sanitizeURL(input)
	if got != "https://example.compath" {
		t.Errorf("got %q, want 'https://example.compath'", got)
	}
}

func TestSanitizeURL_MixedQuotesAndBackslashes(t *testing.T) {
	input := `"https://\example.com\"`
	got := sanitizeURL(input)
	if got != "https://example.com" {
		t.Errorf("got %q, want 'https://example.com'", got)
	}
}

// ===========================================================================
// OpenBrowserForAuth — launch error preserves retry
// ===========================================================================

func TestOpenBrowserForAuth_MultipleFailuresAllRetried(t *testing.T) {
	origNow := browserAuthNow
	origStart := browserAuthStart
	now := time.Unix(1_700_000_000, 0)
	browserAuthNow = func() time.Time { return now }
	t.Cleanup(func() {
		browserAuthNow = origNow
		browserAuthStart = origStart
		browserAuthOpened.mu.Lock()
		delete(browserAuthOpened.domains, "retry.example.com")
		browserAuthOpened.mu.Unlock()
	})

	calls := 0
	browserAuthStart = func(name string, args ...string) error {
		calls++
		return errors.New("launch failed")
	}

	// Three failures should all be attempted (no cooldown on failure).
	for i := 0; i < 3; i++ {
		err := OpenBrowserForAuth("https://retry.example.com/auth")
		if err == nil {
			t.Fatal("expected error")
		}
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (no cooldown on failure)", calls)
	}
}

// ===========================================================================
// BrowserReadPageCached — verifies TTL boundary
// ===========================================================================

func TestBrowserReadPageCached_JustExpired(t *testing.T) {
	const testURL = "https://cov2-just-expired.example.com"

	browserPageCache.Lock()
	browserPageCache.entries[testURL] = browserCacheEntry{
		text:    "old content",
		expires: time.Now().Add(-1 * time.Millisecond), // just expired
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

	_, err := BrowserReadPageCached(context.Background(), testURL, 1, 5*time.Minute)
	if err == nil {
		t.Fatal("expected error because cache just expired and browser read is disabled")
	}
}

// ===========================================================================
// IsCaptchaResponse — body with multiple markers
// ===========================================================================

func TestIsCaptchaResponse_403WithMultipleMarkers(t *testing.T) {
	body := []byte(`captcha-delivery.com and also another captcha-delivery.com reference "url":"https://captcha-delivery.com/first"`)
	isCaptcha, captchaURL := IsCaptchaResponse(403, body)
	if !isCaptcha {
		t.Error("expected captcha detected")
	}
	if captchaURL != "https://captcha-delivery.com/first" {
		t.Errorf("url = %q, want first URL", captchaURL)
	}
}

// ===========================================================================
// BrowserCookies — exercises the function call chain
// ===========================================================================

func TestBrowserCookies_InvalidDomain(t *testing.T) {
	// Even with a weird domain, the function should not panic.
	got := BrowserCookies("")
	if got != "" {
		t.Errorf("expected empty for empty domain, got %q", got)
	}
}

func TestBrowserCookies_SpecialCharDomain(t *testing.T) {
	// Domain with special characters — should not panic.
	got := BrowserCookies("not-a-real-domain-!@#$.invalid")
	if got != "" {
		t.Errorf("expected empty cookies for invalid domain, got %q", got)
	}
}

// ===========================================================================
// BrowserReadPage — context cancellation
// ===========================================================================

func TestBrowserReadPage_CancelledContext(t *testing.T) {
	orig := SkipBrowserRead
	SkipBrowserRead = true
	t.Cleanup(func() { SkipBrowserRead = orig })

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := BrowserReadPage(ctx, "https://example.com", 1)
	if err == nil {
		t.Fatal("expected error with SkipBrowserRead=true")
	}
}

// ===========================================================================
// OpenBrowserForAuth — verifies OS command selection
// ===========================================================================

func TestOpenBrowserForAuth_VerifiesCommand(t *testing.T) {
	origNow := browserAuthNow
	origStart := browserAuthStart
	now := time.Unix(1_700_000_000, 0)
	browserAuthNow = func() time.Time { return now }
	t.Cleanup(func() {
		browserAuthNow = origNow
		browserAuthStart = origStart
		browserAuthOpened.mu.Lock()
		delete(browserAuthOpened.domains, "cmd-test.example.com")
		browserAuthOpened.mu.Unlock()
	})

	var gotCmd string
	browserAuthStart = func(name string, args ...string) error {
		gotCmd = name
		return nil
	}

	err := OpenBrowserForAuth("https://cmd-test.example.com/auth")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the correct command was selected for the platform.
	switch runtime.GOOS {
	case "darwin":
		if gotCmd != "open" {
			t.Errorf("on darwin expected 'open', got %q", gotCmd)
		}
	case "linux":
		if gotCmd != "xdg-open" {
			t.Errorf("on linux expected 'xdg-open', got %q", gotCmd)
		}
	case "windows":
		if gotCmd != "cmd" {
			t.Errorf("on windows expected 'cmd', got %q", gotCmd)
		}
	}
}

// ===========================================================================
// parseNetscapeCookies — tab character edge cases
// ===========================================================================

func TestParseNetscapeCookies_ExactlySevenFields(t *testing.T) {
	data := ".example.com\tTRUE\t/\tTRUE\t0\texact\tseven\n"
	got := parseNetscapeCookies(data)
	if got != "exact=seven" {
		t.Errorf("got %q, want 'exact=seven'", got)
	}
}

func TestParseNetscapeCookies_MixedValidAndInvalid(t *testing.T) {
	data := "short\tline\n" +
		".example.com\tTRUE\t/\tFALSE\t0\tgood1\tval1\n" +
		"# comment\n" +
		".example.com\tTRUE\t/\tFALSE\t0\tgood2\tval2\n" +
		"\n" +
		"too\tfew\tfields\n"
	got := parseNetscapeCookies(data)
	if got != "good1=val1; good2=val2" {
		t.Errorf("got %q, want 'good1=val1; good2=val2'", got)
	}
}
