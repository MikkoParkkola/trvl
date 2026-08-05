// Package flights implements Google Flights search via the internal batchexecute API.
package flights

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/logredact"

	"github.com/MikkoParkkola/trvl/internal/jsonutil"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// parseFlights extracts FlightResult structs from the raw flight entries
// returned by batchexec.ExtractFlightData.
//
// Each flight entry is a deeply nested JSON array with positional semantics
// (verified against live Google Flights API responses 2026-04):
//
//	entry[0]     — flight info array
//	entry[0][2]  — legs array (individual flight segments)
//	entry[0][3]  — overall departure airport code
//	entry[0][4]  — overall departure date [y, m, d]
//	entry[0][5]  — overall departure time [h, min]
//	entry[0][6]  — overall arrival airport code
//	entry[0][7]  — overall arrival date [y, m, d]
//	entry[0][8]  — overall arrival time [h, min]
//	entry[0][9]  — total duration in minutes
//	entry[1]     — price array
//	entry[1][0]  — price sub-array: last element is price in minor or major units
//	entry[1][1]  — booking token (protobuf, contains currency code)
//
// Leg structure (entry[0][2][i]):
//
//	leg[3]   — departure airport code
//	leg[4]   — departure airport name
//	leg[5]   — arrival airport name
//	leg[6]   — arrival airport code
//	leg[8]   — departure time [hour, minute]
//	leg[10]  — arrival time [hour, minute]
//	leg[11]  — duration in minutes
//	leg[20]  — departure date [year, month, day]
//	leg[21]  — arrival date [year, month, day]
//	leg[22]  — airline info: [code, flight_number, null, airline_name]
func parseFlights(rawFlights []any) []models.FlightResult {
	var results []models.FlightResult

	for _, raw := range rawFlights {
		entry, ok := raw.([]any)
		if !ok || len(entry) < 2 {
			continue
		}

		fr, err := parseOneFlight(entry)
		if err != nil {
			slog.Debug("skipped unparseable flight entry", "error", logredact.Err(err))
			continue
		}

		results = append(results, fr)
	}

	return results
}

// parseFailureIfAllDropped reports a typed parse failure when the upstream handed
// us flight entries but parseFlights decoded none of them — the positional array
// shape rotated underneath us. It returns nil when there were no raw entries (a
// genuine empty result, an honest no-hit) or when at least one entry parsed. The
// returned error wraps models.ErrParseFailed so the caller classifies it as
// StatusFailed and the circuit breaker counts it, instead of mistaking a broken
// parser for a route with no flights.
func parseFailureIfAllDropped(parsed, rawEntries int) error {
	if rawEntries > 0 && parsed == 0 {
		return fmt.Errorf("google flights: parsed 0 of %d entries: %w", rawEntries, models.ErrParseFailed)
	}
	return nil
}

// parseOneFlight parses a single flight entry into a FlightResult.
func parseOneFlight(entry []any) (models.FlightResult, error) {
	var fr models.FlightResult

	// entry[0] is the flight info array
	flightInfo, ok := entry[0].([]any)
	if !ok {
		return fr, fmt.Errorf("unexpected flight entry format")
	}

	// Parse legs from flightInfo[2] — the legs array in Google's live response.
	if len(flightInfo) > 2 {
		fr.Legs = parseLegs(flightInfo[2])
	}
	fr.Stops = max(len(fr.Legs)-1, 0)

	// Parse total duration from flightInfo[9]
	if len(flightInfo) > 9 {
		fr.Duration = jsonutil.ToInt(flightInfo[9])
	}

	// Parse price from entry[1]
	if len(entry) > 1 {
		price, currency := parsePrice(entry[1])
		fr.Price = price
		fr.Currency = currency
	}

	// Parse bag allowances from entry[4][6] (carry-on + checked bags).
	// Format: [carry_on_flag, checked_bag_flag]
	//   carry_on_flag:  0 = included in price
	//   checked_bag_flag: 0 = not included, 1 = one bag included, 2 = two bags
	if len(entry) > 4 {
		parseBagAllowance(entry[4], &fr)
	}

	// Parse CO2 emissions from entry[0][12] (grams CO2, when present).
	if len(flightInfo) > 12 {
		if v := jsonutil.ToInt(flightInfo[12]); v > 0 {
			fr.Emissions = v
		}
	}

	// Compute layover durations between consecutive legs.
	computeLayovers(fr.Legs)

	return fr, nil
}

