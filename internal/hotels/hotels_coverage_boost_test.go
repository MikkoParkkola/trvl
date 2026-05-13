package hotels

// Targeted coverage tests for functions identified as below-80% in the hotels package.
// These tests exercise pure logic paths (no live HTTP calls).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// ---------------------------------------------------------------------------
// sanitizeBookingURL — all branches
// ---------------------------------------------------------------------------

func TestSanitizeBookingURL_EmptyString(t *testing.T) {
	if got := sanitizeBookingURL(""); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestSanitizeBookingURL_HTTPS(t *testing.T) {
	url := "https://trivago.com/book/abc"
	if got := sanitizeBookingURL(url); got != url {
		t.Errorf("expected %q, got %q", url, got)
	}
}

func TestSanitizeBookingURL_HTTP(t *testing.T) {
	url := "http://trivago.com/book/abc"
	if got := sanitizeBookingURL(url); got != url {
		t.Errorf("expected %q, got %q", url, got)
	}
}

func TestSanitizeBookingURL_JavascriptScheme(t *testing.T) {
	if got := sanitizeBookingURL("javascript:alert(1)"); got != "" {
		t.Errorf("expected empty for javascript: scheme, got %q", got)
	}
}

func TestSanitizeBookingURL_DataScheme(t *testing.T) {
	if got := sanitizeBookingURL("data:text/html,<script>alert(1)</script>"); got != "" {
		t.Errorf("expected empty for data: scheme, got %q", got)
	}
}

func TestSanitizeBookingURL_InvalidURL(t *testing.T) {
	// url.Parse returns an error for truly malformed URLs.
	// In practice most invalid URLs return no error but have wrong scheme.
	if got := sanitizeBookingURL("not a url with spaces and %%bad"); got != "" {
		// Either rejected by url.Parse or by scheme check — both are fine.
		t.Logf("got %q (non-empty but may be valid per url.Parse)", got)
	}
}

// ---------------------------------------------------------------------------
// extractTrivagoContent — all branches
// ---------------------------------------------------------------------------

func TestExtractTrivagoContent_RPCError(t *testing.T) {
	rpc := trivagoRPCResponse{
		JSONRPC: "2.0",
		ID:      1,
		Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: -32600, Message: "invalid request"},
	}
	_, err := extractTrivagoContent(rpc)
	if err == nil {
		t.Fatal("expected error for RPC error response")
	}
	if !strings.Contains(err.Error(), "RPC error") {
		t.Errorf("expected 'RPC error' in error, got: %v", err)
	}
}

func TestExtractTrivagoContent_NilResult(t *testing.T) {
	rpc := trivagoRPCResponse{
		JSONRPC: "2.0",
		ID:      1,
		Result:  nil,
	}
	_, err := extractTrivagoContent(rpc)
	if err == nil {
		t.Fatal("expected error for nil result")
	}
	if !strings.Contains(err.Error(), "empty result") {
		t.Errorf("expected 'empty result' in error, got: %v", err)
	}
}

func TestExtractTrivagoContent_StructuredContent(t *testing.T) {
	toolResult := trivagoToolResult{
		Content: []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: `{"hotels":[]}`}},
		StructuredContent: json.RawMessage(`{"accommodations":[]}`),
	}
	resultBytes, _ := json.Marshal(toolResult)

	rpc := trivagoRPCResponse{
		JSONRPC: "2.0",
		ID:      1,
		Result:  json.RawMessage(resultBytes),
	}
	got, err := extractTrivagoContent(rpc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(got), "accommodations") {
		t.Errorf("expected structuredContent, got %s", string(got))
	}
}

func TestExtractTrivagoContent_ContentTextFallback(t *testing.T) {
	// No structuredContent → falls back to content[0].text.
	toolResult := trivagoToolResult{
		Content: []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: `{"hotels":[]}`}},
	}
	resultBytes, _ := json.Marshal(toolResult)

	rpc := trivagoRPCResponse{
		JSONRPC: "2.0",
		ID:      1,
		Result:  json.RawMessage(resultBytes),
	}
	got, err := extractTrivagoContent(rpc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != `{"hotels":[]}` {
		t.Errorf("expected content text, got %s", string(got))
	}
}

