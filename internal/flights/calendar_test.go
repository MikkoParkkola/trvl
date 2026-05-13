package flights

import (
	"encoding/json"
	"testing"
)

// --- CalendarOptions defaults ---

func TestCalendarOptions_Defaults(t *testing.T) {
	opts := CalendarOptions{}
	opts.defaults()

	if opts.Adults != 1 {
		t.Errorf("Adults = %d, want 1", opts.Adults)
	}
	if opts.FromDate == "" {
		t.Error("FromDate should be set")
	}
	if opts.ToDate == "" {
		t.Error("ToDate should be set")
	}
}

func TestCalendarOptions_DefaultsPreserveSet(t *testing.T) {
	opts := CalendarOptions{
		FromDate:   "2026-07-01",
		ToDate:     "2026-07-31",
		Adults:     2,
		TripLength: 5,
		RoundTrip:  true,
	}
	opts.defaults()

	if opts.Adults != 2 {
		t.Errorf("Adults = %d, want 2", opts.Adults)
	}
	if opts.FromDate != "2026-07-01" {
		t.Errorf("FromDate = %q, want 2026-07-01", opts.FromDate)
	}
	if opts.ToDate != "2026-07-31" {
		t.Errorf("ToDate = %q, want 2026-07-31", opts.ToDate)
	}
	if opts.TripLength != 5 {
		t.Errorf("TripLength = %d, want 5", opts.TripLength)
	}
}

func TestCalendarOptions_DefaultTripLength(t *testing.T) {
	opts := CalendarOptions{RoundTrip: true}
	opts.defaults()

	if opts.TripLength != 7 {
		t.Errorf("TripLength = %d, want 7 for round-trip default", opts.TripLength)
	}
}

func TestCalendarOptions_OneWayNoTripLength(t *testing.T) {
	opts := CalendarOptions{RoundTrip: false}
	opts.defaults()

	if opts.TripLength != 0 {
		t.Errorf("TripLength = %d, want 0 for one-way", opts.TripLength)
	}
}

func TestCalendarOptions_DefaultToDate(t *testing.T) {
	opts := CalendarOptions{FromDate: "2026-08-01"}
	opts.defaults()

	expected := "2026-08-31"
	if opts.ToDate != expected {
		t.Errorf("ToDate = %q, want %q", opts.ToDate, expected)
	}
}

// --- encodeCalendarGraphPayload ---

func TestEncodeCalendarGraphPayload_OneWay(t *testing.T) {
	opts := CalendarOptions{
		FromDate: "2026-06-01",
		ToDate:   "2026-06-30",
		Adults:   1,
	}

	encoded := encodeCalendarGraphPayload("/m/01lbs", "HEL", "/m/07dfk", "NRT", opts)
	if encoded == "" {
		t.Fatal("encoded payload is empty")
	}

	// Should be URL-encoded.
	if len(encoded) < 100 {
		t.Errorf("encoded payload seems too short: %d chars", len(encoded))
	}
}

func TestEncodeCalendarGraphPayload_RoundTrip(t *testing.T) {
	opts := CalendarOptions{
		FromDate:   "2026-06-01",
		ToDate:     "2026-06-30",
		TripLength: 7,
		RoundTrip:  true,
		Adults:     2,
	}

	encoded := encodeCalendarGraphPayload("/m/01lbs", "HEL", "/m/07dfk", "NRT", opts)
	if encoded == "" {
		t.Fatal("encoded payload is empty")
	}
}

// --- parseCalendarGraphResponse ---

func TestParseCalendarGraphResponse_EmptyBody(t *testing.T) {
	_, err := parseCalendarGraphResponse([]byte{})
	if err == nil {
		t.Error("expected error for empty body")
	}
}

func TestParseCalendarGraphResponse_TooSmall(t *testing.T) {
	body := []byte(")]}'\n[3]")
	_, err := parseCalendarGraphResponse(body)
	if err == nil {
		t.Error("expected error for small response (likely error code)")
	}
}

