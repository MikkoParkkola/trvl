package batchexec

import (
	"net/http"
	"time"

	"golang.org/x/time/rate"
)

// TEST-ONLY HELPER (trvl#539, TRVL.HARDEN.2). No production caller, enforced by
// scripts/ci/check-test-only-helpers.sh.
//
// NewTestClient and its redirect transport used to live in client.go alongside
// production code. Nothing called them from production -- which is exactly why
// the gosec G704 finding on the transport was unreachable and got baselined --
// but "nothing calls it today" is a property no gate enforces. A future
// production caller would make that finding live without changing the baseline
// count, so the gate would not notice.
//
// The first attempt put this in a _test.go file, which would have made a
// production caller fail to COMPILE. That is the stronger guarantee the
// criterion asks for, and it does not work here: _test.go is compiled only when
// testing THIS package, so 12 files across internal/flights, internal/hotels
// and internal/explore stopped building. The issue anticipated exactly this and
// named the fallback -- "if NewTestClient cannot be moved behind a test-only
// build tag without breaking the test layout, item 2 becomes a lint rule
// instead of a move."
//
// So: a dedicated file with no production code in it, and a CI check that fails
// if any non-test file references the helper. Weaker than a compile error,
// stronger than the comment-and-hope it replaces, and it fails in CI rather
// than in review.
//
// Separating it also required removing a type assertion in stealthClient that
// asked whether the transport was this test type. Production code has no
// business asking "am I in a test"; it now reads a reuseTransportForStealth
// flag, which says what the branch actually decides.

// NewTestClient creates a Client that redirects all requests to the given test
// server URL. It uses a plain http.Client (no TLS fingerprinting) with high
// rate limits for fast tests. The URL rewriting transport preserves the
// original path and query string.
func NewTestClient(baseURL string) *Client {
	return &Client{
		http: &http.Client{
			Transport: &testRedirectTransport{baseURL: baseURL},
			Timeout:   5 * time.Second,
		},
		limiter: rate.NewLimiter(rate.Limit(1000), 1),
		// Keep the redirect transport when the stealth path engages, so
		// stealth-engaged tests stay offline and deterministic.
		reuseTransportForStealth: true,
	}
}

// testRedirectTransport rewrites request URLs to point at a local test server
// while preserving the original path and query string.
type testRedirectTransport struct {
	baseURL string
}

func (t *testRedirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite the URL to point at the test server.
	newURL := t.baseURL + req.URL.Path
	if req.URL.RawQuery != "" {
		newURL += "?" + req.URL.RawQuery
	}
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, newURL, req.Body)
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header
	return http.DefaultTransport.RoundTrip(newReq)
}
