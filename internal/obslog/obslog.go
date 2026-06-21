// Package obslog records observed prices from ad-hoc searches into the price
// history store (MIK-6229). It is the thin seam between the search handlers
// (CLI + MCP) and the watch.Store, so the "log every search" behaviour lives in
// exactly one place rather than being copy-pasted into every call site.
//
// All functions are best-effort: a logging failure must never break a search.
// Callers may ignore the returned error or surface it as a non-fatal warning.
package obslog

import (
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// Recorder is the subset of watch.Store that obslog needs. Defined as an
// interface so tests can substitute a fake and so callers are not forced to
// pass a fully-loaded store.
type Recorder interface {
	RecordObservation(routeKey string, price float64, currency string) error
}

// FlightSearch logs the cheapest fare from a flight search result under the
// route key flight|ORIGIN|DEST|DATE. No-op when the result is empty or carries
// no positive price.
func FlightSearch(r Recorder, origin, destination, date string, res *models.FlightSearchResult) error {
	if r == nil || res == nil || len(res.Flights) == 0 {
		return nil
	}
	price, currency := cheapestFlight(res.Flights)
	if price <= 0 {
		return nil
	}
	return r.RecordObservation(watch.RouteKey("flight", origin, destination, date), price, currency)
}

// HotelPrices logs the cheapest provider price from a hotel price lookup under
// the route key hotel|HOTELID||CHECKIN. The destination slot is left empty
// because a hotel is identified by its own ID, not an O/D pair.
func HotelPrices(r Recorder, hotelID, checkIn string, res *models.HotelPriceResult) error {
	if r == nil || res == nil || len(res.Providers) == 0 {
		return nil
	}
	price, currency := cheapestProvider(res.Providers)
	if price <= 0 {
		return nil
	}
	return r.RecordObservation(watch.RouteKey("hotel", hotelID, "", checkIn), price, currency)
}

func cheapestFlight(flights []models.FlightResult) (float64, string) {
	var best float64
	var cur string
	for _, f := range flights {
		if f.Price > 0 && (best == 0 || f.Price < best) {
			best, cur = f.Price, f.Currency
		}
	}
	return best, cur
}

func cheapestProvider(providers []models.ProviderPrice) (float64, string) {
	var best float64
	var cur string
	for _, p := range providers {
		if p.Price > 0 && (best == 0 || p.Price < best) {
			best, cur = p.Price, p.Currency
		}
	}
	return best, cur
}
