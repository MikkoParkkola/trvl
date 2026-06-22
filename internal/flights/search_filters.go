package flights

import (
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// buildFlightBookingURL constructs a Google Flights deep link for a route and date.
// Optionally includes return date (round-trip) and currency parameters.
// Inspired by @Alorse's contribution in PR #33.
func buildFlightBookingURL(origin, destination, date, returnDate, currency string) string {
	destCity := iataToCity(destination)
	originCity := iataToCity(origin)
	destDisplay := strings.ReplaceAll(destCity, " ", "+")
	originDisplay := strings.ReplaceAll(originCity, " ", "+")
	u := fmt.Sprintf("https://www.google.com/travel/flights?q=Flights+to+%s+from+%s+on+%s",
		destDisplay, originDisplay, date)
	if returnDate != "" {
		u += "+through+" + returnDate
	}
	if currency != "" {
		u += "&curr=" + currency
	}
	return u
}

// iataToCity maps common IATA airport codes to their city names for use in
// human-readable Google Flights URLs.
func iataToCity(iata string) string {
	cities := map[string]string{
		"HEL": "Helsinki", "NRT": "Tokyo", "HND": "Tokyo",
		"JFK": "New York", "EWR": "New York", "LGA": "New York",
		"LAX": "Los Angeles", "SFO": "San Francisco",
		"CDG": "Paris", "ORY": "Paris",
		"LHR": "London", "LGW": "London", "STN": "London",
		"SIN": "Singapore",
		"DXB": "Dubai",
		"BCN": "Barcelona",
		"AMS": "Amsterdam",
		"FRA": "Frankfurt",
		"MUC": "Munich",
		"VIE": "Vienna",
		"ZRH": "Zurich",
		"MAD": "Madrid",
		"FCO": "Rome", "CIA": "Rome",
		"MXP": "Milan", "LIN": "Milan", "BGY": "Milan",
		"ARN": "Stockholm",
		"CPH": "Copenhagen",
		"OSL": "Oslo",
		"DUB": "Dublin",
		"BRU": "Brussels", "CRL": "Brussels",
		"LIS": "Lisbon",
		"ATH": "Athens",
		"WAW": "Warsaw",
		"BUD": "Budapest",
		"PRG": "Prague",
		"TXL": "Berlin", "BER": "Berlin",
		"ORD": "Chicago", "MDW": "Chicago",
		"ATL": "Atlanta",
		"MIA": "Miami",
		"BOS": "Boston",
		"SEA": "Seattle",
		"DFW": "Dallas", "DAL": "Dallas",
		"DEN": "Denver",
		"IAH": "Houston", "HOU": "Houston",
		"PHX": "Phoenix",
		"YYZ": "Toronto",
		"YVR": "Vancouver",
		"YUL": "Montreal",
		"MEX": "Mexico City",
		"GRU": "Sao Paulo",
		"EZE": "Buenos Aires",
		"BOG": "Bogota",
		"SCL": "Santiago",
		"LIM": "Lima",
		"NBO": "Nairobi",
		"JNB": "Johannesburg",
		"CPT": "Cape Town",
		"CAI": "Cairo",
		"PEK": "Beijing", "PKX": "Beijing",
		"PVG": "Shanghai",
		"HKG": "Hong Kong",
		"ICN": "Seoul",
		"BKK": "Bangkok",
		"KUL": "Kuala Lumpur",
		"CGK": "Jakarta",
		"SYD": "Sydney",
		"MEL": "Melbourne",
	}
	if city, ok := cities[iata]; ok {
		return city
	}
	return iata
}

// buildFilters constructs the nested array structure for the flight search payload.
// This extends batchexec.BuildFlightFilters with support for cabin class, stops,
// round-trip, sort order, and airline filters.
func buildFilters(origin, destination, date string, opts SearchOptions) any {
	// Outbound segment
	outbound := buildSegment(origin, destination, date, opts)

	segments := []any{outbound}

	// Add return segment for round-trip
	if opts.ReturnDate != "" {
		ret := buildSegment(destination, origin, opts.ReturnDate, opts)
		segments = append(segments, ret)
	}

	// Trip type: 2 = one-way, 1 = round-trip
	tripType := 2
	if opts.ReturnDate != "" {
		tripType = 1
	}

	// Sort by: Google uses 1=best, 2=price, 3=duration, 4=departure, 5=arrival
	sortBy := 1 // default: best
	switch opts.SortBy {
	case models.SortCheapest:
		sortBy = 2
	case models.SortDuration:
		sortBy = 3
	case models.SortDepartureTime:
		sortBy = 4
	case models.SortArrivalTime:
		sortBy = 5
	}

	filters := []any{
		// outer[0]: empty array (flights mode)
		[]any{},
		// outer[1]: settings array
		[]any{
			nil,                         // [0]
			nil,                         // [1]
			tripType,                    // [2] trip type
			nil,                         // [3]
			[]any{},                     // [4]
			int(opts.CabinClass),        // [5] cabin class
			[]any{opts.Adults, 0, 0, 0}, // [6] passengers
			priceLimit(opts.MaxPrice),   // [7] max price (nil or int)
			nil,                         // [8]
			nil,                         // [9]
			bagsFilter(opts.CarryOnBags, opts.CheckedBags), // [10] bags [carryOn, checked]
			nil,                                    // [11]
			nil,                                    // [12]
			segments,                               // [13] flight segments
			nil,                                    // [14]
			nil,                                    // [15]
			nil,                                    // [16]
			1,                                      // [17]
			nil,                                    // [18]
			nil,                                    // [19]
			nil,                                    // [20]
			nil,                                    // [21]
			nil,                                    // [22]
			nil,                                    // [23]
			nil,                                    // [24]
			nil,                                    // [25] (was alliance — moved to segment[5])
			nil,                                    // [26]
			nil,                                    // [27]
			excludeBasicEconomy(opts.ExcludeBasic), // [28] exclude basic economy
		},
		// outer[2]: sort by
		sortBy,
		// outer[3]: show all
		1,
		// outer[4]
		0,
		// outer[5]
		1,
	}

	return filters
}

// buildSegment constructs a single flight segment (one direction).
func buildSegment(from, to, date string, opts SearchOptions) any {
	// Build airlines filter
	var airlines any
	if len(opts.Airlines) > 0 {
		airlineEntries := make([]any, len(opts.Airlines))
		for i, code := range opts.Airlines {
			airlineEntries[i] = code
		}
		airlines = airlineEntries
	}

	// MaxStops: 0=any, 1=nonstop, 2=1stop, 3=2+stops
	stops := int(opts.MaxStops)

	return []any{
		// [0] departure airports
		[]any{[]any{[]any{from, 0}}},
		// [1] arrival airports
		[]any{[]any{[]any{to, 0}}},
		// [2] departure time window [startHour, endHour] or nil
		departTimeWindow(opts.DepartAfter, opts.DepartBefore),
		// [3] stops
		stops,
		// [4] airlines
		airlines,
		// [5] alliance filter — verified via live probe: segment[5] with
		// []any{"STAR_ALLIANCE"} returns 45/115 flights (61% reduction)
		alliancesFilter(opts.Alliances),
		// [6] date
		date,
		// [7] max duration in minutes
		durationLimit(opts.MaxDuration),
		// [8] selected flight
		nil,
		// [9] layover airports
		nil,
		// [10]
		nil,
		// [11]
		nil,
		// [12] layover duration
		nil,
		// [13] emissions filter (1 = less emissions only)
		emissionsFilter(opts.LessEmissions),
		// [14]
		3,
	}
}

// priceLimit returns the max price for the batchexecute filter, or nil if unset.
func priceLimit(maxPrice int) any {
	if maxPrice <= 0 {
		return nil
	}
	return maxPrice
}

// bagsFilter returns the bags array for the batchexecute filter, or nil if unset.
// Wire format is []any{carryOnCount, checkedCount} — verified via live API probe.
// Scalar int returns 400; array is required. Both carry-on AND checked bag filters
// work server-side, even though Google's UI only exposes carry-on.
func bagsFilter(carryOn, checked int) any {
	if carryOn <= 0 && checked <= 0 {
		return nil
	}
	return []any{carryOn, checked}
}

// filterFlightsWithCheckedBag returns only flights that include at least one
// free checked bag. This is a client-side post-filter on parsed response data
// (offer[4][6]). The server-side bags filter at outer[1][10] is a price
// recalculation hint, not a result filter — it changes displayed prices but
// doesn't remove flights.
func filterFlightsWithCheckedBag(flights []models.FlightResult) []models.FlightResult {
	filtered := flights[:0]
	for _, f := range flights {
		if f.CheckedBagsIncluded != nil && *f.CheckedBagsIncluded >= 1 {
			filtered = append(filtered, f)
		}
	}
	return filtered
}

// filterFlightsByAlliance keeps only flights where the first leg's airline
// belongs to one of the requested alliances. Uses the airline→alliance map
// from loyalty.go. This is a client-side fallback because the server-side
// alliance filter at outer[1][25] returns 400 for all tested formats.
func filterFlightsByAlliance(flights []models.FlightResult, alliances []string) []models.FlightResult {
	want := make(map[string]bool, len(alliances))
	for _, a := range alliances {
		want[strings.ToLower(a)] = true
	}

	filtered := flights[:0]
	for _, f := range flights {
		if len(f.Legs) == 0 {
			continue
		}
		airline := f.Legs[0].AirlineCode
		if alliance, ok := allianceMembership[airline]; ok && want[alliance] {
			filtered = append(filtered, f)
		}
	}
	return filtered
}

// durationLimit returns the max duration in minutes, or nil if unset.
func durationLimit(maxDuration int) any {
	if maxDuration <= 0 {
		return nil
	}
	return maxDuration
}

// excludeBasicEconomy returns the flag for the batchexecute filter.
func excludeBasicEconomy(exclude bool) int {
	if exclude {
		return 1
	}
	return 0
}

// alliancesFilter returns the alliances array for the batchexecute filter,
// or nil if no alliances are specified.
//
// Accepted alliance names (case-insensitive): STAR_ALLIANCE, ONEWORLD, SKYTEAM.
// Unknown values are passed through as-is to avoid silently dropping filters.
func alliancesFilter(alliances []string) any {
	if len(alliances) == 0 {
		return nil
	}
	entries := make([]any, len(alliances))
	for i, a := range alliances {
		entries[i] = strings.ToUpper(strings.TrimSpace(a))
	}
	return entries
}

// departTimeWindow parses "HH:MM" strings and returns the segment [2] value
// []any{startHour, endHour}, or nil when neither bound is set.
// Malformed values are silently ignored (treated as unset).
func departTimeWindow(after, before string) any {
	start := parseHour(after)
	end := parseHour(before)
	if start < 0 && end < 0 {
		return nil
	}
	// Use 0 for an unset lower bound and 24 for an unset upper bound so the
	// API sees a well-formed window even when only one side is specified.
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 24
	}
	return []any{start, end}
}

