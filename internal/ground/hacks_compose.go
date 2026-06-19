package ground

import (
	"context"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// hackComposeTimeout bounds the auto-compose savings engine so a slow detector
// never blocks the naive ground results.
const hackComposeTimeout = 12 * time.Second

// hacksComposeKey is a context marker set while the savings engine runs. Several
// hack detectors fan out by calling ground.SearchByName themselves; without this
// guard each detector search would recursively auto-compose hacks again. A
// search whose context carries the marker skips auto-compose.
type hacksComposeKey struct{}

func disableHacksCompose(ctx context.Context) context.Context {
	return context.WithValue(ctx, hacksComposeKey{}, true)
}

func hacksComposeDisabled(ctx context.Context) bool {
	v, _ := ctx.Value(hacksComposeKey{}).(bool)
	return v
}

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

// cheapestRoutePrice returns the lowest strictly-positive route price, or 0.
func cheapestRoutePrice(routes []models.GroundRoute) float64 {
	best := 0.0
	for _, r := range routes {
		if r.Price <= 0 {
			continue
		}
		if best == 0 || r.Price < best {
			best = r.Price
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
// No-op when the engine is unwired, when opted out (opts.NoHacks), when this is a
// nested detector-initiated search (re-entrancy guard), when the result is not a
// successful priced search, or when the naive cheapest price is unknown.
func attachGroundHackSaving(ctx context.Context, result *models.GroundSearchResult, from, to, date string, opts SearchOptions) {
	if composeHackSaving == nil || result == nil || !result.Success {
		return
	}
	if opts.NoHacks || hacksComposeDisabled(ctx) {
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
	}

	hctx, cancel := context.WithTimeout(disableHacksCompose(ctx), hackComposeTimeout)
	defer cancel()

	if saving := composeHackSaving(hctx, req); saving != nil {
		result.HackSaving = saving
	}
}
