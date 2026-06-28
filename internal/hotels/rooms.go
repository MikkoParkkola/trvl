package hotels

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// RoomType represents a specific room category at a hotel.
type RoomType struct {
	Name              string  `json:"name"`
	Price             float64 `json:"price"`
	NightlyPrice      float64 `json:"nightly_price,omitempty"`
	TotalPrice        float64 `json:"total_price,omitempty"`
	TaxesAndFees      float64 `json:"taxes_and_fees,omitempty"`
	TaxesFeesIncluded *bool   `json:"taxes_fees_included,omitempty"`
	Currency          string  `json:"currency"`
	Provider          string  `json:"provider,omitempty"`
	ProviderURL       string  `json:"provider_url,omitempty"`
	RateID            string  `json:"rate_id,omitempty"`
	RatePlanName      string  `json:"rate_plan_name,omitempty"`
	MatchConfidence   string  `json:"match_confidence,omitempty"`
	// Readiness is the composed booking-readiness verdict (ready|caution|
	// unverified) from roomReadiness: one trustworthy gate over the scattered
	// price, link-durability, identity, and refundability signals. See MIK-6232.
	Readiness          string                      `json:"readiness,omitempty"`
	MaxGuests          int                         `json:"max_guests,omitempty"`
	BedType            string                      `json:"bed_type,omitempty"`
	SizeM2             float64                     `json:"size_m2,omitempty"`
	Description        string                      `json:"description,omitempty"`
	Amenities          []string                    `json:"amenities,omitempty"`
	CancellationPolicy string                      `json:"cancellation_policy,omitempty"`
	Refundable         *bool                       `json:"refundable,omitempty"`
	FreeCancellation   *bool                       `json:"free_cancellation,omitempty"`
	Board              string                      `json:"board,omitempty"`
	BreakfastIncluded  *bool                       `json:"breakfast_included,omitempty"`
	InventoryOptions   []models.RoomInventoryQuote `json:"inventory_options,omitempty"`
}

// RoomAvailability is the response for a room-type search.
type RoomAvailability struct {
	Success  bool       `json:"success"`
	HotelID  string     `json:"hotel_id"`
	Name     string     `json:"name,omitempty"`
	CheckIn  string     `json:"check_in"`
	CheckOut string     `json:"check_out"`
	Rooms    []RoomType `json:"rooms"`
	Notice   string     `json:"notice,omitempty"`
	Error    string     `json:"error,omitempty"`
}

// RoomSearchOptions configures a room availability search.
type RoomSearchOptions struct {
	HotelID      string // Google Hotels entity ID
	CheckIn      string // YYYY-MM-DD
	CheckOut     string // YYYY-MM-DD
	Currency     string // e.g. "USD", "EUR"
	Guests       int    // searched guest count; defaults to 2
	ChildrenAges []int  // requested child ages
	Rooms        int    // requested room count
	BookingURL   string // optional Booking.com hotel URL for rich room data
	Location     string // optional city/area hint for search-based fallback
}

// GetRoomAvailability fetches room-level pricing for a specific hotel.
//
// It fetches the hotel entity page and parses AF_initDataCallback blocks
// to extract room type names, prices, and provider information.
//
// When a BookingURL is provided (via opts or the bookingURL parameter),
// the function also fetches the Booking.com detail page to extract rich
// room data (descriptions, amenities, bed types, sizes) and merges those
// rooms into the result.
func GetRoomAvailability(ctx context.Context, hotelID, checkIn, checkOut, currency string) (*RoomAvailability, error) {
	return GetRoomAvailabilityWithOpts(ctx, RoomSearchOptions{
		HotelID:  hotelID,
		CheckIn:  checkIn,
		CheckOut: checkOut,
		Currency: currency,
	})
}