// parseHour parses a strict "HH:MM" string (exactly 5 characters) and returns
// the hour as an integer [0, 23]. Returns -1 on any parse error or out-of-range value.
func parseHour(hhmm string) int {
	// Must be exactly "HH:MM" — 5 characters, colon at index 2.
	if len(hhmm) != 5 || hhmm[2] != ':' {
		return -1
	}
	h0, h1 := hhmm[0], hhmm[1]
	if h0 < '0' || h0 > '9' || h1 < '0' || h1 > '9' {
		return -1
	}
	m0, m1 := hhmm[3], hhmm[4]
	if m0 < '0' || m0 > '9' || m1 < '0' || m1 > '9' {
		return -1
	}
	hour := int(h0-'0')*10 + int(h1-'0')
	if hour > 23 {
		return -1
	}
	return hour
}

// currencyGLMap maps ISO 4217 currency codes to Google's gl= country codes.
// When gl= is set, Google Flights returns prices in that country's currency.
var currencyGLMap = map[string]string{
	"EUR": "FI", // Finland (Eurozone)
	"USD": "US",
	"GBP": "GB",
	"CHF": "CH",
	"SEK": "SE",
	"NOK": "NO",
	"DKK": "DK",
	"PLN": "PL",
	"CZK": "CZ",
	"HUF": "HU",
	"TRY": "TR",
	"JPY": "JP",
	"AUD": "AU",
	"CAD": "CA",
	"NZD": "NZ",
	"INR": "IN",
	"BRL": "BR",
	"MXN": "MX",
	"THB": "TH",
	"SGD": "SG",
	"HKD": "HK",
	"KRW": "KR",
	"TWD": "TW",
	"ILS": "IL",
	"AED": "AE",
	"ZAR": "ZA",
	"RUB": "RU",
	"RON": "RO",
	"BGN": "BG",
	"ISK": "IS",
}

// CurrencyToGL returns the gl= country code for a currency, or "" if unknown
// or empty. When the result is "", no gl= parameter should be appended to the
// request URL (Google will use IP-based geolocation).
func CurrencyToGL(currency string) string {
	if currency == "" {
		return ""
	}
	return currencyGLMap[strings.ToUpper(currency)]
}

// emissionsFilter returns the emissions flag for the batchexecute filter.
// Wire format is []any{1} — scalar 1 returns 400. Verified via live probe:
// [1] at segment[13] returns 13 flights (89% reduction), scalar 1 returns 400.
func emissionsFilter(less bool) any {
	if less {
		return []any{1}
	}
	return nil
}
