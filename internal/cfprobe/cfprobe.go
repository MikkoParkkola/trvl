// Package cfprobe is the budget-gated execution layer for MIK-6234 Tier-1/Tier-2
// counterfactual probes — the levers that genuinely fan out to providers
// (nearby-airport, split-ticket, hidden-city, ...). It is the firewall that
// makes that fan-out safe.
//
// Every probe draws from a two-lane probebudget: counterfactual probes use a
// separate, slow-refilling, best-effort bucket. When the bucket is empty the
// probe is refused (StatusBudgetExhausted) and the caller MUST fall back to
// cached or call-free results — never a silent fresh fan-out. Interactive
// (user-facing) traffic is never metered by this lane, so a saturated probe
// budget can never delay a live search.
//
// The fan-out itself is injected as a thunk, so this package is unit-testable
// without touching the network and composes with any producer (the hacks engine
// today; a scheduler-amortized producer for Tier 1 tomorrow).
package cfprobe

import (
	"time"

	"github.com/MikkoParkkola/trvl/internal/counterfactual"
	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/probebudget"
)

// Status reports the outcome of a probe attempt.
type Status string

const (
	// StatusRan means the budget permitted the probe and the fan-out executed.
	StatusRan Status = "ran"
	// StatusBudgetExhausted means the probe lane had no tokens; the caller must
	// serve cached / call-free results instead of fanning out.
	StatusBudgetExhausted Status = "budget_exhausted"
)

// Default-budget tuning: a small bucket that refills slowly, so interactive
// fan-out is allowed in short bursts but cannot run unbounded.
const (
	defaultCapacity     = 3.0
	defaultRefillPerSec = 1.0 / 600.0 // one token every 10 minutes
)

// Engine gates counterfactual fan-out behind a two-lane budget.
type Engine struct {
	budget *probebudget.Budget
}

// NewEngine builds an engine with the given probe-lane capacity and refill rate
// (tokens per second). A nil clock uses the real clock.
func NewEngine(capacity, refillPerSec float64, clock probebudget.Clock) *Engine {
	return &Engine{budget: probebudget.New(capacity, refillPerSec, clock)}
}

var defaultEngine = NewEngine(defaultCapacity, defaultRefillPerSec, nil)

// Default returns the process-singleton engine.
func Default() *Engine { return defaultEngine }

// Probe runs fanOut() and converts its money-saving hacks into counterfactual
// savings, but ONLY if the probe budget permits. When the budget is exhausted it
// returns (nil, StatusBudgetExhausted) and fanOut is never called — no provider
// calls are issued. fanOut is a thunk so callers inject hacks.DetectAll (or any
// producer) and tests inject a fake.
func (e *Engine) Probe(now time.Time, fanOut func() []hacks.Hack) ([]counterfactual.Saving, Status) {
	if !e.budget.TryAcquireProbe() {
		return nil, StatusBudgetExhausted
	}
	return HacksToSavings(fanOut(), now), StatusRan
}

// ProbeTokens exposes remaining probe-lane tokens (for diagnostics/tests).
func (e *Engine) ProbeTokens() float64 { return e.budget.ProbeTokens() }

// HacksToSavings converts money-saving hacks into counterfactual savings. Hacks
// with no positive saving are dropped. CallFree is false: these came from a
// fan-out, and the renderer must not present them as zero-cost.
func HacksToSavings(found []hacks.Hack, now time.Time) []counterfactual.Saving {
	var out []counterfactual.Saving
	for _, h := range found {
		if h.Savings <= 0 {
			continue
		}
		desc := h.Title
		if h.Description != "" {
			desc = h.Title + " — " + h.Description
		}
		out = append(out, counterfactual.Saving{
			Kind:        counterfactual.KindProbe,
			Description: desc,
			Amount:      h.Savings,
			Currency:    h.Currency,
			AsOf:        now,
			CallFree:    false,
		})
	}
	return out
}
