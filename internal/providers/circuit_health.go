package providers

import (
	"fmt"
	"time"
)

// CircuitHealth is the user-facing state of the provider circuit breaker.
type CircuitHealth struct {
	State       string `json:"state"` // closed, open, half_open
	ErrorCount  int    `json:"error_count,omitempty"`
	Reason      string `json:"reason,omitempty"`
	NextRetryAt string `json:"next_retry_at,omitempty"`
	FixHint     string `json:"fix_hint,omitempty"`
}

// CircuitBreakerHealth mirrors the runtime's circuit-break decision so
// provider_health can explain whether a provider will be tried, skipped, or
// allowed through as a half-open recovery probe.
func CircuitBreakerHealth(cfg *ProviderConfig, now time.Time) CircuitHealth {
	if cfg == nil {
		return CircuitHealth{State: "unknown"}
	}
	if now.IsZero() {
		now = time.Now()
	}
	if cfg.ErrorCount < circuitBreakerThreshold {
		return CircuitHealth{State: "closed", ErrorCount: cfg.ErrorCount}
	}

	tripAt := cfg.LastErrorAt
	if tripAt.IsZero() {
		tripAt = cfg.LastSuccess
	}
	if tripAt.IsZero() {
		return CircuitHealth{
			State:      "open",
			ErrorCount: cfg.ErrorCount,
			Reason:     fmt.Sprintf("tripped after %d consecutive failures; no failure timestamp recorded", cfg.ErrorCount),
			FixHint:    "fix the upstream credential, cookie, or endpoint, then run `trvl provider reset <id>` to clear the breaker",
		}
	}

	retryAt := tripAt.Add(circuitBreakerCooldown)
	if now.Sub(tripAt) < circuitBreakerCooldown {
		return CircuitHealth{
			State:       "open",
			ErrorCount:  cfg.ErrorCount,
			Reason:      fmt.Sprintf("tripped after %d consecutive failures", cfg.ErrorCount),
			NextRetryAt: retryAt.UTC().Format(time.RFC3339),
			FixHint:     "wait for cooldown to elapse, or run `trvl provider reset <id>` to retry immediately",
		}
	}
	return CircuitHealth{
		State:      "half_open",
		ErrorCount: cfg.ErrorCount,
		Reason:     "cooldown elapsed; the next search will probe this provider",
		FixHint:    "run a provider search to test recovery, or reset the provider with `trvl provider reset <id>` after fixing credentials",
	}
}
