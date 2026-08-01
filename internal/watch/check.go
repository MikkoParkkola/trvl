package watch

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotelarb"
	"github.com/MikkoParkkola/trvl/internal/pricealert"
)

// PriceChecker retrieves the current cheapest price for a route.
// Implementations bridge to flights.SearchFlights or hotels.SearchHotels
// without creating an import dependency from the watch package.
type PriceChecker interface {
	// CheckPrice returns the cheapest price and currency for the given watch.
	// For date-range and route watches, also returns the cheapest date found.
	// Returns 0 price if no results are found (not an error).
	CheckPrice(ctx context.Context, w Watch) (price float64, currency string, cheapestDate string, err error)
}

// RoomChecker retrieves available rooms for a hotel and matches them against criteria.
// Implementations bridge to hotels.GetRoomAvailability without creating an import
// dependency from the watch package.
type RoomChecker interface {
	// CheckRooms returns matching rooms for a room watch. Each returned RoomMatch
	// contains the room name, description, and price. Returns nil if no matches.
	CheckRooms(ctx context.Context, w Watch) ([]RoomMatch, error)
}

// RoomMatch represents a room that matched the watch keywords.
type RoomMatch struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Price       float64 `json:"price"`
	Currency    string  `json:"currency"`
	Provider    string  `json:"provider,omitempty"`
}

// CheckResult holds the outcome of checking a single watch.
type CheckResult struct {
	Watch                     Watch
	NewPrice                  float64
	Currency                  string
	PrevPrice                 float64
	BelowGoal                 bool    // price dropped below threshold
	PriceDrop                 float64 // negative = price decreased (good)
	CheapestDate              string  // for range/route watches: which date was cheapest
	RoomFound                 bool    // room watch: a matching room was found
	RoomMatches               []RoomMatch
	LastMinuteDeal            bool
	LastMinuteDiscountPercent float64
	// Proactive price-drop alert (innovation #6). PriceDropAlert is true when the
	// fare fell past the configured threshold below the baseline and this is a
	// new, deduplicated event. AlertBaseline / AlertDropPercent describe it.
	PriceDropAlert   bool
	AlertBaseline    float64
	AlertDropPercent float64
	Error            error
}

// CheckAll checks all watches using the provided price checker and records
// results in the store. Pauses 3 seconds between checks to respect API rate limits.
// Returns a result for each watch.
func CheckAll(ctx context.Context, store *Store, checker PriceChecker) []CheckResult {
	return CheckAllWithRooms(ctx, store, checker, nil)
}

// CheckAllWithRooms checks all watches, using the room checker for room-type watches
// and the price checker for flight/hotel watches.
func CheckAllWithRooms(ctx context.Context, store *Store, checker PriceChecker, roomChecker RoomChecker) []CheckResult {
	return checkWatchesWithRoomsAndWebhookContext(ctx, ctx, store, checker, roomChecker, store.List())
}

// CheckAllWithRoomsAndWebhookContext checks all watches while allowing webhook
// delivery to outlive the check timeout. The webhook context should typically be
// a longer-lived parent context that is canceled when the caller is shutting
// down.
func CheckAllWithRoomsAndWebhookContext(checkCtx, webhookCtx context.Context, store *Store, checker PriceChecker, roomChecker RoomChecker) []CheckResult {
	return checkWatchesWithRoomsAndWebhookContext(checkCtx, webhookCtx, store, checker, roomChecker, store.List())
}

