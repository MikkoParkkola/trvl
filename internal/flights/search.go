package flights

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/sync/singleflight"
)

// flightGroup deduplicates concurrent in-flight searches with identical parameters.
// Only truly concurrent requests are coalesced; the existing cache layer handles
// TTL-based reuse between sequential calls.
var flightGroup singleflight.Group

// DefaultClient returns a shared batchexec.Client for the flights package.
// The client is created once and reused across all requests, enabling
// connection reuse and shared rate limiting.
func DefaultClient() *batchexec.Client {
	return batchexec.SharedClient()
}

// SearchOptions configures a flight search.
type SearchOptions struct {
	ReturnDate string            // Return date for round-trip (YYYY-MM-DD); empty = one-way
	CabinClass models.CabinClass // Cabin class (default: Economy)
	MaxStops   models.MaxStops   // Maximum stops filter
	SortBy     models.SortBy     // Result sort order
	Airlines   []string          // Restrict to these airline IATA codes
	Adults     int               // Number of adult passengers (default: 1)

	// Currency forces Google Flights to return prices in this currency by
	// setting the gl= (geolocation) query parameter. When empty, no gl= is
	// added and Google uses IP-based geolocation (which may return RUB, PLN,
	// etc. depending on the user's IP). This is used by the optimizer to
	// ensure all candidates are priced in the same currency (EUR).
	Currency string

	// Server-side filters passed to Google Flights batchexecute.
	MaxPrice    int // Max price in whole currency units (0 = no limit)
	MaxDuration int // Max total flight duration in minutes (0 = no limit)
	CarryOnBags int // Carry-on bags filter (0 = no filter, 1+ = require N carry-on bags included)
	CheckedBags int // Checked bags filter (0 = no filter, 1+ = require N checked bags included)
	// Wire format at outer[1][10] is []any{carryOn, checked} — verified via live probe.
	// Scalar int returns 400 Bad Request; array is required.
	ExcludeBasic  bool     // Exclude basic economy fares
	Alliances     []string // Alliance filter; e.g. ["STAR_ALLIANCE", "ONEWORLD", "SKYTEAM"]
	DepartAfter   string   // Earliest departure time "HH:MM" (e.g. "06:00")
	DepartBefore  string   // Latest departure time "HH:MM" (e.g. "22:00")
	LessEmissions bool     // Only show flights with less emissions

	// Client-side post-filters (applied after server response).
	RequireCheckedBag bool // Only show flights with ≥1 free checked bag
}

// defaults fills in zero-value fields with sensible defaults.
func (o *SearchOptions) defaults() {
	if o.Adults <= 0 {
		o.Adults = 1
	}
	if o.CabinClass == 0 {
		o.CabinClass = models.Economy
	}
}

// SearchFlights searches for flights from origin to destination on the given date.
//
// origin and destination are IATA airport codes (e.g. "HEL", "NRT").
// date is the departure date as "YYYY-MM-DD".
//
// Returns a FlightSearchResult with parsed flight options, or an error.
// Uses a shared default client for connection reuse and rate limiting.
func SearchFlights(ctx context.Context, origin, destination, date string, opts SearchOptions) (*models.FlightSearchResult, error) {
	return SearchFlightsWithClient(ctx, DefaultClient(), origin, destination, date, opts)
}

// flightSearchKey builds a singleflight dedup key from the search parameters.
func flightSearchKey(origin, destination, date string, opts SearchOptions) string {
	return fmt.Sprintf("flight|%s|%s|%s|%s|%d|%d|%d|%d|%d|%d|%d|%d|%s|%s|%v|%v|%v|%s|%s|%s",
		origin, destination, date, opts.ReturnDate,
		opts.CabinClass, opts.MaxStops, opts.SortBy, opts.Adults,
		opts.MaxPrice, opts.MaxDuration, opts.CarryOnBags, opts.CheckedBags,
		opts.Currency, strings.Join(opts.Airlines, ","),
		opts.ExcludeBasic, opts.LessEmissions, opts.RequireCheckedBag,
		strings.Join(opts.Alliances, ","), opts.DepartAfter, opts.DepartBefore,
	)
}

// SearchFlightsWithClient is like SearchFlights but accepts a pre-built client,
// useful for reusing connections across multiple requests.
func SearchFlightsWithClient(ctx context.Context, client *batchexec.Client, origin, destination, date string, opts SearchOptions) (*models.FlightSearchResult, error) {
	opts.defaults()

	if origin == "" || destination == "" || date == "" {
		return &models.FlightSearchResult{
			Error: "origin, destination, and date are required",
		}, fmt.Errorf("origin, destination, and date are required")
	}
	if client == nil {
		return &models.FlightSearchResult{
			Error: "client is required",
		}, fmt.Errorf("client is required")
	}

	key := flightSearchKey(origin, destination, date, opts)
	v, err, _ := flightGroup.Do(key, func() (any, error) {
		return searchFlightsCore(ctx, client, origin, destination, date, opts)
	})
	if err != nil {
		if r, ok := v.(*models.FlightSearchResult); ok {
			return r, err
		}
		return &models.FlightSearchResult{Error: err.Error()}, err
	}
	// singleflight.Do returns the same *FlightSearchResult to all callers that
	// coalesce on the same key. Callers mutate result.Flights and result.Count
	// (e.g. preferences filtering in mcp/tools_flights.go), which would race
	// across concurrent callers. Return a shallow copy per caller: a fresh
	// struct header with its own Flights slice header so Count / Flights
	// writes are caller-private. The underlying FlightResult elements are
	// treated as immutable after search completes and remain safely shared.
	shared := v.(*models.FlightSearchResult)
	if shared == nil {
		return nil, nil
	}
	cp := *shared
	if shared.Flights != nil {
		cp.Flights = make([]models.FlightResult, len(shared.Flights))
		copy(cp.Flights, shared.Flights)
	}
	return &cp, nil
}

