package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/travelctx"
)

// resolveDestOriginOptional validates the destination (always required) and
// resolves the origin from, in precedence order: the explicit origin
// argument, the user's saved home airport, then best-effort geo-IP location.
// This makes trvl location-aware by default on the MCP surface: an AI agent
// can call search_flights with only a destination and trvl fills in the
// origin the same way the CLI does.
//
// originSource reports how the origin was obtained (travelctx.SourceExplicit
// / SourcePrefs / SourceGeoIP) so the handler can disclose it to the agent.
// geoOK gates the network path; pass false to stay offline (tests, CI).
func resolveDestOriginOptional(ctx context.Context, args map[string]any, geoOK bool) (origin, dest string, originSource travelctx.Source, err error) {
	dest = strings.TrimSpace(argString(args, "destination"))
	if dest == "" {
		return "", "", travelctx.SourceUnknown, fmt.Errorf("destination is required")
	}
	dest = resolveMCPLocation(dest)
	if verr := models.ValidateIATA(dest); verr != nil {
		return "", "", travelctx.SourceUnknown, fmt.Errorf("invalid destination %q: %w", dest, verr)
	}

	explicit := strings.TrimSpace(argString(args, "origin"))
	prefs, _ := preferences.Load() //nolint:errcheck // default prefs on nil/err
	tctx := travelctx.Resolve(ctx, prefs, travelctx.Options{
		ExplicitOrigin: explicit,
		AllowGeoIP:     geoOK,
	})
	if !tctx.Origin.HasAirport() {
		return "", "", travelctx.SourceUnknown, fmt.Errorf("origin is required: none supplied and none could be resolved from preferences or location; pass origin, or set a home airport via preferences")
	}
	origin = resolveMCPLocation(tctx.Origin.Airport)
	if verr := models.ValidateIATA(origin); verr != nil {
		return "", "", travelctx.SourceUnknown, fmt.Errorf("resolved origin %q is invalid: %w", origin, verr)
	}
	return origin, dest, tctx.Origin.Source, nil
}
