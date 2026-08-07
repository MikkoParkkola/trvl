package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"

	"github.com/MikkoParkkola/trvl/internal/logredact"
)

// Webhook notification delivery.
//
// Split out of check.go, which was allowlisted past the 800-line ceiling
// (trvl#551). The allowlist reason cited a state machine hardened over 21
// review rounds -- true of the price-check logic, not of this. Delivery is an
// SSRF guard, a redirect policy, a credential redaction layer and an HTTP
// client; check.go's whole dependency on it is one call to fireWebhook.
//
// The security properties collected here, so they are reviewable together:
//
//   - the outbound client refuses non-public destinations at dial time, so a
//     watch's WebhookURL cannot be aimed at loopback, private or link-local
//     addresses;
//   - redirects are refused rather than followed, because a redirecting
//     receiver would otherwise launder the destination check above;
//   - the URL is the CREDENTIAL for Slack/Discord-style endpoints, so nothing
//     here logs more than scheme+host -- see webhookLogTarget and
//     webhookSafeErr, the latter because net/http embeds the full URL in
//     *url.Error and would otherwise undo the redaction through the err field.
//
// Moved verbatim.

// newSafeWebhookClient builds the HTTP client used for user-supplied webhook
// URLs. Round 22 found the previous default (http.DefaultClient, no
// restriction at all) let a watch's WebhookURL reach ANY address reachable
// from this process -- loopback, RFC1918/RFC4193 private ranges, and
// link-local metadata endpoints included -- a classic SSRF (server-side
// request forgery: tricking a server into making a request the attacker
// couldn't make directly) primitive. The guard lives in the dial's Control
// hook rather than a one-time URL check so it also re-validates every
// redirect hop's resolved address, not just the original host. Found by GPT
// second-opinion review, 2026-07-30 (round 22).
func newSafeWebhookClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, c syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("webhook: invalid dial address: %w", err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("webhook: refusing to dial non-literal address %q", host)
			}
			if !isPublicWebhookIP(ip) {
				return fmt.Errorf("webhook: refusing to dial non-public address")
			}
			return nil
		},
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{DialContext: dialer.DialContext},
		// Round 25: a hop-count cap alone still lets Go's default redirect
		// handling run -- which builds a Referer header from the FULL prior
		// URL (path + query, exactly where the Slack/Discord-style webhook
		// tokens documented above live) and sends it cross-origin, and
		// replays the JSON body on a 307/308 to whatever host the response
		// names. A watch's WebhookURL is not a trusted redirect chain: the
		// receiving server controls where it points next. Refuse to follow
		// redirects at all and surface the 3xx as-is; no legitimate webhook
		// receiver needs its notifier to chase a redirect. Found by GPT
		// second-opinion review, 2026-07-31 (round 25).
		// Hoisted to the package-level webhookCheckRedirect var (rather
		// than inlined) so the regression test in
		// webhook_redirect_round25_test.go exercises the SAME policy value
		// production uses, not a hand-copied duplicate that could silently
		// drift out of sync (Grok second-opinion review, round 25,
		// optional finding 3).
		CheckRedirect: webhookCheckRedirect,
	}
}