// searchFlightsCore performs the actual flight search without singleflight wrapping.
func searchFlightsCore(ctx context.Context, client *batchexec.Client, origin, destination, date string, opts SearchOptions) (*models.FlightSearchResult, error) {
	googleResult, googleErr := searchGoogleFlightsWithClient(ctx, client, origin, destination, date, opts)
	googleSucceeded := googleErr == nil
	currency := flightSearchCurrency(googleResult)

	var googleFlights []models.FlightResult
	if googleSucceeded && googleResult != nil {
		googleFlights = googleResult.Flights
	}

	var kiwiFlights []models.FlightResult
	var kiwiErr error
	kiwiSucceeded := false
	if kiwiSearchEligible(client, opts) {
		kiwiFlights, kiwiErr = SearchKiwiFlights(ctx, origin, destination, date, currency, opts)
		if kiwiErr != nil {
			slog.Warn("kiwi flight search failed", "origin", origin, "destination", destination, "date", date, "error", kiwiErr)
		} else {
			kiwiSucceeded = true
		}
	}

	mergedFlights := mergeFlightResults(googleFlights, kiwiFlights, opts)
	if googleSucceeded || kiwiSucceeded {
		return &models.FlightSearchResult{
			Success:  true,
			Count:    len(mergedFlights),
			TripType: tripTypeForSearch(opts),
			Flights:  mergedFlights,
		}, nil
	}

	if googleErr != nil && kiwiErr != nil {
		err := errors.Join(googleErr, kiwiErr)
		return &models.FlightSearchResult{
			Error: err.Error(),
		}, err
	}
	if googleErr != nil {
		return googleResult, googleErr
	}
	if kiwiErr != nil {
		return &models.FlightSearchResult{
			Error: kiwiErr.Error(),
		}, kiwiErr
	}

	return &models.FlightSearchResult{
		Error: "unreachable flight search state",
	}, fmt.Errorf("unreachable flight search state")
}

func searchGoogleFlightsWithClient(ctx context.Context, client *batchexec.Client, origin, destination, date string, opts SearchOptions) (*models.FlightSearchResult, error) {
	filters := buildFilters(origin, destination, date, opts)

	encoded, err := batchexec.EncodeFlightFilters(filters)
	if err != nil {
		return &models.FlightSearchResult{
			Error: fmt.Sprintf("encode filters: %v", err),
		}, fmt.Errorf("encode filters: %w", err)
	}

	gl := CurrencyToGL(opts.Currency)
	status, body, err := client.SearchFlightsGL(ctx, encoded, gl)
	if err != nil {
		return &models.FlightSearchResult{
			Error: fmt.Sprintf("request failed: %v", err),
		}, fmt.Errorf("request failed: %w", err)
	}

	if status == 403 {
		return &models.FlightSearchResult{
			Error: "blocked by Google (403)",
		}, batchexec.ErrBlocked
	}
	if status != 200 {
		return &models.FlightSearchResult{
			Error: fmt.Sprintf("unexpected status %d", status),
		}, fmt.Errorf("unexpected status %d", status)
	}

	inner, err := batchexec.DecodeFlightResponse(body)
	if err != nil {
		return &models.FlightSearchResult{
			Error: fmt.Sprintf("decode response: %v", err),
		}, fmt.Errorf("decode response: %w", err)
	}

	rawFlights, err := batchexec.ExtractFlightData(inner)
	if err != nil {
		return &models.FlightSearchResult{
			Error: fmt.Sprintf("extract flights: %v", err),
		}, fmt.Errorf("extract flights: %w", err)
	}

	flights := parseFlights(rawFlights)

	// Add booking URLs. Prices are in the API's native currency (IP-based).
	// Currency conversion, if needed, happens in the CLI display layer.
	for i := range flights {
		flights[i].Provider = "google_flights"
		flights[i].BookingURL = buildFlightBookingURL(origin, destination, date)
	}

	return &models.FlightSearchResult{
		Success:  true,
		Count:    len(flights),
		TripType: tripTypeForSearch(opts),
		Flights:  flights,
	}, nil
}

// buildFlightBookingURL constructs a Google Flights deep link for a route and date.
// Optionally includes return date (round-trip) and currency parameters.
// Inspired by @Alorse's contribution in PR #33.
func buildFlightBookingURL(origin, destination, date string, returnDate ...string) string {
	url := fmt.Sprintf("https://www.google.com/travel/flights?q=Flights+to+%s+from+%s+on+%s", destination, origin, date)
	if len(returnDate) > 0 && returnDate[0] != "" {
		url += "+through+" + returnDate[0]
	}
	return url
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