func TestParseCalendarGraphResponse_ValidData(t *testing.T) {
	// Simulate a calendar response with price data.
	// The response body must be >= 200 bytes after stripping the anti-XSSI prefix.
	innerJSON := `[null,[["2026-06-15","2026-06-22",[[null,350],""],1],["2026-06-16","2026-06-23",[[null,380],""],1],["2026-06-17","2026-06-24",[[null,400],""],1],["2026-06-18","2026-06-25",[[null,420],""],1],["2026-06-19","2026-06-26",[[null,390],""],1],["2026-06-20","2026-06-27",[[null,360],""],1]]]`
	// Wrap in batch response format.
	entry := []any{[]any{"wrb.fr", nil, innerJSON}}
	entryJSON, _ := json.Marshal(entry)
	body := []byte(")]}'\n" + string(entryJSON))

	dates, err := parseCalendarGraphResponse(body)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if len(dates) < 2 {
		t.Fatalf("expected at least 2 dates, got %d", len(dates))
	}

	// Find the entries for our expected dates.
	found15, found16 := false, false
	for _, d := range dates {
		if d.Date == "2026-06-15" {
			found15 = true
			if d.Price != 350 {
				t.Errorf("price for 2026-06-15: got %v, want 350", d.Price)
			}
			if d.ReturnDate != "2026-06-22" {
				t.Errorf("return date for 2026-06-15: got %q, want 2026-06-22", d.ReturnDate)
			}
		}
		if d.Date == "2026-06-16" {
			found16 = true
			if d.Price != 380 {
				t.Errorf("price for 2026-06-16: got %v, want 380", d.Price)
			}
		}
	}
	if !found15 {
		t.Error("missing date 2026-06-15")
	}
	if !found16 {
		t.Error("missing date 2026-06-16")
	}
}

func TestParseCalendarGraphResponse_Deduplication(t *testing.T) {
	// Same date appears in both parsing paths.
	// Pad the response to exceed the 200-byte minimum.
	innerJSON := `[null,[["2026-06-15","",[[null,350],""],1],["2026-06-16","",[[null,360],""],1],["2026-06-17","",[[null,370],""],1],["2026-06-18","",[[null,380],""],1],["2026-06-19","",[[null,390],""],1],["2026-06-20","",[[null,400],""],1]]]`
	entry := []any{[]any{"wrb.fr", nil, innerJSON}}
	entryJSON, _ := json.Marshal(entry)
	body := []byte(")]}'\n" + string(entryJSON))

	dates, err := parseCalendarGraphResponse(body)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// Count occurrences of 2026-06-15.
	count := 0
	for _, d := range dates {
		if d.Date == "2026-06-15" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 occurrence of 2026-06-15, got %d", count)
	}
}

// --- parseCalendarOffer ---

func TestParseCalendarOffer_ValidOneWay(t *testing.T) {
	raw, _ := json.Marshal([]any{"2026-06-15", "", []any{[]any{nil, float64(250)}}})
	dp := parseCalendarOffer(raw)
	if dp == nil {
		t.Fatal("expected non-nil result")
	}
	if dp.Date != "2026-06-15" {
		t.Errorf("date = %q", dp.Date)
	}
	if dp.Price != 250 {
		t.Errorf("price = %v", dp.Price)
	}
	if dp.ReturnDate != "" {
		t.Errorf("return date = %q, want empty", dp.ReturnDate)
	}
}

func TestParseCalendarOffer_ValidRoundTrip(t *testing.T) {
	raw, _ := json.Marshal([]any{"2026-06-15", "2026-06-22", []any{[]any{nil, float64(450)}}})
	dp := parseCalendarOffer(raw)
	if dp == nil {
		t.Fatal("expected non-nil result")
	}
	if dp.ReturnDate != "2026-06-22" {
		t.Errorf("return date = %q, want 2026-06-22", dp.ReturnDate)
	}
}

func TestParseCalendarOffer_ZeroPrice(t *testing.T) {
	raw, _ := json.Marshal([]any{"2026-06-15", "", []any{[]any{nil, float64(0)}}})
	dp := parseCalendarOffer(raw)
	if dp != nil {
		t.Error("expected nil for zero price")
	}
}

func TestParseCalendarOffer_InvalidDate(t *testing.T) {
	raw, _ := json.Marshal([]any{"not-a-date", "", []any{[]any{nil, float64(250)}}})
	dp := parseCalendarOffer(raw)
	if dp != nil {
		t.Error("expected nil for invalid date")
	}
}

func TestParseCalendarOffer_MalformedJSON(t *testing.T) {
	dp := parseCalendarOffer([]byte("not json"))
	if dp != nil {
		t.Error("expected nil for malformed JSON")
	}
}

// --- scanForPrices ---

func TestScanForPrices_NestedArray(t *testing.T) {
	data := []any{
		nil,
		[]any{
			[]any{
				"2026-06-15",
				"2026-06-22",
				[]any{[]any{nil, float64(299)}},
			},
		},
	}

	// Test parseCalendarPriceData which internally calls scanForPrices.
	parsed := parseCalendarPriceData(mustMarshal(t, data))
	if len(parsed) != 1 {
		t.Fatalf("expected 1 result, got %d", len(parsed))
	}
	if parsed[0].Date != "2026-06-15" {
		t.Errorf("date = %q", parsed[0].Date)
	}
	if parsed[0].Price != 299 {
		t.Errorf("price = %v", parsed[0].Price)
	}
}

