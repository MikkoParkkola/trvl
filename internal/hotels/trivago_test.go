package hotels

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---- parseTrivagoResponse tests ----

func TestParseTrivagoResponsePlainJSON(t *testing.T) {
	// Plain JSON-RPC response with structuredContent.
	payload := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"hotels\":[]}"}],"structuredContent":{"hotels":[]}}}`
	got, err := parseTrivagoResponse([]byte(payload))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(got), "hotels") {
		t.Errorf("got %s, want hotels key", string(got))
	}
}

func TestParseTrivagoResponsePlainJSONFallbackToText(t *testing.T) {
	// Plain JSON-RPC response without structuredContent — falls back to text.
	payload := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"hotels\":[]}"}]}}`
	got, err := parseTrivagoResponse([]byte(payload))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != `{"hotels":[]}` {
		t.Errorf("got %s, want {\"hotels\":[]}", string(got))
	}
}

func TestParseTrivagoResponseRPCError(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"invalid request"}}`
	_, err := parseTrivagoResponse([]byte(payload))
	if err == nil {
		t.Fatal("expected error for RPC error response, got nil")
	}
	if !strings.Contains(err.Error(), "RPC error") {
		t.Errorf("error message should mention RPC error, got: %v", err)
	}
}

func TestParseTrivagoResponseEmpty(t *testing.T) {
	_, err := parseTrivagoResponse([]byte("not json at all"))
	if err == nil {
		t.Fatal("expected error for garbage body, got nil")
	}
}

// ---- parseTrivagoSuggestions tests ----

func TestParseTrivagoSuggestionsStructured(t *testing.T) {
	raw := json.RawMessage(`{
		"suggestions": [
			{"suggestion_type": "ConceptSearchSuggestion", "ns": 200, "id": 22235, "location": "Paris", "location_label": "France", "location_type": "City"},
			{"suggestion_type": "ConceptSearchSuggestion", "ns": 200, "id": 51272, "location": "Paris", "location_label": "Texas, USA", "location_type": "City"}
		]
	}`)

	got, err := parseTrivagoSuggestions(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.NS != 200 {
		t.Errorf("NS: got %d, want 200", got.NS)
	}
	if got.ID != 22235 {
		t.Errorf("ID: got %d, want 22235", got.ID)
	}
}

func TestParseTrivagoSuggestionsAlternateKey(t *testing.T) {
	// "results" key instead of "suggestions".
	raw := json.RawMessage(`{"results": [{"ns": 200, "id": 7, "location": "Rome"}]}`)
	got, err := parseTrivagoSuggestions(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != 7 {
		t.Errorf("ID: got %d, want 7", got.ID)
	}
	if got.NS != 200 {
		t.Errorf("NS: got %d, want 200", got.NS)
	}
}

func TestParseTrivagoSuggestionsEmpty(t *testing.T) {
	// Empty suggestions list — should return an error.
	raw := json.RawMessage(`{"suggestions": []}`)
	_, err := parseTrivagoSuggestions(raw)
	if err == nil {
		t.Fatal("expected error for empty suggestions, got nil")
	}
}

func TestParseTrivagoSuggestionsRawNSID(t *testing.T) {
	// Alternate key names with capitalized fields.
	raw := json.RawMessage(`{"items": [{"NS": 100, "ID": 42}]}`)
	got, err := parseTrivagoSuggestions(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.NS != 100 {
		t.Errorf("NS: got %d, want 100", got.NS)
	}
	if got.ID != 42 {
		t.Errorf("ID: got %d, want 42", got.ID)
	}
}

// ---- parseTrivagoAccommodations tests ----

func TestParseTrivagoAccommodationsNewFormat(t *testing.T) {
	raw := json.RawMessage(`{
		"accommodations": [
			{
				"accommodation_id": "abc123",
				"accommodation_name": "Hotel Roma",
				"address": "Via del Corso 1, Rome",
				"postal_code": "00186",
				"country_city": "Rome, Italy",
				"hotel_rating": 4,
				"review_rating": "8.6",
				"review_count": 512,
				"currency": "EUR",
				"price_per_night": "€149",
				"price_per_stay": "€596",
				"advertisers": "booking.com",
				"latitude": 41.9028,
				"longitude": 12.4964,
				"accommodation_url": "https://www.trivago.com/en-US/oar/hotel-roma?search=foo",
				"booking_url": "https://www.trivago.com/book/abc123",
				"top_amenities": "WiFi, Pool, Spa",
				"distance": "0.5 miles to Colosseum",
				"distance_to_city_center": {"value": 1.2, "unit": "MILES"}
			}
		]
	}`)

	results, err := parseTrivagoAccommodations(raw, "USD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	h := results[0]
	if h.Name != "Hotel Roma" {
		t.Errorf("name: got %q, want %q", h.Name, "Hotel Roma")
	}
	if h.Price != 149.0 {
		t.Errorf("price: got %.1f, want 149.0", h.Price)
	}
	if h.Currency != "EUR" {
		t.Errorf("currency: got %q, want EUR", h.Currency)
	}
	// Rating 8.6 on 10-scale -> 4.3 on 5-scale.
	if h.Rating != 4.3 {
		t.Errorf("rating: got %.1f, want 4.3", h.Rating)
	}
	if h.Stars != 4 {
		t.Errorf("stars: got %d, want 4", h.Stars)
	}
	if h.Lat != 41.9028 {
		t.Errorf("lat: got %v, want 41.9028", h.Lat)
	}
	if h.ReviewCount != 512 {
		t.Errorf("reviewCount: got %d, want 512", h.ReviewCount)
	}
	if h.BookingURL == "" {
		t.Error("expected non-empty booking URL")
	}
}

func TestParseTrivagoAccommodationsLegacyFormat(t *testing.T) {
	raw := json.RawMessage(`{
		"accommodations": [
			{
				"name": "Hotel Roma",
				"rating": 4.3,
				"reviewCount": 512,
				"stars": 4,
				"address": "Via del Corso 1, Rome",
				"latitude": 41.9028,
				"longitude": 12.4964,
				"price": {"amount": 149.0, "currency": "EUR"},
				"bookingLinks": [
					{"url": "https://trivago.com/book/1", "price": 149.0, "currency": "EUR", "provider": "booking.com"},
					{"url": "https://trivago.com/book/2", "price": 139.0, "currency": "EUR", "provider": "hotels.com"}
				]
			}
		]
	}`)

	results, err := parseTrivagoAccommodations(raw, "USD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	h := results[0]
	if h.Name != "Hotel Roma" {
		t.Errorf("name: got %q, want %q", h.Name, "Hotel Roma")
	}
	if h.Price != 139.0 {
		t.Errorf("price: got %.1f, want 139.0 (cheapest booking link)", h.Price)
	}
	if h.Currency != "EUR" {
		t.Errorf("currency: got %q, want EUR", h.Currency)
	}
	if h.Rating != 4.3 {
		t.Errorf("rating: got %.1f, want 4.3", h.Rating)
	}
	if h.Stars != 4 {
		t.Errorf("stars: got %d, want 4", h.Stars)
	}
	if h.Lat != 41.9028 {
		t.Errorf("lat: got %v, want 41.9028", h.Lat)
	}
}

func TestParseTrivagoAccommodationsPriceSourceTagging(t *testing.T) {
	raw := json.RawMessage(`{
		"accommodations": [
			{
				"accommodation_name": "Budget Inn",
				"currency": "USD",
				"price_per_night": "$59",
				"price_per_stay": "$236"
			}
		]
	}`)

	results, err := parseTrivagoAccommodations(raw, "USD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	h := results[0]
	if len(h.Sources) == 0 {
		t.Fatal("expected at least one PriceSource")
	}
	src := h.Sources[0]
	if src.Provider != "trivago" {
		t.Errorf("source provider: got %q, want trivago", src.Provider)
	}
	if src.Price != 59.0 {
		t.Errorf("source price: got %.1f, want 59.0", src.Price)
	}
}

func TestParseTrivagoAccommodationsAlternateKey(t *testing.T) {
	// "hotels" key instead of "accommodations".
	raw := json.RawMessage(`{
		"hotels": [
			{"accommodation_name": "Hotel Alt", "currency": "GBP", "price_per_night": "£80"}
		]
	}`)

	results, err := parseTrivagoAccommodations(raw, "GBP")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestParseTrivagoAccommodationsEmpty(t *testing.T) {
	raw := json.RawMessage(`{"accommodations": []}`)
	results, err := parseTrivagoAccommodations(raw, "USD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty list, got %d", len(results))
	}
}

func TestParseTrivagoAccommodationsSkipsEmptyName(t *testing.T) {
	raw := json.RawMessage(`{
		"accommodations": [
			{"accommodation_name": "", "name": "", "currency": "USD", "price_per_night": "$50"},
			{"accommodation_name": "Real Hotel", "currency": "USD", "price_per_night": "$75"}
		]
	}`)

	results, err := parseTrivagoAccommodations(raw, "USD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result (skipping empty name), got %d", len(results))
	}
	if results[0].Name != "Real Hotel" {
		t.Errorf("expected Real Hotel, got %q", results[0].Name)
	}
}

// ---- trivagoParsePrice tests ----

func TestTrivagoParsePrice(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"€211", 211},
		{"$150", 150},
		{"£89", 89},
		{"", 0},
	}
	for _, tt := range tests {
		got := trivagoParsePrice(tt.input)
		if got != tt.want {
			t.Errorf("trivagoParsePrice(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ---- parseRatingString tests ----

func TestParseRatingString(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"8.4", 4.2},
		{"8.6", 4.3},
		{"10.0", 5.0},
		{"4.5", 4.5}, // Already on 5-scale, no division.
		{"", 0},
	}
	for _, tt := range tests {
		got := parseRatingString(tt.input)
		if got != tt.want {
			t.Errorf("parseRatingString(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ---- trivagoMCPCall integration with mock server ----

func TestTrivagoMCPCallMockServer(t *testing.T) {
	// Mock server that handles init + tool call.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		ct := r.Header.Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Errorf("expected application/json content-type, got %q", ct)
		}

		var req trivagoRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if req.Method == "initialize" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "test-session-123")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"test","version":"0.1.0"}}}`)
			return
		}

		if req.Method != "tools/call" {
			t.Errorf("expected method tools/call, got %q", req.Method)
		}

		// Verify session ID.
		if sid := r.Header.Get("Mcp-Session-Id"); sid != "test-session-123" {
			t.Errorf("expected session ID test-session-123, got %q", sid)
		}

		// Return a valid accommodation response with structuredContent.
		payload := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{}"}],"structuredContent":{"accommodations":[{"accommodation_id":"mock1","accommodation_name":"Mock Hotel","hotel_rating":4,"review_rating":"9.0","review_count":200,"currency":"EUR","price_per_night":"€120","price_per_stay":"€480","accommodation_url":"https://example.com","latitude":48.0,"longitude":2.0,"address":"1 Rue Test","postal_code":"75001","country_city":"Paris, France","advertisers":"booking.com","distance":"0.5 miles","distance_to_city_center":{"value":1.0,"unit":"MILES"},"top_amenities":"WiFi, Pool"}]}}}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, payload)
	}))
	defer srv.Close()

	// Temporarily replace the HTTP client for this test.
	origClient := trivagoHTTPClient
	trivagoHTTPClient = &http.Client{
		Transport: &rewriteTransport{target: srv.URL},
	}
	defer func() { trivagoHTTPClient = origClient }()

	raw, err := trivagoMCPCall(context.Background(), "test-session-123", "trivago-accommodation-search", map[string]any{
		"ns":        200,
		"id":        22235,
		"arrival":   "2026-07-01",
		"departure": "2026-07-05",
		"adults":    2,
	})
	if err != nil {
		t.Fatalf("trivagoMCPCall failed: %v", err)
	}

	var accom trivagoAccomResult
	if err := json.Unmarshal(raw, &accom); err != nil {
		t.Fatalf("unmarshal accommodation result: %v", err)
	}
	if len(accom.Accommodations) != 1 {
		t.Fatalf("expected 1 accommodation, got %d", len(accom.Accommodations))
	}
	if accom.Accommodations[0].AccommodationName != "Mock Hotel" {
		t.Errorf("expected Mock Hotel, got %q", accom.Accommodations[0].AccommodationName)
	}
}

