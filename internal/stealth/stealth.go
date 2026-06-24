// Package stealth implements the operator-authorized gate for trvl's opt-in
// --stealth capability.
//
// Stealth reuses the existing Chrome TLS/HTTP2 fingerprint transport to perform
// authorized first-party access for flight and hotel search. It is DEFAULT OFF
// and, even when requested, activates ONLY for hosts the operator has explicitly
// authorized via the TRVL_STEALTH_ALLOWLIST environment variable.
//
// This is a fail-safe, refuse-by-default scope fence: an empty or unset
// allowlist means stealth never activates for any host. Use against sites that
// prohibit automated access is the operator's responsibility.
package stealth

import (
	"os"
	"strings"
)

// AllowlistEnv is the environment variable that holds the operator-authorized
// stealth allowlist: a comma-separated list of hostnames. Matching is
// case-insensitive and supports exact host match or suffix match for entries
// that begin with a dot (e.g. ".google.com" matches "www.google.com").
const AllowlistEnv = "TRVL_STEALTH_ALLOWLIST"

// Authorized reports whether stealth may activate for the given host.
//
// It reads the allowlist from TRVL_STEALTH_ALLOWLIST at call time so operators
// can scope access per invocation. The gate is fail-safe: an empty or unset
// allowlist returns false for every host (refuse-by-default), and an empty host
// is never authorized.
//
// Matching rules (all case-insensitive):
//   - exact match: "www.google.com" authorizes host "www.google.com"
//   - suffix match: ".google.com" authorizes any host ending in ".google.com"
//     (e.g. "www.google.com") and the bare apex "google.com"
func Authorized(host string) bool {
	return authorizedIn(host, os.Getenv(AllowlistEnv))
}

// authorizedIn is the pure core of Authorized, separated for testability so the
// allowlist can be supplied directly without environment mutation.
func authorizedIn(host, allowlist string) bool {
	host = canonicalHost(host)
	if host == "" {
		return false
	}
	for _, raw := range strings.Split(allowlist, ",") {
		entry := strings.ToLower(strings.TrimSpace(raw))
		if entry == "" {
			continue
		}
		if entry == host {
			return true
		}
		if strings.HasPrefix(entry, ".") {
			// ".google.com" matches "www.google.com" (suffix) and the bare
			// apex "google.com" (entry without the leading dot).
			if strings.HasSuffix(host, entry) || host == entry[1:] {
				return true
			}
		}
	}
	return false
}

// canonicalHost lowercases the host and strips any :port suffix so the gate
// compares bare hostnames. It tolerates inputs that already lack a port.
func canonicalHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	// Strip a trailing :port if present. IPv6 literals are out of scope for
	// the allowlist (provider hosts are DNS names), so a simple last-colon
	// trim is sufficient and safe for "host" and "host:port" forms.
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host[i:], "]") {
		host = host[:i]
	}
	return host
}
