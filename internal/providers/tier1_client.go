// tier1_client.go is the first-line acquisition client for cookie-gated
// providers. It impersonates Chrome at the TLS (JA3/JA4) and HTTP/2 layers via
// github.com/bogdanfinn/tls-client so that passive Cloudflare/Akamai/PerimeterX
// fingerprinting classifies the request as a real browser rather than a bot.
//
// Tier-1 is the cheap, always-on path: a single Go HTTP round-trip with a
// browser-shaped fingerprint, seeded with cookies harvested from the user's
// installed browser (kooky) and the on-disk ~/.trvl/cookies cache. When Tier-1
// still hits an interactive challenge page, the caller escalates to the Tier-2
// CDP cookie-refresh (see tier2_chromedp.go), which is gated behind an explicit
// opt-in.
//
// The exposed surface is intentionally net/http-shaped: Tier1Client satisfies
// the Fetcher interface (Do/Get over the standard library's *http.Request /
// *http.Response) so providers do not need to learn the bogdanfinn/fhttp type
// system. Conversion reuses the existing bridge helpers in fhttp_transport.go.
package providers

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"

	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"

	fhttp "github.com/bogdanfinn/fhttp"

	"github.com/MikkoParkkola/trvl/internal/cookies"
)

// Fetcher is the minimal net/http-shaped surface a provider needs from an
// acquisition client. Both Tier1Client and the standard library's *http.Client
// satisfy it, so providers can be written against the interface and swapped
// between a plain client and the browser-impersonating Tier-1 client.
type Fetcher interface {
	Do(req *http.Request) (*http.Response, error)
	Get(url string) (*http.Response, error)
}

// tier1Config holds the resolved options for a Tier1Client.
type tier1Config struct {
	profile        profiles.ClientProfile
	timeoutSeconds int
	followRedirect bool
	insecure       bool
}

// Tier1Option configures NewTier1Client.
type Tier1Option func(*tier1Config)

// WithChromeProfile overrides the Chrome impersonation profile. Defaults to the
// tls-client default (currently Chrome 146), which matches the utls Chrome146
// ClientHello used elsewhere in the runtime.
func WithChromeProfile(p profiles.ClientProfile) Tier1Option {
	return func(c *tier1Config) { c.profile = p }
}

// WithTier1Timeout sets the per-request timeout in seconds (default 30).
func WithTier1Timeout(seconds int) Tier1Option {
	return func(c *tier1Config) {
		if seconds > 0 {
			c.timeoutSeconds = seconds
		}
	}
}

// WithTier1FollowRedirects controls redirect following (default true).
func WithTier1FollowRedirects(follow bool) Tier1Option {
	return func(c *tier1Config) { c.followRedirect = follow }
}

// WithTier1InsecureSkipVerify disables TLS certificate verification. Intended
// for tests against local self-signed servers; do not use in production paths.
func WithTier1InsecureSkipVerify() Tier1Option {
	return func(c *tier1Config) { c.insecure = true }
}

// Tier1Client is a browser-impersonating Fetcher backed by tls-client. It is
// safe for concurrent use; callers should still serialize same-provider
// requests through a ProviderLimiter (see provider_limiter.go).
type Tier1Client struct {
	inner tlsclient.HttpClient

	seededMu sync.Mutex
	seeded   bool
}

// Interface guard.
var _ Fetcher = (*Tier1Client)(nil)

// NewTier1Client builds a Chrome-impersonating Tier-1 client. It does not seed
// any cookies; call SeedCookies (or use NewTier1ClientForURL) to inject the
// user's session for a specific target.
func NewTier1Client(opts ...Tier1Option) (*Tier1Client, error) {
	cfg := tier1Config{
		profile:        profiles.DefaultClientProfile,
		timeoutSeconds: 30,
		followRedirect: true,
	}
	for _, o := range opts {
		o(&cfg)
	}

	clientOpts := []tlsclient.HttpClientOption{
		tlsclient.WithClientProfile(cfg.profile),
		tlsclient.WithTimeoutSeconds(cfg.timeoutSeconds),
		tlsclient.WithCookieJar(tlsclient.NewCookieJar()),
	}
	if !cfg.followRedirect {
		clientOpts = append(clientOpts, tlsclient.WithNotFollowRedirects())
	}
	if cfg.insecure {
		clientOpts = append(clientOpts, tlsclient.WithInsecureSkipVerify())
	}

	inner, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), clientOpts...)
	if err != nil {
		return nil, err
	}
	return &Tier1Client{inner: inner}, nil
}