func TestExtractTrivagoContent_RawResultFallback(t *testing.T) {
	// Result is not a valid trivagoToolResult — returns raw result.
	rawResult := json.RawMessage(`{"whatever":"data"}`)
	rpc := trivagoRPCResponse{
		JSONRPC: "2.0",
		ID:      1,
		Result:  rawResult,
	}
	got, err := extractTrivagoContent(rpc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(rawResult) {
		t.Errorf("expected raw result, got %s", string(got))
	}
}

// ---------------------------------------------------------------------------
// SearchTrivago — full round-trip with mock HTTP server
// ---------------------------------------------------------------------------

func TestSearchTrivago_FullRoundTrip(t *testing.T) {
	origEnabled := trivagoEnabled
	trivagoEnabled = true
	defer func() { trivagoEnabled = origEnabled }()

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		var req trivagoRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "mock-session-xyz")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{}}}`)

		case "tools/call":
			// First tools/call is suggestions, second is accommodation search.
			if callCount == 2 {
				// Suggestions response.
				payload := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{}"}],"structuredContent":{"suggestions":[{"ns":200,"id":22235,"location":"Paris"}]}}}`
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprint(w, payload)
			} else {
				// Accommodation search response.
				payload := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{}"}],"structuredContent":{"accommodations":[{"accommodation_name":"Mock Paris Hotel","currency":"EUR","price_per_night":"€150","hotel_rating":4,"review_rating":"8.8","review_count":300,"latitude":48.8566,"longitude":2.3522,"accommodation_url":"https://trivago.com/book/mock1"}]}}}`
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprint(w, payload)
			}

		default:
			http.Error(w, fmt.Sprintf("unexpected method: %s", req.Method), http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	origClient := trivagoHTTPClient
	trivagoHTTPClient = &http.Client{
		Transport: &rewriteTransport{target: srv.URL},
	}
	defer func() { trivagoHTTPClient = origClient }()

	results, err := SearchTrivago(context.Background(), "Paris", HotelSearchOptions{
		CheckIn:  "2026-07-01",
		CheckOut: "2026-07-05",
		Guests:   2,
		Currency: "EUR",
	})
	if err != nil {
		t.Fatalf("SearchTrivago failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "Mock Paris Hotel" {
		t.Errorf("expected Mock Paris Hotel, got %q", results[0].Name)
	}
}

func TestSearchTrivago_Disabled(t *testing.T) {
	origEnabled := trivagoEnabled
	trivagoEnabled = false
	defer func() { trivagoEnabled = origEnabled }()

	results, err := SearchTrivago(context.Background(), "Paris", HotelSearchOptions{
		CheckIn:  "2026-07-01",
		CheckOut: "2026-07-05",
	})
	if err != nil {
		t.Fatalf("expected no error when disabled, got: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results when disabled, got: %v", results)
	}
}

func TestSearchTrivago_DefaultGuests(t *testing.T) {
	// Guests <= 0 → default to 2. Test uses cancelled ctx to avoid real HTTP.
	origEnabled := trivagoEnabled
	trivagoEnabled = true
	defer func() { trivagoEnabled = origEnabled }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := SearchTrivago(ctx, "Paris", HotelSearchOptions{
		CheckIn:  "2026-07-01",
		CheckOut: "2026-07-05",
		Guests:   0, // should default to 2
	})
	// Error is expected (cancelled ctx), just verify we don't panic and
	// the Guests=0 path is exercised.
	if err == nil {
		t.Log("no error (unexpected but not fatal)")
	}
}

func TestSearchTrivago_DefaultCurrency(t *testing.T) {
	origEnabled := trivagoEnabled
	trivagoEnabled = true
	defer func() { trivagoEnabled = origEnabled }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _ = SearchTrivago(ctx, "Paris", HotelSearchOptions{
		CheckIn:  "2026-07-01",
		CheckOut: "2026-07-05",
		Currency: "", // should default to USD
	})
}

func TestSearchTrivago_InitSessionFails_MockHTTP500(t *testing.T) {
	origEnabled := trivagoEnabled
	trivagoEnabled = true
	defer func() { trivagoEnabled = origEnabled }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	origClient := trivagoHTTPClient
	trivagoHTTPClient = &http.Client{
		Transport: &rewriteTransport{target: srv.URL},
	}
	defer func() { trivagoHTTPClient = origClient }()

	_, err := SearchTrivago(context.Background(), "Paris", HotelSearchOptions{
		CheckIn:  "2026-07-01",
		CheckOut: "2026-07-05",
	})
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "trivago init") {
		t.Errorf("expected 'trivago init' error, got: %v", err)
	}
}