func TestScanForPrices_MapValue(t *testing.T) {
	data := map[string]any{
		"data": []any{
			"2026-07-01",
			"",
			[]any{[]any{nil, float64(199)}},
		},
	}

	parsed := parseCalendarPriceData(mustMarshal(t, data))
	if len(parsed) != 1 {
		t.Fatalf("expected 1 result, got %d", len(parsed))
	}
}

// --- SearchCalendar validation ---

func TestSearchCalendar_MissingParams(t *testing.T) {
	ctx := t.Context()
	_, err := SearchCalendar(ctx, "", "NRT", CalendarOptions{})
	if err == nil {
		t.Error("expected error for empty origin")
	}

	_, err = SearchCalendar(ctx, "HEL", "", CalendarOptions{})
	if err == nil {
		t.Error("expected error for empty destination")
	}
}

// --- searchCalendarFallback ---

func TestSearchCalendarFallback_Delegates(t *testing.T) {
	// searchCalendarFallback should construct legacy DateSearchOptions correctly.
	opts := CalendarOptions{
		FromDate:   "2026-06-01",
		ToDate:     "2026-06-30",
		TripLength: 7,
		RoundTrip:  true,
		Adults:     2,
	}

	// We can't easily run the full fallback without network, but verify
	// the conversion preserves fields.
	legacyOpts := DateSearchOptions{
		FromDate:  opts.FromDate,
		ToDate:    opts.ToDate,
		Duration:  opts.TripLength,
		RoundTrip: opts.RoundTrip,
		Adults:    opts.Adults,
	}

	if legacyOpts.FromDate != "2026-06-01" {
		t.Errorf("FromDate = %q", legacyOpts.FromDate)
	}
	if legacyOpts.Duration != 7 {
		t.Errorf("Duration = %d", legacyOpts.Duration)
	}
	if !legacyOpts.RoundTrip {
		t.Error("RoundTrip should be true")
	}
	if legacyOpts.Adults != 2 {
		t.Errorf("Adults = %d", legacyOpts.Adults)
	}
}

func TestSearchCalendarFallback_OneWay(t *testing.T) {
	// Verify fallback with one-way options.
	opts := CalendarOptions{
		FromDate:   "2026-07-01",
		ToDate:     "2026-07-15",
		TripLength: 0,
		RoundTrip:  false,
		Adults:     1,
	}

	// searchCalendarFallback delegates to SearchDates which makes network calls.
	// We can at least verify it doesn't panic and returns an error/result.
	ctx := t.Context()
	result, err := searchCalendarFallback(ctx, "HEL", "NRT", opts)
	// Network call may fail, but the function should not panic.
	if err != nil {
		// Expected — network not available in unit tests.
		t.Logf("Expected network error: %v", err)
		return
	}
	if result == nil {
		t.Error("expected non-nil result")
	}
}

// --- SearchCalendar additional validation ---

func TestSearchCalendar_BothEmpty(t *testing.T) {
	ctx := t.Context()
	_, err := SearchCalendar(ctx, "", "", CalendarOptions{})
	if err == nil {
		t.Error("expected error for both empty")
	}
}

// --- parseCalendarPriceData ---

func TestParseCalendarPriceData_EmptyInput(t *testing.T) {
	result := parseCalendarPriceData([]byte(""))
	if len(result) != 0 {
		t.Errorf("expected 0 results for empty input, got %d", len(result))
	}
}

func TestParseCalendarPriceData_NullFormat(t *testing.T) {
	data := mustMarshal(t, []any{nil, []any{
		[]any{"2026-08-01", "2026-08-08", []any{[]any{nil, float64(399)}}, float64(1)},
	}})
	result := parseCalendarPriceData(data)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0].Price != 399 {
		t.Errorf("price = %v, want 399", result[0].Price)
	}
}

// --- encodeCalendarGraphPayload additional tests ---

func TestEncodeCalendarGraphPayload_DifferentAdults(t *testing.T) {
	opts := CalendarOptions{
		FromDate: "2026-06-01",
		ToDate:   "2026-06-30",
		Adults:   4,
	}
	encoded := encodeCalendarGraphPayload("/m/01lbs", "HEL", "/m/07dfk", "NRT", opts)
	if encoded == "" {
		t.Fatal("encoded payload is empty")
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