// NewTier1ClientForURL builds a Tier-1 client and immediately seeds it with the
// best available cookies for targetURL (cached first, then live browser
// harvest). The returned count reports how many cookies were injected.
func NewTier1ClientForURL(targetURL string, opts ...Tier1Option) (*Tier1Client, int, error) {
	c, err := NewTier1Client(opts...)
	if err != nil {
		return nil, 0, err
	}
	n := c.SeedCookies(targetURL)
	return c, n, nil
}

// SeedCookies injects cookies for targetURL into the client's jar. It harvests
// from the user's installed browser via kooky (BrowserCookiesForURL); if that
// yields nothing it falls back to the persisted ~/.trvl/cookies cache. It
// returns the number of cookies injected.
func (c *Tier1Client) SeedCookies(targetURL string) int {
	u, err := url.Parse(targetURL)
	if err != nil || u.Host == "" {
		return 0
	}

	cookies := BrowserCookiesForURL(targetURL)
	if len(cookies) == 0 {
		cookies = cachedCookiesForURL(targetURL)
	}
	if len(cookies) == 0 {
		return 0
	}
	c.inner.SetCookies(u, toFHTTPCookies(cookies))
	c.markSeeded()
	return len(cookies)
}

// markSeeded records that this client's jar now holds cookies harvested from
// the user's browser (or from the cache that was itself filled from the
// browser). It is what discardSeededIfDeclined keys on.
func (c *Tier1Client) markSeeded() {
	c.seededMu.Lock()
	defer c.seededMu.Unlock()
	c.seeded = true
}

// discardSeededIfDeclined throws away the whole jar if the user has declined
// browser cookies since it was seeded.
//
// Seeding happens once; requests happen for as long as the client lives. Both
// sources SeedCookies draws from now refuse after a decline, so a decline can
// no longer put browser cookies IN — but it cannot reach the ones already
// there, and the tls-client jar attaches them to every later request with no
// revoke path of its own. This is the same shape the vault had in round 8, and
// the fix is the same: swap the jar, which is the tls-client's own documented
// way to clear it. The cache is left alone deliberately — loadCachedCookies
// already refuses every read after a decline, so nothing can be served from it.
func (c *Tier1Client) discardSeededIfDeclined() {
	c.seededMu.Lock()
	defer c.seededMu.Unlock()
	if !c.seeded || !cookies.Disabled() {
		return
	}
	c.inner.SetCookieJar(tlsclient.NewCookieJar())
	c.seeded = false
}

// Cookies returns the cookies the jar currently holds for targetURL. Returns
// nil on a malformed URL.
func (c *Tier1Client) Cookies(targetURL string) []*http.Cookie {
	u, err := url.Parse(targetURL)
	if err != nil || u.Host == "" {
		return nil
	}
	c.discardSeededIfDeclined()
	return permittedAfterRead(fromFHTTPCookies(c.inner.GetCookies(u)))
}

// Do executes a standard net/http request through the Chrome-impersonating
// transport, bridging to and from the bogdanfinn/fhttp types tls-client uses.
func (c *Tier1Client) Do(req *http.Request) (*http.Response, error) {
	// The jar attaches its cookies inside c.inner.Do, so the decline has to be
	// answered before that call, not after it. A Cookie header a caller set by
	// hand is deliberately left alone: the opt-out is about the user's browser,
	// not about cookies in general, and a server-established session is neither
	// harvested nor the user's. Those headers are held to
	// cookies.HeaderIfPermitted by the lint in attach_decline_test.go.
	c.discardSeededIfDeclined()
	freq, err := toFHTTPRequest(req)
	if err != nil {
		return nil, err
	}
	fresp, err := c.inner.Do(freq)
	if err != nil {
		return nil, err
	}
	return toStdResponse(fresp, req), nil
}

