package batchexec

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// EncodeBatchExecute builds the f.req payload for a batchexecute call.
//
// The Google batchexecute protocol wraps each RPC in:
//
//	[[[rpcid, JSON.stringify(args), null, "generic"]]]
//
// This function takes an rpcid and an already-JSON-encoded args string,
// wraps it in the batchexecute envelope, then URL-encodes the result.
func EncodeBatchExecute(rpcid, argsJSON string) string {
	inner := []any{rpcid, argsJSON, nil, "generic"}
	outer := []any{[]any{inner}}

	raw, _ := json.Marshal(outer)
	return url.QueryEscape(string(raw))
}

// EncodeFlightFilters builds the f.req payload for a flight search.
//
// The fli Python library constructs a deeply nested array representing flight
// search parameters, then encodes it as:
//
//	url_encode(json([null, json(filters)]))
//
// This Go implementation mirrors that: given an already-built filters structure,
// it serialises to JSON, wraps in [null, "<json-string>"], and URL-encodes.
func EncodeFlightFilters(filters any) (string, error) {
	filtersJSON, err := json.Marshal(filters)
	if err != nil {
		return "", fmt.Errorf("marshal filters: %w", err)
	}

	// Wrap: [null, "<json-encoded-filters>"]
	wrapped := []any{nil, string(filtersJSON)}
	wrappedJSON, err := json.Marshal(wrapped)
	if err != nil {
		return "", fmt.Errorf("marshal wrapped: %w", err)
	}

	return url.QueryEscape(string(wrappedJSON)), nil
}

// BuildFlightFilters constructs the minimal flight search filters array
// matching fli's FlightSearchFilters.format() output.
//
// Parameters:
//   - departureAirport: IATA code (e.g. "HEL")
//   - arrivalAirport: IATA code (e.g. "NRT")
//   - date: travel date as "YYYY-MM-DD"
//   - adults: number of adult passengers
//
// The returned structure is the "filters" array that gets passed to
// EncodeFlightFilters for final encoding.
func BuildFlightFilters(departureAirport, arrivalAirport, date string, adults int) any {
	// Segment structure matches fli's format:
	// [departure, arrival, time_restrictions, stops, airlines, null, date,
	//  max_duration, selected_flight, layover_airports, null, null,
	//  layover_duration, emissions_filter, 3]
	segment := []any{
		// [0] departure airports: [[[code, 0]]]
		[]any{[]any{[]any{departureAirport, 0}}},
		// [1] arrival airports: [[[code, 0]]]
		[]any{[]any{[]any{arrivalAirport, 0}}},
		// [2] time restrictions
		nil,
		// [3] stops (0 = any)
		0,
		// [4] airlines
		nil,
		// [5] unknown
		nil,
		// [6] travel date
		date,
		// [7] max duration
		nil,
		// [8] selected flight
		nil,
		// [9] layover airports
		nil,
		// [10] unknown
		nil,
		// [11] unknown
		nil,
		// [12] layover duration
		nil,
		// [13] emissions filter
		nil,
		// [14] unknown (fli hardcodes 3)
		3,
	}

	// Main filters structure matches fli's format():
	// [outer0, settings, sort_by, show_all, 0, 1]
	filters := []any{
		// outer[0]: empty array (flights) or null (dates)
		[]any{},
		// outer[1]: settings array
		[]any{
			nil,                    // [0]
			nil,                    // [1]
			2,                      // [2] trip type: 2 = one way
			nil,                    // [3]
			[]any{},                // [4]
			1,                      // [5] seat type: 1 = economy
			[]any{adults, 0, 0, 0}, // [6] passengers: [adults, children, infants_lap, infants_seat]
			nil,                    // [7] price limit
			nil,                    // [8]
			nil,                    // [9]
			nil,                    // [10] bags filter
			nil,                    // [11]
			nil,                    // [12]
			[]any{segment},         // [13] flight segments
			nil,                    // [14]
			nil,                    // [15]
			nil,                    // [16]
			1,                      // [17] hardcoded
			nil,                    // [18]
			nil,                    // [19]
			nil,                    // [20]
			nil,                    // [21]
			nil,                    // [22]
			nil,                    // [23]
			nil,                    // [24]
			nil,                    // [25]
			nil,                    // [26]
			nil,                    // [27]
			0,                      // [28] exclude basic economy: 0 = allow
		},
		// outer[2]: sort by (1 = best)
		1,
		// outer[3]: show all results (1 = yes)
		1,
		// outer[4]: unknown (0)
		0,
		// outer[5]: unknown (1)
		1,
	}

	return filters
}

