package hotels

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	cookiesconsent "github.com/MikkoParkkola/trvl/internal/cookies"
)

// The room lookup is reachable from any MCP client through hotel_rooms, which
// is advertised as read-only. Its booking_url argument used to decide which
// host trvl connected to, which made a read token enough to aim the HTTP client
// at localhost, a private network, or a metadata endpoint. The destination is
// now pinned before the first request, so a foreign host gets no request at all
// -- not merely no credentials.
func TestFetchBookingRooms_RefusesForeignHostBeforeAnyRequest(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	for _, target := range []string{
		srv.URL + "/hotel/x.html",
		"http://169.254.169.254/latest/meta-data/",
		"https://evil.example/hotel/x.html",
		"https://www.booking.com@evil.example/",
		// IPv6 zone identifier smuggling: url.Hostname yields "::1%.booking.com",
		// which ends in ".booking.com" but dials IPv6 loopback.
		"https://[::1%25.booking.com]:8080/hotel/x.html",
		"https://[::1]:8080/hotel/x.html",
		"http://www.booking.com/hotel/x.html",
	} {
		rooms, err := defaultFetchBookingRooms(context.Background(), target, "2026-08-01", "2026-08-03", "EUR")
		if !errors.Is(err, ErrNotBookingURL) {
			t.Errorf("%s: expected ErrNotBookingURL, got rooms=%d err=%v", target, len(rooms), err)
		}
	}

	if hits != 0 {
		t.Errorf("the refused URLs still produced %d request(s); the host check must run before the fetch, not after", hits)
	}
}

// The room lookup takes its URL from the caller: an MCP booking_url argument,
// or a link carried on a search result. buildBookingDetailURL concatenates
// query parameters onto it without parsing, so the host is whatever the caller
// said. When the page answers 202/403/503, the WAF branch reads the user's live
// Booking.com session cookies and retries with them attached.
//
// The pin above stops a foreign host reaching this function through the public
// entry point. This is the second line: fetchBookingPage itself must not hand
// the credentials to a host they were not read for, whatever routed it here.
func TestFetchBookingPage_WithholdsCookiesFromForeignHost(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Cookie"))
		w.WriteHeader(http.StatusForbidden) // WAF-challenge shape
	}))
	defer srv.Close()

	// Both retry shapes: the bkng-only header, then the wider bkng/session/bkng_*
	// header. Either one leaking is a full session handover.
	orig := browserCookies
	defer func() { browserCookies = orig }()
	read := 0
	browserCookies = func(string) []*http.Cookie {
		read++
		return []*http.Cookie{
			{Name: "bkng", Value: "live-session-token"},
			{Name: "session", Value: "live-session-id"},
			{Name: "bkng_sso_auth", Value: "live-sso"},
		}
	}

	// srv.URL is not booking.com, which is the whole point.
	if _, err := fetchBookingPage(context.Background(), srv.URL); err == nil {
		t.Fatal("expected an error from a host that never returns 200")
	}

	if read == 0 {
		t.Fatal("the WAF branch never ran, so this test proved nothing; " +
			"check that a 403 still triggers the browser-cookie retry")
	}
	if len(got) == 0 {
		t.Fatal("no request reached the test server")
	}
	// Positive control. Without this, a change that withholds cookies from
	// every host -- or a run with browser cookies switched off entirely --
	// would satisfy the assertions below while proving nothing about the host
	// check. Same cookie string, same gate, a real booking.com destination:
	// this must transmit.
	permitted := cookiesconsent.HeaderIfPermittedForURL(
		"bkng=live-session-token", "https://www.booking.com/hotel/x.html", bookingCookieSite)
	if permitted == "" {
		t.Fatal("the permission gate withheld cookies from booking.com itself; " +
			"the empty headers below prove nothing about the destination check")
	}

	for i, cookie := range got {
		if cookie != "" {
			t.Errorf("request %d to a non-booking.com host carried a Cookie header %q; "+
				"browser cookies read for booking.com must never be sent anywhere else", i, cookie)
		}
	}
}

// allowLocalBookingHost lets a parser test point the lookup at a local fixture
// server. It is deliberately absent from the guard tests above, which must run
// against the production destination pin.
func allowLocalBookingHost(t *testing.T) {
	t.Helper()
	orig := bookingHostAllowed
	bookingHostAllowed = func(string, string) bool { return true }
	t.Cleanup(func() { bookingHostAllowed = orig })
}
