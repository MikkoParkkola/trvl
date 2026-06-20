// Package models — confidence.go: an honest, evidence-based assessment of how
// likely a quoted result is to be bookable at the shown price. It is distinct
// from deal quality ("is this cheap?") — it answers "can I trust this price
// right now?". The score is computed by internal/fareintel from existing
// signals (price freshness, provider reliability, multi-source corroboration,
// and — when available — historical price position). When trvl lacks the signal
// to judge, Rated is false and Label is ConfidenceUnrated: trvl never fabricates
// a confidence number.
//
// Tracking: innovation #3 (confidence + freshness scoring).
package models

// Confidence label constants.
const (
	ConfidenceHigh    = "high"
	ConfidenceMedium  = "medium"
	ConfidenceLow     = "low"
	ConfidenceUnrated = "unrated"
)

// Confidence is a per-result bookability assessment. The zero value is an
// honest "unrated" (Rated=false, Label==""); callers should treat an empty or
// unrated Confidence as "no signal", never as a low score.
type Confidence struct {
	// Rated is false when there was insufficient signal to score the result.
	// When false, Score is meaningless and Label is ConfidenceUnrated.
	Rated bool `json:"rated"`
	// Score is the 0..1 likelihood the price is bookable as shown. Only
	// meaningful when Rated is true; omitted from JSON when unrated/zero.
	Score float64 `json:"score,omitempty"`
	// Label is a coarse bucket: high|medium|low|unrated.
	Label string `json:"label"`
	// Freshness classifies the assessed price (live|recent|stale); empty when
	// the price age is unknown.
	Freshness string `json:"freshness,omitempty"`
	// Basis is a short human-readable explanation, e.g.
	// "live API price corroborated by 3 sources".
	Basis string `json:"basis"`
	// Signals lists the machine-readable signal tags that fed the score, so a
	// caller can see exactly why a result scored as it did.
	Signals []string `json:"signals,omitempty"`
}

// Percent renders the score as an integer percentage 0..100. Returns 0 when
// unrated (callers should check Rated before showing a percentage).
func (c Confidence) Percent() int {
	if !c.Rated {
		return 0
	}
	p := int(c.Score*100 + 0.5)
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	return p
}

// UnratedConfidence returns an honest unrated assessment with the given reason.
func UnratedConfidence(reason string) Confidence {
	return Confidence{Rated: false, Label: ConfidenceUnrated, Basis: reason}
}
