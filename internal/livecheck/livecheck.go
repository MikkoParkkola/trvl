// Package livecheck provides the real flight/hotel price-checking logic used to
// re-price active watches. It bridges the watch package to the live
// flights/hotels search APIs without either of those packages depending on
// watch, and is shared by the CLI watch daemon and the MCP check_watches tool so
// the two cannot diverge (the original divergence — a live CLI checker and a
// stubbed MCP checker — is what caused check_watches to silently return price 0).
package livecheck

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/dategrid"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// Checker implements watch.PriceChecker against the real flight/hotel search
// APIs. The zero value is ready to use.
type Checker struct{}

// Compile-time assertion that Checker satisfies the watch.PriceChecker contract.
var _ watch.PriceChecker = Checker{}

// CheckPrice returns the cheapest live price for the watch. A zero price with a
// nil error means "searched, found nothing" (an honest empty result); a non-nil
// error means the search itself failed. It never fabricates a price.
func (Checker) CheckPrice(ctx context.Context, w watch.Watch) (float64, string, string, error) {
	switch w.Type {
	case "flight":
		return checkFlight(ctx, w)
	case "hotel":
		return checkHotel(ctx, w)
	default:
		return 0, "", "", fmt.Errorf("unknown watch type: %s", w.Type)
	}
}

// cheapest returns the element with the lowest positive price. A zero or
// negative price never displaces a positive one, and the first positive price
// wins ties — the exact semantics the flight/date/hotel selection loops used to
// duplicate inline. If no element has a positive price it returns the first
// element (an honest "found nothing priceable"). Callers guarantee a non-empty
// slice; the price accessor lets one helper serve all three result types.
//
// The sentinel is "best has price <= 0" rather than the inline loops' "== 0":
// this is a no-op for real provider data (prices are never negative) but makes
// the selector correct in the degenerate case where the first element carries a
// negative price and a later one is genuinely positive.
func cheapest[T any](items []T, price func(T) float64) T {
	best := items[0]
	bestP := price(best)
	for _, x := range items[1:] {
		p := price(x)
		if p > 0 && (bestP <= 0 || p < bestP) {
			best, bestP = x, p
		}
	}
	return best
}

