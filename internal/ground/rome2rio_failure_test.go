package ground

import (
	"errors"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestRome2RioParseFailure pins the zero-route discriminator end-to-end through
// the provider status mapping, fixture-free:
//   - decoded>0           -> nil (healthy, real routes)
//   - matched>0,decoded==0 -> typed ErrParseFailed -> StatusFailed (breaker counts it)
//   - matched==0,decoded==0 -> "no route" -> isProviderNotApplicable -> StatusSkipped
//
// The last two cases are the reliability fix: before, every zero-route render
// returned a generic untyped error that classified as StatusFailed, so a city
// pair Rome2Rio genuinely does not cover would false-trip the circuit breaker.
func TestRome2RioParseFailure(t *testing.T) {
	if err := rome2rioParseFailure(3, 5, "London", "Paris"); err != nil {
		t.Errorf("decoded>0 should be nil, got %v", err)
	}

	parseFail := rome2rioParseFailure(0, 4, "London", "Paris")
	if !errors.Is(parseFail, models.ErrParseFailed) {
		t.Errorf("matched>0,decoded==0 should wrap ErrParseFailed, got %v", parseFail)
	}
	if got := models.ClassifyProviderError(parseFail); got != models.StatusFailed {
		t.Errorf("parse failure should classify as StatusFailed, got %v", got)
	}
	if isProviderNotApplicable(parseFail) {
		t.Errorf("parse failure must NOT read as provider-not-applicable: %v", parseFail)
	}

	noRoute := rome2rioParseFailure(0, 0, "Helsinki", "Tromso")
	if errors.Is(noRoute, models.ErrParseFailed) {
		t.Errorf("genuine empty should NOT wrap ErrParseFailed, got %v", noRoute)
	}
	if !isProviderNotApplicable(noRoute) {
		t.Errorf("genuine no-route should be provider-not-applicable (StatusSkipped path), got %v", noRoute)
	}
}
