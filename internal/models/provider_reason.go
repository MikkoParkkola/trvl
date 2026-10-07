package models

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Failure reasons for a provider that did not answer (MIK-7989). Status says
// whether to retry; Reason says what happened, so a provider that blocks a
// cloud IP is not confused with an outage, a DNS problem or "no results".
const (
	ReasonBlocked     = "blocked"      // 403, bot wall, captcha: the provider refused this client
	ReasonRateLimited = "rate_limited" // 429 or an explicit rate limit
	ReasonUnavailable = "unavailable"  // 5xx: the provider itself is failing
	ReasonDNS         = "dns"          // the provider's host name did not resolve
	ReasonNetwork     = "network"      // connection refused/reset, TLS or routing failure
	ReasonTimeout     = "timeout"      // no answer within the time budget
	ReasonOther       = "other"
)

var (
	// URLs are removed before matching: a request to a cloudfront host that
	// fails with a reset is a network failure, not a CDN block.
	reasonURL = regexp.MustCompile(`https?://[^\s"']+`)
	// Status codes are matched as whole numbers so a port such as :5000 is
	// not read as HTTP 500.
	reasonCode = regexp.MustCompile(`\b(403|429|5\d\d)\b`)
)

// ClassifyProviderReason maps provider error text to a Reason. Order matters:
// DNS and timeouts are named by the transport and are unambiguous; an explicit
// rate limit wins over a 403 that some providers send with it.
func ClassifyProviderReason(msg string) string {
	if strings.TrimSpace(msg) == "" {
		return ""
	}
	// Providers wrap ErrRateLimited for every retryable failure (403, 429 and
	// 503 alike), so its own text says nothing about which one happened.
	m := strings.ToLower(strings.ReplaceAll(msg, ErrRateLimited.Error(), ""))
	m = reasonURL.ReplaceAllString(m, "")
	codes := reasonCode.FindAllString(m, -1)
	hasCode := func(match func(string) bool) bool {
		for _, c := range codes {
			if match(c) {
				return true
			}
		}
		return false
	}
	switch {
	case containsAny(m, "no such host", "server misbehaving", "dns"):
		return ReasonDNS
	case containsAny(m, "timeout", "timed out", "deadline exceeded"):
		return ReasonTimeout
	case hasCode(func(c string) bool { return c == "429" }) || containsAny(m, "too many requests", "rate limit", "rate-limit", "ratelimit", "quota", "cooldown", "cooling down"):
		return ReasonRateLimited
	case hasCode(func(c string) bool { return c == "403" }) || containsAny(m, "forbidden", "blocked", "captcha", "challenge", "bot detect", "datadome", "akamai", "cloudfront"):
		return ReasonBlocked
	case hasCode(func(c string) bool { return strings.HasPrefix(c, "5") }) || containsAny(m, "service unavailable", "temporarily unavailable", "bad gateway"):
		return ReasonUnavailable
	case containsAny(m, "connection refused", "connection reset", "no route to host", "network is unreachable", "tls:", "eof"):
		return ReasonNetwork
	default:
		return ReasonOther
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// EffectiveReason is the explicit Reason, else the one derived from Error.
// Text renderers use it so their wording matches the JSON.
func (p ProviderStatus) EffectiveReason() string {
	if p.Reason != "" || p.Error == "" {
		return p.Reason
	}
	return ClassifyProviderReason(p.Error)
}

// MarshalJSON fills Reason from Error so every JSON output (MCP and CLI)
// carries it without each provider having to set it.
func (p ProviderStatus) MarshalJSON() ([]byte, error) {
	type plain ProviderStatus // method-free alias: no recursion
	out := plain(p)
	out.Reason = p.EffectiveReason()
	return json.Marshal(out)
}

// ProviderFailureLines renders each provider that did not answer as
// "- Name: reason (error)", or "" if none failed. A search where every
// provider failed is reported through its error alone (MCP tools/call drops
// structured output on error; the CLI returns before printing), so these
// lines are how a user tells a block from an outage.
func ProviderFailureLines(statuses []ProviderStatus) string {
	var lines []string
	for _, st := range statuses {
		if st.Error == "" {
			continue
		}
		name := st.Name
		if name == "" {
			name = st.ID
		}
		lines = append(lines, "- "+name+": "+st.EffectiveReason()+" ("+st.Error+")")
	}
	if len(lines) == 0 {
		return ""
	}
	return "Provider status:\n" + strings.Join(lines, "\n")
}