func checkWatchesWithRoomsAndWebhookContext(checkCtx, webhookCtx context.Context, store *Store, checker PriceChecker, roomChecker RoomChecker, watches []Watch) []CheckResult {
	checkCtx, webhookCtx = normalizeCheckAndWebhookContexts(checkCtx, webhookCtx)

	// One provider call per distinct polled target for the whole round. Watches
	// that differ only in price threshold share the search and are then
	// evaluated independently below (#509, MULTIPRICE.2).
	checker = newRoundCache(checker)

	results := make([]CheckResult, 0, len(watches))

	for i, w := range watches {
		var r CheckResult
		if w.IsRoomWatch() && roomChecker != nil {
			r = checkRoomWithWebhookContext(checkCtx, webhookCtx, store, roomChecker, w)
		} else if w.IsRoomWatch() {
			r = CheckResult{Watch: w, Error: fmt.Errorf("room checker not configured")}
		} else {
			r = checkOneWithWebhookContext(checkCtx, webhookCtx, store, checker, w)
		}
		results = append(results, r)

		// Pause between checks to respect rate limits (skip after last).
		if i < len(watches)-1 {
			select {
			case <-checkCtx.Done():
				return results
			case <-time.After(3 * time.Second):
			}
		}
	}
	return results
}

// BoundedOptions tunes CheckAllBounded for synchronous callers (e.g. the MCP
// check_watches tool) that must return within a request deadline rather than
// running as an unbounded background daemon.
type BoundedOptions struct {
	// Concurrency caps how many watches are re-priced in parallel. Defaults to
	// DefaultBoundedConcurrency when <= 0.
	Concurrency int
	// PerWatchTimeout bounds each individual live search. A watch that exceeds it
	// is reported with an explicit timeout Error rather than a fabricated price.
	// Defaults to DefaultBoundedPerWatchTimeout when <= 0.
	PerWatchTimeout time.Duration
}

const (
	// DefaultBoundedConcurrency is the default parallelism for CheckAllBounded.
	DefaultBoundedConcurrency = 4
	// DefaultBoundedPerWatchTimeout is the default per-watch deadline.
	DefaultBoundedPerWatchTimeout = 15 * time.Second
)

// CheckAllBounded re-prices every watch concurrently with a per-watch timeout
// and a concurrency cap, recording results in the store. Unlike CheckAll it does
// not pause between checks, making it suitable for synchronous request/response
// callers. Results are returned in the same order as store.List(). A watch whose
// live search exceeds PerWatchTimeout (or whose parent context is canceled) is
// returned with a non-nil Error so callers can render an honest "not checked"
// status instead of a misleading price of 0.
func CheckAllBounded(ctx context.Context, store *Store, checker PriceChecker, roomChecker RoomChecker, opts BoundedOptions) []CheckResult {
	if ctx == nil {
		ctx = context.Background()
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = DefaultBoundedConcurrency
	}
	perWatch := opts.PerWatchTimeout
	if perWatch <= 0 {
		perWatch = DefaultBoundedPerWatchTimeout
	}

	watches := store.List()
	results := make([]CheckResult, len(watches))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	// Single-flight per polled target: duplicate routes issue one provider call
	// for the round even under concurrency (#509, MULTIPRICE.2).
	checker = newRoundCache(checker)

	for i, w := range watches {
		wg.Add(1)
		go func(i int, w Watch) {
			defer wg.Done()

			// Respect parent cancellation before acquiring a slot.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i] = CheckResult{Watch: w, Error: ctx.Err()}
				return
			}

			// The webhook context is the parent ctx so delivery is not killed by
			// the per-watch search deadline; the check context is bounded.
			checkCtx, cancel := context.WithTimeout(ctx, perWatch)
			defer cancel()

			switch {
			case w.IsRoomWatch() && roomChecker != nil:
				results[i] = checkRoomWithWebhookContext(checkCtx, ctx, store, roomChecker, w)
			case w.IsRoomWatch():
				results[i] = CheckResult{Watch: w, Error: fmt.Errorf("room checker not configured")}
			default:
				results[i] = checkOneWithWebhookContext(checkCtx, ctx, store, checker, w)
			}
		}(i, w)
	}

	wg.Wait()
	return results
}

// checkOne performs a price check for a single watch.
func checkOne(ctx context.Context, store *Store, checker PriceChecker, w Watch) CheckResult {
	return checkOneWithWebhookContext(ctx, ctx, store, checker, w)
}