// GetRoomAvailabilityWithOpts fetches room-level pricing with full options,
// including optional Booking.com room enrichment.
//
// Google's entity page now uses deferred data loading via batchexecute RPCs
// that require browser session context. The inline AF_initDataCallback blocks
// are empty. As a fallback, this function searches for the hotel on the
// Google Hotels search page (which still embeds data inline) and constructs
// room entries from the search result price data.
func GetRoomAvailabilityWithOpts(ctx context.Context, opts RoomSearchOptions) (*RoomAvailability, error) {
	if opts.HotelID == "" {
		return nil, fmt.Errorf("hotel ID is required")
	}
	if opts.CheckIn == "" || opts.CheckOut == "" {
		return nil, fmt.Errorf("check-in and check-out dates are required")
	}
	if opts.Currency == "" {
		opts.Currency = "USD"
	}

	// Try the entity page first (fast path, works when Google serves inline data).
	// Also capture any location hint extracted from the entity page so the
	// search-page fallback can use it when no Location was provided by the caller
	// (e.g. raw hotel ID lookups from the CLI or MCP without a name hint).
	rooms, hotelName, entityLocation := tryEntityPage(ctx, opts)
	if opts.Location == "" && entityLocation != "" {
		opts.Location = entityLocation
	}

	// Fetch Booking.com rooms to provide room-level data alongside Google's.
	// Runs synchronously before the fallback so Booking data is available
	// regardless of whether the Google entity page returns room data.
	var bookingRooms []RoomType
	var bookingNotice string
	if opts.BookingURL != "" {
		br, brErr := FetchBookingRooms(ctx, opts.BookingURL, opts.CheckIn, opts.CheckOut, opts.Currency)
		if brErr != nil {
			slog.Debug("booking rooms fetch failed", "error", brErr)
			// A bot-wall / 429 / transient 5xx is retryable, not a genuine
			// "no rooms" result. Surface it so the caller knows Booking room
			// pricing was withheld this time rather than silently absent — the
			// same rate-limited-vs-failed distinction the completeness model
			// preserves at the search layer. Hard parse failures stay quiet
			// (the debug log above) since a retry won't help.
			bookingNotice = bookingRateLimitNotice(brErr)
		} else {
			bookingRooms = br
		}
	}

	// Google's entity-page inline room data is dead, but the yY52ce
	// batchexecute RPC still returns the live booking-partner price matrix for
	// the hotel. Pull it and convert each partner price into a property-level
	// room entry. This surfaces the full OTA matrix — including any Booking.com
	// partner URL, which the downstream exact-room Booking.com fetch needs to
	// produce a booking-ready offer.
	if len(rooms) == 0 {
		rpcRooms, rpcName := tryBatchExecutePrices(ctx, opts)
		if len(rpcRooms) > 0 {
			rooms = rpcRooms
			if hotelName == "" {
				hotelName = rpcName
			}
		}
	}

	// Fallback: search for the hotel on the search page by location extracted
	// from the hotel ID's geocoded area. The search page still has inline
	// AF_initDataCallback data.
	if len(rooms) == 0 {
		rooms, hotelName = trySearchPageFallback(ctx, opts)
	}
	notice := ""

	// Opt-in Google Hotels room-level detail fetch. Google's public yY52ce RPC
	// returns a null payload to static clients (probed 2026-06-25 — see
	// google_room_detail.go), so the per-room matrix is only reachable when an
	// operator supplies a session-holding relay via GOOGLE_HOTELS_DETAIL_API_BASE.
	// When configured this surfaces real room-level Google rates (room_level
	// confidence + ProviderURL); when unconfigured it is a silent no-op. Run it
	// only when the free Google paths have not already produced a verified room,
	// and merge so any existing lead-ins stay while the bookability sort leads
	// with the real room rate.
	if googleRoomDetailConfigured() && (len(rooms) == 0 || !hasVerifiedRoom(rooms)) {
		detailRooms, detailName, detailNotice := tryGoogleRoomDetail(ctx, opts)
		if len(detailRooms) > 0 {
			if len(rooms) == 0 {
				rooms = detailRooms
			} else {
				rooms = mergeRoomTypes(rooms, detailRooms)
			}
			if hotelName == "" {
				hotelName = detailName
			}
		} else if detailNotice != "" {
			// Room data was withheld by a retryable bot-wall, not absent.
			notice = appendNotice(notice, detailNotice)
		}
	}

	// SerpAPI is the richest room source we have (named rooms, verified
	// tax-inclusive prices, refundability) but every lookup spends metered
	// quota. Consult it only when the free Google paths could not produce a
	// verified room-level price -- i.e. nothing at all, or only sub-verified
	// lead-ins / "similar" matches. This upgrades a weak Google result (e.g. a
	// nightly-only "similar" price that downgrades to caution) into a
	// booking-ready offer, without burning a search when Google already
	// returned a verified room.
	if len(rooms) == 0 || !hasVerifiedRoom(rooms) {
		serpRooms, serpName, serpNotice := trySerpAPIRoomFallback(ctx, opts)
		if len(serpRooms) > 0 {
			if len(rooms) == 0 {
				rooms = serpRooms
			} else {
				// Keep Google's lead-ins, add SerpAPI's verified rooms.
				rooms = mergeRoomTypes(rooms, serpRooms)
			}
			notice = serpNotice
			if hotelName == "" {
				hotelName = serpName
			}
		} else if serpNotice != "" {
			// SerpAPI returned no rooms but flagged a retryable rate-limit;
			// surface it rather than silently degrading to no upgrade.
			notice = appendNotice(notice, serpNotice)
		}
	}

	// Consult Agoda for a room-level rate when the free Google paths (and the
	// optional SerpAPI upgrade) still produced no verified room price. Agoda is
	// key-free and its search already carries per-room inventory, so this adds a
	// real second priced provider to the drill-down without burning the hot
	// path. Merge so any Google lead-ins stay; bookability sort below leads with
	// whichever provider actually has a bookable price.
	if len(rooms) == 0 || !hasVerifiedRoom(rooms) {
		if agodaRooms, agodaNotice := tryAgodaRoomLevel(ctx, opts); len(agodaRooms) > 0 {
			if len(rooms) == 0 {
				rooms = agodaRooms
			} else {
				rooms = mergeRoomTypes(rooms, agodaRooms)
			}
		} else if agodaNotice != "" {
			// Agoda room data was withheld by a retryable bot-wall, not absent.
			notice = appendNotice(notice, agodaNotice)
		}
	}

	// Merge Booking.com rooms with Google rooms if both are available.
	if len(bookingRooms) > 0 {
		rooms = mergeRoomTypes(rooms, bookingRooms)
	} else if bookingNotice != "" {
		// Booking room data was withheld by a retryable bot-wall, not absent.
		notice = appendNotice(notice, bookingNotice)
	}

	// Lead with the rooms that carry a real, bookable price. Sources are merged
	// in fetch order (Google entity, partner matrix, search fallback, SerpAPI,
	// Booking) so a property-level lead-in can otherwise sit above a verified
	// room-level rate. Order by bookability (exact > similar > property-level
	// lead-in), then cheapest-first within a tier, so the most actionable price
	// surfaces first and unpriced lead-ins sink to the bottom.
	sortRoomsByBookability(rooms)

	// Compose the per-room booking-readiness verdict over the now-final trust
	// signals (price, link durability, room identity, refundability). One gate
	// the agent and CLI can lead with instead of re-deriving from scattered
	// fields. See MIK-6232.
	for i := range rooms {
		rooms[i].Readiness = roomReadiness(rooms[i])
	}

	return &RoomAvailability{
		Success:  true,
		HotelID:  opts.HotelID,
		Name:     hotelName,
		CheckIn:  opts.CheckIn,
		CheckOut: opts.CheckOut,
		Rooms:    rooms,
		Notice:   notice,
	}, nil
}
