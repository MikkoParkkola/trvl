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
// The defining invariant: an absent signal is never treated as true. An absence
// of evidence is never elevated to a positive assertion. The only path to Ready
// is four explicit "yes" values.
//
// Not every data source can supply every signal. The hotel-prices endpoint
// carries no cancellation terms at all, so refundability there is not "we looked
// and found nothing" but "this source has nothing to look at" - and a verdict
// that reports both the same way tells a reader a property was assessed and
// found wanting when in fact the path itself is capped. Availability lets a
// caller declare that ceiling so the verdict can state it, which is the
// difference between honest uncertainty and a silent two-tier scale wearing a
// three-tier label.
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

// Availability declares which signals a data source is capable of supplying.
//
// The zero value claims everything is obtainable, which is the honest claim for
// a room-level source. A source that structurally cannot produce a signal must
// say so, or its verdict implies a judgement it never made.
type Availability struct {
	NoVerification   bool // cannot establish whether a price is verified
	NoLinkDurability bool // cannot classify a booking link as durable
	NoIdentity       bool // cannot confirm the property or fare identity
	NoRefundability  bool // carries no cancellation or refund terms
}

// beyondReach reports whether the named signal is out of reach for this source.
func (a Availability) beyondReach(label string) bool {
	switch label {
	case "verified":
		return a.NoVerification
	case "link_stable":
		return a.NoLinkDurability
	case "identity_confirmed":
		return a.NoIdentity
	case "refundability_known":
		return a.NoRefundability
	}
	return false
}

// Verdict is the output of Evaluate: a coarse Readiness level and a slice of
// human-readable reasons explaining any downgrade.
type Verdict struct {
	Readiness Readiness
	// Reasons lists the signals that caused a downgrade, e.g.
	// ["refundability_known absent → downgraded"].
	// Empty when Readiness is Ready.
	Reasons []string

	// Ceiling is the best readiness this data source could ever report. When it
	// is below Ready, a Caution verdict is not a finding about the property: the
	// path cannot do better for anything. Callers should say so rather than
	// leaving a reader to infer a problem that is not there.
	Ceiling Readiness

	// CeilingReasons names the signals the source cannot supply at all, as
	// distinct from ones it looked for and did not find.
	CeilingReasons []string
}

// Capped reports whether this verdict came from a source that could not have
// reached Ready regardless of the property.
func (v Verdict) Capped() bool {
	return v.Ceiling != "" && v.Ceiling != Ready
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
	v := EvaluateWith(in, Availability{})
	// Legacy callers get no ceiling at all, not a ceiling of Ready. A caller that
	// has not declared its limits should be observably unchanged, or every
	// existing consumer starts carrying a claim it never made.
	v.Ceiling = ""
	return v
}

// EvaluateWith is Evaluate for a source that cannot supply every signal. The
// resulting Verdict carries the ceiling, so a caller can distinguish "this
// property is uncertain" from "this path never says better than caution".
func EvaluateWith(in Input, av Availability) Verdict {
	signals := [4]named{
		{"verified", in.Verified},
		{"link_stable", in.LinkStable},
		{"identity_confirmed", in.IdentityConfirmed},
		{"refundability_known", in.RefundabilityKnown},
	}

	var reasons []string
	var ceilingReasons []string
	allUnknown := true
	anyFalseOrUnknown := false

	for i, s := range signals {
		if av.beyondReach(s.label) {
			ceilingReasons = append(ceilingReasons, s.label+" not available from this source")
			// Declaring a signal unobtainable and then asserting it is a
			// contradiction, and honouring the assertion would let Readiness
			// reach Ready while Ceiling says Caution — a ceiling that does not
			// constrain anything. The declaration wins: the signal is treated as
			// absent regardless of what was passed.
			signals[i].signal = nil
			// It constrains the verdict but does NOT appear in the ordinary
			// reasons. Listing it there was the original complaint in a new
			// costume: "refundability unknown → downgraded" reads as a finding
			// about the property, when the truth is that this source has nothing
			// to look at. The two lists mean different things and a signal
			// belongs to exactly one of them.
			anyFalseOrUnknown = true
			continue
		}
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

	ceiling := Ready
	if len(ceilingReasons) > 0 {
		// A source missing any signal can never reach Ready, because Ready needs
		// all four explicitly true.
		ceiling = Caution
	}

	switch {
	case !anyFalseOrUnknown:
		return Verdict{Readiness: Ready, Ceiling: ceiling, CeilingReasons: ceilingReasons}
	case allUnknown:
		return Verdict{Readiness: Unverified, Reasons: reasons, Ceiling: ceiling, CeilingReasons: ceilingReasons}
	default:
		return Verdict{Readiness: Caution, Reasons: reasons, Ceiling: ceiling, CeilingReasons: ceilingReasons}
	}
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
		// "all signals confirmed" is only true when every signal was obtainable
		// and confirmed. On a capped source the ordinary reasons are empty
		// because the missing signal is a ceiling reason, not a property
		// finding — and printing "caution — all signals confirmed" asserts
		// something plainly false about a verdict that is not ready.
		// The reasons guard matters because Verdict is exported and decodable:
		// a value round-tripped through JSON can carry a ceiling with no reasons,
		// and appending an empty join would emit a dangling separator.
		if v.Capped() && len(v.CeilingReasons) > 0 {
			return "every obtainable signal confirmed; " + strings.Join(v.CeilingReasons, "; ")
		}
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
