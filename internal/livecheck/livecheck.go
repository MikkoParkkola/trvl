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

	cheapest := result.Flights[0]
	for _, f := range result.Flights[1:] {
		if f.Price > 0 && (cheapest.Price == 0 || f.Price < cheapest.Price) {
			cheapest = f
		}
	}
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

	cheapest := result.Dates[0]
	for _, d := range result.Dates[1:] {
		if d.Price > 0 && (cheapest.Price == 0 || d.Price < cheapest.Price) {
			cheapest = d
		}
	}
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
	_ = store.Put(dategrid.RouteKey(origin, destination), currency, pts, time.Now())
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

	cheapest := result.Hotels[0]
	for _, h := range result.Hotels[1:] {
		if h.Price > 0 && (cheapest.Price == 0 || h.Price < cheapest.Price) {
			cheapest = h
		}
	}
	return cheapest.Price, cheapest.Currency, checkIn, nil
}