// cheapestByCurrency picks the cheapest positive-priced item using the same
// currency-tiered selection watch.checkRoomWithWebhookContext (round 20)
// applies to room matches: (1) cheapest in preferredCurrency (the watch's
// own currency) if any item carries it; else (2) cheapest within the single
// largest same-currency group among items that DO carry a currency --
// NEVER comparing magnitudes across DIFFERENT currencies to pick a winner,
// tie-broken by lexicographically smallest currency code so the result is
// deterministic regardless of provider order; else (3) cheapest among
// currencyless items.
//
// Replaces the plain cheapest() above for flight/date-range/hotel
// selection: that raw-magnitude minimum compared prices across DIFFERENT
// currencies (e.g. a JPY offer numerically "winning" over a EUR offer)
// exactly the bug class round 20 eliminated from room selection, and could
// strand a valid same-currency sibling behind a cheaper foreign-currency or
// currencyless offer, or trigger a false currency-change reset downstream
// in the watch package. Confirmed unsafe to leave deferred by GPT
// second-opinion review, 2026-07-30 (round 21).
func cheapestByCurrency[T any](items []T, price func(T) float64, currency func(T) string, preferredCurrency string) T {
	pick := func(pool []T) (T, bool) {
		var best T
		bestP := -1.0
		found := false
		for _, it := range pool {
			p := price(it)
			if p > 0 && (!found || p < bestP) {
				best, bestP, found = it, p, true
			}
		}
		return best, found
	}

	// Round 22 found grouping on the RAW currency string let "EUR", "eur",
	// and " EUR " land in three separate buckets -- normalization only
	// happens later, in watch.checkOneWithWebhookContext. A cheaper
	// lowercase-EUR offer could lose the group-size tiebreak to USD, handing
	// back a USD quote for a EUR watch and triggering a false currency-change
	// reset downstream. Canonicalize (trim+uppercase) at this boundary too,
	// same treatment check.go already gives the checker's overall return
	// value. Found by GPT second-opinion review, 2026-07-30 (round 22).
	normCur := func(c string) string { return strings.ToUpper(strings.TrimSpace(c)) }

	// Round 24 found zero/negative-price rows were still being counted here
	// even though pick() can never choose one as a winner (it requires
	// p > 0): a currency whose only rows were zero-price could still win the
	// largest-group tie-break below on row count alone, then fail to
	// produce a result and fall through past a currency that actually had a
	// valid positive-price offer -- silently discarding a real quote in
	// favor of the currencyless pool or the raw items[0] fallback. Exclude
	// non-positive-price rows from grouping so group size (and thus which
	// currency wins the tie-break) reflects only rows that could actually
	// win selection. Found by GPT second-opinion review, 2026-07-31 (round
	// 24).
	byCur := map[string][]T{}
	for _, it := range items {
		if price(it) <= 0 {
			continue
		}
		c := normCur(currency(it))
		byCur[c] = append(byCur[c], it)
	}

	preferredCurrency = normCur(preferredCurrency)
	if preferredCurrency != "" {
		if best, ok := pick(byCur[preferredCurrency]); ok {
			return best
		}
		// Grok round-25 optional finding #2 proposed short-circuiting to
		// "no quote" here on a preferred-currency miss, reasoning that
		// falling through to the largest-other-known-currency tier below
		// would let a transient provider gap masquerade as a real currency
		// change downstream. Round-26 second-opinion review (Grok) found
		// that fix was applied at the wrong layer: checkOneWithWebhookContext
		// (check.go) already handles this correctly and with more
		// information than this generic helper has -- its hasPriorObservation/
		// currencyChanged/firstQuoteMismatch logic (rounds 14-21) safely
		// ADOPTS a new currency on both a genuine change and a first-ever
		// quote, resetting only the currency-denominated scalars
		// (LastPrice/LowestPrice/CheapestDate/BaselinePrice/
		// LastAlertedPrice) and clearing BelowPrice/AlertDropAbs with an
		// explicit AlertsClearedByCurrencyChange flag the user can see --
		// it never silently drops the quote. Short-circuiting here instead
		// returned the zero value unconditionally on ANY preferred miss,
		// which (a) duplicated that protection redundantly and (b) broke
		// first-quote establishment entirely: a brand-new watch whose
		// preferred currency the provider never quotes on poll 1 would
		// never adopt any price, because check.go's firstQuoteMismatch path
		// can only run on a quote this function actually returns. Reverted;
		// fall through to the known-currency-group tier below, same as
		// round 22. Found by Grok second-opinion review, 2026-07-31 (round
		// 26).
	}

	var knownCurrencies []string
	for cur := range byCur {
		if cur != "" {
			knownCurrencies = append(knownCurrencies, cur)
		}
	}
	switch {
	case len(knownCurrencies) == 1:
		if best, ok := pick(byCur[knownCurrencies[0]]); ok {
			return best
		}
	case len(knownCurrencies) > 1:
		chosenCur, chosenCount := "", -1
		for _, cur := range knownCurrencies {
			n := len(byCur[cur])
			if n > chosenCount || (n == chosenCount && cur < chosenCur) {
				chosenCur, chosenCount = cur, n
			}
		}
		if best, ok := pick(byCur[chosenCur]); ok {
			return best
		}
	}

	if best, ok := pick(byCur[""]); ok {
		return best
	}
	return items[0]
}

func checkFlight(ctx context.Context, w watch.Watch) (float64, string, string, error) {
	// Route watch or date range: use calendar/dates search.
	if w.IsRouteWatch() || w.IsDateRange() {
		return checkFlightRange(ctx, w)
	}

	// Specific date search.
	//
	// NOTE: an earlier round of this fix pinned SearchOptions.Currency to
	// w.Currency here to stop a spurious-flip source (see check.go's
	// currency-change reset). That pin was reverted: SearchOptions.Currency
	// is a misnomer -- search.go:725-726 maps it to Google's `gl` (country
	// market) parameter, not a `curr` (display-currency) parameter
	// (batchexec/client.go:437-440 confirms gl selects which fares are
	// shown; curr exists separately and is never wired here). Pinning it
	// would silently change which market's flights get searched, not just
	// the currency label -- a materially different and larger bug than the
	// one this PR fixes. Wiring the real `curr` parameter through
	// SearchFlightsGLCurrStealth (leaving gl untouched) is tracked as a
	// separate follow-up, out of scope for this PR's currency-change-reset
	// fix. Found by adversarial review, 2026-07-29 (round 9).
	opts := flights.SearchOptions{ReturnDate: w.ReturnDate}
	result, err := flights.SearchFlights(ctx, w.Origin, w.Destination, w.DepartDate, opts)
	if err != nil {
		return 0, "", "", err
	}
	if !result.Success || len(result.Flights) == 0 {
		return 0, "", "", nil
	}

	cheapest := cheapestByCurrency(result.Flights, func(f models.FlightResult) float64 { return f.Price }, func(f models.FlightResult) string { return f.Currency }, w.Currency)
	return cheapest.Price, cheapest.Currency, w.DepartDate, nil
}

