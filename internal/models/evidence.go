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
)

// ClassifyProviderError maps an error to StatusTimeout when it is a deadline /
// timeout, otherwise StatusFailed. This is the distinction that prevents a
// timeout from being presented to the user as "nothing found".
func ClassifyProviderError(err error) string {
	if err == nil {
		return StatusOK
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return StatusTimeout
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out") || strings.Contains(msg, "deadline") {
		return StatusTimeout
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
		if s.Status == StatusTimeout || s.Status == StatusFailed || s.Status == StatusError {
			c.Missing = append(c.Missing, s.ID)
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
	if len(c.Missing) > 0 {
		b.WriteString("; ")
		b.WriteString(strings.Join(c.Missing, ", "))
		b.WriteString(" did not respond")
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
