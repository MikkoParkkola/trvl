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
)

// newSafeWebhookClient builds the HTTP client used for user-supplied webhook
// URLs. The previous default (http.DefaultClient, no restriction at all) let
// a watch's WebhookURL reach ANY address reachable from this process --
// loopback, RFC1918/RFC4193 private ranges, and link-local metadata
// endpoints included -- a classic SSRF (server-side request forgery)
// primitive. The guard lives in the dial's Control hook rather than a
// one-time URL check so it also re-validates every redirect hop's resolved
// address, not just the original host. Ported from main (PR #508, rounds
// 22-25) -- release/1.21.0 never had this guard.
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
		// A hop-count cap alone still lets Go's default redirect handling
		// run -- which builds a Referer header from the FULL prior URL and
		// replays the JSON body on a 307/308 to whatever host the response
		// names. Refuse to follow redirects at all and surface the 3xx as-is.
		CheckRedirect: webhookCheckRedirect,
	}
}

// webhookCheckRedirect is the shared redirect policy for outbound webhook
// requests: never follow, always surface the 3xx as-is.
var webhookCheckRedirect = func(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// specialUseWebhookBlocks lists IETF special-use / non-public address ranges
// that net.IP.IsPrivate() and friends do NOT cover.
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
	"fec0::/10",       // deprecated IPv6 site-local (RFC 3879)
	"64:ff9b:1::/48",  // NAT64 local-use translation prefix (RFC 8215)
	"2001::/32",       // Teredo tunneling (RFC 4380)
	"2002::/16",       // 6to4 (RFC 3056)
	"100::/64",        // discard-only prefix (RFC 6666)
	"2001:2::/48",     // benchmarking (RFC 5180)
	"3fff::/20",       // documentation (RFC 9637)
	"5f00::/16",       // segment routing SRv6 (RFC 9602)
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
// URL resolve to.
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

	// Reject anything but plain http/https up front.
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
		slog.Warn("webhook: marshal payload", "watch_id", r.Watch.ID, "err", err)
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
		slog.Warn("webhook: POST failed", "watch_id", r.Watch.ID, "host", webhookLogTarget(r.Watch.WebhookURL), "err", webhookSafeErr(err))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// With CheckRedirect refusing to follow, a redirecting receiver
		// surfaces here as a 3xx instead of silently succeeding.
		slog.Warn("webhook: receiver redirected, notification not delivered", "watch_id", r.Watch.ID, "host", webhookLogTarget(r.Watch.WebhookURL), "status", resp.StatusCode)
		return
	}
	if resp.StatusCode >= 400 {
		// A receiver returning 4xx (bad payload, auth failure, rate limit) or
		// 5xx (outage) fails delivery just as silently as a redirect without
		// this. Log host + status only, never the response body.
		slog.Warn("webhook: receiver returned an error status, notification not delivered", "watch_id", r.Watch.ID, "host", webhookLogTarget(r.Watch.WebhookURL), "status", resp.StatusCode)
	}
}

// webhookLogTarget reduces a user-supplied webhook URL to a form that is safe to
// log. Slack and Discord both carry the shared secret in the PATH, so the path,
// query and fragment are all dropped and only the host survives.
func webhookLogTarget(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "invalid"
	}
	return u.Host
}

// webhookSafeErr strips the URL out of a *url.Error so it doesn't leak a
// webhook secret embedded in the path/query via the "err" log attribute.
func webhookSafeErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s %s: %w", ue.Op, webhookLogTarget(ue.URL), ue.Err)
	}
	return err
}
