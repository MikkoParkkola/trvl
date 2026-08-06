package providers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// AllowLocalEnv opts a process back in to provider destinations that the
// default policy refuses: loopback, private, link-local and unspecified
// addresses.
//
// It exists for one case only -- pointing trvl at a mock provider running on
// the developer's own machine -- which is why it is an environment variable
// and not a provider config field. A config field would travel with the config:
// anyone who could hand trvl a provider could also hand it the permission to
// reach that provider's internal network. The env var can only be set by
// whoever starts the process, which is the person entitled to decide that
// localhost is a legitimate destination here.
const AllowLocalEnv = "TRVL_ALLOW_LOCAL_PROVIDERS"

// ErrDestinationRefused is the sentinel behind every refusal in this file.
//
// It is returned from a net.Dialer.Control hook, so a caller sees it wrapped:
// *url.Error -> *net.OpError -> this. Both of those wrappers implement Unwrap,
// so errors.Is(err, ErrDestinationRefused) holds through the whole chain, and
// callers must ask that question rather than matching on message text.
var ErrDestinationRefused = errors.New("destination refused by policy")

// localDestinationsAllowed reports whether the operator has opted in to local
// destinations. Read per call rather than cached at init so a test can set it
// with t.Setenv, and so a long-running process picks it up the same way it
// picks up the consent variables.
func localDestinationsAllowed() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(AllowLocalEnv))) {
	case "", "0", "false":
		return false
	default:
		return true
	}
}

// refusedIP reports the reason an IP is off limits, or "" if it is allowed.
//
// The list is deliberately about *reachability*, not about a blocklist of
// known-sensitive addresses. 169.254.169.254 -- the cloud metadata address
// every SSRF write-up names -- is refused here because it is link-local, not
// because it is enumerated; enumerating it would leave every sibling address
// on the same interface open.
func refusedIP(ip net.IP) string {
	// An IPv4-mapped IPv6 address (::ffff:127.0.0.1) answers false to the
	// IsLoopback etc. checks in its 16-byte form on some paths, so fold it to
	// its 4-byte form first and ask about that.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	switch {
	case ip.IsLoopback():
		return "loopback address"
	case ip.IsPrivate():
		return "private address"
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		// Includes 169.254.169.254 and fe80::/10.
		return "link-local address"
	case ip.IsUnspecified():
		// 0.0.0.0 and :: route to the local host on most stacks.
		return "unspecified address"
	case ip.IsInterfaceLocalMulticast(), ip.IsMulticast():
		return "multicast address"
	}
	return ""
}

// CheckDestinationURL applies the parts of the policy that can be decided from
// the URL text alone: the scheme, and a host written as an IP literal.
//
// It exists next to the dial-time hook rather than instead of it. A dial hook
// cannot refuse file:// or gopher:// because those never dial, and it cannot
// produce a good error message at the seam where a caller hands us a config.
// It also cannot be the whole policy: a hostname's address is not known here,
// and a name that resolves public now can resolve private at request time.
// Both halves are needed; neither is sufficient.
func CheckDestinationURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil // nothing to reach; other validation owns emptiness
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: unparseable url %q", ErrDestinationRefused, raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		// Refused even under the local opt-in: the opt-in is about reaching a
		// mock on localhost, not about widening what a provider may speak.
		return fmt.Errorf("%w: scheme %q is not http or https", ErrDestinationRefused, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: url %q has no host", ErrDestinationRefused, raw)
	}
	if localDestinationsAllowed() {
		return nil
	}
	// "localhost" is checked by name as well as by address. It normally
	// resolves to a loopback address and the dial hook would catch it, but a
	// hosts file can point it elsewhere, and refusing it by name keeps the
	// error the caller sees honest about what they asked for.
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return fmt.Errorf("%w: %s resolves to the local host", ErrDestinationRefused, host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if reason := refusedIP(ip); reason != "" {
			return fmt.Errorf("%w: %s is a %s (set %s=1 to allow local providers)", ErrDestinationRefused, host, reason, AllowLocalEnv)
		}
	}
	return nil
}

// dialControl is the request-time half of the policy, installed on every
// dialer this package builds.
//
// It runs after DNS resolution and before connect, and address is the resolved
// IP:port -- which is the only place a check can see what will actually be
// contacted. Validating the configured hostname at registration time cannot do
// this: the name may resolve differently by the time the request runs, and
// several URLs this package fetches are built at request time from search
// arguments and from earlier responses, so they were never seen at
// registration at all.
func dialControl(_, address string, _ syscall.RawConn) error {
	if localDestinationsAllowed() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// net.ParseIP rejects a zone identifier, so "::1%lo0" parses as nil
		// even though it is a perfectly dialable loopback address. Ask the
		// zone-aware parser before concluding this is not an address, then drop
		// the zone so refusedIP sees the bare IP it knows how to classify.
		if addr, err := netip.ParseAddr(host); err == nil {
			ip = net.IP(addr.WithZone("").AsSlice())
		}
	}
	if ip == nil {
		// Control is documented to receive the resolved address, so anything
		// that is not an address here is a form this policy does not
		// understand. Refuse it: an unclassifiable destination that is allowed
		// through is a hole shaped exactly like the one this policy exists to
		// close, and a false refusal is visible and reportable while a false
		// allow is neither.
		return fmt.Errorf("%w: %q is not an address this policy can classify", ErrDestinationRefused, host)
	}
	if reason := refusedIP(ip); reason != "" {
		return fmt.Errorf("%w: %s is a %s (set %s=1 to allow local providers)", ErrDestinationRefused, host, reason, AllowLocalEnv)
	}
	return nil
}

