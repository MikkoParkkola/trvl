package providers

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
)

// TestCachedCookiesForURL_RoundTrip is the same-package regression test for the
// Booking WAF rebuild (fix/booking-waf-token-apollo): the Booking provider reads
// a previously-harvested aws-waf-token from the on-disk cache via
// CachedCookiesForURL so it can replay searches without re-driving a browser.
// If the cache read regresses, every Booking request re-harvests (or 202s).
func TestCachedCookiesForURL_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const target = "https://www.booking.com/searchresults.html"

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("jar: %v", err)
	}
	u, _ := url.Parse(target)
	jar.SetCookies(u, []*http.Cookie{{Name: "aws-waf-token", Value: "sample-token-value"}})
	saveCachedCookies(&http.Client{Jar: jar}, target)

	got := CachedCookiesForURL(target)
	if len(got) == 0 {
		t.Fatal("expected cached cookies after save, got none")
	}
	var found bool
	for _, c := range got {
		if c.Name == "aws-waf-token" && c.Value == "sample-token-value" {
			found = true
		}
	}
	if !found {
		t.Errorf("aws-waf-token not round-tripped through cache, got %+v", got)
	}
}

func TestCachedCookiesForURL_MissReturnsNil(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := CachedCookiesForURL("https://www.booking.com/searchresults.html"); got != nil {
		t.Errorf("expected nil on cache miss, got %+v", got)
	}
}
