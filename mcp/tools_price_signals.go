// Package mcp — price-signal helpers (MIK-6229/6232/6234).
//
// These helpers centralise the obslog/pricesignal/counterfactual/booking
// wiring so neither the flight nor the hotel handler carries duplicated logic.
// All operations are best-effort: a store error must never break a search.
package mcp

import (
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

// Freshness windows for cached, call-free savings served on the MCP path.
const (
	mcpGridFreshness  = 24 * time.Hour
	mcpProbeFreshness = 12 * time.Hour
)

// flightPriceSignals logs the cheapest fare from a flight search into the
// watch store and returns the price-position signal plus any call-free
// counterfactual savings.
//
// Guards:
//   - Only wired for single O/D routes; multi-airport searches have no
//     canonical route key and are silently skipped (returns nil, nil).
//   - A store error causes an early return with nil results; never propagated.
func flightPriceSignals(
	origin, dest, date string,
	result *models.FlightSearchResult,
) (pos *pricesignal.Position, savings []counterfactual.Saving) {
	if result == nil || !result.Success || len(result.Flights) == 0 {
		return nil, nil
	}
	// Multi-airport: no single canonical key — skip silently.
	if origin == "" || dest == "" {
		return nil, nil
	}

	store, err := watch.DefaultStore()
	if err != nil {
		return nil, nil
	}
	if err := store.Load(); err != nil {
		return nil, nil
	}

	// Log cheapest fare — best-effort.
	_ = obslog.FlightSearch(store, origin, dest, date, result)

	// Price-position signal against accumulated history.
	key := watch.RouteKey("flight", origin, dest, date)
	cheapestCurrency := result.Flights[0].Currency
	cheapestPrice := result.Flights[0].Price
	for _, f := range result.Flights[1:] {
		if f.Price > 0 && f.Price < cheapestPrice {
			cheapestPrice = f.Price
			cheapestCurrency = f.Currency
		}
	}
	if cheapestPrice <= 0 {
		return nil, nil
	}
	p := pricesignal.Compute(store.RoutePrices(key, cheapestCurrency), cheapestPrice, 0)
	pos = &p

	// Tier-0 counterfactual savings — zero provider calls.
	now := time.Now()
	if s := counterfactual.SameDayAlternative(result.Flights, 10, now); s != nil {
		savings = append(savings, *s)
	}
	if s := counterfactual.VsHistory(pos, cheapestCurrency, now); s != nil {
		savings = append(savings, *s)
	}
	// Shift-day from the persisted price grid + Tier-1 probes pre-computed by the
	// watch monitor — both served call-free from cache (no provider calls here).
	savings = append(savings, shiftDaySavingsMCP(origin, dest, date, now)...)
	savings = append(savings, tier1CachedSavingsMCP(origin, dest, now)...)
	return pos, savings
}

// shiftDaySavingsMCP reads the persisted price grid for a route and returns
// call-free shift-day counterfactuals. No provider calls; nothing when no fresh
// grid covers the route.
func shiftDaySavingsMCP(origin, destination, date string, now time.Time) []counterfactual.Saving {
	store, err := dategrid.DefaultStore()
	if err != nil || store.Load() != nil {
		return nil
	}
	g, ok := store.Get(dategrid.RouteKey(origin, destination))
	if !ok || !g.Fresh(now, mcpGridFreshness) {
		return nil
	}
	grid := make([]models.DatePriceResult, 0, len(g.Points))
	for _, p := range g.Points {
		grid = append(grid, models.DatePriceResult{Date: p.Date, ReturnDate: p.ReturnDate, Price: p.Price, Currency: p.Currency})
	}
	return counterfactual.ShiftDay(grid, date, 10, g.UpdatedAt)
}

// tier1CachedSavingsMCP returns Tier-1 savings the watch monitor pre-computed
// for this route, served call-free from the probe cache.
func tier1CachedSavingsMCP(origin, destination string, now time.Time) []counterfactual.Saving {
	store, err := probecache.DefaultStore()
	if err != nil || store.Load() != nil {
		return nil
	}
	e, ok := store.Get(probecache.RouteKey(origin, destination))
	if !ok || !e.Fresh(now, mcpProbeFreshness) {
		return nil
	}
	return e.Savings
}

// hotelPriceSignals logs the cheapest provider price from a hotel price lookup
// into the watch store and returns the price-position signal plus a
// booking-readiness verdict.
//
// A store error causes an early return with nil position; readiness is still
// computed because it derives only from the result, not the store.
func hotelPriceSignals(
	hotelID, checkIn string,
	result *models.HotelPriceResult,
) (pos *pricesignal.Position, readiness *booking.Verdict) {
	if result == nil || len(result.Providers) == 0 {
		return nil, nil
	}

	// Booking readiness is derived purely from result — no store needed.
	v := hotelBookingReadiness(hotelID, result.Providers)
	readiness = &v

	// Price-position: requires the store.
	store, err := watch.DefaultStore()
	if err != nil {
		return nil, readiness
	}
	if err := store.Load(); err != nil {
		return nil, readiness
	}

	_ = obslog.HotelPrices(store, hotelID, checkIn, result)

	key := watch.RouteKey("hotel", hotelID, "", checkIn)
	cheapest := cheapestProviderPrice(result.Providers)
	if cheapest.Price <= 0 {
		return nil, readiness
	}
	p := pricesignal.Compute(store.RoutePrices(key, cheapest.Currency), cheapest.Price, 0)
	pos = &p
	return pos, readiness
}

// hotelBookingReadiness maps the signals available on the hotel_prices endpoint
// into a booking.Verdict. This mirrors bookingReadinessForPrices from the CLI
// (cmd/trvl/prices.go) without importing package main.
//
// Signal mapping:
//   - IdentityConfirmed  ← hotelID non-empty (caller supplied a place ID)
//   - LinkStable         ← cheapest provider LinkDurability: "stable"→true, "expiring"→false
//   - Verified           ← cheapest provider PriceConfidence: verified/room_level→true, unverified→false
//   - RefundabilityKnown ← not available on the prices endpoint; left nil
func hotelBookingReadiness(hotelID string, providers []models.ProviderPrice) booking.Verdict {
	cheapest := cheapestProviderPrice(providers)
	var in booking.Input
	if hotelID != "" {
		in.IdentityConfirmed = booking.True()
	}
	switch cheapest.LinkDurability {
	case "stable":
		in.LinkStable = booking.True()
	case "expiring":
		in.LinkStable = booking.False()
		// default → nil (signal absent)
	}
	switch cheapest.PriceConfidence {
	case models.PriceConfidenceVerified, models.PriceConfidenceRoomLevel:
		in.Verified = booking.True()
	case models.PriceConfidenceUnverified:
		in.Verified = booking.False()
		// default → nil (signal absent)
	}
	// RefundabilityKnown: prices endpoint does not carry refundability data.
	// Leave nil — treated conservatively by booking.Evaluate.
	return booking.Evaluate(in)
}

// cheapestProviderPrice returns the lowest positive-priced provider, or a zero
// value if all prices are non-positive.
func cheapestProviderPrice(providers []models.ProviderPrice) models.ProviderPrice {
	var best models.ProviderPrice
	for _, p := range providers {
		if p.Price > 0 && (best.Price == 0 || p.Price < best.Price) {
			best = p
		}
	}
	return best
}

// roomsBookingReadiness maps the richer signals on the hotel_rooms endpoint into
// a booking.Verdict. Unlike hotel_prices, rooms carry refundability and a
// classifiable link, so "ready" is reachable here. Mirrors the CLI
// bookingReadinessForRooms without importing package main.
func roomsBookingReadiness(result *hotels.RoomAvailability) booking.Verdict {
	var in booking.Input
	if result.HotelID != "" {
		in.IdentityConfirmed = booking.True()
	}
	// Refundability: known when any room exposes a refund/cancellation signal.
	for _, r := range result.Rooms {
		if r.Refundable != nil || r.FreeCancellation != nil || r.CancellationPolicy != "" {
			in.RefundabilityKnown = booking.True()
			break
		}
	}
	// Verified: any room inventory option with verified/room-level confidence.
	for _, r := range result.Rooms {
		done := false
		for _, opt := range r.InventoryOptions {
			switch opt.PriceConfidence {
			case models.PriceConfidenceVerified, models.PriceConfidenceRoomLevel:
				in.Verified = booking.True()
				done = true
			}
			if done {
				break
			}
		}
		if done {
			break
		}
	}
	// LinkStable: classify any room booking URL; a stable link unlocks "ready".
	sawExpiring := false
	for _, r := range result.Rooms {
		urls := []string{r.ProviderURL}
		for _, opt := range r.InventoryOptions {
			urls = append(urls, opt.ProviderURL)
		}
		for _, u := range urls {
			switch hotels.ClassifyLinkDurability(u) {
			case "stable":
				in.LinkStable = booking.True()
			case "expiring":
				sawExpiring = true
			}
		}
		if in.LinkStable != nil {
			break
		}
	}
	if in.LinkStable == nil && sawExpiring {
		in.LinkStable = booking.False()
	}
	return booking.Evaluate(in)
}
