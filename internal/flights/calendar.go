package flights

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// CalendarOptions configures a calendar graph or grid search.
type CalendarOptions struct {
	FromDate   string // Start of date range (YYYY-MM-DD)
	ToDate     string // End of date range (YYYY-MM-DD)
	TripLength int    // Trip length in days for round-trip (0 = one-way)
	RoundTrip  bool   // Whether to search round-trip
	Adults     int    // Number of adults (default: 1)
}

// defaults fills in zero-value fields.
func (o *CalendarOptions) defaults() {
	if o.Adults <= 0 {
		o.Adults = 1
	}
	if o.FromDate == "" {
		o.FromDate = time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	}
	if o.ToDate == "" {
		from, err := models.ParseDate(o.FromDate)
		if err == nil {
			o.ToDate = from.AddDate(0, 0, 30).Format("2006-01-02")
		}
	}
	if o.RoundTrip && o.TripLength <= 0 {
		o.TripLength = 7
	}
}

// SearchCalendar searches for cheapest flight prices across a date range using
// Google's GetCalendarGraph endpoint. This is a single-request alternative to
// the N-call approach in dates.go.
//
// It first resolves IATA codes to Google city codes (required by the endpoint),
// then calls GetCalendarGraph with those codes.
//
// Falls back to the legacy SearchDates approach if the CalendarGraph call fails.
func SearchCalendar(ctx context.Context, origin, dest string, opts CalendarOptions) (*models.DateSearchResult, error) {
	opts.defaults()

	if origin == "" || dest == "" {
		return nil, fmt.Errorf("origin and destination are required")
	}

	client := DefaultClient()

	// Resolve IATA codes to Google city codes.
	srcCode, err := batchexec.ResolveCityCode(ctx, client, origin)
	if err != nil {
		// Fall back to legacy approach on resolution failure.
		return searchCalendarFallback(ctx, origin, dest, opts)
	}

	dstCode, err := batchexec.ResolveCityCode(ctx, client, dest)
	if err != nil {
		return searchCalendarFallback(ctx, origin, dest, opts)
	}

	// Build and send the CalendarGraph request.
	encoded := encodeCalendarGraphPayload(srcCode, origin, dstCode, dest, opts)

	status, body, err := client.PostCalendarGraph(ctx, encoded)
	if err != nil {
		return searchCalendarFallback(ctx, origin, dest, opts)
	}

	if status == 403 {
		return nil, batchexec.ErrBlocked
	}

	if status != 200 {
		return searchCalendarFallback(ctx, origin, dest, opts)
	}

	// Try to parse the CalendarGraph response.
	dates, err := parseCalendarGraphResponse(body)
	if err != nil || len(dates) == 0 {
		// Small response or [3] error -- fall back to legacy.
		return searchCalendarFallback(ctx, origin, dest, opts)
	}

	// Sort by date.
	sort.Slice(dates, func(i, j int) bool {
		return dates[i].Date < dates[j].Date
	})

	tripType := "one_way"
	if opts.RoundTrip {
		tripType = "round_trip"
	}

	// CalendarGraph returns prices in the IP's local currency without a label.
	// Detect the actual currency by doing a quick flight search on the first date.
	// Currency conversion, if needed, happens in the CLI display layer.
	if len(dates) > 0 && dates[0].Currency == "" {
		sourceCurrency := detectSourceCurrencyWithClient(ctx, client, origin, dest, dates[0].Date)
		if sourceCurrency != "" {
			for i := range dates {
				dates[i].Currency = sourceCurrency
			}
		}
	}

	return &models.DateSearchResult{
		Success:   true,
		Count:     len(dates),
		TripType:  tripType,
		DateRange: fmt.Sprintf("%s to %s", opts.FromDate, opts.ToDate),
		Dates:     dates,
	}, nil
}

// searchCalendarFallback falls back to the legacy N-call date search.
func searchCalendarFallback(ctx context.Context, origin, dest string, opts CalendarOptions) (*models.DateSearchResult, error) {
	legacyOpts := DateSearchOptions{
		FromDate:  opts.FromDate,
		ToDate:    opts.ToDate,
		Duration:  opts.TripLength,
		RoundTrip: opts.RoundTrip,
		Adults:    opts.Adults,
	}
	return SearchDates(ctx, origin, dest, legacyOpts)
}