// parseLegs extracts flight legs from the legs array.
func parseLegs(raw any) []models.FlightLeg {
	legsArr, ok := raw.([]any)
	if !ok {
		return nil
	}

	var legs []models.FlightLeg
	for _, rawLeg := range legsArr {
		leg, ok := rawLeg.([]any)
		if !ok {
			continue
		}

		fl := parseOneLeg(leg)
		legs = append(legs, fl)
	}

	return legs
}

// parseOneLeg parses a single leg from the nested array.
//
// Real Google leg structure (33 elements observed):
//
//	[0]  = null
//	[1]  = null
//	[2]  = operating airline name (may be null)
//	[3]  = departure airport code
//	[4]  = departure airport name
//	[5]  = arrival airport name
//	[6]  = arrival airport code
//	[7]  = null
//	[8]  = departure time [hour, minute]
//	[9]  = null (or 1 for +1 day offset)
//	[10] = arrival time [hour, minute]
//	[11] = duration in minutes
//	[17] = aircraft type
//	[20] = departure date [year, month, day]
//	[21] = arrival date [year, month, day]
//	[22] = airline info [code, flight_number, null, airline_name]
func parseOneLeg(leg []any) models.FlightLeg {
	var fl models.FlightLeg

	if len(leg) > 3 {
		fl.DepartureAirport.Code = toString(leg[3])
	}
	if len(leg) > 4 {
		fl.DepartureAirport.Name = toString(leg[4])
	}
	if len(leg) > 6 {
		fl.ArrivalAirport.Code = toString(leg[6])
	}
	if len(leg) > 5 {
		fl.ArrivalAirport.Name = toString(leg[5])
	}

	// Departure time: combine date [20] and time [8]
	if len(leg) > 20 {
		fl.DepartureTime = formatDateTime(leg[20], leg[8])
	}
	// Arrival time: combine date [21] and time [10]
	if len(leg) > 21 && len(leg) > 10 {
		fl.ArrivalTime = formatDateTime(leg[21], leg[10])
	}

	if len(leg) > 11 {
		fl.Duration = jsonutil.ToInt(leg[11])
	}

	// Aircraft type at leg[17]: string e.g. "Airbus A350", "Boeing 787"
	if len(leg) > 17 {
		fl.Aircraft = toString(leg[17])
	}

	// Airline info at leg[22]: [code, flight_number, null, airline_name]
	if len(leg) > 22 {
		if info, ok := leg[22].([]any); ok && len(info) >= 2 {
			fl.AirlineCode = toString(info[0])
			fl.FlightNumber = toString(info[0]) + " " + toString(info[1])
			if len(info) > 3 {
				fl.Airline = toString(info[3])
			}
		}
	}

	return fl
}

// parsePrice extracts price and currency from the price array.
//
// Google's live format: entry[1] = [ [null, price_amount], booking_token_string ]
// The price is the last numeric element in entry[1][0].
// The currency is encoded as protobuf inside the booking token string at entry[1][1].
func parsePrice(raw any) (float64, string) {
	arr, ok := raw.([]any)
	if !ok {
		return 0, ""
	}

	var amount float64
	var currency string

	// Try entry[1][0] — sub-array containing the price.
	// Price is the last element: e.g. [null, 2581] -> 2581
	if len(arr) > 0 {
		if priceArr, ok := arr[0].([]any); ok && len(priceArr) > 0 {
			// Walk backwards to find the first numeric value (price)
			for i := len(priceArr) - 1; i >= 0; i-- {
				if f, ok := jsonutil.ToFloat(priceArr[i]); ok && f > 0 {
					amount = f
					break
				}
			}
		}
	}

	// Try to extract currency from the booking token at entry[1][1].
	// The token is a base64-encoded protobuf that contains the 3-letter currency code.
	if len(arr) > 1 {
		if token, ok := arr[1].(string); ok && len(token) > 10 {
			currency = extractCurrencyFromToken(token)
		}
	}

	// Fallback: scan for explicit currency code or nested price in the array
	if amount == 0 {
		for _, v := range arr {
			if f, ok := jsonutil.ToFloat(v); ok && f > 0 {
				amount = f
				break
			}
		}
	}
	if currency == "" && amount > 0 {
		// Look for 3-letter uppercase string in the array
		for _, v := range arr {
			if s, ok := v.(string); ok && len(s) == 3 && s >= "A" && s == strings.ToUpper(s) {
				currency = s
				break
			}
		}
	}

	// Round 22 found defaulting an undetected currency to "USD" here fabricates
	// an authoritative-looking label: it passes IsValidCurrencyFormat and is
	// then compared against the watch's real currency in
	// checkOneWithWebhookContext, so a transient parse miss (token decode
	// failure, unexpected array shape) reads as a genuine currency CHANGE and
	// wipes the watch's alert threshold and price history. Leaving currency
	// empty routes it through check.go's existing "provider gave no currency"
	// handling instead, which errors out the single check rather than
	// destructively resetting watch state. Found by GPT second-opinion
	// review, 2026-07-30 (round 22).

	return amount, currency
}

