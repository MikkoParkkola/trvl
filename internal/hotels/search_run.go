package hotels

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/searchctx"
)

// hotelSearchRun is what one shared search run hands every caller: the result
// and the run's Google events, frozen once the run returns (#826).
type hotelSearchRun struct {
	result *models.HotelSearchResult
	call   googleCall
}

// searchHotelsCall is SearchHotelsWithClient that also returns the run's
// Google events, for the single-hotel lookups that decide a Google outcome.
func searchHotelsCall(ctx context.Context, client *batchexec.Client, location string, opts HotelSearchOptions) (*models.HotelSearchResult, googleCall, error) {
	// Every provider gets the English city name; only the Google fetch swaps
	// in the local form Google resolves (see googleLocation in searchHotelsCore).
	location = englishHotelCity(location)
	if opts.CheckIn == "" || opts.CheckOut == "" {
		return nil, googleCall{}, fmt.Errorf("check-in and check-out dates are required")
	}
	if opts.Guests <= 0 {
		opts.Guests = 2
	}
	if opts.Currency == "" {
		opts.Currency = "USD" // Google's default when no currency specified
	} else {
		opts.Currency = strings.ToUpper(opts.Currency)
	}

	// Validate dates.
	if _, err := parseDateArray(opts.CheckIn); err != nil {
		return nil, googleCall{}, fmt.Errorf("parse check-in date: %w", err)
	}
	if _, err := parseDateArray(opts.CheckOut); err != nil {
		return nil, googleCall{}, fmt.Errorf("parse check-out date: %w", err)
	}

	key := hotelSearchKey(location, opts)
	run, err := doHotelSearchRun(ctx, key, func(sharedCtx context.Context) (hotelSearchRun, error) {
		var call googleCall // written only by this run's goroutine
		res, err := searchHotelsCore(sharedCtx, client, location, opts, &call)
		return hotelSearchRun{result: res, call: call}, err
	})
	return run.result, run.call, err
}

func doHotelSearchSingleflight(ctx context.Context, key string, fn func(context.Context) (*models.HotelSearchResult, error)) (*models.HotelSearchResult, error) {
	run, err := doHotelSearchRun(ctx, key, func(sharedCtx context.Context) (hotelSearchRun, error) {
		res, err := fn(sharedCtx)
		return hotelSearchRun{result: res}, err
	})
	return run.result, err
}

func doHotelSearchRun(ctx context.Context, key string, fn func(context.Context) (hotelSearchRun, error)) (hotelSearchRun, error) {
	if err := ctx.Err(); err != nil {
		return hotelSearchRun{}, err
	}

	ch := hotelGroup.DoChan(key, func() (any, error) {
		sharedCtx, cancel := searchctx.DetachedWithin(ctx, sharedHotelSearchTimeout)
		defer cancel()
		return fn(sharedCtx)
	})

	select {
	case <-ctx.Done():
		// The winner keeps running until the detached shared deadline expires; if
		// the caller times out first, forget the key so later callers do not join
		// that doomed execution.
		hotelGroup.Forget(key)
		return hotelSearchRun{}, ctx.Err()
	case res := <-ch:
		return sharedHotelRun(res.Val, res.Err)
	}
}

// sharedHotelRun gives each caller its own copy of a shared run: a cloned
// result it may mutate and its own slice of the frozen Google events.
func sharedHotelRun(v any, err error) (hotelSearchRun, error) {
	run, ok := v.(hotelSearchRun)
	if !ok {
		res, rerr := sharedHotelResult(v, err)
		return hotelSearchRun{result: res}, rerr
	}
	out := hotelSearchRun{call: googleCall{events: slices.Clone(run.call.events)}}
	if run.result != nil {
		out.result = cloneHotelSearchResult(run.result)
	}
	return out, err
}
