package booking

import (
	"strings"
	"testing"
)

// TestEvaluateWith_DeclaredCeilingIsReported covers the distinction an external
// tester surfaced: he ran six indexed properties through a path that returned
// Caution every time and could not tell whether that meant six uncertain
// properties or a scale that never says better. The verdict read identically
// either way.
//
// A source that cannot supply a signal must say so, because Ready needs all four
// true — so such a source is capped no matter how good the property's data is.
func TestEvaluateWith_DeclaredCeilingIsReported(t *testing.T) {
	// Everything obtainable is true except the one this source cannot see.
	in := Input{
		Verified:          True(),
		LinkStable:        True(),
		IdentityConfirmed: True(),
		// RefundabilityKnown deliberately absent: unobtainable here.
	}

	v := EvaluateWith(in, Availability{NoRefundability: true})

	if v.Readiness != Caution {
		t.Fatalf("expected Caution, got %q", v.Readiness)
	}
	if !v.Capped() {
		t.Fatal("a source missing a required signal must report a ceiling; without it Caution implies a finding about the property")
	}
	if v.Ceiling != Caution {
		t.Fatalf("expected the ceiling to be Caution, got %q", v.Ceiling)
	}
	if len(v.CeilingReasons) != 1 {
		t.Fatalf("expected one ceiling reason naming the unobtainable signal, got %v", v.CeilingReasons)
	}
}

// TestEvaluateWith_FullSourceIsNotCapped is the half that keeps the flag
// meaningful. A room-level source can reach Ready, so marking it capped would
// train readers to ignore the signal.
func TestEvaluateWith_FullSourceIsNotCapped(t *testing.T) {
	in := Input{
		Verified:           True(),
		LinkStable:         True(),
		IdentityConfirmed:  True(),
		RefundabilityKnown: True(),
	}

	v := EvaluateWith(in, Availability{})

	if v.Readiness != Ready {
		t.Fatalf("expected Ready, got %q", v.Readiness)
	}
	if v.Capped() {
		t.Fatalf("a source that can supply every signal must not be reported as capped, got ceiling %q", v.Ceiling)
	}
}

// TestEvaluate_UnchangedForExistingCallers guards the migration: plain Evaluate
// must keep behaving exactly as before, or every caller that has not opted in
// starts claiming a ceiling it never declared.
func TestEvaluate_UnchangedForExistingCallers(t *testing.T) {
	in := Input{Verified: True()} // three signals absent

	v := Evaluate(in)

	if v.Readiness != Caution {
		t.Fatalf("expected Caution, got %q", v.Readiness)
	}
	if v.Capped() {
		t.Fatal("Evaluate must not imply a ceiling; only a caller that declares its limits gets one")
	}
}

// TestEvaluateWith_CappedSourceStillDistinguishesTheProperty proves the ceiling
// does not flatten real findings. A capped source that also has a false signal
// must still list that as a reason, or the ceiling would mask a genuine problem.
func TestEvaluateWith_CappedSourceStillDistinguishesTheProperty(t *testing.T) {
	in := Input{
		Verified:          True(),
		LinkStable:        False(), // a real finding: the link expires
		IdentityConfirmed: True(),
	}

	v := EvaluateWith(in, Availability{NoRefundability: true})

	if !v.Capped() {
		t.Fatal("expected the source to remain capped")
	}
	var sawLink bool
	for _, r := range v.Reasons {
		if r == "link_stable false → downgraded" {
			sawLink = true
		}
	}
	if !sawLink {
		t.Fatalf("the ceiling swallowed a real finding about the property; reasons were %v", v.Reasons)
	}
}

// TestEvaluateWith_DeclaredUnavailableCannotBeAsserted closes the hole where a
// ceiling did not constrain anything.
//
// Declaring a signal unobtainable and then passing it as true is a
// contradiction, and honouring the assertion let Readiness reach Ready while the
// Ceiling said Caution. The declaration wins: the signal is treated as absent
// whatever was passed, so the ceiling actually caps the verdict.
func TestEvaluateWith_DeclaredUnavailableCannotBeAsserted(t *testing.T) {
	in := Input{
		Verified:           True(),
		LinkStable:         True(),
		IdentityConfirmed:  True(),
		RefundabilityKnown: True(), // contradicts the declaration below
	}

	v := EvaluateWith(in, Availability{NoRefundability: true})

	if v.Readiness == Ready {
		t.Fatal("reached Ready on a source that declared refundability unobtainable; the ceiling has to cap the verdict, not sit beside it")
	}
	if !v.Capped() {
		t.Fatal("expected the verdict to remain capped")
	}
}

