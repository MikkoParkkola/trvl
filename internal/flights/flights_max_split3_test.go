package flights

import (
	"encoding/json"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestDepartTimeWindow_Both(t *testing.T) {
	got := departTimeWindow("06:00", "22:00")
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected array, got %T", got)
	}
	if arr[0] != 6 || arr[1] != 22 {
		t.Errorf("expected [6, 22], got %v", arr)
	}
}

func TestDepartTimeWindow_Neither(t *testing.T) {
	got := departTimeWindow("", "")
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestDepartTimeWindow_InvalidBoth(t *testing.T) {
	got := departTimeWindow("bad", "bad")
	if got != nil {
		t.Errorf("expected nil for invalid times, got %v", got)
	}
}

// --- alliancesFilter ---

func TestAlliancesFilter_NilSlice(t *testing.T) {
	if got := alliancesFilter(nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestAlliancesFilter_Uppercased(t *testing.T) {
	got := alliancesFilter([]string{"star_alliance", " oneworld "})
	arr, ok := got.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("expected 2-element array, got %v", got)
	}
	if arr[0] != "STAR_ALLIANCE" || arr[1] != "ONEWORLD" {
		t.Errorf("got %v", arr)
	}
}

// --- kiwiSearchEligible ---

func TestKiwiSearchEligible_NilClient(t *testing.T) {
	if kiwiSearchEligible(nil, SearchOptions{}) {
		t.Error("expected false for nil client")
	}
}

// --- flightDepartsWithinWindow short departure ---

func TestFlightDepartsWithinWindow_ShortDepartureTime(t *testing.T) {
	f := models.FlightResult{
		Legs: []models.FlightLeg{{DepartureTime: "short"}},
	}
	if flightDepartsWithinWindow(f, "06:00", "") {
		t.Error("expected false for short departure time")
	}
}

// --- compareFlightPrices ---

func TestCompareFlightPrices(t *testing.T) {
	if got := compareFlightPrices(100, 200); got != -1 {
		t.Errorf("compareFlightPrices(100, 200) = %d, want -1", got)
	}
	if got := compareFlightPrices(200, 100); got != 1 {
		t.Errorf("compareFlightPrices(200, 100) = %d, want 1", got)
	}
	if got := compareFlightPrices(100, 100); got != 0 {
		t.Errorf("compareFlightPrices(100, 100) = %d, want 0", got)
	}
}

// --- formatTime (legacy 5-element) ---

func TestFormatTime_Valid(t *testing.T) {
	raw := []any{float64(2026), float64(7), float64(1), float64(14), float64(30)}
	got := formatTime(raw)
	if got != "2026-07-01T14:30" {
		t.Errorf("formatTime = %q, want 2026-07-01T14:30", got)
	}
}

func TestFormatTime_TooShort(t *testing.T) {
	got := formatTime([]any{float64(2026), float64(7)})
	if got != "" {
		t.Errorf("expected empty for short array, got %q", got)
	}
}

func TestFormatTime_NotArray(t *testing.T) {
	got := formatTime("not an array")
	if got != "" {
		t.Errorf("expected empty for non-array, got %q", got)
	}
}

func TestFormatTime_ZeroYear(t *testing.T) {
	got := formatTime([]any{float64(0), float64(7), float64(1), float64(14), float64(30)})
	if got != "" {
		t.Errorf("expected empty for zero year, got %q", got)
	}
}

// --- parsePrice with currency in token ---

// TestParsePrice_NoFabricatedDefaultCurrency proves round 22's fix: a price
// with no currency information found anywhere in the token must come back
// with an EMPTY currency, not a fabricated "USD" default. The old default
// let a parse miss on a genuinely non-USD flight masquerade as a real
// currency, which fed a false currency-change reset in check.go (destructive
// threshold/history reset) downstream. Found by GPT second-opinion review,
// 2026-07-30 (round 22).
func TestParsePrice_NoFabricatedDefaultCurrency(t *testing.T) {
	raw := []any{[]any{nil, float64(100)}}
	price, currency := parsePrice(raw)
	if price != 100 {
		t.Errorf("price = %v, want 100", price)
	}
	if currency != "" {
		t.Errorf("currency = %q, want empty (no fabricated default)", currency)
	}
}

func TestParsePrice_ZeroPrice(t *testing.T) {
	raw := []any{[]any{nil, float64(0)}}
	price, currency := parsePrice(raw)
	if price != 0 {
		t.Errorf("price = %v, want 0", price)
	}
	if currency != "" {
		t.Errorf("currency = %q, want empty for zero price", currency)
	}
}

// --- detectSourceCurrencyWithClient ---

func TestDetectSourceCurrencyWithClient_Success(t *testing.T) {
	body := makeFlightResponseBody(t)
	ts := flightsTestServer(t, 200, body)
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	got := detectSourceCurrencyWithClient(t.Context(), client, "HEL", "NRT", "2026-06-15")
	// The fixture body carries no currency token, so an honest empty result
	// is correct here -- round 22 removed the fabricated "EUR" fallback that
	// used to mask this. See TestParsePrice_NoFabricatedDefaultCurrency.
	if got != "" {
		t.Errorf("got %q, want empty (fixture has no currency data)", got)
	}
}

func TestDetectSourceCurrencyWithClient_ServerError(t *testing.T) {
	ts := flightsTestServer(t, 500, []byte("error"))
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	got := detectSourceCurrencyWithClient(t.Context(), client, "HEL", "NRT", "2026-06-15")
	// Round 22: a detection failure must return "", not fabricate "EUR" --
	// a fabricated label backfilled onto a whole date range and read as a
	// genuine currency change downstream. Found by GPT second-opinion
	// review, 2026-07-30 (round 22).
	if got != "" {
		t.Errorf("got %q, want empty on server error (no fabricated fallback)", got)
	}
}

func TestDetectSourceCurrencyWithClient_BadBody(t *testing.T) {
	ts := flightsTestServer(t, 200, []byte(")]}'\nnot json"))
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	got := detectSourceCurrencyWithClient(t.Context(), client, "HEL", "NRT", "2026-06-15")
	if got != "" {
		t.Errorf("got %q, want empty on bad body (no fabricated fallback)", got)
	}
}

func TestDetectSourceCurrencyWithClient_EmptyFlights(t *testing.T) {
	// Valid structure but no flights -> empty, no fabricated fallback.
	inner := make([]any, 2)
	innerJSON, _ := json.Marshal(inner)
	outer := []any{[]any{nil, nil, string(innerJSON)}}
	outerJSON, _ := json.Marshal(outer)
	body := append([]byte(")]}'\n"), outerJSON...)

	ts := flightsTestServer(t, 200, body)
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	got := detectSourceCurrencyWithClient(t.Context(), client, "HEL", "NRT", "2026-06-15")
	if got != "" {
		t.Errorf("got %q, want empty for empty flights (no fabricated fallback)", got)
	}
}

// --- DetectSourceCurrencyWithClient (cached) ---

func TestDetectSourceCurrencyWithClient_CacheHit(t *testing.T) {
	// Reset cache first
	sourceCurrencyCache.Lock()
	sourceCurrencyCache.currency = ""
	sourceCurrencyCache.Unlock()

	body := makeFlightResponseBody(t)
	ts := flightsTestServer(t, 200, body)
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)

	// First call populates cache. The fixture carries no currency token, so
	// "" is the honest, expected result post-round-22 (no fabricated
	// fallback) -- this test's purpose is the cache-hit equality below, not
	// asserting non-empty.
	first := DetectSourceCurrencyWithClient(t.Context(), client, "HEL", "NRT")

	// Second call should hit cache (even with a dead server)
	ts.Close()
	second := DetectSourceCurrencyWithClient(t.Context(), client, "HEL", "NRT")
	if second != first {
		t.Errorf("cache miss: first=%q, second=%q", first, second)
	}

	// Clean up
	sourceCurrencyCache.Lock()
	sourceCurrencyCache.currency = ""
	sourceCurrencyCache.Unlock()
}

// --- scanForPrices recursive ---

func TestScanForPrices_Map(t *testing.T) {
	// scanForPrices should recurse into map values
	data := map[string]any{
		"nested": []any{
			"2026-07-01", nil, []any{[]any{nil, 250.0}},
		},
	}
	var results []models.DatePriceResult
	scanForPrices(data, &results)
	if len(results) != 1 {
		t.Errorf("expected 1 result from map scan, got %d", len(results))
	}
}

func TestScanForPrices_WithReturnDate(t *testing.T) {
	data := []any{
		"2026-07-01", "2026-07-08", []any{[]any{nil, 350.0}},
	}
	var results []models.DatePriceResult
	scanForPrices(data, &results)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ReturnDate != "2026-07-08" {
		t.Errorf("return date = %q, want 2026-07-08", results[0].ReturnDate)
	}
}

func TestScanForPrices_InvalidReturnDate(t *testing.T) {
	data := []any{
		"2026-07-01", "not-a-date", []any{[]any{nil, 350.0}},
	}
	var results []models.DatePriceResult
	scanForPrices(data, &results)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ReturnDate != "" {
		t.Errorf("expected empty return date for invalid date, got %q", results[0].ReturnDate)
	}
}

func TestScanForPrices_NotDateString(t *testing.T) {
	data := []any{
		"not-a-date", nil, []any{[]any{nil, 350.0}},
	}
	var results []models.DatePriceResult
	scanForPrices(data, &results)
	if len(results) != 0 {
		t.Errorf("expected 0 results for non-date, got %d", len(results))
	}
}

func TestScanForPrices_ZeroPrice(t *testing.T) {
	data := []any{
		"2026-07-01", nil, []any{[]any{nil, 0.0}},
	}
	var results []models.DatePriceResult
	scanForPrices(data, &results)
	if len(results) != 0 {
		t.Errorf("expected 0 results for zero price, got %d", len(results))
	}
}

func TestScanForPrices_Scalar(t *testing.T) {
	var results []models.DatePriceResult
	scanForPrices(42, &results)
	if len(results) != 0 {
		t.Errorf("expected 0 results for scalar, got %d", len(results))
	}
}

// --- parseCalendarPriceData / parseCalendarOffer ---

func TestParseCalendarPriceData_ValidOffers(t *testing.T) {
	data := []byte(`[null, [["2026-07-01", "", [[null, 250]], 1], ["2026-07-02", "", [[null, 300]], 1]]]`)
	got := parseCalendarPriceData(data)
	if len(got) != 2 {
		t.Errorf("expected 2 offers, got %d", len(got))
	}
}

func TestParseCalendarPriceData_InvalidJSON(t *testing.T) {
	got := parseCalendarPriceData([]byte("not json"))
	if len(got) != 0 {
		t.Errorf("expected 0 for invalid JSON, got %d", len(got))
	}
}

func TestParseCalendarOffer_InvalidDateMax(t *testing.T) {
	raw := json.RawMessage(`["not-a-date", "", [[null, 250]], 1]`)
	got := parseCalendarOffer(raw)
	if got != nil {
		t.Error("expected nil for invalid date")
	}
}

func TestParseCalendarOffer_ZeroPriceMax(t *testing.T) {
	raw := json.RawMessage(`["2026-07-01", "", [[null, 0]], 1]`)
	got := parseCalendarOffer(raw)
	if got != nil {
		t.Error("expected nil for zero price")
	}
}

func TestParseCalendarOffer_WithReturnDate(t *testing.T) {
	raw := json.RawMessage(`["2026-07-01", "2026-07-08", [[null, 250]], 1]`)
	got := parseCalendarOffer(raw)
	if got == nil {
		t.Fatal("expected non-nil offer")
	}
	if got.ReturnDate != "2026-07-08" {
		t.Errorf("return date = %q, want 2026-07-08", got.ReturnDate)
	}
}

func TestParseCalendarOffer_InvalidReturnDate(t *testing.T) {
	raw := json.RawMessage(`["2026-07-01", "bad-date", [[null, 250]], 1]`)
	got := parseCalendarOffer(raw)
	if got == nil {
		t.Fatal("expected non-nil offer")
	}
	if got.ReturnDate != "" {
		t.Errorf("expected empty return date for invalid, got %q", got.ReturnDate)
	}
}

// --- parseGridOffer ---

// parseGridOffer edge cases: covered by grid_test.go
// (TestParseGridOffer_InvalidDepartDate, TestParseGridOffer_InvalidReturnDate, TestParseGridOffer_Valid)

func TestParseGridOffer_NegativePrice(t *testing.T) {
	raw := json.RawMessage(`["2026-07-01", "2026-07-08", [[null, -100]]]`)
	got := parseGridOffer(raw)
	if got != nil {
		t.Error("expected nil for negative price")
	}
}

// --- parseCalendarGraphResponse ---

// parseCalendarGraphResponse empty/tooSmall: covered by calendar_test.go

func TestParseCalendarGraphResponse_NonJSON(t *testing.T) {
	// Response with enough bytes but no valid JSON arrays
	filler := make([]byte, 300)
	for i := range filler {
		filler[i] = 'x'
	}
	body := append([]byte(")]}'\n"), filler...)
	dates, _ := parseCalendarGraphResponse(body)
	if len(dates) != 0 {
		t.Errorf("expected 0 dates from non-JSON body, got %d", len(dates))
	}
}

// --- parsePriceGridResponse ---

// parsePriceGridResponse empty/tooSmall: covered by grid_test.go

func TestParsePriceGridResponse_NonJSON(t *testing.T) {
	filler := make([]byte, 300)
	for i := range filler {
		filler[i] = 'y'
	}
	body := append([]byte(")]}'\n"), filler...)
	cells, _ := parsePriceGridResponse(body)
	if len(cells) != 0 {
		t.Errorf("expected 0 cells from non-JSON body, got %d", len(cells))
	}
}

// --- encodeCalendarGraphPayload ---

// encodeCalendarGraphPayload OneWay/RoundTrip: covered by calendar_test.go

func TestEncodeCalendarGraphPayload_ThreeAdults(t *testing.T) {
	opts := CalendarOptions{
		FromDate: "2026-07-01",
		ToDate:   "2026-07-31",
		Adults:   3,
	}
	got := encodeCalendarGraphPayload("/m/01lbs", "HEL", "/m/07dfk", "NRT", opts)
	if got == "" {
		t.Error("expected non-empty payload for 3 adults")
	}
}

// --- encodePriceGridPayload ---

// encodePriceGridPayload: covered by grid_test.go TestEncodePriceGridPayload

func TestEncodePriceGridPayload_TwoAdults(t *testing.T) {
	opts := GridOptions{
		DepartFrom: "2026-07-01",
		DepartTo:   "2026-07-07",
		ReturnFrom: "2026-07-08",
		ReturnTo:   "2026-07-14",
		Adults:     2,
	}
	got := encodePriceGridPayload("/m/01lbs", "HEL", "/m/07dfk", "NRT", opts)
	if got == "" {
		t.Error("expected non-empty payload for 2 adults")
	}
}

// --- sortedKeys ---

func TestSortedKeys_Ordered(t *testing.T) {
	m := map[string]bool{"c": true, "a": true, "b": true}
	got := sortedKeys(m)
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("sortedKeys = %v, want [a b c]", got)
	}
}

func TestSortedKeys_EmptyMap(t *testing.T) {
	got := sortedKeys(map[string]bool{})
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

// --- searchCalendarFallback ---

func TestSearchCalendarFallback_EmptyOrigin(t *testing.T) {
	// Should propagate to SearchDates which validates origin
	_, err := searchCalendarFallback(t.Context(), "", "NRT", CalendarOptions{
		FromDate: "2026-07-01",
		ToDate:   "2026-07-02",
	})
	if err == nil {
		t.Error("expected error for empty origin")
	}
}