// extractCurrencyFromToken attempts to extract a 3-letter currency code from
// a base64-encoded protobuf booking token. The currency appears as a protobuf
// string field (tag with wire type 2) containing exactly 3 uppercase ASCII chars.
func extractCurrencyFromToken(token string) string {
	data, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		// Try URL-safe or raw variants
		data, err = base64.RawStdEncoding.DecodeString(token)
		if err != nil {
			data, err = base64.URLEncoding.DecodeString(token)
			if err != nil {
				return ""
			}
		}
	}

	// Scan for a 3-byte sequence that looks like a currency code.
	// In protobuf, a string field is: varint tag | varint length | bytes.
	// We look for length=3 followed by 3 uppercase ASCII letters.
	for i := 0; i < len(data)-3; i++ {
		if data[i] == 3 { // length prefix = 3
			c1, c2, c3 := data[i+1], data[i+2], data[i+3]
			if c1 >= 'A' && c1 <= 'Z' && c2 >= 'A' && c2 <= 'Z' && c3 >= 'A' && c3 <= 'Z' {
				code := string(data[i+1 : i+4])
				// Validate it looks like a real currency code (not just random uppercase)
				if isKnownCurrency(code) {
					return code
				}
			}
		}
	}

	return ""
}

// isKnownCurrency checks if a 3-letter code is a recognized currency.
// This is a small subset covering the most common currencies for travel searches.
func isKnownCurrency(code string) bool {
	switch code {
	case "USD", "EUR", "GBP", "JPY", "CNY", "KRW", "THB", "INR", "AUD", "CAD",
		"CHF", "SEK", "NOK", "DKK", "PLN", "CZK", "HUF", "RON", "BGN", "HRK",
		"TRY", "BRL", "MXN", "ARS", "CLP", "COP", "PEN", "ZAR", "EGP", "AED",
		"SAR", "QAR", "OMR", "BHD", "KWD", "ILS", "SGD", "HKD", "TWD", "MYR",
		"IDR", "PHP", "VND", "NZD", "RUB", "UAH", "ISK":
		return true
	}
	return false
}

// parseBagAllowance extracts carry-on and checked bag info from the offer array.
// The bag data is at offer[6] = [carry_on_flag, checked_bag_flag].
// carry_on_flag: 0 = included in price (any other value = fee required)
// checked_bag_flag: 0 = not included, 1 = one bag included, 2 = two bags included
func parseBagAllowance(offer any, fr *models.FlightResult) {
	offerArr, ok := offer.([]any)
	if !ok || len(offerArr) <= 6 {
		return
	}

	bagArr, ok := offerArr[6].([]any)
	if !ok || len(bagArr) < 2 {
		return
	}

	// carry_on_flag: 0 means included
	if carryOn, ok := jsonutil.ToFloat(bagArr[0]); ok {
		included := carryOn == 0
		fr.CarryOnIncluded = &included
	}

	// checked_bag_flag: 0=none, 1=one bag, 2=two bags
	if checked, ok := jsonutil.ToFloat(bagArr[1]); ok {
		n := int(checked)
		fr.CheckedBagsIncluded = &n
	}
}

// appendPadded2 writes a zero-padded 2-digit int into buf at offset i.
func appendPadded2(buf []byte, i, n int) {
	buf[i] = byte('0' + n/10)
	buf[i+1] = byte('0' + n%10)
}

