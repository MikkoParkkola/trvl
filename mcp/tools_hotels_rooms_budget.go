package mcp

import (
	"math"
	"slices"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotels"
)

// hotelRoomsResponse is the hotel_rooms reply: room availability, the
// booking-readiness verdict, and how many rooms the 50 KB budget left out.
type hotelRoomsResponse struct {
	*hotels.RoomAvailability
	BookingReadiness        string   `json:"booking_readiness,omitempty"`
	BookingReadinessReasons []string `json:"booking_readiness_reasons,omitempty"`
	BudgetOmitted           int      `json:"budget_omitted,omitempty"`
}

// summaryCheapestRoom is the room the hotel_rooms summary names as cheapest:
// the lowest positive headline price, else the first room. rooms must not be
// empty.
func summaryCheapestRoom(rooms []hotels.RoomType) hotels.RoomType {
	cheapest := rooms[0]
	for _, r := range rooms[1:] {
		if r.Price > 0 && (cheapest.Price == 0 || r.Price < cheapest.Price) {
			cheapest = r
		}
	}
	return cheapest
}

// cheapestRoomAt is the index of the first room with the lowest
// roomNightlyIn, the room a budget cut keeps. rooms must not be empty.
func cheapestRoomAt(rooms []hotels.RoomType, currency string, nights int) int {
	best := 0
	for i := range rooms {
		if roomNightlyIn(rooms[i], currency, nights) < roomNightlyIn(rooms[best], currency, nights) {
			best = i
		}
	}
	return best
}

// priciestRoomAt is the position of the room that costs the most per night
// once every price is on one basis and in one currency (roomNightlyIn),
// skipping positions skip reports; -1 when every room is skipped. skip may
// be nil.
func priciestRoomAt(rooms []hotels.RoomType, currency string, nights int, skip func(pos int) bool) int {
	worst, worstPrice := -1, 0.0
	for i, r := range rooms {
		if skip != nil && skip(i) {
			continue
		}
		if p := roomNightlyIn(r, currency, nights); worst < 0 || p > worstPrice {
			worst, worstPrice = i, p
		}
	}
	return worst
}

// roomCutter returns the budget drop step for a room list: it removes the
// priciest room per night (priciestRoomAt), down to one room.
func roomCutter(currency string, nights int) func([]hotels.RoomType) ([]hotels.RoomType, bool) {
	return func(rooms []hotels.RoomType) ([]hotels.RoomType, bool) {
		if len(rooms) <= 1 {
			return rooms, false
		}
		i := priciestRoomAt(rooms, currency, nights, nil)
		return slices.Delete(rooms, i, i+1), true
	}
}

// roomNightlyIn puts a room's price on one basis for comparison: the nightly
// rate, else the stay total over the nights, else the headline price,
// converted into currency when the room is quoted in another. A room with no
// price, or no rate into currency, is +Inf: it cannot be compared, so a cut
// takes it first.
func roomNightlyIn(r hotels.RoomType, currency string, nights int) float64 {
	p := r.NightlyPrice
	if p <= 0 && r.TotalPrice > 0 {
		p = r.TotalPrice / float64(max(nights, 1))
	}
	if p <= 0 {
		p = r.Price
	}
	if p <= 0 {
		return math.Inf(1)
	}
	have, want := strings.ToUpper(strings.TrimSpace(r.Currency)), strings.ToUpper(strings.TrimSpace(currency))
	if have != "" && want != "" && have != want {
		rate, ok := convertRateFunc(have, want)
		if !ok {
			return math.Inf(1)
		}
		p *= rate
	}
	return p
}

// stayNights counts the nights between two ISO dates (layout 2006-01-02);
// 1 when either date does not parse or the stay is not positive.
func stayNights(checkIn, checkOut string) int {
	in, errIn := time.Parse("2006-01-02", checkIn)
	out, errOut := time.Parse("2006-01-02", checkOut)
	if errIn != nil || errOut != nil || !out.After(in) {
		return 1
	}
	return int(out.Sub(in).Hours() / 24)
}