func checkOneWithWebhookContext(checkCtx, webhookCtx context.Context, store *Store, checker PriceChecker, w Watch) CheckResult {
	checkCtx, webhookCtx = normalizeCheckAndWebhookContexts(checkCtx, webhookCtx)

	price, currency, cheapestDate, err := checker.CheckPrice(checkCtx, w)
	if err != nil {
		return CheckResult{Watch: w, Error: err}
	}

	result := CheckResult{
		Watch:        w,
		NewPrice:     price,
		Currency:     currency,
		PrevPrice:    w.LastPrice,
		CheapestDate: cheapestDate,
	}

	if price > 0 {
		// Calculate price change.
		if w.LastPrice > 0 {
			result.PriceDrop = price - w.LastPrice
		}

		if signal := detectWatchLastMinuteDeal(w, price); signal.Triggered {
			result.LastMinuteDeal = true
			result.LastMinuteDiscountPercent = signal.DiscountPercent
		}

		// Check threshold.
		if w.BelowPrice > 0 && price <= w.BelowPrice {
			result.BelowGoal = true
		}

		// Update watch state.
		w.LastCheck = time.Now()
		w.LastPrice = price
		w.Currency = currency
		if cheapestDate != "" {
			w.CheapestDate = cheapestDate
		}
		if w.LowestPrice == 0 || price < w.LowestPrice {
			w.LowestPrice = price
		}

		// Persist updates.
		//
		// The check holds a detached copy of the watch taken before the provider
		// call, so writing that whole copy back would revert anything a concurrent
		// tool call changed in the meantime — a threshold edit, a webhook URL, an
		// alert setting (#512, TRVL.STORE.TXN.2). Mutate re-reads the committed
		// record inside the store transaction and applies only the fields the
		// check owns. Everything else survives untouched.
		var alert pricealert.Alert
		var alertFired bool
		saved, err := store.Mutate(w.ID, func(cur *Watch) {
			cur.LastCheck = w.LastCheck
			cur.LastPrice = w.LastPrice
			cur.Currency = w.Currency
			if cheapestDate != "" {
				cur.CheapestDate = w.CheapestDate
			}
			if cur.LowestPrice == 0 || price < cur.LowestPrice {
				cur.LowestPrice = price
			}

			// Proactive price-drop alert: capture/track a baseline and fire exactly
			// one alert when the fare falls past the configured threshold. State is
			// stored on the watch so it survives daemon restarts and reloads.
			//
			// Evaluated here, against the committed record, rather than against the
			// pre-provider copy: Baseline and LastAlertedPrice are running state, so
			// deriving them from a stale copy re-arms the dedup window a concurrent
			// round just set and alerts twice for one drop (#512).
			state, a, fired := pricealert.Evaluate(
				pricealert.State{Baseline: cur.BaselinePrice, LastAlertedAt: cur.LastAlertedPrice},
				price,
				pricealert.Threshold{DropPercent: cur.AlertDropPct, DropAbsolute: cur.AlertDropAbs},
			)
			cur.BaselinePrice = state.Baseline
			cur.LastAlertedPrice = state.LastAlertedAt
			alert, alertFired = a, fired
		})
		if err != nil {
			result.Error = fmt.Errorf("update watch: %w", err)
			return result
		}
		if alertFired {
			result.PriceDropAlert = true
			result.AlertBaseline = alert.Baseline
			result.AlertDropPercent = alert.DropPercent
		}
		w = saved

		if err := store.RecordPrice(w.ID, price, currency); err != nil {
			result.Error = fmt.Errorf("record price: %w", err)
			return result
		}

		// Update the result's watch to reflect saved state.
		result.Watch = w

		// Fire webhook on price drop. The webhook context can outlive the check
		// timeout, but should still be canceled when the scheduler stops.
		if result.PriceDrop < 0 || result.LastMinuteDeal {
			go fireWebhook(webhookCtx, result)
		}
	}

	return result
}

