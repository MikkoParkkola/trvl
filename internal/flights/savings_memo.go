package flights

import (
	"context"
	"fmt"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/searchctx"
)

// SearchFlightsWithClient is like SearchFlights but accepts a pre-built client,
// useful for reusing connections across multiple requests.
//
// Inside a running savings pass many detectors ask the same query: it runs
// once per pass and each caller gets its own deep copy. A test override or
// hook replaces the search itself, so it is never memoized.
func SearchFlightsWithClient(ctx context.Context, client *batchexec.Client, origin, destination, date string, opts SearchOptions) (*models.FlightSearchResult, error) {
	if !searchctx.InSavingsPass(ctx) || opts.SearchOverride != nil || opts.afklmNewProvider != nil || opts.afklmTestFlights != nil {
		return searchFlightsWithClient(ctx, client, origin, destination, date, opts)
	}
	res, err := searchctx.Memo(ctx, flightQueryKey(ctx, client, origin, destination, date, opts), func(passCtx context.Context) (*models.FlightSearchResult, error) {
		return searchFlightsWithClient(withCallerFlags(ctx, passCtx), client, origin, destination, date, opts)
	})
	return cloneFlightSearchResult(res), err
}

// withCallerFlags carries the per-call flags a shared search reads from the
// caller's context onto the pass context it runs under.
func withCallerFlags(caller, passCtx context.Context) context.Context {
	if skiplaggedOneWayDisabled(caller) {
		passCtx = disableSkiplaggedOneWay(passCtx)
	}
	if prefetchDisabled(caller) {
		passCtx = context.WithValue(passCtx, prefetchCtxKey{}, struct{}{})
	}
	return passCtx
}

// flightQueryKey is the full query: flightSearchKey plus every option it
// leaves out, the caller's context flags, and the client whose transport the
// search uses. The shared in-flight call and the savings-pass memo both key on
// it, so they can never disagree about which searches are the same.
func flightQueryKey(ctx context.Context, client *batchexec.Client, origin, destination, date string, opts SearchOptions) string {
	opts.defaults()
	return fmt.Sprintf("%s%s|%p|%d|%d|%v|%v|%s|%v|%s|%s",
		flightSearchKey(origin, destination, date, opts), callerFlagsKey(ctx, opts), client,
		opts.DepartureFlexDays, opts.ReturnFlexDays, opts.FirstResult, opts.NoHacks,
		canonicalStringSlice(opts.SuppressedHacks), opts.Stealth, opts.wizzHost, opts.transaviaHost,
	)
}

// callerFlagsKey is the part of a query that lives in the caller's context:
// whether the search attaches savings, and whether a round-trip leg skips
// one-way Skiplagged or a prefetch spawned it. Searches that differ in any of
// these never share an in-flight call or a memo entry.
func callerFlagsKey(ctx context.Context, opts SearchOptions) string {
	return fmt.Sprintf("|savingsoff=%v|skip1w=%v|prefetch=%v",
		searchctx.SavingsOff(ctx, opts.NoHacks), skiplaggedOneWayDisabled(ctx), prefetchDisabled(ctx))
}
