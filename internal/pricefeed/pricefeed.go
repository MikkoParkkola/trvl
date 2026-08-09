// Package pricefeed is the single source of truth for trvl's price-intelligence
// wiring: price-position, call-free counterfactual savings, and booking
// readiness. Both the CLI (cmd/trvl) and the MCP handlers (mcp) call it, so the
// two surfaces can never drift apart — previously each carried its own copy of
// this logic (MIK-6229/6232/6234 improve pass).
//
// Every operation is best-effort and independent: a failure reading one backing
// store never suppresses signals that come from another. All functions are
// safe to call with empty/nil inputs.
package pricefeed

import (
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/booking"
	"github.com/MikkoParkkola/trvl/internal/counterfactual"
	"github.com/MikkoParkkola/trvl/internal/dategrid"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/obslog"
	"github.com/MikkoParkkola/trvl/internal/pricesignal"
	"github.com/MikkoParkkola/trvl/internal/probecache"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// Tuning shared by both surfaces.
const (
	// MinDelta is the minimum saving (in price units) worth surfacing.
	MinDelta = 10.0
	// GridFreshness bounds how recent a persisted price grid must be to drive a
	// call-free shift-day counterfactual.
	GridFreshness = 24 * time.Hour
	// ProbeFreshness bounds how recent a Tier-1 cached probe must be to serve.
	ProbeFreshness = 12 * time.Hour
)

// FlightResult bundles the price signals for a single-O/D flight search.
type FlightResult struct {
	Position *pricesignal.Position
	Savings  []counterfactual.Saving
}

// Flight logs the observed fare and computes price-position plus all call-free
// savings (same-day, vs-history, shift-day, Tier-1) for a single-O/D flight
// search. Multi-airport searches (empty origin/dest) return a zero result.
//
// The watch store, grid store, and probe cache are read independently: a watch
// store failure still yields shift-day and Tier-1 savings (which do not depend
// on it).
func Flight(origin, dest, date string, result *models.FlightSearchResult, now time.Time) FlightResult {
	var out FlightResult
	if result == nil || !result.Success || len(result.Flights) == 0 || origin == "" || dest == "" {
		return out
	}
	cheapestPrice, cheapestCurrency := cheapestFlight(result.Flights)

	// Price-position + observation logging (watch store; independent best-effort).
	if store, err := watch.DefaultStore(); err == nil && store.Load() == nil {
		_ = obslog.FlightSearch(store, origin, dest, date, result)
		// A blank currency yields no position at all (trvl#570). RoutePrices
		// exact-matches its argument since #564, so an empty one selects every
		// currencyless observation on the route -- and those can be different
		// real currencies, one provider omitting the label on a EUR quote and
		// another on a JPY one. A median over that pool is computed across
		// incomparable numbers, and Position carries a buy/wait Verdict, so the
		// result is an active recommendation derived from prices that may not
		// share a unit.
		//
		// Refusing rather than degrading, for the same reason #549 refuses a
		// blank-currency saving: a missing signal is recoverable, a confidently
		// wrong one is not. This one has no visible tell either -- the blank
		// saving at least rendered a conspicuous double space where the currency
		// belonged, whereas a position renders normally and reads as trustworthy.
		if cur := strings.ToUpper(strings.TrimSpace(cheapestCurrency)); cheapestPrice > 0 && cur != "" {
			key := watch.RouteKey("flight", origin, dest, date)
			p := pricesignal.Compute(store.RoutePrices(key, cur), cheapestPrice, 0)
			out.Position = &p
		}
	}

	// Call-free savings.
	if s := counterfactual.SameDayAlternative(result.Flights, MinDelta, now); s != nil {
		out.Savings = append(out.Savings, *s)
	}
	if s := counterfactual.VsHistory(out.Position, cheapestCurrency, now); s != nil {
		out.Savings = append(out.Savings, *s)
	}
	out.Savings = append(out.Savings, ShiftDay(origin, dest, date, now)...)
	out.Savings = append(out.Savings, Tier1(origin, dest, now)...)
	return out
}

// ShiftDay returns call-free shift-day counterfactuals read from the persisted
// price grid. Nothing is returned when no fresh grid covers the route.
func ShiftDay(origin, destination, date string, now time.Time) []counterfactual.Saving {
	store, err := dategrid.DefaultStore()
	if err != nil || store.Load() != nil {
		return nil
	}
	g, ok := store.Get(dategrid.RouteKey(origin, destination))
	if !ok || !g.Fresh(now, GridFreshness) {
		return nil
	}
	grid := make([]models.DatePriceResult, 0, len(g.Points))
	for _, p := range g.Points {
		grid = append(grid, models.DatePriceResult{Date: p.Date, ReturnDate: p.ReturnDate, Price: p.Price, Currency: p.Currency})
	}
	return counterfactual.ShiftDay(grid, date, MinDelta, g.UpdatedAt)
}

// Tier1 returns savings the watch monitor pre-computed for this route, served
// call-free from the probe cache. Nothing is returned when no fresh entry exists.
func Tier1(origin, destination string, now time.Time) []counterfactual.Saving {
	store, err := probecache.DefaultStore()
	if err != nil || store.Load() != nil {
		return nil
	}
	e, ok := store.Get(probecache.RouteKey(origin, destination))
	if !ok || !e.Fresh(now, ProbeFreshness) {
		return nil
	}
	return e.Savings
}

// HotelPosition logs the cheapest provider price and returns its price-position.
// Returns nil when there is no positive price or the store is unavailable.
func HotelPosition(hotelID, checkIn string, result *models.HotelPriceResult) *pricesignal.Position {
	if result == nil || len(result.Providers) == 0 {
		return nil
	}
	store, err := watch.DefaultStore()
	if err != nil || store.Load() != nil {
		return nil
	}
	_ = obslog.HotelPrices(store, hotelID, checkIn, result)
	cheapest := CheapestProvider(result.Providers)
	if cheapest.Price <= 0 {
		return nil
	}
	// TRVL.POSBLANK.4: HotelPosition has the same shape as the flight path and
	// is affected identically, so it is fixed here rather than recorded as
	// unaffected. A blank provider currency selects the currencyless
	// observations for this hotel, which can be different real currencies, and
	// the resulting median would ship as a buy/wait verdict.
	cur := strings.ToUpper(strings.TrimSpace(cheapest.Currency))
	if cur == "" {
		return nil
	}
	key := watch.RouteKey("hotel", hotelID, "", checkIn)
	p := pricesignal.Compute(store.RoutePrices(key, cur), cheapest.Price, 0)
	return &p
}

// HotelPricesReadiness maps the signals available on the hotel-prices endpoint
// into a booking verdict.
//
// This endpoint now carries seller cancellation terms when the upstream result
// states them. Ready is therefore reachable for those results. When no seller
// states any terms, the verdict declares a refundability ceiling rather than
// implying a finding about the hotel; callers can distinguish missing upstream
// evidence from an explicitly non-refundable offer.
func HotelPricesReadiness(hotelID string, providers []models.ProviderPrice) booking.Verdict {
	cheapest := CheapestProvider(providers)
	var in booking.Input
	if hotelID != "" {
		in.IdentityConfirmed = booking.True()
	}
	switch cheapest.LinkDurability {
	case "stable":
		in.LinkStable = booking.True()
	case "expiring":
		in.LinkStable = booking.False()
	}
	switch cheapest.PriceConfidence {
	case models.PriceConfidenceVerified, models.PriceConfidenceRoomLevel:
		in.Verified = booking.True()
	case models.PriceConfidenceUnverified:
		in.Verified = booking.False()
	}
	// Refundability is no longer unobtainable on this path (trvl#535). The
	// seller's cancellation terms were always present upstream and were being
	// dropped when the per-seller list was built; they are now carried through,
	// so the ceiling is lifted when the selected seller actually carries them.
	//
	// All four signals must describe the same concrete offer. Price, link, and
	// verification above come from cheapest, so borrowing cancellation terms
	// from another seller would manufacture an offer that nobody actually made.
	refundabilityKnown := cheapest.FreeCancellation != nil || cheapest.FreeCancellationUntil != ""
	if refundabilityKnown {
		in.RefundabilityKnown = booking.True()
		return booking.Evaluate(in)
	}
	return booking.EvaluateWith(in, booking.Availability{NoRefundability: true})
}

// RoomsReadiness maps the richer room-level signals into a booking verdict.
// The per-room readiness field is the source of truth because it evaluates one
// concrete room offer. Aggregating the strongest signal from different rows can
// otherwise produce a synthetic "ready" offer that no provider returned.
func RoomsReadiness(result *hotels.RoomAvailability) booking.Verdict {
	if result == nil || len(result.Rooms) == 0 {
		return booking.Evaluate(booking.Input{})
	}

	sawCaution := false
	for _, room := range result.Rooms {
		switch room.Readiness {
		case hotels.ReadinessReady:
			return booking.Verdict{Readiness: booking.Ready}
		case hotels.ReadinessCaution:
			sawCaution = true
		case "":
			// Backward-compatible path for callers that construct RoomAvailability
			// directly rather than receiving it from the hotel pipeline. Evaluate
			// only this row; never borrow a signal from a sibling room.
			verdict := roomOfferReadiness(result.HotelID, room)
			if verdict.Readiness == booking.Ready {
				return verdict
			}
			if verdict.Readiness == booking.Caution {
				sawCaution = true
			}
		}
	}
	if sawCaution {
		return booking.Verdict{
			Readiness: booking.Caution,
			Reasons:   []string{"no individual room offer satisfies all readiness signals"},
		}
	}
	return booking.Verdict{
		Readiness: booking.Unverified,
		Reasons:   []string{"no room offer carries a usable readiness assessment"},
	}
}

func roomOfferReadiness(hotelID string, room hotels.RoomType) booking.Verdict {
	var in booking.Input
	if hotelID != "" {
		in.IdentityConfirmed = booking.True()
	}
	if room.Refundable != nil || room.FreeCancellation != nil || room.CancellationPolicy != "" {
		in.RefundabilityKnown = booking.True()
	}
	for _, option := range room.InventoryOptions {
		switch option.PriceConfidence {
		case models.PriceConfidenceVerified, models.PriceConfidenceRoomLevel:
			in.Verified = booking.True()
		}
	}

	sawExpiring := false
	urls := make([]string, 0, len(room.InventoryOptions)+1)
	urls = append(urls, room.ProviderURL)
	for _, option := range room.InventoryOptions {
		urls = append(urls, option.ProviderURL)
	}
	for _, url := range urls {
		switch hotels.ClassifyLinkDurability(url) {
		case "stable":
			in.LinkStable = booking.True()
		case "expiring":
			sawExpiring = true
		}
	}
	if in.LinkStable == nil && sawExpiring {
		in.LinkStable = booking.False()
	}
	return booking.Evaluate(in)
}

// CheapestProvider returns the lowest positive-priced provider within a single
// currency cohort — the most common currency among priced providers — or a zero
// value. Ranking a nominal minimum across mixed currencies would let a
// nominally-small foreign price win dishonestly (100 JPY beating 90 EUR just
// because 100 < 90 is false economics), so providers outside the dominant cohort
// are excluded rather than compared. When no provider carries a currency it falls
// back to the lowest positive price. Mirrors the cohort rule already enforced in
// internal/multimodal and internal/trip.
func CheapestProvider(providers []models.ProviderPrice) models.ProviderPrice {
	cohort := dominantProviderCurrency(providers)
	var best models.ProviderPrice
	for _, p := range providers {
		if p.Price <= 0 {
			continue
		}
		if cohort != "" && p.Currency != cohort {
			continue
		}
		if best.Price == 0 || p.Price < best.Price {
			best = p
		}
	}
	return best
}

// dominantProviderCurrency returns the most frequently occurring non-empty
// currency among positive-priced providers, with a deterministic first-seen
// tie-break, or "" when none carry a currency.
func dominantProviderCurrency(providers []models.ProviderPrice) string {
	counts := make(map[string]int)
	best := ""
	bestN := 0
	for _, p := range providers {
		if p.Price <= 0 || p.Currency == "" {
			continue
		}
		counts[p.Currency]++
		if counts[p.Currency] > bestN {
			best, bestN = p.Currency, counts[p.Currency]
		}
	}
	return best
}

// CheapestHotel returns the honest headline pick for the hotel summary's
// "Lowest lead-in" line, or a zero value when nothing qualifies.
//
// The comparability signal is ComparablePrice, not currency frequency:
// normalizeHotelCurrencies sets ComparablePrice on exactly the hotels it could
// express in the target currency (native or FX-converted), and leaves the
// incomparable tail (FX conversion failed) at ComparablePrice == 0. When any
// comparable hotel exists, the pick comes only from that cohort, by lowest
// ComparablePrice. Frequency would be wrong here: if the majority of hotels
// fail conversion, the foreign tail could form a bogus "dominant" cohort and
// reintroduce the nominally-small dishonest winner (e.g. JPY 50 beating EUR 90).
//
// Only when no hotel was normalized at all (no derivable target => every
// ComparablePrice is 0) does it fall back to the dominant currency cohort and
// then lowest price, mirroring CheapestProvider. Full payload is retained
// elsewhere; only the headline pick is guarded.
func CheapestHotel(hotels []models.HotelResult) models.HotelResult {
	var best models.HotelResult
	comparableExists := false
	for _, h := range hotels {
		if h.Price > 0 && h.ComparablePrice > 0 {
			comparableExists = true
			break
		}
	}
	if comparableExists {
		for _, h := range hotels {
			if h.Price <= 0 || h.ComparablePrice <= 0 {
				continue
			}
			if best.ComparablePrice == 0 || h.ComparablePrice < best.ComparablePrice {
				best = h
			}
		}
		return best
	}
	// No normalization occurred: fall back to the dominant currency cohort.
	cohort := dominantHotelCurrency(hotels)
	for _, h := range hotels {
		if h.Price <= 0 {
			continue
		}
		if cohort != "" && h.Currency != cohort {
			continue
		}
		if best.Price == 0 || h.Price < best.Price {
			best = h
		}
	}
	return best
}

// dominantHotelCurrency returns the most frequently occurring non-empty
// currency among positive-priced hotels. The tie-break is deterministic: the
// currency that first reaches the maximum running count wins. Returns "" when
// none carry a currency.
func dominantHotelCurrency(hotels []models.HotelResult) string {
	counts := make(map[string]int)
	best := ""
	bestN := 0
	for _, h := range hotels {
		if h.Price <= 0 || h.Currency == "" {
			continue
		}
		counts[h.Currency]++
		if counts[h.Currency] > bestN {
			best, bestN = h.Currency, counts[h.Currency]
		}
	}
	return best
}

func cheapestFlight(flights []models.FlightResult) (float64, string) {
	var price float64
	var currency string
	for _, f := range flights {
		if f.Price > 0 && (price == 0 || f.Price < price) {
			price = f.Price
			currency = f.Currency
		}
	}
	return price, currency
}