func TestTrivagoMCPCallHTTP429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	origClient := trivagoHTTPClient
	trivagoHTTPClient = &http.Client{
		Transport: &rewriteTransport{target: srv.URL},
	}
	defer func() { trivagoHTTPClient = origClient }()

	_, err := trivagoMCPCall(context.Background(), "some-session", "trivago-search-suggestions", map[string]any{"query": "Paris"})
	if err == nil {
		t.Fatal("expected error for 429 response, got nil")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should mention 429, got: %v", err)
	}
}

func TestTrivagoInitSessionMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req trivagoRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if req.Method != "initialize" {
			t.Errorf("expected method initialize, got %q", req.Method)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "test-session-abc")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"Trivago","version":"0.2.0"}}}`)
	}))
	defer srv.Close()

	origClient := trivagoHTTPClient
	trivagoHTTPClient = &http.Client{
		Transport: &rewriteTransport{target: srv.URL},
	}
	defer func() { trivagoHTTPClient = origClient }()

	sessionID, err := trivagoInitSession(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sessionID != "test-session-abc" {
		t.Errorf("expected session ID test-session-abc, got %q", sessionID)
	}
}

// ---- SearchTrivago error handling ----

func TestSearchTrivagoMissingDates(t *testing.T) {
	origEnabled := trivagoEnabled
	trivagoEnabled = true
	defer func() { trivagoEnabled = origEnabled }()

	_, err := SearchTrivago(context.Background(), "Paris", HotelSearchOptions{})
	if err == nil {
		t.Fatal("expected error for missing dates, got nil")
	}
}

func TestSearchTrivagoCancelledContext(t *testing.T) {
	origEnabled := trivagoEnabled
	trivagoEnabled = true
	defer func() { trivagoEnabled = origEnabled }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := SearchTrivago(ctx, "Paris", HotelSearchOptions{
		CheckIn:  "2026-07-01",
		CheckOut: "2026-07-05",
	})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

// ---- helper types ----

// rewriteTransport rewrites all outbound requests to target for testing.
type rewriteTransport struct {
	target string
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(rt.target, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

// mustMarshalString returns the JSON-encoded form of a string value.
func mustMarshalString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

var _ = mustMarshalString // suppress unused warning; kept for test utility
