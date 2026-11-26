package netguard

import (
	"errors"
	"net"
	"os"
	"strings"
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

// ErrDestinationRefused is the sentinel behind every address-policy refusal.
//
// It is returned from a net.Dialer.Control hook, so a caller sees it wrapped:
// *url.Error -> *net.OpError -> this. Both of those wrappers implement Unwrap,
// so errors.Is(err, ErrDestinationRefused) holds through the whole chain, and
// callers must ask that question rather than matching on message text.
var ErrDestinationRefused = errors.New("destination refused by policy")

// LocalDestinationsAllowed reports whether the operator has opted in to local
// destinations. Read per call rather than cached at init so a test can set it
// with t.Setenv, and so a long-running process picks it up the same way it
// picks up the consent variables.
func LocalDestinationsAllowed() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(AllowLocalEnv))) {
	case "", "0", "false":
		return false
	default:
		return true
	}
}

// PrivateProxyAllowed reports whether the operator has opted in to reaching a
// proxy on a private address. AllowLocalEnv implies it: someone who has already
// allowed private destinations cannot be protected by refusing a private proxy.
func PrivateProxyAllowed() bool {
	if LocalDestinationsAllowed() {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(AllowPrivateProxyEnv))) {
	case "", "0", "false":
		return false
	default:
		return true
	}
}

// RefusedIP reports the reason an IP is off limits, or "" if it is allowed.
//
// The list is deliberately about *reachability*, not about a blocklist of
// known-sensitive addresses. 169.254.169.254 -- the cloud metadata address
// every SSRF write-up names -- is refused here because it is link-local, not
// because it is enumerated; enumerating it would leave every sibling address
// on the same interface open.
func RefusedIP(ip net.IP) string {
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