// BuildHotelSearchPayload constructs a batchexecute payload for hotel search.
//
// The rpcid for hotel search is "AtySUc". The args encode the location query.
// This is a best-effort construction based on observed Chrome traffic.
func BuildHotelSearchPayload(location string, checkIn, checkOut [3]int, adults int) string {
	// Hotel search args observed format:
	// [null,null,null,null,null,null,null,null,null,null,[location],
	//  null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]
	// The exact format may vary; we try a minimal payload.
	args := fmt.Sprintf(`[null,null,null,null,null,null,null,null,null,null,[%q],null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`, location)
	return EncodeBatchExecute("AtySUc", args)
}

// BuildHotelPricePayload constructs a batchexecute payload for hotel price
// lookup with the default occupancy (2 adults, no children).
//
// It preserves the historical request shape. Callers that know the real party
// size should use BuildHotelPricePayloadWithOccupancy so Google filters the
// partner-price matrix to the requested occupancy server-side instead of
// returning the generic two-adult quote.
//
// rpcid "yY52ce" looks up prices for a specific hotel ID.
// Date arrays are [year, month, day].
func BuildHotelPricePayload(hotelID string, checkIn, checkOut [3]int, currency string) string {
	return BuildHotelPricePayloadWithOccupancy(hotelID, checkIn, checkOut, currency, 2, nil)
}

// BuildHotelPricePayloadWithOccupancy is like BuildHotelPricePayload but carries
// the requested occupancy into the yY52ce RPC, so Google scopes partner prices
// to that party size at the source rather than after the fetch.
//
// The occupancy block is [adults, [childAge...], 0]. adults and the child-age
// list are the verified fields and are plumbed from the caller. The trailing
// field's exact meaning is not confirmed upstream, so it is left at its
// historical 0.
// ponytail: third occupancy field unverified — left at 0; revisit only if
// Google starts keying multi-room quotes off it.
func BuildHotelPricePayloadWithOccupancy(hotelID string, checkIn, checkOut [3]int, currency string, adults int, childAges []int) string {
	if adults <= 0 {
		adults = 2
	}
	children := "[]"
	if len(childAges) > 0 {
		children = "["
		for i, age := range childAges {
			if i > 0 {
				children += ","
			}
			children += fmt.Sprintf("%d", age)
		}
		children += "]"
	}
	args := fmt.Sprintf(`[null,[%d,%d,%d],[%d,%d,%d],[%d,%s,0],%q,%q]`,
		checkIn[0], checkIn[1], checkIn[2],
		checkOut[0], checkOut[1], checkOut[2],
		adults, children,
		hotelID, currency)
	return EncodeBatchExecute("yY52ce", args)
}

// BuildHotelReviewPayload constructs a batchexecute payload for hotel reviews.
//
// rpcid "ocp93e" fetches guest reviews for a specific hotel ID.
// The limit controls how many reviews to request.
func BuildHotelReviewPayload(hotelID string, limit int) string {
	if limit <= 0 {
		limit = 10
	}
	args := fmt.Sprintf(`[%q,null,null,null,null,%d]`, hotelID, limit)
	return EncodeBatchExecute("ocp93e", args)
}