// encodeCalendarGraphPayload builds the f.req body for GetCalendarGraph
// using resolved Google city codes.
//
// The payload format matches gflights' getCalendarRawData + getPriceGraphReqData:
//   - City codes use flag 5 (city), airport codes use flag 0 (airport)
//   - Segments are wrapped in a JSON array at position 13 of the settings
//   - The outer envelope is [null, "[null, <rawData>], ..., [dateRange], ...]"]
func encodeCalendarGraphPayload(srcCityCode, srcAirport, dstCityCode, dstAirport string, opts CalendarOptions) string {
	tripType := 2 // one-way
	if opts.RoundTrip && opts.TripLength > 0 {
		tripType = 1 // round-trip
	}

	// Build source/dest locations: airport code with flag 0, city code with flag 5.
	// gflights serialises airports first, then cities.
	serSrc := fmt.Sprintf(`[\"%s\",0],[\"%s\",5]`, srcAirport, srcCityCode)
	serDst := fmt.Sprintf(`[\"%s\",0],[\"%s\",5]`, dstAirport, dstCityCode)

	// Build the raw data matching gflights' getCalendarRawData.
	// The rawData opens the settings array and segments array but does NOT close
	// them -- the suffix provides the closing brackets along with additional
	// settings elements (null,null,null,1) and the date range wrapper.
	rawData := fmt.Sprintf(`[null,null,%d,null,[],%d,[%d,0,0,0],null,null,null,null,null,null,[`,
		tripType, 1, opts.Adults) // class=1 economy

	// Outbound segment
	rawData += fmt.Sprintf(`[[[%s]],[[%s]],null,0,null,null,\"%s\",null,null,null,null,null,null,null,3]`,
		serSrc, serDst, opts.FromDate)

	// Return segment (for round-trip)
	if opts.RoundTrip && opts.TripLength > 0 {
		rawData += fmt.Sprintf(`,[[[%s]],[[%s]],null,0,null,null,\"%s\",null,null,null,null,null,null,null,1]`,
			serDst, serSrc, opts.ToDate)
	}

	// NOTE: rawData is intentionally left unclosed. The suffix closes:
	//   ] -> segments array
	//   ,null,null,null,1] -> additional settings elements + close settings array
	//   ,["fromDate","toDate"]] -> date range + close outer array

	prefix := `[null,"[null,`

	var suffix string
	if opts.RoundTrip && opts.TripLength > 0 {
		suffix = fmt.Sprintf(`],null,null,null,1,null,null,null,null,null,[]],[\"%s\",\"%s\"],null,[%d,%d]]"]`,
			opts.FromDate, opts.ToDate, opts.TripLength, opts.TripLength)
	} else {
		suffix = fmt.Sprintf(`],null,null,null,1],[\"%s\",\"%s\"]]"]`,
			opts.FromDate, opts.ToDate)
	}

	return url.QueryEscape(prefix + rawData + suffix)
}

// parseCalendarGraphResponse parses the response from GetCalendarGraph.
//
// The response uses length-prefixed format. Each section contains price data
// in the format: [null, [[startDate, returnDate, [[null, price], ""], 1], ...]]
func parseCalendarGraphResponse(body []byte) ([]models.DatePriceResult, error) {
	stripped := batchexec.StripAntiXSSI(body)
	if len(stripped) == 0 {
		return nil, batchexec.ErrEmptyResponse
	}

	// Check for [3] error response (small body, indicates bad query format).
	if len(stripped) < 200 {
		return nil, fmt.Errorf("response too small (%d bytes), likely error code", len(stripped))
	}

	var dates []models.DatePriceResult

	// Parse length-prefixed response lines.
	text := string(stripped)
	lines := strings.Split(text, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[") {
			continue
		}

		// Try to extract inner JSON from the batch response line.
		// Format: [["wrb.fr",null,"<inner-json>",...]]
		var outer [][]any
		if err := json.Unmarshal([]byte(line), &outer); err != nil {
			continue
		}

		for _, entry := range outer {
			if len(entry) < 3 {
				continue
			}
			innerStr, ok := entry[2].(string)
			if !ok || len(innerStr) < 10 {
				continue
			}

			parsed := parseCalendarPriceData([]byte(innerStr))
			dates = append(dates, parsed...)
		}
	}

	// Also try direct JSON decode of the whole body.
	entries, err := batchexec.DecodeBatchResponse(body)
	if err == nil {
		for _, entry := range entries {
			entryArr, ok := entry.([]any)
			if !ok {
				continue
			}
			for _, elem := range entryArr {
				s, ok := elem.(string)
				if !ok || len(s) < 10 {
					continue
				}
				parsed := parseCalendarPriceData([]byte(s))
				dates = append(dates, parsed...)
			}
		}
	}

	// Deduplicate by date.
	seen := make(map[string]bool)
	var unique []models.DatePriceResult
	for _, d := range dates {
		if !seen[d.Date] {
			seen[d.Date] = true
			unique = append(unique, d)
		}
	}

	return unique, nil
}

// parseCalendarPriceData extracts price entries from a decoded calendar section.
//
// Expected format: [null, [[date, returnDate, [[null, price], ""], 1], ...]]
func parseCalendarPriceData(data []byte) []models.DatePriceResult {
	var result []models.DatePriceResult

	// Try format: [null, [offer1, offer2, ...]]
	var section []json.RawMessage
	if err := json.Unmarshal(data, &[]any{nil, &section}); err == nil {
		for _, raw := range section {
			if dp := parseCalendarOffer(raw); dp != nil {
				result = append(result, *dp)
			}
		}
		return result
	}

	// Try alternate formats by scanning for date-like patterns.
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return nil
	}

	scanForPrices(generic, &result)
	return result
}

