package explore

import (
	"context"
	"os"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
)

// --- EncodeExplorePayload ---

func TestEncodeExplorePayload_OneWay(t *testing.T) {
	payload := EncodeExplorePayload("HEL", ExploreOptions{
		DepartureDate: "2026-06-15",
		Adults:        1,
	})

	if payload == "" {
		t.Fatal("payload is empty")
	}

	// Should contain the airport code, date, and trip type 2 (one-way).
	if !containsEncoded(payload, "HEL") {
		t.Error("payload should contain origin airport HEL")
	}
	if !containsEncoded(payload, "2026-06-15") {
		t.Error("payload should contain departure date")
	}
}

func TestEncodeExplorePayload_RoundTrip(t *testing.T) {
	payload := EncodeExplorePayload("JFK", ExploreOptions{
		DepartureDate: "2026-07-01",
		ReturnDate:    "2026-07-08",
		Adults:        2,
	})

	if payload == "" {
		t.Fatal("payload is empty")
	}

	// Round-trip should contain both dates and trip type 1.
	if !containsEncoded(payload, "JFK") {
		t.Error("payload should contain origin airport JFK")
	}
	if !containsEncoded(payload, "2026-07-01") {
		t.Error("payload should contain departure date")
	}
	if !containsEncoded(payload, "2026-07-08") {
		t.Error("payload should contain return date")
	}
}

func TestEncodeExplorePayload_WithCoordinates(t *testing.T) {
	payload := EncodeExplorePayload("HEL", ExploreOptions{
		DepartureDate: "2026-06-15",
		ReturnDate:    "2026-06-22",
		NorthLat:      45.0,
		SouthLat:      35.0,
		EastLng:       30.0,
		WestLng:       -10.0,
	})

	if payload == "" {
		t.Fatal("payload is empty")
	}

	// With coordinates, the payload should contain the bounding box values.
	if !containsEncoded(payload, "45.0") {
		t.Error("payload should contain north latitude")
	}
	if !containsEncoded(payload, "35.0") {
		t.Error("payload should contain south latitude")
	}
}

func TestEncodeExplorePayload_DefaultAdults(t *testing.T) {
	// Adults = 0 should default to 1.
	payload := EncodeExplorePayload("LHR", ExploreOptions{
		DepartureDate: "2026-06-15",
		Adults:        0,
	})

	if payload == "" {
		t.Fatal("payload is empty")
	}

	// Verify the payload is valid (not empty) with defaulted adults.
	if !containsEncoded(payload, "LHR") {
		t.Error("payload should contain airport code")
	}
}

func TestEncodeExplorePayload_NoCoordinatesUsesNull(t *testing.T) {
	payload := EncodeExplorePayload("CDG", ExploreOptions{
		DepartureDate: "2026-08-01",
	})

	if !containsEncoded(payload, "null") {
		t.Error("payload without coordinates should contain null for coords")
	}
}

// --- ParseExploreResponse ---

func TestParseExploreResponse_GoldenFile(t *testing.T) {
	data, err := os.ReadFile("testdata/explore_response.json")
	if err != nil {
		t.Fatalf("read golden file: %v", err)
	}

	destinations, err := ParseExploreResponse(data)
	if err != nil {
		t.Fatalf("ParseExploreResponse: %v", err)
	}

	if len(destinations) != 3 {
		t.Fatalf("expected 3 destinations, got %d", len(destinations))
	}

	// First destination: Lisbon.
	d0 := destinations[0]
	if d0.AirportCode != "LIS" {
		t.Errorf("dest 0 airport: got %q, want LIS", d0.AirportCode)
	}
	if d0.Price != 89 {
		t.Errorf("dest 0 price: got %v, want 89", d0.Price)
	}
	if d0.AirlineName != "Ryanair" {
		t.Errorf("dest 0 airline: got %q, want Ryanair", d0.AirlineName)
	}
	if d0.Stops != 0 {
		t.Errorf("dest 0 stops: got %d, want 0", d0.Stops)
	}
	if d0.CityID != "/m/04llb" {
		t.Errorf("dest 0 city ID: got %q, want /m/04llb", d0.CityID)
	}

	// Second destination: Barcelona.
	d1 := destinations[1]
	if d1.AirportCode != "BCN" {
		t.Errorf("dest 1 airport: got %q, want BCN", d1.AirportCode)
	}
	if d1.Price != 215 {
		t.Errorf("dest 1 price: got %v, want 215", d1.Price)
	}
	if d1.Stops != 1 {
		t.Errorf("dest 1 stops: got %d, want 1", d1.Stops)
	}

	// Third destination: Athens.
	d2 := destinations[2]
	if d2.AirportCode != "ATH" {
		t.Errorf("dest 2 airport: got %q, want ATH", d2.AirportCode)
	}
	if d2.Price != 150 {
		t.Errorf("dest 2 price: got %v, want 150", d2.Price)
	}
}

