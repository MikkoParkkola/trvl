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
	res, err := searchctx.Memo(ctx, flightMemoKey(ctx, client, origin, destination, date, opts), func(passCtx context.Context) (*models.FlightSearchResult, error) {
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

// flightMemoKey is the full query for the savings-pass memo: flightSearchKey
// plus every option it leaves out, the caller's context flags, and the client.
func flightMemoKey(ctx context.Context, client *batchexec.Client, origin, destination, date string, opts SearchOptions) string {
	opts.defaults()
	return fmt.Sprintf("%s|%p|%d|%d|%v|%v|%s|%v|%s|%s|skip1w=%v|prefetch=%v",
		flightSearchKey(origin, destination, date, opts), client,
		opts.DepartureFlexDays, opts.ReturnFlexDays, opts.FirstResult, opts.NoHacks,
		canonicalStringSlice(opts.SuppressedHacks), opts.Stealth, opts.wizzHost, opts.transaviaHost,
		skiplaggedOneWayDisabled(ctx), prefetchDisabled(ctx),
	)
}

// savingsOffKey separates searches that attach no savings (opted out, or
// inside or suppressed by a savings pass) from those that do, so the shared
// in-flight call never hands a top-level caller a result without its saving.
func savingsOffKey(ctx context.Context, opts SearchOptions) string {
	return fmt.Sprintf("|savingsoff=%v", opts.NoHacks || searchctx.SavingsPassSuppressed(ctx))
}
