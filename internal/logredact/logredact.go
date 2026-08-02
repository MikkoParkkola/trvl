// Package logredact reduces values to a loggable form at the point of logging.
//
// Why a fingerprint and not a reduced URL: review rounds on the log-leak sweep
// established that no character-level reduction of a URL is provably
// non-sensitive. Secrets ride in the path (Slack/Discord webhook style), in the
// query, in the userinfo, and occasionally in a subdomain. This package
// therefore keeps NO part of a URL. It emits a correlation id only, so two log
// lines about the same request can still be tied together.
//
// Because no hostname is ever kept, there is no IPv6 zone id to strip: the
// zone-id hazard (url.URL.Host retains "%eth0") cannot reach a log record
// through this package by construction. Do not add a host-preserving helper
// here without re-opening that decision.
//
// The fingerprint is salted with a per-process random value. A bare hash of a
// URL is reversible by dictionary attack whenever the URL space is small, which
// it is. Salting costs nothing, keeps ids stable for the lifetime of the process
// (all correlation needs), and makes the id useless outside it.
package logredact

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Redacted is the placeholder substituted for a secret-shaped value.
const Redacted = "<redacted>"

// salt randomizes fingerprints per process. Failure to read the system CSPRNG
// leaves it zeroed, which only weakens correlation-id opacity, never
// correctness, so it is not worth panicking over at package init.
var salt = func() []byte {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return b
}()

// urlRe matches an absolute URL in free text. The terminator set excludes
// quotes and angle brackets so a URL embedded in a Go error string
// (`Get "https://…": dial tcp …`) ends at the closing quote.
var urlRe = regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9+.\-]*://[^\s"'<>\\]+`)

// secretKVRe matches `key=value` / `key: value` shapes whose key names a
// credential. Applied after URL replacement, so it only sees free text.
// The separator tolerates a closing quote so JSON (`"access_token":"v"`) is
// covered as well as headers and query fragments. The value alternation keeps
// an auth scheme ("Bearer x") from being mistaken for the whole value.
var secretKVRe = regexp.MustCompile(
	`(?i)\b(api[_-]?key|apikey|access[_-]?token|auth[_-]?token|id[_-]?token|refresh[_-]?token|token|secret|client[_-]?secret|password|passwd|pwd|passphrase|authorization|session[_-]?id|sessionid|cookie|set-cookie|signature|sig)["']?\s*[=:]\s*("[^"]*"|'[^']*'|(?:bearer|basic|token)\s+[^\s,;&)"']+|[^\s,;&)"']+)`)

// authRe catches a bare credential presentation with no key name in front.
var authRe = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=\-]{8,}`)

// URL reduces a URL to a per-process correlation id of the form "url#<12 hex>".
// The empty string maps to the empty string. Nothing of the input survives.
func URL(raw string) string {
	if raw == "" {
		return ""
	}
	return "url#" + fingerprint(raw)
}

// Text scrubs URL-shaped substrings and credential-shaped key/value pairs out
// of arbitrary text. Use it for strings that may embed either, such as upstream
// response bodies or third-party messages.
func Text(s string) string {
	if s == "" {
		return ""
	}
	s = urlRe.ReplaceAllStringFunc(s, URL)
	s = secretKVRe.ReplaceAllString(s, "${1}="+Redacted)
	return authRe.ReplaceAllString(s, "${1} "+Redacted)
}

// Err renders an error for logging with URLs and credentials scrubbed. A nil
// error yields the empty string.
//
// This exists because net/http wraps every transport failure in a *url.Error
// whose Error() embeds the full request URL, query string included. Logging
// err.Error() directly therefore leaks the URL at whatever level the call site
// uses, which is routinely Warn.
func Err(err error) string {
	if err == nil {
		return ""
	}
	return Text(err.Error())
}

// Path reduces a URL path (or any path-shaped fragment) to a correlation id.
// Webhook paths carry the secret in the path itself, so no prefix is kept.
func Path(p string) string {
	if p == "" {
		return ""
	}
	return "path#" + fingerprint(p)
}

func fingerprint(s string) string {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(h.Sum(nil))[:12]
}
