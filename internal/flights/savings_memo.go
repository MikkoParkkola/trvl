package flights

import (
	"context"
	"fmt"
	"slices"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/searchctx"
)

// SearchFlightsWithClient is like SearchFlights but accepts a pre-built client,
// useful for reusing connections across multiple requests.
func SearchFlightsWithClient(ctx context.Context, client *batchexec.Client, origin, destination, date string, opts SearchOptions) (*models.FlightSearchResult, error) {
	if !searchctx.SavingsPassSuppressed(ctx) {
		return searchFlightsWithClient(ctx, client, origin, destination, date, opts)
	}
	// Inside a savings pass many detectors ask the same query: it runs once per
	// pass, and each caller gets its own copy of the flight list to sort or trim.
	// ponytail: legs inside each FlightResult stay shared; copy deeper if a
	// detector ever edits them.
	res, err := searchctx.Memo(ctx, flightMemoKey(client, origin, destination, date, opts), func() (*models.FlightSearchResult, error) {
		return searchFlightsWithClient(ctx, client, origin, destination, date, opts)
	})
	if res != nil {
		cp := *res
		cp.Flights = slices.Clone(res.Flights)
		res = &cp
	}
	return res, err
}

// flightMemoKey is the full query for the savings-pass memo: flightSearchKey
// plus every option it leaves out, and the client.
func flightMemoKey(client *batchexec.Client, origin, destination, date string, opts SearchOptions) string {
	opts.defaults()
	return fmt.Sprintf("%s|%p|%d|%d|%v|%v|%s|%v|%p|%p|%p|%s|%s",
		flightSearchKey(origin, destination, date, opts), client,
		opts.DepartureFlexDays, opts.ReturnFlexDays, opts.FirstResult, opts.NoHacks,
		canonicalStringSlice(opts.SuppressedHacks), opts.Stealth,
		opts.SearchOverride, opts.afklmNewProvider, opts.afklmTestFlights, opts.wizzHost, opts.transaviaHost,
	)
}
