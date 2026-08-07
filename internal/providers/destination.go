package providers

import (
	"context"
	"crypto/tls"
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

	"golang.org/x/net/http/httpproxy"
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

// AllowPrivateProxyEnv opts in to reaching an HTTP proxy on a private address.
//
// Separate from AllowLocalEnv on purpose. A corporate HTTP_PROXY is almost
// always RFC1918, so without this the only way to use one was to allow private
// DESTINATIONS as well -- which is exactly the control that keeps a redirect or
// a hostile DNS answer away from the cloud metadata address. Wanting to obey
// your employer's egress policy should not require switching off the guard
// against server-side request forgery.
//
// This one relaxes the PROXY hop only. The destination is still checked first
// and still refused on a private address unless AllowLocalEnv is also set.
const AllowPrivateProxyEnv = "TRVL_ALLOW_PRIVATE_PROXY"

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

// privateProxyAllowed reports whether the operator has opted in to reaching a
// proxy on a private address. AllowLocalEnv implies it: someone who has already
// allowed private destinations cannot be protected by refusing a private proxy.
func privateProxyAllowed() bool {
	if localDestinationsAllowed() {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(AllowPrivateProxyEnv))) {
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
	return checkURLAllowingLocal(raw, localDestinationsAllowed())
}

// checkURLAllowingLocal is CheckDestinationURL with the local-address decision
// handed in, so the proxy hop can be judged by the proxy policy rather than the
// destination one. See AllowPrivateProxyEnv.
func checkURLAllowingLocal(raw string, allowLocal bool) error {
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
	if allowLocal {
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
	return guardedDialerAllowingLocal(false)
}

// guardedDialerAllowingLocal is guardedDialer with the address check optionally
// relaxed. See directGuardedTransportAllowingLocal for the one caller that
// relaxes it and why that does not unguard the destination.
func guardedDialerAllowingLocal(allowLocal bool) *net.Dialer {
	control := dialControl
	if allowLocal {
		control = nil
	}
	return &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   control,
	}
}

// GuardedTransportMode states whether a guarded transport is direct-only or
// honours the process proxy environment. The distinction is part of the API:
// callers must never have to infer which security boundary they received.
type GuardedTransportMode string

const (
	GuardedTransportDirect     GuardedTransportMode = "direct_only"
	GuardedTransportProxyAware GuardedTransportMode = "environment_proxy"
)

type lookupIPsFunc func(context.Context, string) ([]net.IP, error)
type requestProxyFunc func(*http.Request) (*url.URL, error)

// GuardedRoundTripper enforces the destination policy for direct and proxied
// traffic. In proxy-aware mode both hops are resolved, validated, and pinned
// independently before the request is sent.
type GuardedRoundTripper struct {
	// EVERY FIELD HERE IS UNEXPORTED ON PURPOSE, and that is a security
	// property rather than a style choice. The policy lives on the dialer, so
	// if this struct exposed its transport a consumer could write:
	//
	//	t := providers.GuardedTransport()
	//	t.DialTLSContext = somethingElse   // HTTPS now bypasses the policy
	//	t.DialContext = nil                // or remove it outright
	//
	// and still hold a value that looks guarded, passes review, and satisfies
	// any test that inspects its fields. Keeping them unexported makes that
	// bypass unavailable instead of merely discouraged.
	//
	// So: do not export a field here, and do not add an accessor that returns
	// *http.Transport. If a caller needs different behaviour, add a mode to
	// GuardedTransportMode -- which is checked in one place and fails closed.
	mode   GuardedTransportMode
	lookup lookupIPsFunc
	proxy  requestProxyFunc
	direct *http.Transport
}

// NewGuardedTransport returns a policy-carrying transport in the requested
// mode. Unknown modes fail closed by behaving as direct-only.
func NewGuardedTransport(mode GuardedTransportMode) *GuardedRoundTripper {
	if mode != GuardedTransportProxyAware {
		mode = GuardedTransportDirect
	}
	return &GuardedRoundTripper{
		mode:   mode,
		lookup: lookupHostIPs,
		proxy:  proxyFromCurrentEnvironment,
		direct: directGuardedTransport(),
	}
}

// Mode reports the transport's proxy contract.
func (t *GuardedRoundTripper) Mode() GuardedTransportMode {
	if t == nil {
		return GuardedTransportDirect
	}
	return t.mode
}

// GuardedTransport returns the proxy-aware guarded transport used by provider
// and destination traffic. NewGuardedTransport(GuardedTransportDirect) is the
// explicit direct-only alternative.
func GuardedTransport() *GuardedRoundTripper {
	return NewGuardedTransport(GuardedTransportProxyAware)
}

// RoundTrip applies the policy to the final destination before considering the
// proxy. That ordering is intentional: removing the destination check must not
// be masked by a later refusal of a bad proxy.
func (t *GuardedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil || t.direct == nil {
		return nil, errors.New("guarded transport is not initialized")
	}
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("%w: request has no destination URL", ErrDestinationRefused)
	}
	if t.mode != GuardedTransportProxyAware {
		return t.direct.RoundTrip(req)
	}

	proxyURL, err := t.proxy(req)
	if err != nil {
		return nil, fmt.Errorf("resolve proxy configuration: %w", err)
	}
	if proxyURL == nil {
		return t.direct.RoundTrip(req)
	}
	if proxyURL.User != nil {
		return nil, errors.New("authenticated proxies are not supported by the guarded transport")
	}
	if !strings.EqualFold(proxyURL.Scheme, "http") {
		return nil, fmt.Errorf("proxy scheme %q is not supported by the guarded transport", proxyURL.Scheme)
	}

	// The DESTINATION is pinned first and under the strict policy: whatever the
	// proxy arrangement, the address this request is ultimately for must pass.
	pinnedDestination, serverName, err := pinHTTPURL(req.Context(), req.URL, t.lookup)
	if err != nil {
		return nil, err
	}
	// The PROXY is pinned under its own opt-in. It is a hop, not a target, and
	// a corporate one is almost always on a private address -- see
	// AllowPrivateProxyEnv for why sharing the destination switch was the wrong
	// control plane.
	pinnedProxy, _, err := pinHTTPURLAllowingLocal(req.Context(), proxyURL, t.lookup, privateProxyAllowed())
	if err != nil {
		return nil, fmt.Errorf("proxy: %w", err)
	}

	clone := req.Clone(req.Context())
	clone.URL = pinnedDestination
	if req.Host != "" {
		clone.Host = req.Host
	} else {
		clone.Host = req.URL.Host
	}

	// The dial in a proxied request goes to the PROXY, not to the destination:
	// the real target travels inside the CONNECT or the absolute-form request
	// line and is never the address dialled. So this transport's dial-time
	// check has to be the proxy policy, or a private proxy is pinned as
	// acceptable one line above and then refused by the dialler.
	//
	// The destination is not thereby unguarded. It was pinned under the strict
	// policy at the top of this function, and clone.URL is that pinned value:
	// the check simply already happened, against the right address.
	transport := directGuardedTransportAllowingLocal(privateProxyAllowed())
	transport.Proxy = func(*http.Request) (*url.URL, error) { return pinnedProxy, nil }
	if strings.EqualFold(req.URL.Scheme, "https") {
		transport.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: serverName,
		}
	}
	return transport.RoundTrip(clone)
}

