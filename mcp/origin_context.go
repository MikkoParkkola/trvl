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
// Origin and destination may each be a comma-separated multi-airport list
// ("ORY,BVA,CDG") or a city name that fans out to several airports; each
// token is validated individually and the (possibly comma-joined) value is
// returned for downstream multi-airport routing.
//
// originSource reports how the origin was obtained (travelctx.SourceExplicit
// / SourcePrefs / SourceGeoIP) so the handler can disclose it to the agent.
// geoOK gates the network path; pass false to stay offline (tests, CI).
func resolveDestOriginOptional(ctx context.Context, args map[string]any, geoOK bool) (origin, dest string, originSource travelctx.Source, err error) {
	destArg := strings.TrimSpace(argString(args, "destination"))
	if destArg == "" {
		return "", "", travelctx.SourceUnknown, fmt.Errorf("destination is required")
	}
	// Parse the destination per-token so a valid multi-airport list is not
	// rejected as one malformed IATA code (the search_flights bug).
	dest, verr := validateAirportList(destArg)
	if verr != nil {
		return "", "", travelctx.SourceUnknown, fmt.Errorf("invalid destination %q: %w", destArg, verr)
	}

	// Explicit origin (precedence 1) is parsed per-token here so multi-airport
	// origins work too: travelctx only models a single resolved home/geo
	// airport, so we resolve an explicit list ourselves and short-circuit.
	if explicit := strings.TrimSpace(argString(args, "origin")); explicit != "" {
		origin, oerr := validateAirportList(explicit)
		if oerr != nil {
			return "", "", travelctx.SourceUnknown, fmt.Errorf("invalid origin %q: %w", explicit, oerr)
		}
		return origin, dest, travelctx.SourceExplicit, nil
	}

	// No explicit origin: resolve a single origin from saved home airport then
	// best-effort geo-IP (precedence 2 and 3).
	prefs, _ := preferences.Load() //nolint:errcheck // default prefs on nil/err
	tctx := travelctx.Resolve(ctx, prefs, travelctx.Options{AllowGeoIP: geoOK})
	if !tctx.Origin.HasAirport() {
		return "", "", travelctx.SourceUnknown, fmt.Errorf("origin is required: none supplied and none could be resolved from preferences or location; pass origin, or set a home airport via preferences")
	}
	origin = resolveMCPLocation(tctx.Origin.Airport)
	if verr := models.ValidateIATA(origin); verr != nil {
		return "", "", travelctx.SourceUnknown, fmt.Errorf("resolved origin %q is invalid: %w", origin, verr)
	}
	return origin, dest, tctx.Origin.Source, nil
}