func detectWatchLastMinuteDeal(w Watch, currentPrice float64) hotelarb.LastMinuteSignal {
	if !w.LastMinuteMode || w.Type != "hotel" {
		return hotelarb.LastMinuteSignal{}
	}
	checkIn := w.DepartDate
	if checkIn == "" {
		checkIn = w.DepartFrom
	}
	parsed, err := time.Parse(watchDateLayout, checkIn)
	if err != nil {
		return hotelarb.LastMinuteSignal{}
	}
	return hotelarb.DetectLastMinuteDeal(time.Now(), parsed, w.LastPrice, currentPrice, hotelarb.LastMinuteOptions{
		DropPercentThreshold: w.LastMinuteDropPct,
	})
}

// checkRoom performs a room availability check for a room watch.
func checkRoom(ctx context.Context, store *Store, checker RoomChecker, w Watch) CheckResult {
	return checkRoomWithWebhookContext(ctx, ctx, store, checker, w)
}

func checkRoomWithWebhookContext(checkCtx, webhookCtx context.Context, store *Store, checker RoomChecker, w Watch) CheckResult {
	checkCtx, webhookCtx = normalizeCheckAndWebhookContexts(checkCtx, webhookCtx)

	matches, err := checker.CheckRooms(checkCtx, w)
	if err != nil {
		return CheckResult{Watch: w, Error: err}
	}

	result := CheckResult{
		Watch:       w,
		RoomMatches: matches,
		RoomFound:   len(matches) > 0,
	}

	// If matches found, record the cheapest matching room price.
	if len(matches) > 0 {
		cheapest := matches[0]
		for _, m := range matches[1:] {
			if m.Price > 0 && (cheapest.Price == 0 || m.Price < cheapest.Price) {
				cheapest = m
			}
		}
		result.NewPrice = cheapest.Price
		result.Currency = cheapest.Currency

		// Check threshold.
		if w.BelowPrice > 0 && cheapest.Price > 0 && cheapest.Price <= w.BelowPrice {
			result.BelowGoal = true
		}

		// Calculate price change from last check.
		result.PrevPrice = w.LastPrice
		if w.LastPrice > 0 && cheapest.Price > 0 {
			result.PriceDrop = cheapest.Price - w.LastPrice
		}

		// Update watch state.
		w.LastCheck = time.Now()
		w.MatchedRoom = cheapest.Name
		if cheapest.Price > 0 {
			w.LastPrice = cheapest.Price
			w.Currency = cheapest.Currency
			if w.LowestPrice == 0 || cheapest.Price < w.LowestPrice {
				w.LowestPrice = cheapest.Price
			}
		}
	} else {
		// No matches — still mark as checked.
		w.LastCheck = time.Now()
	}

	// Persist updates. Same reasoning as checkOneWithWebhookContext: apply only
	// the fields this room check owns to the freshly reloaded record, so a
	// concurrent edit to the watch's own settings is not reverted (#512).
	newPrice, newCurrency, matched, lastCheck := w.LastPrice, w.Currency, w.MatchedRoom, w.LastCheck
	haveMatch := len(matches) > 0
	saved, err := store.Mutate(w.ID, func(cur *Watch) {
		cur.LastCheck = lastCheck
		if !haveMatch {
			return
		}
		cur.MatchedRoom = matched
		if result.NewPrice > 0 {
			cur.LastPrice = newPrice
			cur.Currency = newCurrency
			if cur.LowestPrice == 0 || newPrice < cur.LowestPrice {
				cur.LowestPrice = newPrice
			}
		}
	})
	if err != nil {
		result.Error = fmt.Errorf("update watch: %w", err)
		return result
	}
	w = saved
	if result.NewPrice > 0 {
		if err := store.RecordPrice(w.ID, result.NewPrice, result.Currency); err != nil {
			result.Error = fmt.Errorf("record price: %w", err)
			return result
		}
	}

	result.Watch = w

	// Fire webhook on price drop.
	if result.PriceDrop < 0 {
		go fireWebhook(webhookCtx, result)
	}

	return result
}

func normalizeCheckAndWebhookContexts(checkCtx, webhookCtx context.Context) (context.Context, context.Context) {
	if checkCtx == nil {
		checkCtx = context.Background()
	}
	if webhookCtx == nil {
		webhookCtx = checkCtx
	}
	return checkCtx, webhookCtx
}
