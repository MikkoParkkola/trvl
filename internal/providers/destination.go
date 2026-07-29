package providers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
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

// guardedTransport returns the standard transport this package uses for
// provider traffic, with the policy installed.
func guardedTransport() *http.Transport {
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
