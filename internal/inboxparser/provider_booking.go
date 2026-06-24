package inboxparser

import "github.com/MikkoParkkola/trvl/internal/trips"

// bookingProvider recognises Booking.com hotel reservations.
var bookingProvider = provider{
	matches: func(f messageFields) bool {
		return containsAny(f.from, "booking.com", "@booking.") ||
			containsAny(f.subject, "booking.com", "your booking is confirmed", "booking confirmation")
	},
	parse: parseBooking,
}

// parseBooking extracts a hotel stay from a Booking.com confirmation. Body
// lines are of the form:
//
//	Confirmation number: 1234567890
//	Property: Hotel Stary
//	City: Krakow
//	Check-in: 2026-06-16
//	Check-out: 2026-06-19
func parseBooking(f messageFields) (trips.ImportedRecord, []trips.TripLeg, bool) {
	ref := firstNonEmpty(
		valueAfter(f.body, "Confirmation number:"),
		valueAfter(f.body, "Confirmation:"),
		valueAfter(f.body, "Booking number:"),
	)
	property := firstNonEmpty(valueAfter(f.body, "Property:"), valueAfter(f.body, "Hotel:"))
	city := firstNonEmpty(valueAfter(f.body, "City:"), valueAfter(f.body, "Location:"))
	checkIn := firstNonEmpty(valueAfter(f.body, "Check-in:"), valueAfter(f.body, "Check in:"))
	checkOut := firstNonEmpty(valueAfter(f.body, "Check-out:"), valueAfter(f.body, "Check out:"))

	return buildStay(stay{
		provider:   "Booking.com",
		ref:        ref,
		property:   property,
		city:       city,
		checkInRaw: checkIn,
		checkOut:   checkOut,
	})
}
