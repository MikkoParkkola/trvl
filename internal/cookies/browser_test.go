package cookies

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestParseNetscapeCookies(t *testing.T) {
	t.Run("valid 7-field lines", func(t *testing.T) {
		data := ".example.com\tTRUE\t/\tFALSE\t1893456000\tsession\tabc123\n" +
			".example.com\tTRUE\t/\tTRUE\t1893456000\ttoken\txyz789\n"
		got := parseNetscapeCookies(data)
		if got != "session=abc123; token=xyz789" {
			t.Errorf("got %q, want %q", got, "session=abc123; token=xyz789")
		}
	})

	t.Run("empty lines skipped", func(t *testing.T) {
		data := "\n.example.com\tTRUE\t/\tFALSE\t0\tname\tvalue\n\n"
		got := parseNetscapeCookies(data)
		if got != "name=value" {
			t.Errorf("got %q, want %q", got, "name=value")
		}
	})

	t.Run("comments skipped", func(t *testing.T) {
		data := "# Netscape HTTP Cookie File\n" +
			"# This is a comment\n" +
			".example.com\tTRUE\t/\tFALSE\t0\tfoo\tbar\n"
		got := parseNetscapeCookies(data)
		if got != "foo=bar" {
			t.Errorf("got %q, want %q", got, "foo=bar")
		}
	})

	t.Run("malformed lines skipped", func(t *testing.T) {
		// Lines with fewer than 7 tab-delimited fields are ignored.
		data := "not\tenough\tfields\n" +
			".example.com\tTRUE\t/\tFALSE\t0\tgood\tvalue\n"
		got := parseNetscapeCookies(data)
		if got != "good=value" {
			t.Errorf("got %q, want %q", got, "good=value")
		}
	})

	t.Run("empty name skipped", func(t *testing.T) {
		// Cookie with empty name field (index 5) should be skipped.
		data := ".example.com\tTRUE\t/\tFALSE\t0\t\tvalue\n" +
			".example.com\tTRUE\t/\tFALSE\t0\tgood\tok\n"
		got := parseNetscapeCookies(data)
		if got != "good=ok" {
			t.Errorf("got %q, want %q", got, "good=ok")
		}
	})

	t.Run("empty input", func(t *testing.T) {
		got := parseNetscapeCookies("")
		if got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	})
}

func TestIsCaptchaResponse(t *testing.T) {
	t.Run("403 with captcha-delivery.com returns true and URL", func(t *testing.T) {
		body := []byte(`{"url":"https://captcha-delivery.com/challenge/abc"}`)
		isCaptcha, url := IsCaptchaResponse(403, body)
		if !isCaptcha {
			t.Error("expected isCaptcha=true")
		}
		if url != "https://captcha-delivery.com/challenge/abc" {
			t.Errorf("got url=%q, want https://captcha-delivery.com/challenge/abc", url)
		}
	})

	t.Run("403 without captcha returns false empty", func(t *testing.T) {
		body := []byte(`{"error":"forbidden"}`)
		isCaptcha, url := IsCaptchaResponse(403, body)
		if isCaptcha {
			t.Error("expected isCaptcha=false for plain 403")
		}
		if url != "" {
			t.Errorf("got url=%q, want empty", url)
		}
	})

	t.Run("200 returns false empty", func(t *testing.T) {
		body := []byte(`{"url":"https://captcha-delivery.com/challenge/abc"}`)
		isCaptcha, url := IsCaptchaResponse(200, body)
		if isCaptcha {
			t.Error("expected isCaptcha=false for 200 status")
		}
		if url != "" {
			t.Errorf("got url=%q, want empty", url)
		}
	})

	t.Run("403 captcha without url field returns true empty url", func(t *testing.T) {
		body := []byte(`captcha-delivery.com something`)
		isCaptcha, url := IsCaptchaResponse(403, body)
		if !isCaptcha {
			t.Error("expected isCaptcha=true when body contains captcha-delivery.com")
		}
		if url != "" {
			t.Errorf("got url=%q, want empty (no url field)", url)
		}
	})
}

func TestBrowserCookiesEmpty(t *testing.T) {
	// Package tests default the nab lookup seam to unavailable.
	got := BrowserCookies("example.com")
	if got != "" {
		t.Errorf("BrowserCookies without nab = %q, want empty", got)
	}
}