func proxyFromCurrentEnvironment(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return nil, nil
	}
	return httpproxy.FromEnvironment().ProxyFunc()(req.URL)
}

func lookupHostIPs(ctx context.Context, host string) ([]net.IP, error) {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	result := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, address.IP)
	}
	return result, nil
}

// pinHTTPURL resolves a URL once, refuses every unsafe answer, and rewrites the
// URL to a selected IP. Validating all answers prevents a mixed public/private
// DNS response from making safety depend on resolver ordering.
func pinHTTPURL(ctx context.Context, source *url.URL, lookup lookupIPsFunc) (*url.URL, string, error) {
	return pinHTTPURLAllowingLocal(ctx, source, lookup, localDestinationsAllowed())
}

// pinHTTPURLAllowingLocal is pinHTTPURL with the local-address decision handed
// in rather than read from the destination opt-in.
//
// It exists because the PROXY and the DESTINATION are different trust
// decisions and were sharing one switch. A corporate HTTP_PROXY is almost
// always on a private address, so reaching one required
// TRVL_ALLOW_LOCAL_PROVIDERS=1 -- which also unlocks private, loopback and
// link-local DESTINATIONS, including the cloud metadata address that the
// refusal list exists to keep out. The user wanting to route through their
// employer's proxy had to disable the SSRF control to do it.
//
// Splitting them keeps the default strict on both and lets each be relaxed on
// its own. Raised by adversarial review of #587, which noted the ordering was
// already right -- destination checked before proxy -- and that the control
// plane was not.
func pinHTTPURLAllowingLocal(ctx context.Context, source *url.URL, lookup lookupIPsFunc, allowLocal bool) (*url.URL, string, error) {
	if source == nil {
		return nil, "", fmt.Errorf("%w: missing URL", ErrDestinationRefused)
	}
	if err := checkURLAllowingLocal(source.String(), allowLocal); err != nil {
		return nil, "", err
	}

	host := source.Hostname()
	port := source.Port()
	if port == "" {
		switch strings.ToLower(source.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}

	addresses := []net.IP{net.ParseIP(host)}
	if addresses[0] == nil {
		var err error
		addresses, err = lookup(ctx, host)
		if err != nil {
			return nil, "", fmt.Errorf("%w: resolve %s: %v", ErrDestinationRefused, host, err)
		}
	}
	if len(addresses) == 0 {
		return nil, "", fmt.Errorf("%w: %s resolved to no addresses", ErrDestinationRefused, host)
	}

	var selected net.IP
	for _, address := range addresses {
		if address == nil {
			return nil, "", fmt.Errorf("%w: %s returned an unclassifiable address", ErrDestinationRefused, host)
		}
		if !allowLocal {
			if reason := refusedIP(address); reason != "" {
				return nil, "", fmt.Errorf("%w: %s resolved to %s, a %s", ErrDestinationRefused, host, address, reason)
			}
		}
		if selected == nil {
			selected = address
		}
	}

	pinned := *source
	pinned.Host = net.JoinHostPort(selected.String(), port)
	return &pinned, host, nil
}

// directGuardedTransport returns an http.Transport carrying this package's
// destination policy on its dialer.
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
func directGuardedTransport() *http.Transport {
	return directGuardedTransportAllowingLocal(false)
}

// directGuardedTransportAllowingLocal builds the transport with the dial-time
// address check relaxed or not.
//
// Only the proxied path passes true, and only when the proxy opt-in is set:
// there the address dialled IS the proxy, and the destination has already been
// checked and pinned before this transport is built. Every other caller keeps
// the strict check, because there the address dialled is the destination.
func directGuardedTransportAllowingLocal(allowLocal bool) *http.Transport {
	d := guardedDialerAllowingLocal(allowLocal)
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

// guardedTransport is the standard proxy-aware transport used by provider
// traffic inside this package.
func guardedTransport() http.RoundTripper { return GuardedTransport() }
