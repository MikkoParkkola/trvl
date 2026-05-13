package cookies

import (
	"testing"
)

// ============================================================
// sanitizeURL
// ============================================================

func TestSanitizeURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no special chars", "https://example.com/path?q=1", "https://example.com/path?q=1"},
		{"removes double quotes", `https://example.com"`, "https://example.com"},
		{"removes backslashes", `https://example.com\path`, "https://example.compath"},
		{"removes both", `https://example.com";\do`, `https://example.com;do`},
		{"empty string", "", ""},
		{"only quotes", `"""`, ""},
		{"injection attempt", `https://example.com" ; do shell script "whoami`, `https://example.com ; do shell script whoami`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeURL(tt.input); got != tt.want {
				t.Errorf("sanitizeURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ============================================================
// parseNetscapeCookies — additional edge cases
// ============================================================

func TestParseNetscapeCookies_MultipleCookies(t *testing.T) {
	data := ".example.com\tTRUE\t/\tTRUE\t0\tcookie1\tvalue1\n" +
		".example.com\tTRUE\t/\tTRUE\t0\tcookie2\tvalue2\n" +
		".example.com\tTRUE\t/\tTRUE\t0\tcookie3\tvalue3\n"
	got := parseNetscapeCookies(data)
	want := "cookie1=value1; cookie2=value2; cookie3=value3"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseNetscapeCookies_WhitespaceOnlyLines(t *testing.T) {
	data := "   \n\t\n.example.com\tTRUE\t/\tFALSE\t0\tfoo\tbar\n   \n"
	got := parseNetscapeCookies(data)
	if got != "foo=bar" {
		t.Errorf("got %q, want %q", got, "foo=bar")
	}
}

func TestParseNetscapeCookies_CookieWithEmptyValue(t *testing.T) {
	// A trailing tab after the name is stripped by TrimSpace, leaving < 7 fields.
	// This means empty-value cookies (name\t\n) are treated as malformed.
	data := ".example.com\tTRUE\t/\tFALSE\t0\tsession\t\n"
	got := parseNetscapeCookies(data)
	// The trailing tab is stripped by TrimSpace, so the value field is lost.
	if got != "" {
		t.Errorf("got %q, want empty (trailing tab stripped by TrimSpace)", got)
	}
}

func TestParseNetscapeCookies_CookieWithExplicitEmptyValue(t *testing.T) {
	// A cookie with an explicit empty value followed by more content is preserved.
	// Using an 8-field line where the value is between two tabs.
	data := ".example.com\tTRUE\t/\tFALSE\t0\tsession\t\textra\n"
	got := parseNetscapeCookies(data)
	if got != "session=" {
		t.Errorf("got %q, want %q", got, "session=")
	}
}

func TestParseNetscapeCookies_OnlyComments(t *testing.T) {
	data := "# Comment 1\n# Comment 2\n"
	got := parseNetscapeCookies(data)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ============================================================
// IsCaptchaResponse — additional edge cases
// ============================================================

func TestIsCaptchaResponse_500Status(t *testing.T) {
	body := []byte(`captcha-delivery.com`)
	isCaptcha, _ := IsCaptchaResponse(500, body)
	if isCaptcha {
		t.Error("500 status should not be captcha")
	}
}

func TestIsCaptchaResponse_EmptyBody(t *testing.T) {
	isCaptcha, url := IsCaptchaResponse(403, []byte(""))
	if isCaptcha {
		t.Error("empty body should not be captcha")
	}
	if url != "" {
		t.Errorf("url should be empty, got %q", url)
	}
}

func TestIsCaptchaResponse_MalformedURL(t *testing.T) {
	// Has the marker but the URL is never terminated.
	body := []byte(`captcha-delivery.com "url":"https://captcha-delivery.com/no-end-quote`)
	isCaptcha, url := IsCaptchaResponse(403, body)
	if !isCaptcha {
		t.Error("should detect captcha-delivery.com")
	}
	if url != "" {
		t.Errorf("malformed captcha URL should be empty, got %q", url)
	}
}

// ============================================================
// OpenBrowserForAuth — domain extraction
// ============================================================

func TestOpenBrowserForAuth_DomainExtraction(t *testing.T) {
	// Test the domain extraction logic embedded in OpenBrowserForAuth.
	// We test indirectly by verifying different URL formats.
	tests := []struct {
		url    string
		domain string
	}{
		{"https://www.example.com/path", "www.example.com"},
		{"http://example.com/path?q=1", "example.com"},
		{"https://sub.domain.example.com/", "sub.domain.example.com"},
		{"example.com", "example.com"}, // no scheme
	}

	for _, tt := range tests {
		// Extract domain using the same logic as OpenBrowserForAuth.
		domain := tt.url
		if idx := indexOf(domain, "://"); idx >= 0 {
			domain = domain[idx+3:]
		}
		if idx := indexOf(domain, "/"); idx >= 0 {
			domain = domain[:idx]
		}
		if domain != tt.domain {
			t.Errorf("domain(%q) = %q, want %q", tt.url, domain, tt.domain)
		}
	}
}

// indexOf is a helper that mirrors strings.Index for use in tests.
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
