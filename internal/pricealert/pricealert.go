// Package pricealert implements proactive price-drop detection for watched
// routes. It is deliberately dependency-free and pure: given a persisted
// baseline, a freshly observed price, and a configurable threshold, it decides
// whether a drop is large enough — and novel enough — to warrant alerting the
// user exactly once.
//
// The model turns trvl's watch domain from pull (the user asks "what's the
// price?") into push (trvl says "the price just dropped 12% — book now"). The
// package owns only the decision logic; persistence and notification live in the
// watch package that drives it.
package pricealert

// DefaultDropPercent is the percentage fall from baseline that fires an alert
// when a watch sets no explicit threshold.
const DefaultDropPercent = 10.0

// Threshold configures how big a fall from the baseline must be before an alert
// fires. The two limbs are independent: a drop that satisfies EITHER the
// percentage OR the absolute limb qualifies. When both limbs are zero the
// DefaultDropPercent is applied so a watch always has sane proactive behaviour.
type Threshold struct {
	// DropPercent is the minimum percentage fall from baseline (e.g. 10 means
	// "alert when the fare is 10% or more below the baseline"). Ignored when <= 0.
	DropPercent float64
	// DropAbsolute is the minimum absolute fall from baseline in the watch's
	// currency units. Ignored when <= 0.
	DropAbsolute float64
}

// effective returns the threshold to apply, substituting the default percentage
// when the caller configured neither limb.
func (t Threshold) effective() Threshold {
	if t.DropPercent <= 0 && t.DropAbsolute <= 0 {
		return Threshold{DropPercent: DefaultDropPercent}
	}
	return t
}

// State is the persisted, per-watch memory the detector needs across checks.
// It is plain data so callers can serialise it alongside their own records.
type State struct {
	// Baseline is the reference fare a drop is measured against. Zero means
	// "not yet captured": the next positive observation establishes it. The
	// baseline tracks the running high-water mark so a recovered (risen) price
	// re-arms the detector against the new peak.
	Baseline float64
	// LastAlertedAt is the price at which the most recent alert fired, used for
	// deduplication. Zero means "never alerted at the current baseline".
	LastAlertedAt float64
}

// Alert describes a fired price-drop event.
type Alert struct {
	Baseline    float64 // reference fare the drop was measured against
	Current     float64 // the freshly observed price
	Drop        float64 // Baseline - Current (positive == cheaper)
	DropPercent float64 // Drop / Baseline * 100
}

// Evaluate folds a new observation into the state and decides whether to alert.
//
// It returns the updated state (which the caller must persist), the Alert when
// one fired, and a boolean for convenient branching. The rules:
//
//   - A non-positive price (no data) is a no-op: state is returned unchanged.
//   - The first positive price captures the baseline and never alerts.
//   - A price at or above the baseline raises the baseline (new high-water mark)
//     and re-arms dedup; it never alerts.
//   - A price below the baseline alerts only when the fall meets the threshold
//     AND is a new low strictly below the last alerted price — so a single drop
//     fires exactly one alert, and a flat or partial recovery stays silent. A
//     further, deeper drop past the threshold fires again, as a genuinely new
//     event.
func Evaluate(state State, current float64, t Threshold) (State, Alert, bool) {
	if current <= 0 {
		return state, Alert{}, false
	}

	// First observation establishes the baseline.
	if state.Baseline <= 0 {
		state.Baseline = current
		state.LastAlertedAt = 0
		return state, Alert{}, false
	}

	// Price recovered to or above the reference: move the high-water mark up and
	// re-arm the dedup window against the new peak.
	if current >= state.Baseline {
		state.Baseline = current
		state.LastAlertedAt = 0
		return state, Alert{}, false
	}

	drop := state.Baseline - current
	dropPct := drop / state.Baseline * 100

	eff := t.effective()
	meetsPct := eff.DropPercent > 0 && dropPct >= eff.DropPercent
	meetsAbs := eff.DropAbsolute > 0 && drop >= eff.DropAbsolute
	if !meetsPct && !meetsAbs {
		return state, Alert{}, false
	}

	// Dedup: only alert on a genuinely new low. If we already alerted at this
	// price or higher (a flat hold or partial bounce), stay silent.
	if state.LastAlertedAt > 0 && current >= state.LastAlertedAt {
		return state, Alert{}, false
	}

	state.LastAlertedAt = current
	return state, Alert{
		Baseline:    state.Baseline,
		Current:     current,
		Drop:        drop,
		DropPercent: dropPct,
	}, true
}
