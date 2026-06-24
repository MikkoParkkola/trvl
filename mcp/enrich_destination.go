package mcp

import (
	"context"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// destinationEnrichTimeout bounds the optional enrichment fan-out so a slow
// free API never delays a core search result.
const destinationEnrichTimeout = 8 * time.Second

// destinationEnricher resolves best-effort destination intelligence. It is a
// seam so tests can substitute a fast stub instead of hitting live APIs.
var destinationEnricher = destinations.GetDestinationInfo

// enrichDestination fetches best-effort destination intelligence (weather,
// safety, public holidays, currency, country facts) for a known location.
//
// This is the shared "nice to have" attachment that puts enrichment on the
// default search paths: a plain hotel search returns it inline, with no extra
// switch to set. It is deliberately silent — every failure (no location, bad
// geocode, slow API, cancelled context) returns nil and the core search result
// is never blocked, altered, or failed.
//
// Context-gated: an empty location yields nil (no destination, no enrichment).
func enrichDestination(ctx context.Context, location string, dates models.DateRange) *models.DestinationInfo {
	if strings.TrimSpace(location) == "" {
		return nil
	}
	enrichCtx, cancel := context.WithTimeout(ctx, destinationEnrichTimeout)
	defer cancel()
	info, err := destinationEnricher(enrichCtx, location, dates)
	if err != nil {
		return nil
	}
	return info
}
