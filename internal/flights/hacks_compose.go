package flights

import (
	"context"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// hackComposeTimeout bounds the auto-compose savings engine so a slow detector
// never blocks the naive flight results. The naive search has already returned
// by the time this runs; if the budget is exceeded we simply surface no saving.
const hackComposeTimeout = 12 * time.Second

// hacksComposeKey is a context marker set while the savings engine runs. The
// hack detectors fan out by calling flights.SearchFlights themselves; without
// this guard each detector search would recursively auto-compose hacks again,
// exploding into unbounded recursion. A search whose context carries the marker
// skips auto-compose and behaves as a plain naive search.
type hacksComposeKey struct{}

func disableHacksCompose(ctx context.Context) context.Context {
	return context.WithValue(ctx, hacksComposeKey{}, true)
}

func hacksComposeDisabled(ctx context.Context) bool {
	v, _ := ctx.Value(hacksComposeKey{}).(bool)
	return v
}

// HackComposeRequest is the route/baseline the savings engine evaluates. It is
// deliberately built from primitives + models types so the flights package does
// not depend on the hacks package (the hacks detectors import flights, so the
// dependency must point one way only).
type HackComposeRequest struct {
	Origin      string
	Destination string
	Date        string
	ReturnDate  string
	Currency    string
	NaivePrice  float64
	CarryOnOnly bool
	Passengers  int
}

// composeHackSaving is the savings-engine seam. It is nil until the hacks package
// wires it via RegisterHackComposer in its init(), which inverts the import cycle
// (hacks imports flights, not the reverse). Tests override it directly.
var composeHackSaving func(ctx context.Context, req HackComposeRequest) *models.HackSaving

// RegisterHackComposer wires the travel-hacks savings engine into flight search.
// Called once from the hacks package init(). Passing nil disables auto-compose.
func RegisterHackComposer(fn func(ctx context.Context, req HackComposeRequest) *models.HackSaving) {
	composeHackSaving = fn
}

// cheapestFlightPrice returns the lowest strictly-positive headline price across
// the naive flights, or 0 when none are priced.
func cheapestFlightPrice(flights []models.FlightResult) float64 {
	best := 0.0
	for _, f := range flights {
		if f.Price <= 0 {
			continue
		}
		if best == 0 || f.Price < best {
			best = f.Price
		}
	}
	return best
}

// flightHackCurrency picks the currency to report savings in: the explicit
// option currency, else the first priced flight's currency, else EUR.
func flightHackCurrency(flights []models.FlightResult, optCurrency string) string {
	if optCurrency != "" {
		return optCurrency
	}
	for _, f := range flights {
		if f.Currency != "" {
			return f.Currency
		}
	}
	return "EUR"
}

// attachFlightHackSaving runs the savings engine against a completed naive
// search and, when a genuinely cheaper synthesized option exists, attaches the
// single best one to result.HackSaving. The naive Flights are never modified.
//
// It is a no-op when the engine is unwired (composeHackSaving == nil), when
// hacks are opted out (opts.NoHacks), when the search is a nested
// detector-initiated search (re-entrancy guard), when the result is not a
// successful priced search, or when the naive cheapest price is unknown.
func attachFlightHackSaving(ctx context.Context, result *models.FlightSearchResult, origin, destination, date string, opts SearchOptions) {
	if composeHackSaving == nil || result == nil || !result.Success {
		return
	}
	if opts.NoHacks || hacksComposeDisabled(ctx) {
		return
	}
	naive := cheapestFlightPrice(result.Flights)
	if naive <= 0 {
		return
	}

	req := HackComposeRequest{
		Origin:      origin,
		Destination: destination,
		Date:        date,
		ReturnDate:  opts.ReturnDate,
		Currency:    flightHackCurrency(result.Flights, opts.Currency),
		CarryOnOnly: opts.CarryOnBags > 0 && opts.CheckedBags == 0,
		NaivePrice:  naive,
		Passengers:  opts.Adults,
	}

	// Bound the engine and mark the context so the detectors' own
	// flights.SearchFlights fan-out does not recursively auto-compose.
	hctx, cancel := context.WithTimeout(disableHacksCompose(ctx), hackComposeTimeout)
	defer cancel()

	if saving := composeHackSaving(hctx, req); saving != nil {
		result.HackSaving = saving
	}
}
