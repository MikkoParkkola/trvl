// Package models — evidence.go adds the "evidence envelope" vocabulary: a richer
// per-provider status enum and a composite Completeness so callers can never
// conflate "checked, found nothing" with "timed out" or "not configured".
// A timeout must never render as "no results" — that is a lie by omission.
//
// Tracking: MIK-4950 (parent MIK-4948).
package models

import (
	"context"
	"errors"
	"slices"
	"strings"
)

// Provider status values. The first four are the historical set and remain
// valid; the rest add the distinctions the 3-value enum could not express.
const (
	StatusOK            = "ok"             // legacy: checked, succeeded (see also StatusCheckedHit)
	StatusError         = "error"          // legacy: generic failure (prefer StatusFailed/StatusTimeout)
	StatusSkipped       = "skipped"        // intentionally not queried (unsupported options)
	StatusDisabled      = "disabled"       // provider switched off
	StatusCheckedHit    = "checked_hit"    // queried, returned matching results
	StatusCheckedNoHit  = "checked_no_hit" // queried, returned a valid empty result
	StatusFailed        = "failed"         // hard error — provider unreliable this attempt
	StatusTimeout       = "timeout"        // exceeded latency budget — outcome UNKNOWN, not empty
	StatusNotConfigured = "not_configured" // credentials/connector missing — never checked
	StatusNotAuthorized = "not_authorized" // caller lacks scope
	StatusStale         = "stale"          // results exist but older than freshness threshold
	StatusCircuitBroken = "circuit_broken" // provider skipped because recent failures tripped breaker
	StatusRateLimited   = "rate_limited"   // upstream rate-limited / blocked / challenge — RETRYABLE, not empty
)

// ErrRateLimited is the typed sentinel for an upstream provider that rate-limited
// us, blocked the request, or returned a challenge / error payload (e.g. HTTP
// 429/403/503, or a Google travel.frontend.flights.ErrorResponse body) instead of
// real data. It exists so callers can distinguish a RETRYABLE upstream condition
// from a genuine parse bug — the distinction issue #228 (and #198 before it)
// turned on. Wrap it with %w to preserve a clean, user-facing message while still
// letting errors.Is(err, ErrRateLimited) classify the outcome as rate_limited.
var ErrRateLimited = errors.New("upstream provider rate-limited or temporarily unavailable")

// ErrParseFailed marks a provider response the parser could not decode: a 200 OK
// body whose shape rotated underneath the positional/HTML decoder so that NO
// entries parsed even though the upstream returned some. Wrap it with %w to keep
// a clean message while letting errors.Is(err, ErrParseFailed) and
// ClassifyProviderError (-> StatusFailed) distinguish "parser broke" from a
// genuine empty result (StatusCheckedNoHit). The difference is load-bearing: a
// parse failure means the provider is unreliable this attempt and should count
// toward the circuit breaker, whereas an honest empty result must not.
var ErrParseFailed = errors.New("provider response could not be parsed (response shape changed)")

// ErrNotApplicable marks a provider that does not serve the requested pair:
// no station, port or route for it in a static table, or a lookup that
// answered cleanly with no match (an empty Trenitalia station search). Ground
// search classifies "not served" with errors.Is on this sentinel alone, never
// by message text, and maps it to a skipped status, never to a failure.
var ErrNotApplicable = errors.New("provider does not serve this route")

// ProviderNotice is a non-fatal provider caveat: routes may still be present,
// and FixHint tells the caller what would improve the answer. Reason is one of
// the Reason* constants; Err is the underlying cause.
type ProviderNotice struct {
	Reason  string
	FixHint string
	Err     error
}

func (n *ProviderNotice) Error() string {
	if n.Err != nil {
		return n.Err.Error()
	}
	return n.Reason
}

func (n *ProviderNotice) Unwrap() error { return n.Err }

// ClassifyProviderError maps an error to a provider status. A typed rate-limit
// (ErrRateLimited) wins first so a 429/challenge never renders as a hard parse
// failure; deadlines map to StatusTimeout; retryable upstream signatures (rate
// limits, bot-wall blocks, transient 5xx) surfaced as plain strings map to
// StatusRateLimited; everything else is StatusFailed. This is the distinction
// that prevents a timeout/rate-limit/block from being presented to the user as
// "nothing found" or as a permanent failure.
func ClassifyProviderError(err error) string {
	if err == nil {
		return StatusOK
	}
	if errors.Is(err, ErrRateLimited) {
		return StatusRateLimited
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return StatusTimeout
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out") || strings.Contains(msg, "deadline") {
		return StatusTimeout
	}
	// Retryable upstream conditions a provider may surface as a plain string
	// error rather than wrapping ErrRateLimited: rate limits, bot-wall blocks,
	// challenges, and transient 5xx. These are RETRYABLE (the documented intent
	// of StatusRateLimited) — classifying them as StatusFailed would tell the
	// user the provider is permanently broken when retrying (or supplying
	// cookies) would succeed. Substring match is deliberately broad: in this
	// domain "forbidden"/"blocked"/"captcha"/"datadome"/"cloudfront"/"akamai"
	// overwhelmingly mean an anti-bot wall, not a logic bug.
	for _, sig := range []string{
		"429", "rate limit", "rate-limit", "ratelimit", "too many requests",
		"403", "forbidden", "blocked", "captcha", "challenge", "bot detect",
		"datadome", "cloudfront", "akamai",
		"503", "service unavailable", "temporarily unavailable",
	} {
		if strings.Contains(msg, sig) {
			return StatusRateLimited
		}
	}
	return StatusFailed
}