// webhookCheckRedirect is the shared redirect policy for outbound webhook
// requests: never follow, always surface the 3xx as-is. Defined once so
// newSafeWebhookClient (production) and the round-25 redirect regression test
// build their *http.Client from the identical func value.
var webhookCheckRedirect = func(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// specialUseWebhookBlocks lists IETF special-use / non-public address ranges
// that net.IP.IsPrivate() and friends do NOT cover, so a webhook URL could
// still be steered at internal infrastructure through them. Found by GPT
// second-opinion review, 2026-07-30 (round 23): the round-22 SSRF guard only
// checked IsLoopback/IsPrivate/IsLinkLocalUnicast/IsLinkLocalMulticast/
// IsUnspecified/IsMulticast, which lets RFC 6598 shared/CGNAT space and
// several RFC 5735/6890 reserved-for-documentation-or-testing blocks through
// untouched -- all of them can be assigned to real internal infrastructure
// behind a NAT or shared address translator.
var specialUseWebhookBlocks = mustParseWebhookCIDRs(
	"0.0.0.0/8",       // "this" network
	"100.64.0.0/10",   // RFC 6598 carrier-grade NAT / shared address space
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // TEST-NET-1 documentation
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // TEST-NET-2 documentation
	"203.0.113.0/24",  // TEST-NET-3 documentation
	"240.0.0.0/4",     // reserved for future use
	"2001:db8::/32",   // IPv6 documentation

	// Round 24: net.IP.IsPrivate() only covers RFC1918 (IPv4) and RFC4193 ULA
	// (fc00::/7) -- it does NOT cover the older/adjacent IPv6 special-use
	// ranges below, so without these entries a literal like
	// "http://[fec0::1]/" passed isPublicWebhookIP as "public" and was
	// dialed if the host happened to route that prefix. Found by GPT
	// second-opinion review, 2026-07-31 (round 24).
	"fec0::/10",      // deprecated IPv6 site-local (RFC 3879 -- existing deployments may still route it)
	"64:ff9b:1::/48", // NAT64 local-use translation prefix (RFC 8215, explicitly non-globally-reachable)
	"2001::/32",      // Teredo tunneling (RFC 4380)
	"2002::/16",      // 6to4 (RFC 3056)
	"100::/64",       // discard-only prefix (RFC 6666)
	"2001:2::/48",    // benchmarking (RFC 5180)
	"3fff::/20",      // documentation (RFC 9637)
	"5f00::/16",      // segment routing SRv6 (RFC 9602)
)

func mustParseWebhookCIDRs(cidrs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(fmt.Sprintf("webhook: invalid special-use CIDR %q: %v", c, err))
		}
		nets = append(nets, n)
	}
	return nets
}