// Get issues a GET for url using the Chrome-impersonating transport.
func (c *Tier1Client) Get(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// cachedCookiesForURL reads the persisted ~/.trvl/cookies cache for targetURL
// and returns fresh cookies (within cookieCacheTTL) as []*http.Cookie, or nil.
// It reuses loadCachedCookies by routing through a throwaway std client+jar.
func cachedCookiesForURL(targetURL string) []*http.Cookie {
	u, err := url.Parse(targetURL)
	if err != nil || u.Host == "" {
		return nil
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil
	}
	tmp := &http.Client{Jar: jar}
	if !loadCachedCookies(tmp, targetURL) {
		return nil
	}
	return tmp.Jar.Cookies(u)
}

// challengeMarkers are case-insensitive substrings that appear in interactive
// anti-bot challenge pages (Cloudflare "Just a moment…", managed challenge,
// PerimeterX/Akamai/DataDome interstitials). Their presence on a 403/503 response
// means Tier-1 was fingerprinted and the caller should escalate to Tier-2.
//
// DataDome note: the DataDome interstitial is served as the response *body* of a
// 403 on the protected document itself (no distinctive Server header), so it is
// detected purely by these body markers. A DataDome challenge that escalates to
// its behavioural CAPTCHA (the bootstrap carries 't':'bv') cannot be cleared by a
// headless browser — DetectInteractiveCaptcha classifies it as NEEDS_HUMAN — but
// IsChallengePage must still report it as a challenge so callers escalate rather
// than treat the 403 as a hard failure. Observed live on www.thefork.com
// (MIK-2949); see internal/providers/testdata/thefork_datadome_interstitial.html.
var challengeMarkers = []string{
	"just a moment",
	"cf-chl",
	"cf_chl",
	"challenge-platform",
	"checking your browser",
	"attention required",
	"_cf_chl_opt",
	"px-captcha",
	"/_incapsula_",
	// DataDome device-check / CAPTCHA interstitial signatures.
	"captcha-delivery.com",
	"var dd={",
}

// IsChallengePage reports whether an HTTP response looks like an interactive
// anti-bot challenge rather than real content. body may be nil if unread; the
// status code and Cloudflare/Akamai headers alone are often decisive. This is
// the signal the aggregator uses to decide whether to invoke the opt-in Tier-2
// CDP refresh.
func IsChallengePage(resp *http.Response, body []byte) bool {
	if resp == nil {
		return false
	}
	// Cloudflare sets cf-mitigated: challenge on managed challenges.
	if strings.EqualFold(resp.Header.Get("cf-mitigated"), "challenge") {
		return true
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusServiceUnavailable {
		server := strings.ToLower(resp.Header.Get("Server"))
		if strings.Contains(server, "cloudflare") {
			// A bare 403/503 from Cloudflare with no body still warrants a retry
			// via Tier-2 when the challenge markers are present; if body is
			// empty we rely on the marker scan below returning false and let
			// the explicit header check above carry managed challenges.
			if len(body) == 0 {
				return true
			}
		}
	}
	if len(body) == 0 {
		return false
	}
	lower := strings.ToLower(string(body))
	for _, m := range challengeMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// drainBody reads up to limit bytes of resp.Body and returns them, leaving the
// body replaced with a fresh reader over the consumed bytes so the caller can
// still read it. Helper for challenge detection on a live response.
func drainBody(resp *http.Response, limit int64) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(strings.NewReader(string(data)))
	return data
}

// toFHTTPCookies converts net/http cookies to the bogdanfinn/fhttp cookies the
// tls-client jar expects.
func toFHTTPCookies(in []*http.Cookie) []*fhttp.Cookie {
	out := make([]*fhttp.Cookie, 0, len(in))
	for _, c := range in {
		if c == nil {
			continue
		}
		out = append(out, &fhttp.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Path:     c.Path,
			Domain:   c.Domain,
			Expires:  c.Expires,
			Secure:   c.Secure,
			HttpOnly: c.HttpOnly,
		})
	}
	return out
}

// fromFHTTPCookies converts bogdanfinn/fhttp cookies back to net/http cookies.
func fromFHTTPCookies(in []*fhttp.Cookie) []*http.Cookie {
	out := make([]*http.Cookie, 0, len(in))
	for _, c := range in {
		if c == nil {
			continue
		}
		out = append(out, &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Path:     c.Path,
			Domain:   c.Domain,
			Expires:  c.Expires,
			Secure:   c.Secure,
			HttpOnly: c.HttpOnly,
		})
	}
	return out
}
