package inboxparser

import "github.com/MikkoParkkola/trvl/internal/trips"

// airbnbProvider recognises Airbnb reservation confirmations.
var airbnbProvider = provider{
	matches: func(f messageFields) bool {
		return containsAny(f.from, "airbnb.com", "@airbnb.", "automated@airbnb") ||
			containsAny(f.subject, "airbnb", "reservation confirmed", "your trip to")
	},
	parse: parseAirbnb,
}

// parseAirbnb extracts a stay from an Airbnb confirmation. Body lines are of
// the form:
//
//	Confirmation code: HMABCDEF
//	Listing: Sunny loft in the old town
//	City: Krakow
//	Check-in: 2026-06-16
//	Checkout: 2026-06-19
func parseAirbnb(f messageFields) (trips.ImportedRecord, []trips.TripLeg, bool) {
	ref := firstNonEmpty(
		valueAfter(f.body, "Confirmation code:"),
		valueAfter(f.body, "Confirmation:"),
		valueAfter(f.body, "Reservation code:"),
	)
	property := firstNonEmpty(valueAfter(f.body, "Listing:"), valueAfter(f.body, "Property:"))
	city := firstNonEmpty(valueAfter(f.body, "City:"), valueAfter(f.body, "Location:"))
	checkIn := firstNonEmpty(valueAfter(f.body, "Check-in:"), valueAfter(f.body, "Check in:"))
	checkOut := firstNonEmpty(valueAfter(f.body, "Checkout:"), valueAfter(f.body, "Check-out:"))

	return buildStay(stay{
		provider:   "Airbnb",
		ref:        ref,
		property:   property,
		city:       city,
		checkInRaw: checkIn,
		checkOut:   checkOut,
	})
}

// stay is the common shape of a lodging confirmation across hotel-style
// providers.
type stay struct {
	provider   string
	ref        string
	property   string
	city       string
	checkInRaw string
	checkOut   string
}

// buildStay constructs the imported record and hotel leg shared by the
// Booking.com and Airbnb parsers. It returns ok=false when the minimum fields
// (reference, city, and a parseable check-in date) are absent.
func buildStay(s stay) (trips.ImportedRecord, []trips.TripLeg, bool) {
	inISO, inDate, inOK := parseFlexibleTime(s.checkInRaw)
	if s.ref == "" || s.city == "" || !inOK {
		return trips.ImportedRecord{}, nil, false
	}
	outISO, _, _ := parseFlexibleTime(s.checkOut)

	leg := trips.TripLeg{
		Type:      "hotel",
		From:      s.city,
		To:        s.city,
		Provider:  s.provider,
		StartTime: inISO,
		EndTime:   outISO,
		Reference: s.ref,
		Confirmed: true,
	}
	rec := trips.ImportedRecord{
		Type:       "hotel",
		Provider:   s.provider,
		Reference:  s.ref,
		Source:     "email",
		TravelDate: inDate,
		From:       s.city,
		To:         s.city,
		Notes:      s.property,
	}
	return rec, []trips.TripLeg{leg}, true
}