// TestEvaluate_CarriesNoCeiling keeps legacy callers observably unchanged. A
// caller that never declared a limit must not start reporting one, even a
// permissive one.
func TestEvaluate_CarriesNoCeiling(t *testing.T) {
	v := Evaluate(Input{Verified: True()})

	if v.Ceiling != "" {
		t.Fatalf("Evaluate set a ceiling of %q; callers that declared nothing must carry nothing", v.Ceiling)
	}
	if len(v.CeilingReasons) != 0 {
		t.Fatalf("Evaluate set ceiling reasons %v", v.CeilingReasons)
	}
}

// TestEvaluateWith_UnavailableSignalIsNotAPropertyFinding keeps the two reason
// lists meaning different things.
//
// Listing an unobtainable signal in the ordinary reasons was the reported problem
// in a new costume: "refundability_known absent → downgraded" reads as a finding
// about the property, when the truth is this source has nothing to look at. A
// signal belongs to exactly one list — the ceiling explains the source, the
// reasons explain the offer.
func TestEvaluateWith_UnavailableSignalIsNotAPropertyFinding(t *testing.T) {
	in := Input{
		Verified:          True(),
		LinkStable:        False(), // a real finding about this offer
		IdentityConfirmed: True(),
	}

	v := EvaluateWith(in, Availability{NoRefundability: true})

	for _, r := range v.Reasons {
		if strings.Contains(r, "refundability_known") {
			t.Fatalf("an unobtainable signal was reported as a property finding: %q", r)
		}
	}
	var sawCeiling, sawLink bool
	for _, r := range v.CeilingReasons {
		if strings.Contains(r, "refundability_known") {
			sawCeiling = true
		}
	}
	for _, r := range v.Reasons {
		if strings.Contains(r, "link_stable") {
			sawLink = true
		}
	}
	if !sawCeiling {
		t.Fatalf("the unobtainable signal is missing from the ceiling reasons: %v", v.CeilingReasons)
	}
	if !sawLink {
		t.Fatalf("the real finding about the offer was lost: %v", v.Reasons)
	}
	if !v.Capped() {
		t.Fatal("expected the verdict to remain capped")
	}
}

// TestSummary_CappedVerdictDoesNotClaimAllSignalsConfirmed catches the
// contradiction that removing the ceiling signal from the ordinary reasons
// created. With those reasons empty, Summary said "all signals confirmed", so the
// CLI printed "Booking readiness: caution — all signals confirmed": a verdict that
// is not ready, asserting that everything checked out.
func TestSummary_CappedVerdictDoesNotClaimAllSignalsConfirmed(t *testing.T) {
	in := Input{Verified: True(), LinkStable: True(), IdentityConfirmed: True()}
	v := EvaluateWith(in, Availability{NoRefundability: true})

	if len(v.Reasons) != 0 {
		t.Fatalf("precondition: expected no ordinary reasons on a best-case capped verdict, got %v", v.Reasons)
	}
	got := v.Summary()
	if strings.Contains(got, "all signals confirmed") {
		t.Fatalf("a capped verdict claims every signal confirmed: %q", got)
	}
	if !strings.Contains(got, "refundability_known") {
		t.Fatalf("the summary should name what could not be checked, got %q", got)
	}
}

// TestSummary_CeilingWithoutReasonsDoesNotDangle covers the decoded-Verdict case:
// Verdict is exported and carries JSON tags, so a value arriving from a client can
// hold a ceiling with no reasons. Joining an empty list would leave a trailing
// separator in user-visible text.
func TestSummary_CeilingWithoutReasonsDoesNotDangle(t *testing.T) {
	v := Verdict{Readiness: Ready, Ceiling: Caution}
	got := v.Summary()
	if strings.HasSuffix(got, "; ") || strings.HasSuffix(got, ";") {
		t.Fatalf("summary ends in a dangling separator: %q", got)
	}
	if got == "" {
		t.Fatal("summary is empty")
	}
}