// guardedDialer returns a net.Dialer carrying the request-time policy.
func guardedDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   dialControl,
	}
}

// GuardedTransport returns an http.RoundTripper carrying this package's
// destination policy on its dialer, for use by other packages that make
// outbound requests. It is deliberately NOT an *http.Transport -- see the note
// on the return type below.
//
// It exists because internal/destinations built a plain http.Client and so did
// not route through this policy (trvl#539). That was safe only because the
// destinations package composes its URLs from a constant base and numeric
// parameters, so no caller string reached the host -- the guard was the URL
// construction, not the transport. A property nothing enforces is not a guard,
// and a future destinations endpoint taking a caller-supplied value would
// remove it silently.
//
// Callers get the policy at DIAL time, which is what makes it hold even when a
// redirect or a DNS answer moves the connection somewhere the URL did not name.
//
// It returns an http.RoundTripper, not the *http.Transport, and that is the
// point rather than an accident of style. The policy lives on the dialer. An
// exported *http.Transport is a mutable struct, so any consumer could write:
//
//	t := providers.GuardedTransport()
//	t.DialTLSContext = somethingElse   // HTTPS now bypasses the policy
//	t.DialContext = nil                // or remove it outright
//
// and still hold a value that looks guarded, passes review, and satisfies any
// test that inspects its fields. Handing back an opaque round-tripper makes the
// bypass unavailable instead of merely discouraged -- which is the whole lesson
// of #539, where a safety property rested on nobody happening to do the wrong
// thing. Every current consumer assigns this straight into http.Client.Transport
// and needs no other field, so nothing is lost by sealing it.
func GuardedTransport() http.RoundTripper { return guardedRoundTripper{t: guardedTransport()} }

// guardedRoundTripper hides the transport so the dialer carrying the policy
// cannot be reached and replaced.
type guardedRoundTripper struct{ t *http.Transport }

func (g guardedRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return g.t.RoundTrip(r)
}

// guardedTransport returns the standard transport this package uses for
// provider traffic, with the policy installed.
//
// DELIBERATELY DIRECT-ONLY: no Proxy field, so HTTP_PROXY and HTTPS_PROXY are
// ignored. This is a real trade, not an oversight, and it is worth stating
// because it is a behaviour change for one caller.
//
// The policy lives on the dialer and validates the address actually being
// connected to. Through a proxy, that address is the PROXY -- the real
// destination travels inside a CONNECT request the dialer never inspects. So
// enabling ProxyFromEnvironment would not merely be neutral, it would silently
// convert a destination policy into a proxy-reachability check while still
// reporting itself as guarded. That is the shape of defect #539 exists to
// remove.
//
// The cost is real and lands on internal/destinations, which previously used
// http.DefaultTransport and therefore DID honour the proxy variables. Users
// behind a mandatory proxy lose destination lookups. Rather than break them
// silently, warnIfProxyConfigured says so once. Proxy-aware guarded routing --
// validating and pinning both the proxy and the final destination -- is a design
// job, not a flag, and is tracked separately.
//
// Found by adversarial second-opinion review of trvl#539, 2026-08-06.
func guardedTransport() *http.Transport {
	warnIfProxyConfigured()
	d := guardedDialer()
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return d.DialContext(ctx, network, addr)
		},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
	}
}

// proxyWarnOnce keeps the notice below to one line per process rather than one
// per transport construction.
var proxyWarnOnce sync.Once

// warnIfProxyConfigured tells the user once that their proxy settings are not
// being used, and why.
//
// Silence here would be the worst option available: a user behind a mandatory
// proxy would see destination lookups fail with connection errors and have no
// way to connect that to a security policy they cannot see. Degrading is
// sometimes necessary; degrading silently is what this whole issue is about.
func warnIfProxyConfigured() {
	proxyWarnOnce.Do(func() {
		for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
			if os.Getenv(name) == "" {
				continue
			}
			slog.Warn("provider requests ignore your proxy settings, so they will fail if your network requires one",
				"variable", name,
				"why", "the destination policy validates the address actually dialled; through a proxy that is the proxy, "+
					"not the site, so honouring it would report requests as guarded while checking the wrong host",
				"effect", "provider and destination lookups connect directly or not at all")
			return
		}
	})
}