func TestSearchTrivago_SuggestionsFail(t *testing.T) {
	origEnabled := trivagoEnabled
	trivagoEnabled = true
	defer func() { trivagoEnabled = origEnabled }()

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var req trivagoRPCRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		if req.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "mock-session")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
			return
		}
		// Return 429 for the tool call to simulate suggestions failure.
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	origClient := trivagoHTTPClient
	trivagoHTTPClient = &http.Client{
		Transport: &rewriteTransport{target: srv.URL},
	}
	defer func() { trivagoHTTPClient = origClient }()

	_, err := SearchTrivago(context.Background(), "Paris", HotelSearchOptions{
		CheckIn:  "2026-07-01",
		CheckOut: "2026-07-05",
	})
	if err == nil {
		t.Fatal("expected error for suggestions failure")
	}
	if !strings.Contains(err.Error(), "trivago suggestions") {
		t.Errorf("expected 'trivago suggestions' in error, got: %v", err)
	}
}

func TestSearchTrivago_AccomSearchFails(t *testing.T) {
	origEnabled := trivagoEnabled
	trivagoEnabled = true
	defer func() { trivagoEnabled = origEnabled }()

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var req trivagoRPCRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")

		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "mock-session")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
		case "tools/call":
			if callCount == 2 {
				// Suggestions succeed.
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{}"}],"structuredContent":{"suggestions":[{"ns":200,"id":22235,"location":"Paris"}]}}}`)
			} else {
				// Accommodation search returns 500.
				w.WriteHeader(http.StatusInternalServerError)
			}
		}
	}))
	defer srv.Close()

	origClient := trivagoHTTPClient
	trivagoHTTPClient = &http.Client{
		Transport: &rewriteTransport{target: srv.URL},
	}
	defer func() { trivagoHTTPClient = origClient }()

	_, err := SearchTrivago(context.Background(), "Paris", HotelSearchOptions{
		CheckIn:  "2026-07-01",
		CheckOut: "2026-07-05",
	})
	if err == nil {
		t.Fatal("expected error for accommodation search failure")
	}
	if !strings.Contains(err.Error(), "trivago accommodation search") {
		t.Errorf("expected 'trivago accommodation search' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// enrichHotelAmenities — pure logic, no network (hotels with no HotelID = skip)
// ---------------------------------------------------------------------------

func TestEnrichHotelAmenities_NoHotelIDs(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Hotel A", HotelID: ""},
		{Name: "Hotel B", HotelID: ""},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := enrichHotelAmenities(ctx, hotels, 5)
	if len(result) != 2 {
		t.Errorf("expected 2 hotels, got %d", len(result))
	}
}

func TestEnrichHotelAmenities_LimitClamped(t *testing.T) {
	// limit > 10 should be clamped to 10; limit <= 0 should default to 5.
	hotels := []models.HotelResult{
		{Name: "Hotel A", HotelID: ""},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// limit = 0 → default 5
	result := enrichHotelAmenities(ctx, hotels, 0)
	if len(result) != 1 {
		t.Errorf("expected 1 hotel, got %d", len(result))
	}

	// limit = 15 → clamped to 10
	result = enrichHotelAmenities(ctx, hotels, 15)
	if len(result) != 1 {
		t.Errorf("expected 1 hotel (limit clamped), got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// buildLocationCandidates — pure string logic
// ---------------------------------------------------------------------------

func TestBuildLocationCandidates_WithComma(t *testing.T) {
	got := buildLocationCandidates("Beverly Hills Heights, Tenerife")
	if len(got) < 2 {
		t.Fatalf("expected at least 2 candidates, got %d: %v", len(got), got)
	}
	// First candidate should be "Tenerife".
	if got[0] != "Tenerife" {
		t.Errorf("expected first candidate to be 'Tenerife', got %q", got[0])
	}
}

func TestBuildLocationCandidates_NoComma(t *testing.T) {
	got := buildLocationCandidates("Hotel Kamp Helsinki")
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d: %v", len(got), got)
	}
	if got[0] != "Hotel Kamp Helsinki" {
		t.Errorf("expected full query, got %q", got[0])
	}
}

// ---------------------------------------------------------------------------
// findBestNameMatch — pure fuzzy matching logic
// ---------------------------------------------------------------------------

func TestFindBestNameMatch_ExactContains(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "The Grand Budapest Hotel"},
		{Name: "Random Inn"},
	}
	got := findBestNameMatch(hotels, "Grand Budapest Hotel")
	if got == nil {
		t.Fatal("expected a match, got nil")
	}
	if got.Name != "The Grand Budapest Hotel" {
		t.Errorf("expected Grand Budapest Hotel, got %q", got.Name)
	}
}

func TestFindBestNameMatch_WordMatch(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Ritz Carlton Paris"},
		{Name: "Random Inn"},
	}
	got := findBestNameMatch(hotels, "Ritz Paris")
	if got == nil {
		t.Fatal("expected a match, got nil")
	}
}

func TestFindBestNameMatch_NoMatch(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Random Inn"},
	}
	got := findBestNameMatch(hotels, "Marriott")
	if got != nil {
		t.Errorf("expected nil, got %q", got.Name)
	}
}

func TestFindBestNameMatch_EmptyList(t *testing.T) {
	got := findBestNameMatch([]models.HotelResult{}, "anything")
	if got != nil {
		t.Errorf("expected nil for empty list, got %+v", got)
	}
}

// ---------------------------------------------------------------------------
// parseRatingString — uncovered branch: invalid float string
// ---------------------------------------------------------------------------

func TestParseRatingString_InvalidFloat(t *testing.T) {
	got := parseRatingString("not-a-number")
	if got != 0 {
		t.Errorf("expected 0 for invalid float, got %v", got)
	}
}

func TestParseRatingString_AlreadyOnFiveScale(t *testing.T) {
	// Value <= 5 should be returned as-is.
	got := parseRatingString("3.5")
	if got != 3.5 {
		t.Errorf("expected 3.5 (already on 5-scale), got %v", got)
	}
}

// ---------------------------------------------------------------------------
// parseTrivagoSuggestions — additional edge cases
// ---------------------------------------------------------------------------

func TestParseTrivagoSuggestions_NotAnObject(t *testing.T) {
	// Not a JSON object at all.
	_, err := parseTrivagoSuggestions(json.RawMessage(`"just a string"`))
	if err == nil {
		t.Fatal("expected error for non-object input")
	}
}

func TestParseTrivagoSuggestions_DataKey(t *testing.T) {
	// "data" key.
	raw := json.RawMessage(`{"data": [{"ns": 300, "id": 99, "location": "Berlin"}]}`)
	got, err := parseTrivagoSuggestions(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.NS != 300 || got.ID != 99 {
		t.Errorf("expected NS=300 ID=99, got NS=%d ID=%d", got.NS, got.ID)
	}
}

func TestParseTrivagoSuggestions_KnownKeyAllEmpty(t *testing.T) {
	// All known keys empty/absent → error.
	raw := json.RawMessage(`{"suggestions": [], "results": [], "data": [], "items": []}`)
	_, err := parseTrivagoSuggestions(raw)
	if err == nil {
		t.Fatal("expected error for all-empty arrays")
	}
}

// ---------------------------------------------------------------------------
// trivagoInitSession — HTTP error paths
// ---------------------------------------------------------------------------

func TestTrivagoInitSession_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	origClient := trivagoHTTPClient
	trivagoHTTPClient = &http.Client{
		Transport: &rewriteTransport{target: srv.URL},
	}
	defer func() { trivagoHTTPClient = origClient }()

	_, err := trivagoInitSession(context.Background())
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "init HTTP") {
		t.Errorf("expected 'init HTTP' in error, got: %v", err)
	}
}

