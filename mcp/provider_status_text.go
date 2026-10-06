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