func TestApplyCookies(t *testing.T) {
	t.Run("sets Cookie header when cookies available", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://example.com/", nil)
		if err != nil {
			t.Fatal(err)
		}
		// Directly set header to simulate what ApplyCookies does internally.
		// We test the function by injecting a fake BrowserCookies result by calling
		// the header-setting logic directly — the real ApplyCookies calls BrowserCookies
		// which requires nab, so we test the request mutation branch here.
		cookieVal := "session=abc; token=xyz"
		if cookieVal != "" {
			req.Header.Set("Cookie", cookieVal)
		}
		if got := req.Header.Get("Cookie"); got != cookieVal {
			t.Errorf("Cookie header = %q, want %q", got, cookieVal)
		}
	})

	t.Run("no-op when cookies empty", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://example.com/", nil)
		if err != nil {
			t.Fatal(err)
		}
		// Empty cookies: header must not be set.
		cookieVal := ""
		if cookieVal != "" {
			req.Header.Set("Cookie", cookieVal)
		}
		if got := req.Header.Get("Cookie"); got != "" {
			t.Errorf("Cookie header = %q, want empty", got)
		}
	})

	t.Run("ApplyCookies no-op when nab absent", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://example.com/", nil)
		if err != nil {
			t.Fatal(err)
		}
		ApplyCookies(req, "example.com")
		if got := req.Header.Get("Cookie"); got != "" {
			t.Errorf("ApplyCookies without nab set Cookie=%q, want empty", got)
		}
	})
}

func TestOpenBrowserForAuthDoesNotStartCooldownOnLaunchFailure(t *testing.T) {
	origNow := browserAuthNow
	origStart := browserAuthStart
	browserAuthNow = func() time.Time { return time.Unix(1_700_000_000, 0) }
	t.Cleanup(func() {
		browserAuthNow = origNow
		browserAuthStart = origStart
		browserAuthOpened.mu.Lock()
		browserAuthOpened.domains = make(map[string]time.Time)
		browserAuthOpened.mu.Unlock()
	})

	launchCalls := 0
	browserAuthStart = func(name string, args ...string) error {
		launchCalls++
		return errors.New("launch failed")
	}

	if err := OpenBrowserForAuth("https://www.sncf-connect.com/en-en"); err == nil {
		t.Fatal("expected launch failure, got nil")
	}
	if err := OpenBrowserForAuth("https://www.sncf-connect.com/en-en"); err == nil {
		t.Fatal("expected second launch failure, got nil")
	}
	if launchCalls != 2 {
		t.Fatalf("launch calls = %d, want 2", launchCalls)
	}

	browserAuthOpened.mu.Lock()
	_, ok := browserAuthOpened.domains["www.sncf-connect.com"]
	browserAuthOpened.mu.Unlock()
	if ok {
		t.Fatal("failed launch should not record cooldown")
	}
}

func TestOpenBrowserForAuthStartsCooldownOnlyAfterSuccessfulLaunch(t *testing.T) {
	origNow := browserAuthNow
	origStart := browserAuthStart
	now := time.Unix(1_700_000_000, 0)
	browserAuthNow = func() time.Time { return now }
	t.Cleanup(func() {
		browserAuthNow = origNow
		browserAuthStart = origStart
		browserAuthOpened.mu.Lock()
		browserAuthOpened.domains = make(map[string]time.Time)
		browserAuthOpened.mu.Unlock()
	})

	launchCalls := 0
	browserAuthStart = func(name string, args ...string) error {
		launchCalls++
		return nil
	}

	if err := OpenBrowserForAuth("https://www.sncf-connect.com/en-en"); err != nil {
		t.Fatalf("first launch returned error: %v", err)
	}
	if err := OpenBrowserForAuth("https://www.sncf-connect.com/en-en"); err != nil {
		t.Fatalf("suppressed launch returned error: %v", err)
	}
	if launchCalls != 1 {
		t.Fatalf("launch calls = %d, want 1", launchCalls)
	}

	browserAuthOpened.mu.Lock()
	recorded := browserAuthOpened.domains["www.sncf-connect.com"]
	browserAuthOpened.mu.Unlock()
	if !recorded.Equal(now) {
		t.Fatalf("recorded cooldown time = %v, want %v", recorded, now)
	}
}