// appendPadded4 writes a zero-padded 4-digit int into buf at offset i.
func appendPadded4(buf []byte, i, n int) {
	buf[i] = byte('0' + n/1000)
	buf[i+1] = byte('0' + (n/100)%10)
	buf[i+2] = byte('0' + (n/10)%10)
	buf[i+3] = byte('0' + n%10)
}

// fmtDate formats year/month/day as "YYYY-MM-DD" with no allocations.
func fmtDate(year, month, day int) string {
	var buf [10]byte
	buf[4] = '-'
	buf[7] = '-'
	appendPadded4(buf[:], 0, year)
	appendPadded2(buf[:], 5, month)
	appendPadded2(buf[:], 8, day)
	return string(buf[:])
}

// fmtDateTime formats year/month/day/hour/minute as "YYYY-MM-DDTHH:MM" with no allocations.
func fmtDateTime(year, month, day, hour, minute int) string {
	var buf [16]byte
	buf[4] = '-'
	buf[7] = '-'
	buf[10] = 'T'
	buf[13] = ':'
	appendPadded4(buf[:], 0, year)
	appendPadded2(buf[:], 5, month)
	appendPadded2(buf[:], 8, day)
	appendPadded2(buf[:], 11, hour)
	appendPadded2(buf[:], 14, minute)
	return string(buf[:])
}

// formatDateTime combines a date array [year, month, day] and a time array
// [hour, minute] into an ISO 8601 string "YYYY-MM-DDTHH:MM".
//
// Google sometimes omits the minute when it is zero, producing a single-element
// array [hour] instead of [hour, 0]. We treat a 1-element time array as
// [hour, 0] to avoid losing the time portion.
func formatDateTime(dateRaw, timeRaw any) string {
	dateArr, ok := dateRaw.([]any)
	if !ok || len(dateArr) < 3 {
		return ""
	}

	year := jsonutil.ToInt(dateArr[0])
	month := jsonutil.ToInt(dateArr[1])
	day := jsonutil.ToInt(dateArr[2])
	if year == 0 {
		return ""
	}

	timeArr, ok := timeRaw.([]any)
	if !ok || len(timeArr) < 1 {
		// Date only, no time information at all.
		return fmtDate(year, month, day)
	}

	hour := jsonutil.ToInt(timeArr[0])
	minute := 0
	if len(timeArr) >= 2 {
		minute = jsonutil.ToInt(timeArr[1])
	}

	return fmtDateTime(year, month, day, hour, minute)
}

// formatTime converts a time array [year, month, day, hour, minute] to
// an ISO 8601 string "YYYY-MM-DDTHH:MM". This handles the legacy 5-element
// format used in test fixtures.
func formatTime(raw any) string {
	arr, ok := raw.([]any)
	if !ok || len(arr) < 5 {
		return ""
	}

	year := jsonutil.ToInt(arr[0])
	month := jsonutil.ToInt(arr[1])
	day := jsonutil.ToInt(arr[2])
	hour := jsonutil.ToInt(arr[3])
	minute := jsonutil.ToInt(arr[4])

	if year == 0 {
		return ""
	}

	return fmtDateTime(year, month, day, hour, minute)
}

// computeLayovers fills LayoverMinutes for each leg after the first.
// It computes the gap between the arrival time of leg N-1 and the departure
// time of leg N. Both times must be parseable ISO 8601 strings
// ("YYYY-MM-DDTHH:MM"). If either time is missing or unparseable the layover
// is left at 0.
func computeLayovers(legs []models.FlightLeg) {
	const layout = "2006-01-02T15:04"
	for i := 1; i < len(legs); i++ {
		prev := legs[i-1]
		curr := &legs[i]

		if prev.ArrivalTime == "" || curr.DepartureTime == "" {
			continue
		}
		arr, err1 := time.Parse(layout, prev.ArrivalTime)
		dep, err2 := time.Parse(layout, curr.DepartureTime)
		if err1 != nil || err2 != nil {
			continue
		}
		diff := dep.Sub(arr)
		if diff > 0 {
			curr.LayoverMinutes = int(diff.Minutes())
		}
	}
}

// toString safely converts a JSON value to string.
func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	// json.Number or float64 representation
	if f, ok := v.(float64); ok {
		if f == math.Trunc(f) {
			return fmt.Sprintf("%d", int64(f))
		}
		return fmt.Sprintf("%g", f)
	}
	return fmt.Sprintf("%v", v)
}
