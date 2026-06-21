package booking_test

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/booking"
)

// allTrue is the canonical all-confirmed input; reused across subtests.
var allTrue = booking.Input{
	Verified:           booking.True(),
	LinkStable:         booking.True(),
	IdentityConfirmed:  booking.True(),
	RefundabilityKnown: booking.True(),
}

func TestEvaluate_AllTrue_ReturnsReady(t *testing.T) {
	// GIVEN all four signals explicitly true
	// WHEN  Evaluate is called
	// THEN  verdict is Ready with no reasons
	v := booking.Evaluate(allTrue)

	if v.Readiness != booking.Ready {
		t.Errorf("readiness = %q, want %q", v.Readiness, booking.Ready)
	}
	if len(v.Reasons) != 0 {
		t.Errorf("reasons = %v, want none", v.Reasons)
	}
}

// unknownCases enumerates the four single-field-unknown scenarios (core AC).
var unknownCases = []struct {
	name  string
	input booking.Input
	field string // label expected in Reasons
}{
	{
		name: "verified_unknown",
		input: booking.Input{
			Verified:           booking.Unknown(),
			LinkStable:         booking.True(),
			IdentityConfirmed:  booking.True(),
			RefundabilityKnown: booking.True(),
		},
		field: "verified",
	},
	{
		name: "link_stable_unknown",
		input: booking.Input{
			Verified:           booking.True(),
			LinkStable:         booking.Unknown(),
			IdentityConfirmed:  booking.True(),
			RefundabilityKnown: booking.True(),
		},
		field: "link_stable",
	},
	{
		name: "identity_confirmed_unknown",
		input: booking.Input{
			Verified:           booking.True(),
			LinkStable:         booking.True(),
			IdentityConfirmed:  booking.Unknown(),
			RefundabilityKnown: booking.True(),
		},
		field: "identity_confirmed",
	},
	{
		name: "refundability_known_unknown",
		input: booking.Input{
			Verified:           booking.True(),
			LinkStable:         booking.True(),
			IdentityConfirmed:  booking.True(),
			RefundabilityKnown: booking.Unknown(),
		},
		field: "refundability_known",
	},
}

// TestEvaluate_SingleUnknown_NotReady is the load-bearing AC: any unknown
// field must prevent Ready.
func TestEvaluate_SingleUnknown_NotReady(t *testing.T) {
	for _, tc := range unknownCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN one field unknown, the rest true
			// WHEN  Evaluate is called
			// THEN  readiness is NOT ready, and reasons mention the field
			v := booking.Evaluate(tc.input)

			if v.Readiness == booking.Ready {
				t.Errorf("readiness = Ready, want Caution or Unverified (unknown must block Ready)")
			}
			if len(v.Reasons) == 0 {
				t.Error("reasons is empty, want at least one downgrade reason")
			}
			if !reasonsMention(v.Reasons, tc.field) {
				t.Errorf("reasons %v do not mention field %q", v.Reasons, tc.field)
			}
		})
	}
}

// falseCases enumerates the four single-field-false scenarios.
var falseCases = []struct {
	name  string
	input booking.Input
	field string
}{
	{
		name: "verified_false",
		input: booking.Input{
			Verified:           booking.False(),
			LinkStable:         booking.True(),
			IdentityConfirmed:  booking.True(),
			RefundabilityKnown: booking.True(),
		},
		field: "verified",
	},
	{
		name: "link_stable_false",
		input: booking.Input{
			Verified:           booking.True(),
			LinkStable:         booking.False(),
			IdentityConfirmed:  booking.True(),
			RefundabilityKnown: booking.True(),
		},
		field: "link_stable",
	},
	{
		name: "identity_confirmed_false",
		input: booking.Input{
			Verified:           booking.True(),
			LinkStable:         booking.True(),
			IdentityConfirmed:  booking.False(),
			RefundabilityKnown: booking.True(),
		},
		field: "identity_confirmed",
	},
	{
		name: "refundability_known_false",
		input: booking.Input{
			Verified:           booking.True(),
			LinkStable:         booking.True(),
			IdentityConfirmed:  booking.True(),
			RefundabilityKnown: booking.False(),
		},
		field: "refundability_known",
	},
}

