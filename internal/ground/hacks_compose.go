package ground

import (
	"context"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/searchctx"
)

// hackComposeTimeout bounds the auto-compose savings engine so a slow detector
// never blocks the naive ground results.
const hackComposeTimeout = 12 * time.Second

// HackComposeRequest is the route/baseline the savings engine evaluates. Built
// from primitives + models types so the ground package does not depend on the
// hacks package (the hacks detectors import ground, so the dependency points one
// way only).
type HackComposeRequest struct {
	Origin      string
	Destination string
	Date        string
	Currency    string
	NaivePrice  float64
	// Providers, ExcludeProviders and NoCache carry the caller's ground search
	// scope into the detectors' own ground searches.
	Providers        []string
	ExcludeProviders []string
	NoCache          bool
}

// composeHackSaving is the savings-engine seam. It is nil until the hacks package
// wires it via RegisterHackComposer in its init(), inverting the import cycle
// (hacks imports ground, not the reverse). Tests override it directly.
var composeHackSaving func(ctx context.Context, req HackComposeRequest) *models.HackSaving

// RegisterHackComposer wires the travel-hacks savings engine into ground search.
// Called once from the hacks package init(). Passing nil disables auto-compose.
func RegisterHackComposer(fn func(ctx context.Context, req HackComposeRequest) *models.HackSaving) {
	composeHackSaving = fn
}

// cheapestRoutePrice returns the lowest live fare in the comparison currency,
// or 0. An estimate, or a fare that could not be converted, is never the
// naive baseline a saving is measured against.
func cheapestRoutePrice(routes []models.GroundRoute) float64 {
	best := 0.0
	for _, r := range routes {
		if r.PriceProvenance != models.PriceLive || r.ComparablePrice <= 0 {
			continue
		}
		if best == 0 || r.ComparablePrice < best {
			best = r.ComparablePrice
		}
	}
	return best
}

// groundHackCurrency picks the currency to report savings in.
func groundHackCurrency(routes []models.GroundRoute, optCurrency string) string {
	if optCurrency != "" {
		return optCurrency
	}
	for _, r := range routes {
		if r.Currency != "" {
			return r.Currency
		}
	}
	return "EUR"
}

// attachGroundHackSaving runs the savings engine against a completed naive ground
// search and, when a genuinely cheaper synthesized option exists, attaches the
// single best one to result.HackSaving. The naive Routes are never modified.
//
// No-op when the engine is unwired, when opted out (opts.NoHacks), when an
// include provider filter is set, inside another savings pass (re-entrancy
// guard), when the result is not a successful priced search, or when the naive
// cheapest price is unknown.
func attachGroundHackSaving(ctx context.Context, result *models.GroundSearchResult, from, to, date string, opts SearchOptions) {
	if composeHackSaving == nil || result == nil || !result.Success {
		return
	}
	// An include filter scopes the whole search: the detectors would search
	// flights and ground providers the caller did not ask for. An exclude-only
	// filter still runs the pass, scoped by the exclusions.
	if opts.NoHacks || len(opts.Providers) > 0 || searchctx.SavingsPassSuppressed(ctx) {
		return
	}
	naive := cheapestRoutePrice(result.Routes)
	if naive <= 0 {
		return
	}

	req := HackComposeRequest{
		Origin:      from,
		Destination: to,
		Date:        date,
		Currency:    groundHackCurrency(result.Routes, opts.Currency),
		NaivePrice:  naive,
		// The detectors' own ground searches keep the caller's scope.
		Providers:        opts.Providers,
		ExcludeProviders: opts.ExcludeProviders,
		NoCache:          opts.NoCache,
	}

	// Start a savings pass: the detectors' own flight and ground searches see
	// the shared marker, start no pass of their own, and share one memo.
	hctx, cancel := context.WithTimeout(searchctx.StartSavingsPass(ctx), hackComposeTimeout)
	defer cancel()

	if saving := composeHackSaving(hctx, req); saving != nil {
		result.HackSaving = saving
	}
}
