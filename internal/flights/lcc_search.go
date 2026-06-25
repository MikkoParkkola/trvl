package flights

import (
	"context"
	"fmt"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// lccSearcher is the shared signature of the direct low-cost-carrier one-way
// search functions (Ryanair, Wizz Air, Transavia, easyJet, Vueling, Norwegian). Each returns a flat
// slice of one-way FlightResults for a single date.
type lccSearcher func(ctx context.Context, origin, destination, date, currency string, opts SearchOptions) ([]models.FlightResult, error)

// lccRegistry maps an accepted `--provider` name to its display label and
// one-way search function. Aliases (e.g. "wizz") point at the same entry.
var lccRegistry = map[string]struct {
	name   string
	search lccSearcher
}{
	"ryanair":   {"Ryanair", SearchRyanair},
	"wizzair":   {"Wizz Air", SearchWizzair},
	"wizz":      {"Wizz Air", SearchWizzair},
	"transavia": {"Transavia", SearchTransavia},
	"easyjet":   {"easyJet", SearchEasyjet},
	"vueling":   {"Vueling", SearchVueling},
	"vy":        {"Vueling", SearchVueling},
	"norwegian": {"Norwegian", SearchNorwegian},
	"dy":        {"Norwegian", SearchNorwegian},
}

// SearchLowCostCarrier runs a single low-cost carrier as an explicitly
// selectable provider, composing a genuine return ticket (two one-way legs)
// when opts.ReturnDate is set. The provider name must be one of the keys in
// lccRegistry; an unrecognised name is rejected with a clear error.
func SearchLowCostCarrier(ctx context.Context, provider, origin, destination, date string, opts SearchOptions) (*models.FlightSearchResult, error) {
	entry, ok := lccRegistry[provider]
	if !ok {
		return nil, fmt.Errorf("unrecognised low-cost carrier provider %q", provider)
	}
	return searchSingleLCC(ctx, entry.name, entry.search, origin, destination, date, opts)
}

// searchSingleLCC exposes one low-cost carrier as an explicitly selectable
// provider (`--provider ryanair`, etc.).
//
// Low-cost carriers publish no discounted round-trip fare — a return is simply
// two independent one-way tickets — so a round-trip request (opts.ReturnDate
// set) is composed honestly from an outbound and an inbound one-way search into
// FareSplitTickets itineraries with both legs Direction-tagged and a warning
// that the two are booked separately. This is genuine return-ticket data, never
// a bare one-way silently returned for a round-trip request. A one-way request
// wraps the single-leg results directly.
//
// Unlike the silently-skipped fan-out path, an explicit provider request
// surfaces the carrier's own error (e.g. an unconfigured opt-in key) rather than
// an empty result, so the user always learns why nothing came back.
func searchSingleLCC(ctx context.Context, name string, search lccSearcher, origin, destination, date string, opts SearchOptions) (*models.FlightSearchResult, error) {
	currency := opts.Currency
	if currency == "" {
		// LCC endpoints require an explicit currency; EUR matches the default
		// the optimizer normalises every candidate to.
		currency = "EUR"
	}

	// Per-leg searches are one-way: clear ReturnDate (the carriers reject a
	// round-trip request directly) and FirstResult (we must keep enough cheapest
	// options from each direction to pair before collapsing to one).
	legOpts := opts
	legOpts.ReturnDate = ""
	legOpts.FirstResult = false

	outbound, err := search(ctx, origin, destination, date, currency, legOpts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	flights := outbound
	tripType := "one_way"
	if opts.ReturnDate != "" {
		tripType = "round_trip"
		inbound, inErr := search(ctx, destination, origin, opts.ReturnDate, currency, legOpts)
		if inErr != nil {
			return nil, fmt.Errorf("%s inbound %s->%s: %w", name, destination, origin, inErr)
		}
		composed, _ := composeRoundTrips(outbound, inbound, opts)
		flights = composed
	} else {
		// One-way: honour the caller's requested sort over the provider's order.
		sortFlightResults(flights, opts.SortBy)
	}

	status := models.ProviderStatus{
		ID:      name,
		Name:    name,
		Status:  okOrNoHit(len(flights)),
		Results: len(flights),
	}

	return &models.FlightSearchResult{
		Success:          true,
		Count:            len(flights),
		TripType:         tripType,
		Flights:          flights,
		ProviderStatuses: []models.ProviderStatus{status},
	}, nil
}