// TestEvaluate_SingleFalse_NotReady verifies false signals prevent Ready.
func TestEvaluate_SingleFalse_NotReady(t *testing.T) {
	for _, tc := range falseCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN one field false, the rest true
			// WHEN  Evaluate is called
			// THEN  readiness is Caution, and reasons name the field
			v := booking.Evaluate(tc.input)

			if v.Readiness != booking.Caution {
				t.Errorf("readiness = %q, want %q", v.Readiness, booking.Caution)
			}
			if !reasonsMention(v.Reasons, tc.field) {
				t.Errorf("reasons %v do not mention field %q", v.Reasons, tc.field)
			}
		})
	}
}

func TestEvaluate_AllUnknown_ReturnsUnverified(t *testing.T) {
	// GIVEN all signals unknown (zero-value Input)
	// WHEN  Evaluate is called
	// THEN  verdict is Unverified
	v := booking.Evaluate(booking.Input{})

	if v.Readiness != booking.Unverified {
		t.Errorf("readiness = %q, want %q", v.Readiness, booking.Unverified)
	}
	if len(v.Reasons) != 4 {
		t.Errorf("want 4 downgrade reasons (one per unknown field), got %d: %v", len(v.Reasons), v.Reasons)
	}
}

func TestEvaluate_MixedFalseAndUnknown_ReturnsCaution(t *testing.T) {
	// GIVEN one field false and one unknown (rest true)
	// WHEN  Evaluate is called
	// THEN  verdict is Caution (false takes precedence over all-unknown path)
	v := booking.Evaluate(booking.Input{
		Verified:           booking.False(),
		LinkStable:         booking.Unknown(),
		IdentityConfirmed:  booking.True(),
		RefundabilityKnown: booking.True(),
	})

	if v.Readiness != booking.Caution {
		t.Errorf("readiness = %q, want %q", v.Readiness, booking.Caution)
	}
	if len(v.Reasons) != 2 {
		t.Errorf("want 2 reasons (1 false + 1 unknown), got %d: %v", len(v.Reasons), v.Reasons)
	}
}

func TestVerdict_Label(t *testing.T) {
	tests := []struct {
		name  string
		input booking.Input
		want  string
	}{
		{"ready", allTrue, "ready"},
		{"caution_one_false", booking.Input{
			Verified:           booking.False(),
			LinkStable:         booking.True(),
			IdentityConfirmed:  booking.True(),
			RefundabilityKnown: booking.True(),
		}, "caution (1)"},
		{"unverified", booking.Input{}, "unverified (4)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := booking.Evaluate(tc.input).Label()
			if got != tc.want {
				t.Errorf("Label() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVerdict_Summary_ReadyHasNoReasons(t *testing.T) {
	v := booking.Evaluate(allTrue)
	if v.Summary() != "all signals confirmed" {
		t.Errorf("Summary() = %q, want %q", v.Summary(), "all signals confirmed")
	}
}

func TestVerdict_Summary_ContainsReasons(t *testing.T) {
	v := booking.Evaluate(booking.Input{
		Verified:           booking.Unknown(),
		LinkStable:         booking.True(),
		IdentityConfirmed:  booking.True(),
		RefundabilityKnown: booking.True(),
	})
	if !strings.Contains(v.Summary(), "verified") {
		t.Errorf("Summary() = %q, want it to mention 'verified'", v.Summary())
	}
}

// reasonsMention checks whether any element in reasons contains field as a
// substring, covering both "field unknown → …" and "field false → …" forms.
func reasonsMention(reasons []string, field string) bool {
	for _, r := range reasons {
		if strings.Contains(r, field) {
			return true
		}
	}
	return false
}
