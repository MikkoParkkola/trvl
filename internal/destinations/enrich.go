package destinations

import (
	"context"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// enrichBestEffortTimeout bounds a search-footer enrichment so a slow free API
// never drags out a flight/hotel/ground search.
const enrichBestEffortTimeout = 8 * time.Second

// EnrichBestEffort returns destination intelligence (weather, safety, holidays,
// currency, country facts) for a search footer, or nil. It is the additive,
// non-blocking enrichment used by the default CLI and MCP search paths:
//
//   - context-gated: an empty/blank location returns nil without any network call
//   - bounded: capped by an 8s timeout regardless of the caller's context
//   - silently degrading: any error (geocode miss, API down) returns nil
//
// It never blocks or fails the core search — a missing field is honest absence,
// not a fabricated value.
func EnrichBestEffort(ctx context.Context, location string, dates models.DateRange) *models.DestinationInfo {
	if strings.TrimSpace(location) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, enrichBestEffortTimeout)
	defer cancel()
	info, err := GetDestinationInfo(ctx, location, dates)
	if err != nil {
		return nil
	}
	return info
}
