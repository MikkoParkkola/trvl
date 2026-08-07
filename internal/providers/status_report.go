package providers

import (
	"sort"
	"time"
)

// StatusRow is one provider's operational status: health-log aggregates merged
// with live circuit-breaker state. It is the single source of truth rendered by
// the MCP provider_health tool, the `trvl status` CLI, and the /dashboard route.
//
// The JSON tags are a stable wire contract — MCP clients parse them — so do not
// rename them without bumping the tool surface.
type StatusRow struct {
	Provider           string  `json:"provider"`
	Name               string  `json:"name,omitempty"`
	TotalCalls         int     `json:"total_calls"`
	SuccessCount       int     `json:"success_count"`
	ErrorCount         int     `json:"error_count"`
	TimeoutCount       int     `json:"timeout_count"`
	SuccessRate        float64 `json:"success_rate"`
	AvgLatencyMs       int64   `json:"avg_latency_ms"`
	TotalResults       int     `json:"total_results"`
	AvgResults         float64 `json:"avg_results"`
	LastResults        int     `json:"last_results"`
	LastSeen           string  `json:"last_seen,omitempty"`
	LastSuccess        string  `json:"last_success,omitempty"`
	LastFailure        string  `json:"last_failure,omitempty"`
	Freshness          string  `json:"freshness"`
	LastError          string  `json:"last_error,omitempty"`
	LastErrorClass     string  `json:"last_error_class,omitempty"`
	LastHintCode       string  `json:"last_hint_code,omitempty"`
	CircuitState       string  `json:"circuit_state"`
	CircuitErrorCount  int     `json:"circuit_error_count,omitempty"`
	CircuitReason      string  `json:"circuit_reason,omitempty"`
	CircuitNextRetryAt string  `json:"circuit_next_retry_at,omitempty"`
	FixHint            string  `json:"fix_hint,omitempty"`
}

// IsHealthy reports whether the provider is currently usable: circuit closed
// (or recovering via half-open) and, when calls have been made, a success rate
// at or above 50%. Providers with no recorded calls are treated as healthy
// (nothing has failed yet).
func (r StatusRow) IsHealthy() bool {
	if r.CircuitState == "open" {
		return false
	}
	if r.TotalCalls == 0 {
		return true
	}
	return r.SuccessRate >= 0.5
}

// RateLimited reports whether the provider's most recent failure was a
// rate-limit (HTTP 429 / typed RATE_LIMITED) signal.
func (r StatusRow) RateLimited() bool {
	return r.LastErrorClass == string(FixHintRateLimited)
}

// BuildStatusReport merges the per-provider health summary (from the
// health.jsonl log in dir) with live circuit-breaker state (from reg) into a
// stable, provider-sorted slice.
//
// reg may be nil (health-only view); dir is the ~/.trvl directory. now anchors
// circuit-cooldown math — pass the zero Time to default to time.Now().
func BuildStatusReport(reg *Registry, dir string, now time.Time) []StatusRow {
	if now.IsZero() {
		now = time.Now()
	}

	summary := HealthSummary(dir)
	configs := map[string]*ProviderConfig{}
	if reg != nil {
		// ListSafe snapshots each config under RLock so the breaker reads in
		// CircuitBreakerHealth below never race concurrent MarkSuccess/MarkError
		// from an in-flight search. BuildStatusReport runs on concurrent paths
		// (MCP provider_health tool, HTTP dashboard) — #144 class; MIK-5858.
		for _, cfg := range reg.ListSafe() {
			configs[cfg.ID] = cfg
		}
	}

	// max rather than the sum. The capacity here is only a hint, and the two
	// maps overlap heavily in practice (a provider usually appears in both), so
	// the sum over-allocates anyway. CodeQL flags len(a)+len(b) as a possible
	// allocation-size overflow: it cannot prove the addition stays in range,
	// and while two in-memory map lengths cannot realistically sum past int64,
	// "realistically" is not something a scanner can check. Taking the larger
	// is exact for the overlapping case, never smaller than either input, and
	// has no addition to overflow.
	ids := make(map[string]bool, max(len(summary), len(configs)))
	for id := range summary {
		ids[id] = true
	}
	for id := range configs {
		ids[id] = true
	}

	rows := make([]StatusRow, 0, len(ids))
	for id := range ids {
		h := summary[id]
		if h.Provider == "" {
			h.Provider = id
			h.Freshness = "unknown"
		}
		cfg := configs[id]
		circuit := CircuitBreakerHealth(cfg, now)
		name := ""
		if cfg != nil {
			name = cfg.Name
		}
		rows = append(rows, StatusRow{
			Provider:           h.Provider,
			Name:               name,
			TotalCalls:         h.TotalCalls,
			SuccessCount:       h.SuccessCount,
			ErrorCount:         h.ErrorCount,
			TimeoutCount:       h.TimeoutCount,
			SuccessRate:        h.SuccessRate,
			AvgLatencyMs:       h.AvgLatencyMs,
			TotalResults:       h.TotalResults,
			AvgResults:         h.AvgResults,
			LastResults:        h.LastResults,
			LastSeen:           h.LastSeen,
			LastSuccess:        h.LastSuccess,
			LastFailure:        h.LastFailure,
			Freshness:          h.Freshness,
			LastError:          h.LastError,
			LastErrorClass:     h.LastErrorClass,
			LastHintCode:       h.LastHintCode,
			CircuitState:       circuit.State,
			CircuitErrorCount:  circuit.ErrorCount,
			CircuitReason:      circuit.Reason,
			CircuitNextRetryAt: circuit.NextRetryAt,
			FixHint:            circuit.FixHint,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Provider < rows[j].Provider })
	return rows
}