// Completeness states.
const (
	CompletenessComplete = "complete" // every relevant provider returned a definitive answer
	CompletenessPartial  = "partial"  // some providers timed out / failed; results may be incomplete
	CompletenessBlocked  = "blocked"  // no provider returned a usable answer
)

// Completeness is the composite evidence summary for a multi-provider search.
type Completeness struct {
	State     string   `json:"state"`             // complete | partial | blocked
	Queried   int      `json:"queried"`           // providers actually attempted (excludes skipped/not_configured)
	Succeeded int      `json:"succeeded"`         // providers that returned a definitive answer (hit or no-hit)
	Missing   []string `json:"missing,omitempty"` // provider ids that timed out or failed
	// missingWhy[i] says what happened to Missing[i] (see MissingPhrase). It
	// stays out of JSON: provider_statuses already carries the detail.
	missingWhy []string
}

// Clone returns a copy that shares no slice with c; nil slices stay nil.
// Cached search results hand out copies, and every slice here, the unexported
// one included, must be the copy's own.
func (c Completeness) Clone() Completeness {
	c.Missing = slices.Clone(c.Missing)
	c.missingWhy = slices.Clone(c.missingWhy)
	return c
}

// statusDefinitive reports whether a status represents a definitive answer
// (the provider was reached and gave a real result, even if empty).
func statusDefinitive(s string) bool {
	switch s {
	case StatusOK, StatusCheckedHit, StatusCheckedNoHit, StatusStale:
		return true
	}
	return false
}

// statusAttempted reports whether the provider was actually queried (so it
// counts toward the completeness denominator).
func statusAttempted(s string) bool {
	switch s {
	case StatusSkipped, StatusDisabled, StatusNotConfigured, StatusNotAuthorized:
		return false
	}
	return true
}

// ComputeCompleteness derives the composite completeness from per-provider
// statuses. blocked when nothing definitive came back; partial when some
// attempted providers timed out/failed; complete otherwise.
func ComputeCompleteness(statuses []ProviderStatus) Completeness {
	c := Completeness{State: CompletenessComplete}
	for _, s := range statuses {
		if statusAttempted(s.Status) {
			c.Queried++
		}
		if statusDefinitive(s.Status) {
			c.Succeeded++
			continue
		}
		if why := s.MissingPhrase(); why != "" {
			c.Missing = append(c.Missing, s.ID)
			c.missingWhy = append(c.missingWhy, why)
		}
	}
	switch {
	case c.Succeeded == 0 && c.Queried > 0:
		c.State = CompletenessBlocked
	case len(c.Missing) > 0:
		c.State = CompletenessPartial
	}
	return c
}

// Phrases for a provider that gave no usable answer, in the order
// IncompleteNote lists them. Each reads for one provider or several.
const (
	missingNoAnswer    = "did not respond"
	missingRefused     = "refused the request"
	missingRateLimited = "rate-limited the request"
	missingServerError = "returned a server error"
	missingFailed      = "failed"
	missingNotTried    = "not tried (circuit breaker open)"
)

var missingOrder = []string{missingNoAnswer, missingRefused, missingRateLimited, missingServerError, missingFailed, missingNotTried}

// MissingPhrase says what happened to a provider that gave no usable answer,
// or "" when it answered or was never attempted. The cause comes from
// EffectiveReason, which ignores URLs: ClassifyProviderError reads the raw
// text, so a 404 on a URL containing "503" arrives as rate_limited. Anything
// without a specific cause is "failed", which is true of every outcome and
// never claims the provider was silent.
func (p ProviderStatus) MissingPhrase() string {
	switch p.Status {
	case StatusCircuitBroken:
		return missingNotTried
	case StatusTimeout, StatusFailed, StatusError, StatusRateLimited:
	default:
		return ""
	}
	switch p.EffectiveReason() {
	case ReasonTimeout, ReasonDNS, ReasonNetwork:
		return missingNoAnswer
	case ReasonBlocked:
		return missingRefused
	case ReasonRateLimited:
		return missingRateLimited
	case ReasonUnavailable:
		return missingServerError
	case "":
		if p.Status == StatusTimeout {
			return missingNoAnswer // a timeout with no error text is still silence
		}
	}
	return missingFailed
}

// MayClaimExhaustive reports whether the renderer is allowed to assert
// "no results / nothing found / cheapest" language. Only true when every
// relevant provider was reached — otherwise absence is unknown.
func (c Completeness) MayClaimExhaustive() bool {
	// Zero-value (State == "") means completeness was not assessed -> legacy
	// callers keep their behaviour. Only explicit partial/blocked gags claims.
	return c.State == "" || c.State == CompletenessComplete
}

// IncompleteNote returns a human-readable caveat for partial/blocked searches,
// suitable for prepending to rendered output instead of a false "no results".
func (c Completeness) IncompleteNote() string {
	if c.MayClaimExhaustive() {
		return ""
	}
	reached := c.Succeeded
	total := c.Queried
	b := strings.Builder{}
	b.WriteString("Checked ")
	b.WriteString(itoa(reached))
	b.WriteString(" of ")
	b.WriteString(itoa(total))
	b.WriteString(" providers")
	groups := map[string][]string{}
	for i, id := range c.Missing {
		why := missingFailed // built without statuses: no detail to word
		if i < len(c.missingWhy) {
			why = c.missingWhy[i]
		}
		groups[why] = append(groups[why], id)
	}
	for _, why := range missingOrder {
		if ids := groups[why]; len(ids) > 0 {
			b.WriteString("; ")
			b.WriteString(strings.Join(ids, ", "))
			b.WriteString(" ")
			b.WriteString(why)
		}
	}
	b.WriteString(" — results may be incomplete.")
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