func TestParseExploreResponse_EmptyBody(t *testing.T) {
	_, err := ParseExploreResponse([]byte{})
	if err == nil {
		t.Error("expected error for empty body")
	}
}

func TestParseExploreResponse_MalformedJSON(t *testing.T) {
	_, err := ParseExploreResponse([]byte(")]}'\n{invalid json}"))
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestParseExploreResponse_NoDestinations(t *testing.T) {
	// Valid batch response structure but no destination data.
	body := []byte(`)]}'

10
[["wrb.fr",null,"[null,null,null]",null,null,null,"generic"]]
`)
	dests, err := ParseExploreResponse(body)
	// Either error or zero destinations is acceptable.
	if err == nil && len(dests) > 0 {
		t.Errorf("expected no destinations from empty inner data, got %d", len(dests))
	}
}

// --- SearchExplore ---

func TestSearchExplore_EmptyOrigin(t *testing.T) {
	client := batchexec.NewClient()
	ctx := context.Background()

	_, err := SearchExplore(ctx, client, "", ExploreOptions{
		DepartureDate: "2026-06-15",
	})
	if err == nil {
		t.Error("expected error for empty origin")
	}
	if err.Error() != "origin airport is required" {
		t.Errorf("unexpected error message: %v", err)
	}
}

// --- ExploreOptions ---

func TestExploreOptions_ZeroAdults(t *testing.T) {
	// EncodeExplorePayload should default adults=0 to adults=1.
	p1 := EncodeExplorePayload("HEL", ExploreOptions{
		DepartureDate: "2026-06-15",
		Adults:        0,
	})
	p2 := EncodeExplorePayload("HEL", ExploreOptions{
		DepartureDate: "2026-06-15",
		Adults:        1,
	})

	if p1 != p2 {
		t.Error("adults=0 should produce same payload as adults=1")
	}
}

func TestExploreOptions_NegativeAdults(t *testing.T) {
	// Negative adults should also default to 1.
	p1 := EncodeExplorePayload("HEL", ExploreOptions{
		DepartureDate: "2026-06-15",
		Adults:        -5,
	})
	p2 := EncodeExplorePayload("HEL", ExploreOptions{
		DepartureDate: "2026-06-15",
		Adults:        1,
	})

	if p1 != p2 {
		t.Error("negative adults should produce same payload as adults=1")
	}
}

// --- Internal parse helpers ---

func TestExtractInnerJSON_NotArray(t *testing.T) {
	_, err := extractInnerJSON("not an array")
	if err == nil {
		t.Error("expected error for non-array input")
	}
}

func TestExtractInnerJSON_NoJSONString(t *testing.T) {
	_, err := extractInnerJSON([]any{nil, float64(42), true})
	if err == nil {
		t.Error("expected error when no element contains JSON")
	}
}

func TestExtractInnerJSON_ShortStrings(t *testing.T) {
	_, err := extractInnerJSON([]any{"hi", "x", ""})
	if err == nil {
		t.Error("expected error for short strings")
	}
}

func TestExtractInnerJSON_Valid(t *testing.T) {
	// Inner JSON string must be >= 10 chars.
	result, err := extractInnerJSON([]any{"wrb.fr", nil, `[1,2,3,4,5,6]`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	arr, ok := result.([]any)
	if !ok {
		t.Fatal("expected array result")
	}
	if len(arr) != 6 {
		t.Errorf("expected 6 elements, got %d", len(arr))
	}
}

func TestParseExploreFromInner_NotArray(t *testing.T) {
	_, err := parseExploreFromInner("not an array")
	if err == nil {
		t.Error("expected error for non-array input")
	}
}

func TestParseExploreFromInner_Empty(t *testing.T) {
	_, err := parseExploreFromInner([]any{})
	if err == nil {
		t.Error("expected error for empty array")
	}
}

func TestTryParseFormat1_TooShort(t *testing.T) {
	result := tryParseFormat1([]any{"id", "price"})
	if result != nil {
		t.Error("expected nil for short destination")
	}
}

func TestTryParseFormat1_NoPriceNoAirport(t *testing.T) {
	dest := make([]any, 7)
	dest[0] = "/m/abc"
	result := tryParseFormat1(dest)
	if result != nil {
		t.Error("expected nil when price=0 and airport empty")
	}
}

func TestTryParseFormat1_Valid(t *testing.T) {
	dest := []any{
		"/m/04llb",
		[]any{[]any{nil, float64(89)}, "token"},
		nil, nil, nil, nil,
		[]any{"FR", "Ryanair", float64(0), float64(180), nil, "LIS"},
	}

	result := tryParseFormat1(dest)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.AirportCode != "LIS" {
		t.Errorf("airport = %q, want LIS", result.AirportCode)
	}
	if result.Price != 89 {
		t.Errorf("price = %v, want 89", result.Price)
	}
}

func TestTryParseFormat2_TooShort(t *testing.T) {
	result := tryParseFormat2(make([]any, 10))
	if result != nil {
		t.Error("expected nil for array < 15 elements")
	}
}

func TestTryParseFormat2_NoAirportCode(t *testing.T) {
	dest := make([]any, 15)
	dest[0] = "/m/abc"
	dest[2] = "TestCity"
	// dest[14] is nil - no airport code.
	result := tryParseFormat2(dest)
	if result != nil {
		t.Error("expected nil when no airport code at [14]")
	}
}

func TestTryParseFormat2_ReturnsNil(t *testing.T) {
	// Format 2 always returns nil because it lacks price data.
	dest := make([]any, 15)
	dest[0] = "/m/abc"
	dest[2] = "TestCity"
	dest[14] = "LIS"
	result := tryParseFormat2(dest)
	// tryParseFormat2 returns nil even with airport code because no price.
	if result != nil {
		t.Error("expected nil from format 2 (no price)")
	}
}

func TestParseDestinationArray_Empty(t *testing.T) {
	dests := parseDestinationArray(nil)
	if len(dests) != 0 {
		t.Errorf("expected 0 destinations from nil, got %d", len(dests))
	}
}

func TestParseDestinationArray_MixedItems(t *testing.T) {
	items := []any{
		"not an array",
		42,
		nil,
		// Valid destination.
		[]any{
			"/m/04llb",
			[]any{[]any{nil, float64(89)}, "token"},
			nil, nil, nil, nil,
			[]any{"FR", "Ryanair", float64(0), float64(180), nil, "LIS"},
		},
		// Too short.
		[]any{"only", "two"},
	}

	dests := parseDestinationArray(items)
	if len(dests) != 1 {
		t.Fatalf("expected 1 valid destination, got %d", len(dests))
	}
	if dests[0].AirportCode != "LIS" {
		t.Errorf("airport = %q, want LIS", dests[0].AirportCode)
	}
}

// containsEncoded checks if the URL-encoded payload contains the given string.
func containsEncoded(payload, substr string) bool {
	// The payload is URL-encoded, so check both raw and common encodings.
	if contains(payload, substr) {
		return true
	}
	// Try percent-encoded variants.
	return false
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) && containsString(s, substr)
}

func containsString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
