package mcp

import (
	"fmt"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// withProviderFailures appends per-provider failure lines to err, if any.
// tools/call keeps only the error text on failure, so this is the only way an
// agent sees which providers blocked or failed (MIK-7989).
func withProviderFailures(err error, statuses []models.ProviderStatus) error {
	if lines := models.ProviderFailureLines(statuses); lines != "" {
		return fmt.Errorf("%w\n%s", err, lines)
	}
	return err
}

// incompleteAbsence returns the wording for an empty search where some
// provider did not answer, and false when every provider answered and a plain
// "nothing found" is true.
func incompleteAbsence(what string, statuses []models.ProviderStatus) (string, bool) {
	c := models.ComputeCompleteness(statuses)
	if c.MayClaimExhaustive() {
		return "", false
	}
	return fmt.Sprintf("No %s returned. %s\n%s", what, c.IncompleteNote(), models.ProviderFailureLines(statuses)), true
}