// parseCalendarOffer parses a single calendar offer entry.
// Format: [startDate, returnDate, [[null, price], ""], 1]
func parseCalendarOffer(raw json.RawMessage) *models.DatePriceResult {
	var startDate, returnDate string
	var price float64

	// [startDate, returnDate, [[null, price], ...], ...]
	err := json.Unmarshal(raw, &[]any{&startDate, &returnDate, &[]any{&[]any{nil, &price}}})
	if err != nil || price <= 0 {
		return nil
	}

	// Validate date format.
	if _, err := models.ParseDate(startDate); err != nil {
		return nil
	}

	dp := &models.DatePriceResult{
		Date:     startDate,
		Price:    price,
		Currency: "", // Unknown — CalendarGraph returns local currency based on IP.
	}
	if returnDate != "" {
		if _, err := models.ParseDate(returnDate); err == nil {
			dp.ReturnDate = returnDate
		}
	}

	return dp
}

// scanForPrices recursively scans a parsed JSON structure for date+price patterns.
func scanForPrices(v any, results *[]models.DatePriceResult) {
	switch val := v.(type) {
	case []any:
		// Check if this array looks like [date, returnDate, [[null, price], ...], ...]
		if len(val) >= 3 {
			if dateStr, ok := val[0].(string); ok {
				if _, err := models.ParseDate(dateStr); err == nil {
					if priceArr, ok := val[2].([]any); ok && len(priceArr) > 0 {
						if innerArr, ok := priceArr[0].([]any); ok && len(innerArr) > 1 {
							if price, ok := innerArr[1].(float64); ok && price > 0 {
								dp := models.DatePriceResult{
									Date:     dateStr,
									Price:    price,
									Currency: "", // Filled later via detectSourceCurrency
								}
								if retDate, ok := val[1].(string); ok {
									if _, err := models.ParseDate(retDate); err == nil {
										dp.ReturnDate = retDate
									}
								}
								*results = append(*results, dp)
								return
							}
						}
					}
				}
			}
		}
		// Recurse into array elements.
		for _, elem := range val {
			scanForPrices(elem, results)
		}
	case map[string]any:
		for _, elem := range val {
			scanForPrices(elem, results)
		}
	}
}

// sourceCurrencyCache caches the detected API currency per session.
// The currency depends on IP location, not route, so one detection is enough.
var sourceCurrencyCache struct {
	sync.RWMutex
	currency string
}

// DetectSourceCurrency is the exported variant of detectSourceCurrency.
// It caches the result since the API currency depends on IP, not route.
func DetectSourceCurrency(ctx context.Context, origin, dest string) string {
	return DetectSourceCurrencyWithClient(ctx, DefaultClient(), origin, dest)
}

// DetectSourceCurrencyWithClient is like DetectSourceCurrency but reuses the
// provided client for the probing search.
func DetectSourceCurrencyWithClient(ctx context.Context, client *batchexec.Client, origin, dest string) string {
	sourceCurrencyCache.RLock()
	if c := sourceCurrencyCache.currency; c != "" {
		sourceCurrencyCache.RUnlock()
		return c
	}
	sourceCurrencyCache.RUnlock()

	date := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	detected := detectSourceCurrencyWithClient(ctx, client, origin, dest, date)

	sourceCurrencyCache.Lock()
	sourceCurrencyCache.currency = detected
	sourceCurrencyCache.Unlock()

	return detected
}

// detectSourceCurrencyWithClient does a quick flight search to discover the raw currency
// that the Google API returns for this IP location. It reads the currency
// directly from the raw parsed data, BEFORE any conversion.
func detectSourceCurrencyWithClient(ctx context.Context, client *batchexec.Client, origin, dest, date string) string {
	quickCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	opts := SearchOptions{}
	opts.defaults()

	// Round 22 found defaulting every failure path here to "EUR" fabricates a
	// currency label for the ENTIRE date range (backfilled onto every
	// DatePriceResult below), which then reads as a genuine currency change
	// in checkOneWithWebhookContext and wipes alert thresholds/history on a
	// simple transient probe failure (timeout, non-200, decode error) with no
	// relation to the actual market currency. Return "" (genuinely unknown)
	// instead; callers must not backfill an empty detection onto real data.
	// Found by GPT second-opinion review, 2026-07-30 (round 22).
	filters := buildFilters(origin, dest, date, opts)
	encoded, err := batchexec.EncodeFlightFilters(filters)
	if err != nil {
		return ""
	}

	status, body, err := client.SearchFlights(quickCtx, encoded)
	if err != nil || status != 200 {
		return ""
	}

	inner, err := batchexec.DecodeFlightResponse(body)
	if err != nil {
		return ""
	}

	rawFlights, err := batchexec.ExtractFlightData(inner)
	if err != nil {
		return ""
	}

	// Parse one flight to get the raw currency (before conversion).
	flights := parseFlights(rawFlights)
	if len(flights) > 0 && flights[0].Currency != "" {
		return flights[0].Currency
	}
	return ""
}
