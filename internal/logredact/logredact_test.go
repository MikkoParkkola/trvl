package logredact

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestURLKeepsNothing(t *testing.T) {
	raw := "https://user:pw@hooks.slack.com:443/services/T000/B111/SUPERSECRET?token=abc123&x=1#frag"
	got := URL(raw)
	if !strings.HasPrefix(got, "url#") {
		t.Fatalf("URL() = %q, want url# prefix", got)
	}
	for _, leak := range []string{"slack", "hooks", "SUPERSECRET", "abc123", "T000", "B111", "user", "pw", "443", "https", "frag"} {
		if strings.Contains(got, leak) {
			t.Errorf("URL() = %q leaks %q", got, leak)
		}
	}
}

func TestURLStableAndDistinct(t *testing.T) {
	a := URL("https://example.com/a")
	if a != URL("https://example.com/a") {
		t.Error("URL() not stable within a process")
	}
	if a == URL("https://example.com/b") {
		t.Error("URL() collides on distinct inputs")
	}
	if URL("") != "" {
		t.Error("URL(\"\") must stay empty")
	}
}

// A hostname is never kept, so a zone id cannot reach a record. url.URL.Host
// retains the zone; this pins the property that we never read Host at all.
func TestNoIPv6ZoneCanSurvive(t *testing.T) {
	u, err := url.Parse("https://[fe80::1%25eth0]:8443/p?token=zz")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u.Host, "eth0") {
		t.Skip("stdlib no longer retains the zone id; the hazard this guards is gone")
	}
	for _, s := range []string{URL(u.String()), Text("dial " + u.String()), Err(fmt.Errorf("Get %q: fail", u.String()))} {
		if strings.Contains(s, "eth0") || strings.Contains(s, "fe80") {
			t.Errorf("zone id or address survived: %q", s)
		}
	}
}

func TestTextScrubsURLsAndSecrets(t *testing.T) {
	cases := []struct {
		in    string
		leaks []string
	}{
		{`Get "https://api.example.com/v1?api_key=SEKRIT": dial tcp: refused`, []string{"SEKRIT", "api.example.com"}},
		{`authorization: Bearer AAAA-BBBB`, []string{"AAAA-BBBB"}},
		{`{"error":"bad token","access_token":"tok_live_9"}`, []string{"tok_live_9"}},
		{`Set-Cookie: sessionid=deadbeef; Path=/`, []string{"deadbeef"}},
		{`password = hunter2`, []string{"hunter2"}},
	}
	for _, c := range cases {
		got := Text(c.in)
		for _, leak := range c.leaks {
			if strings.Contains(got, leak) {
				t.Errorf("Text(%q) = %q leaks %q", c.in, got, leak)
			}
		}
	}
}

func TestTextPreservesDiagnosticShape(t *testing.T) {
	got := Text(`Get "https://x/y?k=v": dial tcp 10.0.0.1:443: connect: connection refused`)
	if !strings.Contains(got, "connection refused") {
		t.Errorf("Text() dropped the diagnostic remainder: %q", got)
	}
	if Text("") != "" {
		t.Error("Text(\"\") must stay empty")
	}
}

func TestErrNilSafe(t *testing.T) {
	if Err(nil) != "" {
		t.Error("Err(nil) must be empty")
	}
	e := fmt.Errorf("wrapped: %w", errors.New(`Post "https://h/p?secret=zz": eof`))
	if strings.Contains(Err(e), "zz") || strings.Contains(Err(e), "://") {
		t.Errorf("Err() leaked: %q", Err(e))
	}
}

func TestPathKeepsNothing(t *testing.T) {
	got := Path("/services/T0/B1/XyzSecret")
	if !strings.HasPrefix(got, "path#") || strings.Contains(got, "XyzSecret") || strings.Contains(got, "services") {
		t.Errorf("Path() = %q", got)
	}
	if Path("") != "" {
		t.Error("Path(\"\") must stay empty")
	}
}
