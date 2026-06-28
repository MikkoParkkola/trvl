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

func checkFlight(ctx context.Context, w watch.Watch) (float64, string, string, error) {
	// Route watch or date range: use calendar/dates search.
	if w.IsRouteWatch() || w.IsDateRange() {
		return checkFlightRange(ctx, w)
	}

	// Specific date search.
	opts := flights.SearchOptions{ReturnDate: w.ReturnDate}
	result, err := flights.SearchFlights(ctx, w.Origin, w.Destination, w.DepartDate, opts)
	if err != nil {
		return 0, "", "", err
	}
	if !result.Success || len(result.Flights) == 0 {
		return 0, "", "", nil
	}

	cheapest := cheapest(result.Flights, func(f models.FlightResult) float64 { return f.Price })
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

	cheapest := cheapest(result.Dates, func(d models.DatePriceResult) float64 { return d.Price })
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

	cheapest := cheapest(result.Hotels, func(h models.HotelResult) float64 { return h.Price })
	return cheapest.Price, cheapest.Currency, checkIn, nil
}
