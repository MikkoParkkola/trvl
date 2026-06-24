package flights

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// fixedSkiplaggedFlightsBody is a frozen sk_flights_search response
// matching the outputSchema captured from a live tools/list call on
// 2026-04-27. structuredContent carries the typed flights array; we
// also include a content[0].text fallback to exercise the fallback
// path. Two flights — one hidden-city, one direct — so attribute
// mapping into Warnings is testable without a live network call.
const fixedSkiplaggedFlightsBody = `{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "structuredContent": {
      "searchUrl": "https://example.test/search?from=AMS&to=HEL",
      "flights": [
        {
          "type": "FlightCard",
          "id": "f1",
          "airlines": "KL",
          "departure": {"airport": "AMS", "dateTime": "2026-05-15T07:30"},
          "arrival": {"airport": "HEL", "dateTime": "2026-05-15T11:15"},
          "duration": "2h 45m",
          "layovers": 0,
          "price": {"amount": 142.50, "currency": "EUR"},
          "deepLink": "https://example.test/book/f1",
          "attributes": []
        },
        {
          "type": "FlightCard",
          "id": "f2",
          "airlines": "AY",
          "departure": {"airport": "AMS", "dateTime": "2026-05-15T09:00"},
          "arrival": {"airport": "HEL", "dateTime": "2026-05-15T13:30"},
          "duration": "PT4H30M",
          "layovers": 1,
          "price": {"amount": 98.20, "currency": "EUR"},
          "deepLink": "https://example.test/book/f2",
          "attributes": ["hidden_city", "basic_economy"]
        }
      ]
    },
    "content": [{"type": "text", "text": "{\"flights\":[]}"}]
  }
}`

// fixedSkiplaggedSSEBody wraps the same payload in Server-Sent Event
// framing because Skiplagged's live endpoint returns SSE-framed
// responses on Streamable HTTP. parseSkiplaggedResponse must handle
// both bare-JSON and SSE-framed envelopes.
var fixedSkiplaggedSSEBody = "event: message\ndata: " +
	jsonOneLine(fixedSkiplaggedFlightsBody) + "\n\n"

// jsonOneLine collapses a JSON document onto a single line so it can
// be embedded into an SSE `data:` field. Test-only helper.
func jsonOneLine(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	out, err := json.Marshal(v)
	if err != nil {
		return s
	}
	return string(out)
}

// newSkiplaggedTestServer spins up an httptest server that answers the
// MCP initialize handshake by emitting an Mcp-Session-Id header, then
// answers a single tools/call by returning the fixed flights body.
// The returned closer must be called by the test.
func newSkiplaggedTestServer(t *testing.T, sessionID string, body string) (*httptest.Server, func()) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		var rpc skiplaggedRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}

		switch rpc.Method {
		case "initialize":
			if sessionID != "" {
				w.Header().Set("Mcp-Session-Id", sessionID)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
		case "tools/call":
			// Verify session header propagation only when the
			// test harness configured a session ID.
			if sessionID != "" {
				if got := r.Header.Get("Mcp-Session-Id"); got != sessionID {
					http.Error(w, "missing/incorrect Mcp-Session-Id: "+got, http.StatusUnauthorized)
					return
				}
			}
			calls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(body))
		default:
			http.Error(w, "unsupported method: "+rpc.Method, http.StatusBadRequest)
		}
	}))
	closer := func() {
		srv.Close()
		_ = calls // silence linter on unused
	}
	return srv, closer
}

// fixedSkiplaggedRoundTripBody is a single native round-trip fare: one
// FlightCard carrying a returnFlight (the inbound) and a single total price.
const fixedSkiplaggedRoundTripBody = `{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "structuredContent": {
      "searchUrl": "https://example.test/search?from=AMS&to=HEL&rt=1",
      "flights": [
        {
          "type": "FlightCard",
          "id": "rt1",
          "airlines": "AY",
          "departure": {"airport": "AMS", "dateTime": "2026-05-15T07:30"},
          "arrival": {"airport": "HEL", "dateTime": "2026-05-15T11:15"},
          "duration": "2h 45m",
          "layovers": 0,
          "price": {"amount": 210.00, "currency": "EUR"},
          "deepLink": "https://example.test/book/rt1",
          "attributes": [],
          "returnFlight": {
            "departure": {"airport": "HEL", "dateTime": "2026-05-22T18:00"},
            "arrival": {"airport": "AMS", "dateTime": "2026-05-22T21:00"},
            "duration": "3h 00m",
            "layovers": 1
          }
        }
      ]
    },
    "content": [{"type": "text", "text": "{\"flights\":[]}"}]
  }
}`

