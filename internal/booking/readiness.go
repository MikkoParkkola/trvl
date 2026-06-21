// Package booking provides a pure, network-free booking-readiness contract that
// composes existing trust signals (price verification, link durability, identity
// confirmation, and refundability) into a single verdict for CLI and MCP
// consumers.
//
// Verdict lattice (all inputs are tri-state: true / false / unknown)
//
//	All four signals explicitly true              → Ready
//	Any signal unknown, none false               → Caution
//	Any signal false (regardless of unknowns)    → Caution
//	All signals unknown (no information at all)  → Unverified
//
// The defining invariant: unknown is never treated as true. An absence of
// evidence is never elevated to a positive assertion. The only path to Ready
// is four explicit "yes" values.
package booking

import "strings"

// Readiness is the coarse booking-readiness verdict.
type Readiness string

const (
	// Ready means all four signals are explicitly confirmed true. The offer is
	// verified, the link is durable, the property identity is confirmed, and
	// the refundability status is known. Safe to surface as a primary CTA.
	Ready Readiness = "ready"

	// Caution means at least one signal is false or unknown. The offer may be
	// bookable but the consumer should verify before committing.
	Caution Readiness = "caution"

	// Unverified means every signal is unknown — trvl has no evidence to judge
	// any dimension of the offer. Shown only when signals are entirely absent.
	Unverified Readiness = "unverified"
)

// Signal is a tri-state boolean: explicitly true, explicitly false, or unknown
// (nil). Unknown is structurally distinct from false; callers must set a
// non-nil value to make a positive or negative assertion.
type Signal = *bool

// True, False, and Unknown are convenience constructors for Signal values.
// They eliminate the &localVar dance at every call site.
func True() Signal  { b := true; return &b }
func False() Signal { b := false; return &b }

// Unknown returns nil, which is the zero value for Signal. It is provided for
// documentary clarity when building an Input struct.
func Unknown() Signal { return nil }

// Input holds the four independent trust signals that feed the verdict.
// Each field is a Signal (*bool): nil means the information is not available.
type Input struct {
	// Verified is true when the quoted price/offer has been independently
	// confirmed (e.g. Confidence.Label == "high" with Rated == true). False
	// when the price is known to be stale or unverifiable. Nil when trvl
	// returned an unrated Confidence.
	Verified Signal

	// LinkStable is true when the booking deep-link is a direct OTA or property
	// URL (LinkDurability == "stable"). False when it is an expiring ad-click
	// redirect (LinkDurability == "expiring"). Nil when no link is present or
	// durability was not assessed.
	LinkStable Signal

	// IdentityConfirmed is true when the property or fare identity has been
	// positively matched (e.g. MatchConfidence == "high", or name-matched with
	// a canonical Google hotel ID). False when only a fuzzy name match was
	// possible. Nil when no identity signal is available.
	IdentityConfirmed Signal

	// RefundabilityKnown is true when the refund policy is known (regardless of
	// whether the offer is actually refundable). False should not normally be
	// used here — "not refundable" is still "known"; reserve false for the case
	// where the data was available but actively contradicted. Nil when the
	// provider did not surface any cancellation/refundability information.
	RefundabilityKnown Signal
}

// Verdict is the output of Evaluate: a coarse Readiness level and a slice of
// human-readable reasons explaining any downgrade.
type Verdict struct {
	Readiness Readiness
	// Reasons lists the signals that caused a downgrade, e.g.
	// ["refundability unknown → downgraded to caution"].
	// Empty when Readiness is Ready.
	Reasons []string
}

// named bundles a signal with its display name for uniform reason generation.
type named struct {
	label  string
	signal Signal
}

// Evaluate composes the four signals into a Verdict according to the lattice
// documented in the package comment. It is pure and allocation-minimal: only
// the Reasons slice allocates, and only when a downgrade occurs.
//
// Contract:
//   - All signals true          → Ready, no reasons.
//   - Any signal false          → Caution, reason per false signal.
//   - Any signal unknown        → Caution (unless all unknown → Unverified), reason per unknown signal.
//   - All signals unknown       → Unverified, reasons for each.
func Evaluate(in Input) Verdict {
	signals := [4]named{
		{"verified", in.Verified},
		{"link_stable", in.LinkStable},
		{"identity_confirmed", in.IdentityConfirmed},
		{"refundability_known", in.RefundabilityKnown},
	}

	var reasons []string
	allUnknown := true
	anyFalseOrUnknown := false

	for _, s := range signals {
		switch {
		case s.signal == nil:
			reasons = append(reasons, s.label+" unknown → downgraded")
			anyFalseOrUnknown = true
			// allUnknown stays true
		case !*s.signal:
			reasons = append(reasons, s.label+" false → downgraded")
			anyFalseOrUnknown = true
			allUnknown = false
		default:
			// explicitly true
			allUnknown = false
		}
	}

	if !anyFalseOrUnknown {
		return Verdict{Readiness: Ready}
	}
	if allUnknown {
		return Verdict{Readiness: Unverified, Reasons: reasons}
	}
	return Verdict{Readiness: Caution, Reasons: reasons}
}

// Label returns a short display string for embedding in CLI table cells or
// JSON fields, e.g. "ready", "caution (3)", "unverified".
func (v Verdict) Label() string {
	if len(v.Reasons) == 0 {
		return string(v.Readiness)
	}
	return string(v.Readiness) + " (" + itoa(len(v.Reasons)) + ")"
}

// Summary joins all downgrade reasons into a single human-readable sentence
// suitable for a CLI footer or MCP annotation.
func (v Verdict) Summary() string {
	if len(v.Reasons) == 0 {
		return "all signals confirmed"
	}
	return strings.Join(v.Reasons, "; ")
}

// itoa converts a small non-negative integer to its decimal string without
// importing strconv (keeps the package dependency-free for trivial counts).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