func checkFlightRange(ctx context.Context, w watch.Watch) (float64, string, string, error) {
	from := w.DepartFrom
	to := w.DepartTo
	if w.IsRouteWatch() {
		// No dates specified — scan next 60 days.
		from = time.Now().AddDate(0, 0, 1).Format("2006-01-02")
		to = time.Now().AddDate(0, 0, 60).Format("2006-01-02")
	}

	result, err := flights.SearchCalendar(ctx, w.Origin, w.Destination, flights.CalendarOptions{
		FromDate: from,
		ToDate:   to,
	})
	if err != nil {
		return 0, "", "", err
	}
	if !result.Success || len(result.Dates) == 0 {
		return 0, "", "", nil
	}

	// MIK-6234 CF.1: persist the full grid the scheduler just fetched so a
	// later single-date flight search can compute shift-day counterfactuals
	// with zero new provider calls. Best-effort: never affect the check result.
	persistDateGrid(w.Origin, w.Destination, result.Dates)

	cheapest := cheapestByCurrency(result.Dates, func(d models.DatePriceResult) float64 { return d.Price }, func(d models.DatePriceResult) string { return d.Currency }, w.Currency)
	return cheapest.Price, cheapest.Currency, cheapest.Date, nil
}

// persistDateGrid stores a route's price calendar for later call-free shift-day
// analysis. Any failure is swallowed: persistence is a bonus, not a guarantee.
func persistDateGrid(origin, destination string, dates []models.DatePriceResult) {
	store, err := dategrid.DefaultStore()
	if err != nil {
		return
	}
	if err := store.Load(); err != nil {
		return
	}
	persistDateGridTo(store, origin, destination, dates, time.Now())
}

// persistDateGridTo is the testable core of persistDateGrid. It builds the
// points slice from dates (skipping non-positive prices), picks the currency
// from the first positive entry, and writes to store. Errors are silently
// discarded: grid persistence is best-effort.
func persistDateGridTo(store *dategrid.Store, origin, destination string, dates []models.DatePriceResult, now time.Time) {
	pts := make([]dategrid.Point, 0, len(dates))
	currency := ""
	for _, d := range dates {
		if d.Price <= 0 {
			continue
		}
		if currency == "" {
			currency = d.Currency
		}
		pts = append(pts, dategrid.Point{
			Date:       d.Date,
			ReturnDate: d.ReturnDate,
			Price:      d.Price,
			Currency:   d.Currency,
		})
	}
	_ = store.Put(dategrid.RouteKey(origin, destination), currency, pts, now)
}

func checkHotel(ctx context.Context, w watch.Watch) (float64, string, string, error) {
	checkIn := w.DepartDate
	checkOut := w.ReturnDate
	if w.IsRouteWatch() {
		// Default to next weekend.
		now := time.Now()
		fri := now.AddDate(0, 0, int((5-now.Weekday()+7)%7))
		checkIn = fri.Format("2006-01-02")
		checkOut = fri.AddDate(0, 0, 2).Format("2006-01-02")
	}

	opts := hotels.HotelSearchOptions{
		CheckIn:  checkIn,
		CheckOut: checkOut,
		Currency: w.Currency,
	}
	result, err := hotels.SearchHotels(ctx, w.Destination, opts)
	if err != nil {
		return 0, "", "", err
	}
	if len(result.Hotels) == 0 {
		return 0, "", "", nil
	}

	cheapest := cheapestByCurrency(result.Hotels, func(h models.HotelResult) float64 { return h.Price }, func(h models.HotelResult) string { return h.Currency }, w.Currency)
	return cheapest.Price, cheapest.Currency, checkIn, nil
}