func TestSearchSkiplagged_NativeRoundTrip(t *testing.T) {
	srv, closer := newSkiplaggedTestServer(t, "test-sess-rt", fixedSkiplaggedRoundTripBody)
	defer closer()
	defer skiplaggedSetEndpointForTest(srv.URL)()

	skiplaggedEnabled = true
	defer func() { skiplaggedEnabled = true }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := SearchSkiplagged(ctx, "AMS", "HEL", "2026-05-15", SearchOptions{ReturnDate: "2026-05-22"})
	if err != nil {
		t.Fatalf("SearchSkiplagged: %v", err)
	}
	if res == nil || len(res.Flights) != 1 {
		t.Fatalf("expected 1 round-trip itinerary, got %+v", res)
	}
	f := res.Flights[0]
	if f.FareType != models.FareRoundTrip {
		t.Errorf("FareType=%q want %q (native round-trip)", f.FareType, models.FareRoundTrip)
	}
	if len(f.Legs) != 2 {
		t.Fatalf("legs=%d want 2 (outbound + inbound)", len(f.Legs))
	}
	if f.Legs[0].Direction != "outbound" || f.Legs[1].Direction != "inbound" {
		t.Errorf("leg directions: %q then %q want outbound then inbound", f.Legs[0].Direction, f.Legs[1].Direction)
	}
	if f.Legs[0].DepartureAirport.Code != "AMS" || f.Legs[1].DepartureAirport.Code != "HEL" {
		t.Errorf("leg routing: %q then %q want AMS then HEL", f.Legs[0].DepartureAirport.Code, f.Legs[1].DepartureAirport.Code)
	}
	// Single native fare for the whole trip — not a sum of two one-ways.
	if f.Price != 210.00 {
		t.Errorf("price=%v want 210.00 (single native fare)", f.Price)
	}
	if f.Stops != 1 {
		t.Errorf("stops=%d want 1 (0 outbound + 1 inbound)", f.Stops)
	}
	if f.Duration != 165+180 {
		t.Errorf("duration=%d want 345 (165 + 180)", f.Duration)
	}
}

func TestRunSkiplaggedProvider_OneWaySuppressed(t *testing.T) {
	// When the round-trip composer suppresses the one-way Skiplagged legs, the
	// provider must report a transparent skip and make NO HTTP call (no endpoint
	// is configured here, so a call would error rather than skip).
	ctx := disableSkiplaggedOneWay(context.Background())
	out := runSkiplaggedProvider(ctx, nil, "AMS", "HEL", "2026-05-15", SearchOptions{})
	if out.err != nil {
		t.Fatalf("expected no error (call suppressed), got %v", out.err)
	}
	if out.status.Status != "skipped" {
		t.Errorf("status=%q want skipped", out.status.Status)
	}
	if len(out.flights) != 0 {
		t.Errorf("flights=%d want 0 (no call made)", len(out.flights))
	}
}

