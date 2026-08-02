package cookies

import "testing"

// HeaderIfPermittedForURL is the transmission-point half of the same-origin
// promise: cookies read for a site may only be sent to that site. The deny
// direction is proved end-to-end in internal/hotels; this table covers the
// URL shapes that defeat naive matching.
func TestHeaderIfPermittedForURL(t *testing.T) {
	const header = "bkng=live-session-token"

	cases := []struct {
		name    string
		url     string
		allowed bool
	}{
		{"exact host", "https://booking.com/hotel/x.html", true},
		{"www subdomain", "https://www.booking.com/hotel/x.html?checkin=2026-08-01", true},
		{"secure subdomain", "https://secure.booking.com/book.html", true},
		{"uppercase host", "https://WWW.Booking.COM/hotel/x.html", true},
		{"foreign host", "https://evil.example/hotel/x.html", false},
		{"site name in the query only", "https://evil.example/?x=www.booking.com", false},
		{"userinfo trick", "https://www.booking.com@evil.example/", false},
		// url.Hostname keeps an IPv6 zone identifier, so the bracketed literal
		// below arrives as the string "::1%.booking.com" -- suffix-matching
		// ".booking.com" while the dialer would connect to IPv6 loopback.
		{"ipv6 zone id smuggling loopback", "https://[::1%25.booking.com]:8080/x", false},
		{"ipv6 zone id smuggling mapped v4", "https://[::ffff:127.0.0.1%25.booking.com]/x", false},
		// Fullwidth homoglyphs of ':' and '%'. These carry no ASCII punctuation,
		// so the clause above lets them through; only the ASCII requirement stops
		// them. Measured: Go's IDNA profile rejects U+FF1A rather than folding it,
		// so this host would have resolved to "xn--1-kn0i4ba.booking.com" and not
		// to loopback -- refused because a booking.com host is ASCII, not because
		// a route to loopback was demonstrated.
		{"fullwidth homoglyph host", "https://：：１％.booking.com/x", false},
		// The three below are refused by the site match alone -- they carry no
		// ".booking.com" suffix -- so they document intent rather than guard the
		// bypass above. They pass with or without the DNS-name check.
		{"bare ipv6 literal", "https://[::1]:8080/x", false},
		{"bare ipv4 literal", "https://127.0.0.1/x", false},
		{"link local metadata ip", "https://169.254.169.254/latest/meta-data/", false},
		{"lookalike suffix", "https://notbooking.com/hotel/x.html", false},
		{"suffix without the dot", "https://xbooking.com/hotel/x.html", false},
		{"plaintext http", "http://www.booking.com/hotel/x.html", false},
		{"no scheme", "www.booking.com/hotel/x.html", false},
		{"unparseable", "https://%zz/", false},
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := HeaderIfPermittedForURL(header, tc.url, "booking.com")
			if tc.allowed && got != header {
				t.Errorf("HeaderIfPermittedForURL(%q) withheld the header; expected it to pass", tc.url)
			}
			if !tc.allowed && got != "" {
				t.Errorf("HeaderIfPermittedForURL(%q) returned %q; expected the header to be withheld", tc.url, got)
			}
		})
	}

	if got := HeaderIfPermittedForURL("", "https://www.booking.com/", "booking.com"); got != "" {
		t.Errorf("empty header should stay empty, got %q", got)
	}
	if got := HeaderIfPermittedForURL(header, "https://www.booking.com/", ""); got != "" {
		t.Error("an empty site must deny rather than match everything")
	}
}
