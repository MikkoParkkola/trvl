package providers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestNewTier1Client_ImplementsFetcher(t *testing.T) {
	c, err := NewTier1Client()
	if err != nil {
		t.Fatalf("NewTier1Client: %v", err)
	}
	var _ Fetcher = c // compile-time guard, plus runtime non-nil check
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestTier1Client_Get_LiveTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Cookie", r.Header.Get("Cookie"))
		_, _ = io.WriteString(w, "hello-tier1")
	}))
	defer srv.Close()

	c, err := NewTier1Client(WithTier1InsecureSkipVerify())
	if err != nil {
		t.Fatalf("NewTier1Client: %v", err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("Close response body: %v", err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello-tier1" {
		t.Fatalf("body = %q, want hello-tier1", body)
	}
}

func TestTier1Client_Get_PreservesEffectiveRedirectURL(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/destination/Ischia" {
			http.Redirect(w, r, "/destination", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "generic inventory")
	}))
	defer srv.Close()

	c, err := NewTier1Client(WithTier1InsecureSkipVerify())
	if err != nil {
		t.Fatalf("NewTier1Client: %v", err)
	}
	requestedURL := srv.URL + "/destination/Ischia"
	req, err := http.NewRequest(http.MethodGet, requestedURL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got, want := resp.Request.URL.Path, "/destination"; got != want {
		t.Fatalf("effective response path = %q, want %q", got, want)
	}
	if got, want := req.URL.String(), requestedURL; got != want {
		t.Fatalf("original request URL mutated to %q, want %q", got, want)
	}
}

func TestTier1Client_SeedCookies_FromCache(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Cookie", r.Header.Get("Cookie"))
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)

	// Seed the on-disk cache with a cookie for this host.
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(u, []*http.Cookie{{Name: "cf_clearance", Value: "abc123", Path: "/"}})
	saveCachedCookies(&http.Client{Jar: jar}, srv.URL)

	c, err := NewTier1Client(WithTier1InsecureSkipVerify())
	if err != nil {
		t.Fatalf("NewTier1Client: %v", err)
	}
	n := c.SeedCookies(srv.URL)
	if n == 0 {
		t.Fatal("expected at least one cookie seeded from cache")
	}

	got := c.Cookies(srv.URL)
	found := false
	for _, ck := range got {
		if ck.Name == "cf_clearance" && ck.Value == "abc123" {
			found = true
		}
	}
	if !found {
		t.Fatalf("seeded cookie not present in jar: %+v", got)
	}

	// And it should be sent on the wire.
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("Close response body: %v", err)
		}
	}()
	if echo := resp.Header.Get("X-Echo-Cookie"); echo == "" {
		t.Fatalf("expected Cookie header echoed, got empty")
	}
}

func TestTier1Client_SeedCookies_NoSourceReturnsZero(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	c, err := NewTier1Client()
	if err != nil {
		t.Fatalf("NewTier1Client: %v", err)
	}
	if n := c.SeedCookies("https://no-cookies.example.com/"); n != 0 {
		t.Fatalf("SeedCookies = %d, want 0", n)
	}
	if n := c.SeedCookies("://bad-url"); n != 0 {
		t.Fatalf("SeedCookies(bad url) = %d, want 0", n)
	}
}

func TestIsChallengePage(t *testing.T) {
	tests := []struct {
		name string
		resp *http.Response
		body string
		want bool
	}{
		{
			name: "cf-mitigated header",
			resp: &http.Response{StatusCode: 403, Header: http.Header{"Cf-Mitigated": {"challenge"}}},
			want: true,
		},
		{
			name: "cloudflare 503 empty body",
			resp: &http.Response{StatusCode: 503, Header: http.Header{"Server": {"cloudflare"}}},
			want: true,
		},
		{
			name: "just a moment body",
			resp: &http.Response{StatusCode: 403, Header: http.Header{}},
			body: "<html><title>Just a moment...</title></html>",
			want: true,
		},
		{
			name: "px captcha body",
			resp: &http.Response{StatusCode: 403, Header: http.Header{}},
			body: "please solve the px-captcha to continue",
			want: true,
		},
		{
			name: "datadome interstitial body (no server header)",
			resp: &http.Response{StatusCode: 403, Header: http.Header{}},
			body: `<script>var dd={'rt':'c','t':'bv','host':'geo.captcha-delivery.com'}</script>`,
			want: true,
		},
		{
			name: "normal 200 page",
			resp: &http.Response{StatusCode: 200, Header: http.Header{}},
			body: "<html>real content</html>",
			want: false,
		},
		{
			name: "nil response",
			resp: nil,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsChallengePage(tt.resp, []byte(tt.body)); got != tt.want {
				t.Fatalf("IsChallengePage = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestIsChallengePage_TheForkDataDomeFixture asserts the DataDome detection
// against the REAL captured interstitial frozen at
// testdata/thefork_datadome_interstitial.html. The fixture is the verbatim 403
// body served by www.thefork.com on 2026-06-24 (MIK-2949), with per-session
// tokens redacted. It is the proof that IsChallengePage now escalates DataDome
// rather than treating its 403 as a hard failure.
func TestIsChallengePage_TheForkDataDomeFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/thefork_datadome_interstitial.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	body := raw
	// The fixture's first line is "HTTP 403"; the body follows. Strip it so we
	// feed IsChallengePage exactly what the HTTP body reader would yield.
	if i := strings.IndexByte(string(raw), '\n'); i >= 0 {
		body = raw[i+1:]
	}
	resp := &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{}}
	if !IsChallengePage(resp, body) {
		t.Fatal("IsChallengePage(thefork datadome fixture) = false, want true")
	}
	// And it must be classified NEEDS_HUMAN (behavioural CAPTCHA), not clearable.
	gotHuman, vendor := DetectInteractiveCaptcha(body)
	if !gotHuman || vendor != "datadome" {
		t.Fatalf("DetectInteractiveCaptcha = (%v,%q), want (true,\"datadome\")", gotHuman, vendor)
	}
}

func TestDrainBody_Rereadable(t *testing.T) {
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("just a moment"))}
	data := drainBody(resp, 1024)
	if string(data) != "just a moment" {
		t.Fatalf("drained = %q", data)
	}
	// Body must still be readable afterwards.
	again, _ := io.ReadAll(resp.Body)
	if string(again) != "just a moment" {
		t.Fatalf("reread = %q", again)
	}
}
