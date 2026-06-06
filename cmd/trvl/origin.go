package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/travelctx"
)

// resolveCLIOrigin resolves a possibly-omitted CLI origin argument the same
// way across every flight-search command. Precedence: an explicit code (or
// the "home" keyword, which routes to the saved home airport) > saved home
// airport > best-effort geo-IP location.
//
// explicit is the raw ORIGIN arg ("" when the user omitted it). noGeo mirrors
// the per-command --no-geo flag / TRVL_NO_GEO env. format gates the human
// notice (suppressed for json). It returns the resolved IATA code, or an
// actionable error when nothing could be determined.
//
// The one-line provenance notice ("from your saved home airport" / "detected
// from your current location") is written to stderr so it never pollutes
// machine-readable stdout.
func resolveCLIOrigin(ctx context.Context, explicit, format string, noGeo bool) (string, error) {
	explicitForCtx := explicit
	if strings.EqualFold(strings.TrimSpace(explicit), "home") {
		explicitForCtx = "" // fall through to prefs/home resolution
	}
	prefs, _ := preferences.Load() //nolint:errcheck // default prefs on error
	tctx := travelctx.Resolve(ctx, prefs, travelctx.Options{
		ExplicitOrigin: explicitForCtx,
		AllowGeoIP:     !noGeo,
	})
	if !tctx.Origin.HasAirport() {
		return "", fmt.Errorf("no origin given and none could be resolved from your preferences or location; pass ORIGIN explicitly or set a home airport via `trvl prefs`")
	}
	// Only narrate when we actually filled in an origin the user didn't type.
	if explicitForCtx == "" && format != "json" {
		name := tctx.Origin.City
		if name == "" {
			name = tctx.Origin.Airport
		}
		switch tctx.Origin.Source {
		case travelctx.SourcePrefs:
			_, _ = fmt.Fprintf(os.Stderr, "Origin %s (%s) — from your saved home airport.\n", tctx.Origin.Airport, name)
		case travelctx.SourceGeoIP:
			_, _ = fmt.Fprintf(os.Stderr, "Origin %s (%s) — detected from your current location. Override with an explicit code or --no-geo.\n", tctx.Origin.Airport, name)
		}
	}
	return tctx.Origin.Airport, nil
}
