// Package mcp — price-signal helpers (MIK-6229/6232/6234).
//
// These are thin delegations to internal/pricefeed, the single source of truth
// shared with the CLI. Keeping the wrappers preserves the handler call sites
// while ensuring the MCP and CLI surfaces compute identical signals.
package mcp

import (
	"time"

	"github.com/MikkoParkkola/trvl/internal/booking"
	"github.com/MikkoParkkola/trvl/internal/counterfactual"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/pricefeed"
	"github.com/MikkoParkkola/trvl/internal/pricesignal"
)

// flightPriceSignals returns the price-position and call-free savings for a
// single-O/D flight search (delegates to pricefeed.Flight).
func flightPriceSignals(origin, dest, date string, result *models.FlightSearchResult) (*pricesignal.Position, []counterfactual.Saving) {
	fr := pricefeed.Flight(origin, dest, date, result, time.Now())
	return fr.Position, fr.Savings
}

// hotelPriceSignals returns the price-position and booking-readiness verdict for
// a hotel price lookup (delegates to pricefeed).
func hotelPriceSignals(hotelID, checkIn string, result *models.HotelPriceResult) (*pricesignal.Position, *booking.Verdict) {
	if result == nil || len(result.Providers) == 0 {
		return nil, nil
	}
	v := pricefeed.HotelPricesReadiness(hotelID, result.Providers)
	pos := pricefeed.HotelPosition(hotelID, checkIn, result)
	return pos, &v
}

// roomsBookingReadiness returns the booking-readiness verdict for room
// availability (delegates to pricefeed.RoomsReadiness).
func roomsBookingReadiness(result *hotels.RoomAvailability) booking.Verdict {
	return pricefeed.RoomsReadiness(result)
}

// hotelBookingReadiness returns the booking-readiness verdict for the prices
// endpoint (delegates to pricefeed.HotelPricesReadiness).
func hotelBookingReadiness(hotelID string, providers []models.ProviderPrice) booking.Verdict {
	return pricefeed.HotelPricesReadiness(hotelID, providers)
}

// cheapestProviderPrice returns the lowest positive-priced provider (delegates
// to pricefeed.CheapestProvider).
func cheapestProviderPrice(providers []models.ProviderPrice) models.ProviderPrice {
	return pricefeed.CheapestProvider(providers)
}