func TestSearchSkiplagged_Success(t *testing.T) {
	srv, closer := newSkiplaggedTestServer(t, "test-sess-001", fixedSkiplaggedFlightsBody)
	defer closer()
	defer skiplaggedSetEndpointForTest(srv.URL)()

	skiplaggedEnabled = true
	defer func() { skiplaggedEnabled = true }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := SearchSkiplagged(ctx, "AMS", "HEL", "2026-05-15", SearchOptions{})
	if err != nil {
		t.Fatalf("SearchSkiplagged: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if res.Count != 2 || len(res.Flights) != 2 {
		t.Fatalf("expected 2 flights, got count=%d len=%d", res.Count, len(res.Flights))
	}

	// First flight: direct, no warnings.
	f1 := res.Flights[0]
	if f1.Provider != "skiplagged" {
		t.Errorf("flight 0: provider=%q want skiplagged", f1.Provider)
	}
	if f1.Price != 142.50 || f1.Currency != "EUR" {
		t.Errorf("flight 0: price=%v %q want 142.50 EUR", f1.Price, f1.Currency)
	}
	if f1.Stops != 0 {
		t.Errorf("flight 0: stops=%d want 0", f1.Stops)
	}
	if f1.Duration != 165 {
		t.Errorf("flight 0: duration=%d minutes want 165", f1.Duration)
	}
	if f1.SelfConnect {
		t.Errorf("flight 0: SelfConnect=true want false")
	}
	if len(f1.Warnings) != 0 {
		t.Errorf("flight 0: warnings=%v want []", f1.Warnings)
	}

	// Second flight: hidden-city, expect SelfConnect=true and warnings.
	f2 := res.Flights[1]
	if !f2.SelfConnect {
		t.Errorf("flight 1: SelfConnect=false want true (hidden_city)")
	}
	if !containsStr(f2.Warnings, "hidden_city") {
		t.Errorf("flight 1: warnings=%v want hidden_city", f2.Warnings)
	}
	if !containsStr(f2.Warnings, "basic_economy") {
		t.Errorf("flight 1: warnings=%v want basic_economy", f2.Warnings)
	}
	if f2.Duration != 270 {
		t.Errorf("flight 1: duration=%d want 270 (PT4H30M)", f2.Duration)
	}
	if f2.BookingURL == "" {
		t.Errorf("flight 1: BookingURL empty")
	}
}

func TestSearchSkiplagged_SSEFraming(t *testing.T) {
	srv, closer := newSkiplaggedTestServer(t, "test-sess-002", fixedSkiplaggedSSEBody)
	defer closer()
	defer skiplaggedSetEndpointForTest(srv.URL)()

	skiplaggedEnabled = true
	ctx := context.Background()

	res, err := SearchSkiplagged(ctx, "AMS", "HEL", "2026-05-15", SearchOptions{})
	if err != nil {
		t.Fatalf("SSE-framed response should parse: %v", err)
	}
	if len(res.Flights) != 2 {
		t.Fatalf("expected 2 flights from SSE body, got %d", len(res.Flights))
	}
}

func TestSearchSkiplagged_RPCError(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":2,"error":{"code":-32602,"message":"Invalid arguments for tool sk_flights_search"}}`
	srv, closer := newSkiplaggedTestServer(t, "", body)
	defer closer()
	defer skiplaggedSetEndpointForTest(srv.URL)()

	skiplaggedEnabled = true
	ctx := context.Background()

	_, err := SearchSkiplagged(ctx, "AMS", "HEL", "2026-05-15", SearchOptions{})
	if err == nil {
		t.Fatal("expected error from RPC error envelope, got nil")
	}
	if !strings.Contains(err.Error(), "RPC error -32602") {
		t.Errorf("error %q should mention RPC error -32602", err.Error())
	}
}

// TestSearchSkiplagged_ToolErrorEnvelope covers the case where the RPC
// envelope itself succeeds but the tool result has `isError: true` —
// observed live when `departureDate` exceeds Skiplagged's ~11-month
// search window. The error text should propagate verbatim so the
// caller can act on it.
func TestSearchSkiplagged_ToolErrorEnvelope(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":2,"result":{"isError":true,"content":[{"type":"text","text":"Failed to fetch from search: Invalid range for depart. Must be no greater than 2027-03-22."}]}}`
	srv, closer := newSkiplaggedTestServer(t, "", body)
	defer closer()
	defer skiplaggedSetEndpointForTest(srv.URL)()

	skiplaggedEnabled = true
	ctx := context.Background()

	_, err := SearchSkiplagged(ctx, "AMS", "HEL", "2030-01-15", SearchOptions{})
	if err == nil {
		t.Fatal("expected error from isError envelope, got nil")
	}
	if !strings.Contains(err.Error(), "tool error") {
		t.Errorf("error %q should mention 'tool error'", err.Error())
	}
	if !strings.Contains(err.Error(), "Invalid range") {
		t.Errorf("error %q should propagate Skiplagged's own message", err.Error())
	}
}

func TestSearchSkiplagged_InputValidation(t *testing.T) {
	skiplaggedEnabled = true
	cases := []struct {
		name, origin, dest, date string
	}{
		{"missing origin", "", "HEL", "2026-05-15"},
		{"missing destination", "AMS", "", "2026-05-15"},
		{"missing date", "AMS", "HEL", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SearchSkiplagged(context.Background(), tc.origin, tc.dest, tc.date, SearchOptions{})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestSearchSkiplagged_DisabledShortCircuits(t *testing.T) {
	prev := skiplaggedEnabled
	skiplaggedEnabled = false
	defer func() { skiplaggedEnabled = prev }()

	res, err := SearchSkiplagged(context.Background(), "AMS", "HEL", "2026-05-15", SearchOptions{})
	if err != nil {
		t.Fatalf("disabled path should not error: %v", err)
	}
	if res == nil || res.Flights != nil {
		t.Fatalf("disabled path should return non-nil result with nil Flights, got %+v", res)
	}
}

func TestParseSkiplaggedDuration(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"5h 30m", 330},
		{"5H30M", 330},
		{"PT5H30M", 330},
		{"pt2h", 120},
		{"45m", 45},
		{"12h", 720},
		{"1h 5m", 65},
		{"garbage", 0},
	}
	for _, tc := range cases {
		got := parseSkiplaggedDuration(tc.in)
		if got != tc.want {
			t.Errorf("parseSkiplaggedDuration(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestBuildSkiplaggedFlightSearchArgs_Mapping(t *testing.T) {
	args := buildSkiplaggedFlightSearchArgs("AMS", "HEL", "2026-05-15", SearchOptions{
		ReturnDate: "2026-05-22",
		Adults:     2,
		MaxStops:   models.NonStop,
		CabinClass: models.Business,
		SortBy:     models.SortCheapest,
		Airlines:   []string{"KL", "AY"},
	})

	if args["origin"] != "AMS" || args["destination"] != "HEL" {
		t.Errorf("origin/destination not propagated: %+v", args)
	}
	if args["returnDate"] != "2026-05-22" {
		t.Errorf("returnDate not propagated: %+v", args)
	}
	if args["adults"] != 2 {
		t.Errorf("adults not propagated: %+v", args)
	}
	if args["maxStops"] != "none" {
		t.Errorf("MaxStops=NonStop should map to \"none\", got %v", args["maxStops"])
	}
	if args["fareClass"] != "business" {
		t.Errorf("CabinClass=Business should map to fareClass=business, got %v", args["fareClass"])
	}
	if args["sort"] != "price" {
		t.Errorf("SortBy=Cheapest should map to sort=price, got %v", args["sort"])
	}
	airlines, _ := args["preferredAirlines"].([]string)
	if len(airlines) != 2 || airlines[0] != "KL" || airlines[1] != "AY" {
		t.Errorf("airlines not propagated: %+v", args["preferredAirlines"])
	}
}

func TestStripSSEFraming(t *testing.T) {
	bare := []byte(`{"a":1}`)
	if got := stripSSEFraming(bare); string(got) != string(bare) {
		t.Errorf("bare json should pass through unchanged")
	}
	sse := []byte("event: message\ndata: {\"a\":1}\n\n")
	if got := stripSSEFraming(sse); string(got) != `{"a":1}` {
		t.Errorf("SSE framing should be stripped, got %q", got)
	}
	noPayload := []byte("event: noop\n\n")
	if got := stripSSEFraming(noPayload); string(got) != string(noPayload) {
		t.Errorf("frame with no JSON payload should be returned as-is")
	}
}

func containsStr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestParseRetryAfter covers both Retry-After forms (RFC 7231 §7.1.3):
// delay-seconds and HTTP-date, plus the absent/garbage fallbacks that
// must yield 0 so the caller uses its default backoff.
func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("5"); got != 5*time.Second {
		t.Errorf("delay-seconds: got %v, want 5s", got)
	}
	if got := parseRetryAfter("0"); got != 0 {
		t.Errorf("zero seconds: got %v, want 0", got)
	}
	if got := parseRetryAfter(""); got != 0 {
		t.Errorf("empty: got %v, want 0", got)
	}
	if got := parseRetryAfter("-3"); got != 0 {
		t.Errorf("negative: got %v, want 0", got)
	}
	if got := parseRetryAfter("not-a-number"); got != 0 {
		t.Errorf("garbage: got %v, want 0", got)
	}
	// An HTTP-date in the past must not produce a negative or non-zero
	// wait — Until() is <=0, so we fall back to 0 (default backoff).
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(past); got != 0 {
		t.Errorf("past HTTP-date: got %v, want 0", got)
	}
	// An HTTP-date ~30s out should parse to a positive, bounded wait.
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got <= 0 || got > 31*time.Second {
		t.Errorf("future HTTP-date: got %v, want (0,31s]", got)
	}
}

// TestSkiplaggedMCPCallRetriesOn429 proves the rate-limit recovery path:
// a first tools/call answered 429 (with a Retry-After hint) is retried
// once and the second 200 succeeds. Before the transient-retry fix a 429
// was terminal, so this is the regression guard for that gap.
func TestSkiplaggedMCPCallRetriesOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&calls, 1) {
		case 1:
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"structuredContent":{"flights":[]}}}`))
		}
	}))
	defer srv.Close()
	restore := skiplaggedSetEndpointForTest(srv.URL)
	defer restore()

	raw, err := skiplaggedMCPCall(context.Background(), "", "sk_flights_search", map[string]any{})
	if err != nil {
		t.Fatalf("expected success after 429 retry, got error: %v", err)
	}
	if !strings.Contains(string(raw), "flights") {
		t.Fatalf("expected flights payload after retry, got: %s", raw)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected 2 calls (429 then 200), got %d", got)
	}
}