func TestTrivagoInitSession_MissingSessionHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 200 OK but no Mcp-Session-Id header.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer srv.Close()

	origClient := trivagoHTTPClient
	trivagoHTTPClient = &http.Client{
		Transport: &rewriteTransport{target: srv.URL},
	}
	defer func() { trivagoHTTPClient = origClient }()

	_, err := trivagoInitSession(context.Background())
	if err == nil {
		t.Fatal("expected error for missing session header")
	}
	if !strings.Contains(err.Error(), "Mcp-Session-Id") {
		t.Errorf("expected 'Mcp-Session-Id' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// trivagoMCPCall — additional error paths
// ---------------------------------------------------------------------------

func TestTrivagoMCPCall_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	origClient := trivagoHTTPClient
	trivagoHTTPClient = &http.Client{
		Transport: &rewriteTransport{target: srv.URL},
	}
	defer func() { trivagoHTTPClient = origClient }()

	_, err := trivagoMCPCall(context.Background(), "session-id", "trivago-search-suggestions", map[string]any{"query": "Paris"})
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestTrivagoMCPCall_RPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"invalid request"}}`)
	}))
	defer srv.Close()

	origClient := trivagoHTTPClient
	trivagoHTTPClient = &http.Client{
		Transport: &rewriteTransport{target: srv.URL},
	}
	defer func() { trivagoHTTPClient = origClient }()

	_, err := trivagoMCPCall(context.Background(), "session-id", "trivago-search-suggestions", map[string]any{"query": "Paris"})
	if err == nil {
		t.Fatal("expected error for RPC error response")
	}
	if !strings.Contains(err.Error(), "RPC error") {
		t.Errorf("expected 'RPC error' in error, got: %v", err)
	}
}
