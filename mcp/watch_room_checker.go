package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// liveRoomChecker implements watch.RoomChecker using the real hotel rooms
// API, for the MCP-embedded scheduler.
//
// Found by adversarial review, 2026-07-28: mcp/server.go's scheduler was
// constructed with a nil room checker (watch.NewScheduler's signature does
// not even accept one), which was harmless before this session's
// cross-process scheduler singleton — the standalone `trvl watch daemon`
// (cmd/trvl/watch.go's liveRoomChecker) has always had a real one, and
// before the singleton lock BOTH the MCP-embedded scheduler and the
// standalone daemon ran independently, so the daemon's scheduler covered
// room watches regardless of what MCP could do.
//
// The singleton lock changes that: only ONE scheduler runs across the
// whole store now. In the normal startup order (MCP server up first,
// long-lived), MCP wins the lock and the daemon's own TryLockScheduler
// call fails and exits immediately (see cmd/trvl/watch_daemon.go). Room
// watches would then never be periodically checked or alerted, silently,
// for as long as MCP holds the lock -- a real functional regression for
// an entire watch type, introduced by this session's own scheduler fix.
//
// This mirrors cmd/trvl/rooms.go's resolveRoomAvailability +
// looksLikeGoogleHotelID + cmd/trvl/watch.go's liveRoomChecker exactly,
// reimplemented here rather than shared: cmd/trvl (package main) exports
// nothing importable from mcp (a different package), and extracting a
// shared internal package for this logic is a larger, separate
// refactor of a live hotel-search code path than this fix's scope.
// Uses this package's own swappable hotels-API func vars
// (getRoomAvailabilityWithOptsFunc, searchHotelByNameFunc — see
// tools_hotels_details.go) rather than calling the hotels package
// directly, matching this package's existing test-mocking convention.
type liveRoomChecker struct{}

func (c *liveRoomChecker) CheckRooms(ctx context.Context, w watch.Watch) ([]watch.RoomMatch, error) {
	currency := w.Currency
	if currency == "" {
		currency = "USD"
	}

	result, err := resolveRoomAvailabilityForWatch(ctx, w.HotelName, w.DepartDate, w.ReturnDate, currency)
	if err != nil {
		return nil, err
	}

	var matches []watch.RoomMatch
	for _, room := range result.Rooms {
		if watch.MatchRoomKeywords(w.RoomKeywords, room.Name, room.Description) {
			matches = append(matches, watch.RoomMatch{
				Name:        room.Name,
				Description: room.Description,
				Price:       room.Price,
				Currency:    room.Currency,
				Provider:    room.Provider,
			})
		}
	}
	return matches, nil
}

// resolveRoomAvailabilityForWatch mirrors cmd/trvl/rooms.go's
// resolveRoomAvailability, specialized to the watch-scheduler call shape
// (no --location hint is available from a Watch record, matching how
// cmd/trvl/watch.go's liveRoomChecker calls the CLI version with
// location="").
func resolveRoomAvailabilityForWatch(ctx context.Context, hotelQuery, checkIn, checkOut, currency string) (*hotels.RoomAvailability, error) {
	if looksLikeGoogleHotelIDForWatch(hotelQuery) {
		opts := hotels.RoomSearchOptions{
			HotelID:  hotelQuery,
			CheckIn:  checkIn,
			CheckOut: checkOut,
			Currency: currency,
		}
		return getRoomAvailabilityWithOptsFunc(ctx, opts)
	}

	hotel, err := searchHotelByNameFunc(ctx, hotelQuery, checkIn, checkOut, currency)
	if err != nil {
		return nil, fmt.Errorf("hotel lookup for %q: %w", hotelQuery, err)
	}
	if hotel.HotelID == "" {
		return nil, fmt.Errorf("hotel %q found (%s) but has no Google ID", hotelQuery, hotel.Name)
	}

	opts := hotels.RoomSearchOptions{
		HotelID:  hotel.HotelID,
		CheckIn:  checkIn,
		CheckOut: checkOut,
		Currency: currency,
		Location: hotelQuery,
	}
	result, err := getRoomAvailabilityWithOptsFunc(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("room availability for %s: %w", hotel.Name, err)
	}
	if result.Name == "" {
		result.Name = hotel.Name
	}
	return result, nil
}

// looksLikeGoogleHotelIDForWatch is a byte-for-byte copy of
// cmd/trvl/rooms.go's unexported looksLikeGoogleHotelID (not importable
// across packages -- see this file's doc comment).
func looksLikeGoogleHotelIDForWatch(value string) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "/g/") {
		return true
	}

	upper := strings.ToUpper(value)
	if strings.HasPrefix(upper, "CHIJ") {
		return true
	}

	return strings.Count(value, ":") == 1 && !strings.ContainsAny(value, " \t")
}