// isPublicWebhookIP reports whether ip is safe to let a user-supplied webhook
// URL resolve to: routable on the public internet, not loopback, not
// RFC1918/RFC4193 private, not link-local (includes the 169.254.169.254
// cloud-metadata address class), not unspecified, not multicast, and not one
// of the IETF special-use ranges in specialUseWebhookBlocks (CGNAT,
// documentation/test, reserved).
func isPublicWebhookIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	for _, n := range specialUseWebhookBlocks {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

var webhookHTTPClient = newSafeWebhookClient()

// SetWebhookHTTPClientForTest swaps the webhook HTTP client and returns the previous client.
func SetWebhookHTTPClientForTest(client *http.Client) *http.Client {
	prev := webhookHTTPClient
	if client == nil {
		webhookHTTPClient = newSafeWebhookClient()
	} else {
		webhookHTTPClient = client
	}
	return prev
}

// webhookPayload is the JSON body POSTed to a watch's WebhookURL on price drop.
type webhookPayload struct {
	WatchID                   string  `json:"watch_id"`
	Type                      string  `json:"type"`
	Origin                    string  `json:"origin,omitempty"`
	Destination               string  `json:"destination,omitempty"`
	HotelName                 string  `json:"hotel_name,omitempty"`
	NewPrice                  float64 `json:"new_price"`
	PrevPrice                 float64 `json:"prev_price"`
	Currency                  string  `json:"currency"`
	PriceDrop                 float64 `json:"price_drop"`
	BelowGoal                 bool    `json:"below_goal"`
	LastMinuteDeal            bool    `json:"last_minute_deal,omitempty"`
	LastMinuteDiscountPercent float64 `json:"last_minute_discount_percent,omitempty"`
	Timestamp                 string  `json:"timestamp"`
}

// fireWebhook sends a price-drop notification to the watch's WebhookURL.
// It is fire-and-forget with a 10-second timeout; errors are logged but not returned.
func fireWebhook(ctx context.Context, r CheckResult) {
	if r.Watch.WebhookURL == "" {
		return
	}

	// Round 22: reject anything but plain http/https up front -- a scheme
	// like "file://" or "gopher://" has no business here and some of Go's
	// non-HTTP RoundTrippers (if ever configured) would otherwise treat it
	// as a local resource read rather than a network request.
	parsedURL, err := url.Parse(r.Watch.WebhookURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		slog.Warn("webhook: rejecting unsupported URL scheme", "watch_id", r.Watch.ID)
		return
	}

	payload := webhookPayload{
		WatchID:                   r.Watch.ID,
		Type:                      r.Watch.Type,
		Origin:                    r.Watch.Origin,
		Destination:               r.Watch.Destination,
		HotelName:                 r.Watch.HotelName,
		NewPrice:                  r.NewPrice,
		PrevPrice:                 r.PrevPrice,
		Currency:                  r.Currency,
		PriceDrop:                 r.PriceDrop,
		BelowGoal:                 r.BelowGoal,
		LastMinuteDeal:            r.LastMinuteDeal,
		LastMinuteDiscountPercent: r.LastMinuteDiscountPercent,
		Timestamp:                 time.Now().UTC().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("webhook: marshal payload", "watch_id", r.Watch.ID, "err", logredact.Err(err))
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsedURL.String(), bytes.NewReader(body))
	if err != nil {
		slog.Warn("webhook: create request", "watch_id", r.Watch.ID, "err", webhookSafeErr(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := webhookHTTPClient.Do(req)
	if err != nil {
		// Round 24: log scheme+host only, never url.Redacted(). Redacted() masks
		// only an embedded userinfo password -- it preserves username, path and
		// query, which is exactly where Slack/Discord-style webhook tokens live.
		// webhookSafeErr additionally unwraps *url.Error, whose Error() string
		// re-embeds the FULL request URL and would otherwise undo the redaction on
		// the same log line. Found by GPT second-opinion review, 2026-07-31.
		slog.Warn("webhook: POST failed", "watch_id", r.Watch.ID, "host", webhookLogTarget(r.Watch.WebhookURL), "err", webhookSafeErr(err))
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// Round 25: with CheckRedirect refusing to follow, a redirecting
		// receiver now surfaces here as a 3xx instead of silently succeeding
		// -- log it as an undelivered notification rather than treating a
		// redirect response as delivery.
		safeHost := parsedURL.Scheme + "://" + parsedURL.Host
		slog.Warn("webhook: receiver redirected, notification not delivered", "watch_id", r.Watch.ID, "host", safeHost, "status", resp.StatusCode)
		return
	}
	if resp.StatusCode >= 400 {
		// Grok round-25 optional finding #1: only the 3xx branch above ever
		// logged a non-2xx response -- a receiver returning 4xx (bad
		// payload, auth failure, rate limit) or 5xx (outage) failed the
		// delivery just as silently as a redirect, with nothing in the logs
		// to explain a "why did my webhook never fire" report. Treat 4xx/5xx
		// the same as a redirect: log host + status as undelivered, never
		// the response body (could echo the request back, including any
		// token embedded in the URL by the receiver's own error page).
		// Fixed as trvl#547.
		safeHost := parsedURL.Scheme + "://" + parsedURL.Host
		slog.Warn("webhook: receiver returned an error status, notification not delivered", "watch_id", r.Watch.ID, "host", safeHost, "status", resp.StatusCode)
	}
}

// webhookLogTarget reduces a user-supplied webhook URL to a form that is safe to
// log. Slack and Discord both carry the shared secret in the PATH, so the path,
// query and fragment are all dropped and only the host survives. A URL that does
// not parse yields a constant rather than an echo of the input, because the
// unparseable case is precisely where a malformed secret would otherwise ride
// through.
func webhookLogTarget(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "invalid"
	}
	return u.Host
}

// webhookSafeErr strips the URL out of a *url.Error.
//
// This is the part that is easy to miss: net/http returns *url.Error from both
// NewRequestWithContext and Client.Do, and url.Error.Error() prints the full URL
// it was given. Redacting the "url" log attribute alone would therefore still
// disclose the secret through the "err" attribute on the very same line.
func webhookSafeErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s %s: %w", ue.Op, webhookLogTarget(ue.URL), ue.Err)
	}
	return err
}
