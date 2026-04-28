package flights

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// --- SearchMultiAirport ---

func TestSearchMultiAirport_EmptyOrigins(t *testing.T) {
	_, err := SearchMultiAirport(t.Context(), nil, []string{"NRT"}, "2026-06-15", SearchOptions{})
	if err == nil {
		t.Error("expected error for empty origins")
	}
}

func TestSearchMultiAirport_EmptyDestinations(t *testing.T) {
	_, err := SearchMultiAirport(t.Context(), []string{"HEL"}, nil, "2026-06-15", SearchOptions{})
	if err == nil {
		t.Error("expected error for empty destinations")
	}
}

func TestSearchMultiAirport_EmptyDate(t *testing.T) {
	_, err := SearchMultiAirport(t.Context(), []string{"HEL"}, []string{"NRT"}, "", SearchOptions{})
	if err == nil {
		t.Error("expected error for empty date")
	}
}

func TestSearchMultiAirport_SameOrigDest(t *testing.T) {
	// When origin == destination, that combo is skipped; no flights found -> Success=false.
	result, err := SearchMultiAirport(t.Context(), []string{"HEL"}, []string{"HEL"}, "2026-06-15", SearchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected Success=false when origin==destination")
	}
}

// --- SearchFlightsWithClient with httptest ---

func flightsTestServer(t *testing.T, statusCode int, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		w.Write(body)
	}))
}

func makeFlightResponseBody(t *testing.T) []byte {
	t.Helper()
	// Build a valid Google Flights response structure.
	// inner data: index [2] has flight data
	leg := make([]any, 23)
	leg[3] = "HEL"
	leg[4] = "Helsinki"
	leg[5] = "Tokyo Narita"
	leg[6] = "NRT"
	leg[8] = []any{10.0, 30.0}
	leg[10] = []any{7.0, 15.0}
	leg[11] = 780.0
	leg[20] = []any{2026.0, 6.0, 15.0}
	leg[21] = []any{2026.0, 6.0, 16.0}
	leg[22] = []any{"AY", "79", nil, "Finnair"}

	flightInfo := make([]any, 13)
	flightInfo[2] = []any{leg}
	flightInfo[9] = 780.0
	flightInfo[12] = 450000.0 // emissions

	offer := make([]any, 7)
	offer[6] = []any{0.0, 1.0} // carry-on included, 1 checked bag

	flight := []any{flightInfo, []any{[]any{nil, 350.0}}, nil, nil, offer}

	inner := make([]any, 4)
	inner[2] = []any{[]any{flight}}

	innerJSON, _ := json.Marshal(inner)
	outer := []any{[]any{nil, nil, string(innerJSON)}}
	outerJSON, _ := json.Marshal(outer)

	return append([]byte(")]}'\n"), outerJSON...)
}

func TestSearchFlightsWithClient_NilClient(t *testing.T) {
	_, err := SearchFlightsWithClient(t.Context(), nil, "HEL", "NRT", "2026-06-15", SearchOptions{})
	if err == nil {
		t.Error("expected error for nil client")
	}
}

func TestSearchFlightsWithClient_Success(t *testing.T) {
	body := makeFlightResponseBody(t)
	ts := flightsTestServer(t, 200, body)
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	result, err := SearchFlightsWithClient(t.Context(), client, "HEL", "NRT", "2026-06-15", SearchOptions{})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if result.Count == 0 {
		t.Error("expected at least 1 flight")
	}
	if result.TripType != "one_way" {
		t.Errorf("trip type = %q, want one_way", result.TripType)
	}
	// Verify provider is set
	for _, f := range result.Flights {
		if f.Provider != "google_flights" {
			t.Errorf("provider = %q, want google_flights", f.Provider)
		}
		if f.BookingURL == "" {
			t.Error("expected non-empty booking URL")
		}
	}
}

func TestSearchFlightsWithClient_RoundTrip(t *testing.T) {
	body := makeFlightResponseBody(t)
	ts := flightsTestServer(t, 200, body)
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	result, err := SearchFlightsWithClient(t.Context(), client, "HEL", "NRT", "2026-06-15", SearchOptions{
		ReturnDate: "2026-06-22",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.TripType != "round_trip" {
		t.Errorf("trip type = %q, want round_trip", result.TripType)
	}
}

func TestSearchFlightsWithClient_403Blocked(t *testing.T) {
	ts := flightsTestServer(t, 403, []byte("Forbidden"))
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	_, err := SearchFlightsWithClient(t.Context(), client, "HEL", "NRT", "2026-06-15", SearchOptions{})
	if err == nil {
		t.Error("expected error for 403")
	}
}

func TestSearchFlightsWithClient_500Error(t *testing.T) {
	ts := flightsTestServer(t, 500, []byte("Internal Error"))
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	_, err := SearchFlightsWithClient(t.Context(), client, "HEL", "NRT", "2026-06-15", SearchOptions{})
	if err == nil {
		t.Error("expected error for 500")
	}
}

func TestSearchFlightsWithClient_BadResponseBody(t *testing.T) {
	ts := flightsTestServer(t, 200, []byte(")]}'\nnot json"))
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	_, err := SearchFlightsWithClient(t.Context(), client, "HEL", "NRT", "2026-06-15", SearchOptions{})
	if err == nil {
		t.Error("expected error for bad response body")
	}
}

func TestSearchFlightsWithClient_EmptyFlightData(t *testing.T) {
	// Valid JSON structure but no flights at indices [2] or [3]
	inner := make([]any, 2)
	innerJSON, _ := json.Marshal(inner)
	outer := []any{[]any{nil, nil, string(innerJSON)}}
	outerJSON, _ := json.Marshal(outer)
	body := append([]byte(")]}'\n"), outerJSON...)

	ts := flightsTestServer(t, 200, body)
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	_, err := SearchFlightsWithClient(t.Context(), client, "HEL", "NRT", "2026-06-15", SearchOptions{})
	if err == nil {
		t.Error("expected error for empty flight data")
	}
}

func TestSearchFlightsWithClient_ContextCancelled(t *testing.T) {
	ts := flightsTestServer(t, 200, []byte(""))
	defer ts.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	client := batchexec.NewTestClient(ts.URL)
	_, err := SearchFlightsWithClient(ctx, client, "HEL", "NRT", "2026-06-15", SearchOptions{})
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

// --- filterFlightResults comprehensive ---

func TestFilterFlightResults_MaxPrice(t *testing.T) {
	flights := []models.FlightResult{
		{Price: 100, Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00"}}},
		{Price: 500, Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00"}}},
		{Price: 200, Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00"}}},
	}
	got := filterFlightResults(flights, SearchOptions{MaxPrice: 300})
	if len(got) != 2 {
		t.Errorf("expected 2 flights under 300, got %d", len(got))
	}
}

func TestFilterFlightResults_MaxDuration(t *testing.T) {
	flights := []models.FlightResult{
		{Duration: 120, Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00"}}},
		{Duration: 600, Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00"}}},
	}
	got := filterFlightResults(flights, SearchOptions{MaxDuration: 300})
	if len(got) != 1 {
		t.Errorf("expected 1 flight under 300min, got %d", len(got))
	}
}

func TestFilterFlightResults_NonStop(t *testing.T) {
	flights := []models.FlightResult{
		{Stops: 0, Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00"}}},
		{Stops: 1, Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00"}}},
		{Stops: 2, Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00"}}},
	}
	got := filterFlightResults(flights, SearchOptions{MaxStops: models.NonStop})
	if len(got) != 1 {
		t.Errorf("expected 1 nonstop, got %d", len(got))
	}
}

func TestFilterFlightResults_OneStop(t *testing.T) {
	flights := []models.FlightResult{
		{Stops: 0, Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00"}}},
		{Stops: 1, Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00"}}},
		{Stops: 2, Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00"}}},
	}
	got := filterFlightResults(flights, SearchOptions{MaxStops: models.OneStop})
	if len(got) != 2 {
		t.Errorf("expected 2 flights with <=1 stop, got %d", len(got))
	}
}

func TestFilterFlightResults_DepartWindow(t *testing.T) {
	flights := []models.FlightResult{
		{Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T06:00"}}},
		{Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T14:00"}}},
		{Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T22:00"}}},
	}
	got := filterFlightResults(flights, SearchOptions{DepartAfter: "08:00", DepartBefore: "20:00"})
	if len(got) != 1 {
		t.Errorf("expected 1 flight in window, got %d", len(got))
	}
}

func TestFilterFlightResults_RequireCheckedBag(t *testing.T) {
	bags1 := 1
	bags0 := 0
	flights := []models.FlightResult{
		{CheckedBagsIncluded: &bags1, Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00"}}},
		{CheckedBagsIncluded: &bags0, Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00"}}},
		{Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00"}}}, // nil
	}
	got := filterFlightResults(flights, SearchOptions{RequireCheckedBag: true})
	if len(got) != 1 {
		t.Errorf("expected 1 flight with checked bag, got %d", len(got))
	}
}

func TestFilterFlightResults_EmptyInput(t *testing.T) {
	got := filterFlightResults(nil, SearchOptions{})
	if got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
}

func TestFilterFlightResults_CombinedFilters(t *testing.T) {
	bags1 := 1
	flights := []models.FlightResult{
		{Price: 100, Duration: 120, Stops: 0, CheckedBagsIncluded: &bags1,
			Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00", AirlineCode: "AY"}}},
		{Price: 600, Duration: 120, Stops: 0,
			Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00", AirlineCode: "AY"}}},
	}
	got := filterFlightResults(flights, SearchOptions{
		MaxPrice:          500,
		RequireCheckedBag: true,
		Airlines:          []string{"AY"},
	})
	if len(got) != 1 {
		t.Errorf("expected 1 flight matching all filters, got %d", len(got))
	}
}

// --- sortFlightResults additional coverage ---

func TestSortFlightResults_ByDuration(t *testing.T) {
	flights := []models.FlightResult{
		{Price: 200, Duration: 600},
		{Price: 200, Duration: 120},
		{Price: 200, Duration: 300},
	}
	sortFlightResults(flights, models.SortDuration)
	if flights[0].Duration != 120 || flights[1].Duration != 300 || flights[2].Duration != 600 {
		t.Errorf("duration sort failed: %d, %d, %d", flights[0].Duration, flights[1].Duration, flights[2].Duration)
	}
}

func TestSortFlightResults_SameDuration_FallbackToPrice(t *testing.T) {
	flights := []models.FlightResult{
		{Price: 500, Duration: 120},
		{Price: 100, Duration: 120},
	}
	sortFlightResults(flights, models.SortDuration)
	if flights[0].Price != 100 {
		t.Errorf("expected cheaper flight first when duration equal, got %v", flights[0].Price)
	}
}

func TestSortFlightResults_Default_FallbackToDuration(t *testing.T) {
	flights := []models.FlightResult{
		{Price: 200, Duration: 600},
		{Price: 200, Duration: 120},
	}
	sortFlightResults(flights, 0) // default sort
	if flights[0].Duration != 120 {
		t.Errorf("expected shorter duration first when price equal, got %d", flights[0].Duration)
	}
}

func TestSortFlightResults_TiebreakerByRoute(t *testing.T) {
	flights := []models.FlightResult{
		{Price: 200, Duration: 120, Provider: "google_flights",
			Legs: []models.FlightLeg{
				{DepartureAirport: models.AirportInfo{Code: "JFK"}, ArrivalAirport: models.AirportInfo{Code: "LAX"},
					DepartureTime: "2026-07-01T10:00"},
			}},
		{Price: 200, Duration: 120, Provider: "google_flights",
			Legs: []models.FlightLeg{
				{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "NRT"},
					DepartureTime: "2026-07-01T10:00"},
			}},
	}
	sortFlightResults(flights, 0)
	// HEL->NRT sorts before JFK->LAX alphabetically
	if flights[0].Legs[0].DepartureAirport.Code != "HEL" {
		t.Errorf("expected HEL first by route sort key, got %s", flights[0].Legs[0].DepartureAirport.Code)
	}
}

func TestSortFlightResults_TiebreakerByProvider(t *testing.T) {
	flights := []models.FlightResult{
		{Price: 200, Duration: 120, Provider: "kiwi",
			Legs: []models.FlightLeg{
				{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "NRT"},
					DepartureTime: "2026-07-01T10:00"},
			}},
		{Price: 200, Duration: 120, Provider: "google_flights",
			Legs: []models.FlightLeg{
				{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "NRT"},
					DepartureTime: "2026-07-01T10:00"},
			}},
	}
	sortFlightResults(flights, 0)
	if flights[0].Provider != "google_flights" {
		t.Errorf("expected google_flights first by provider sort, got %s", flights[0].Provider)
	}
}

// --- parseBagAllowance ---

func TestParseBagAllowance_ValidBags(t *testing.T) {
	offer := make([]any, 7)
	offer[6] = []any{0.0, 2.0} // carry-on included, 2 checked bags

	var fr models.FlightResult
	parseBagAllowance(offer, &fr)

	if fr.CarryOnIncluded == nil || !*fr.CarryOnIncluded {
		t.Error("expected carry-on included")
	}
	if fr.CheckedBagsIncluded == nil || *fr.CheckedBagsIncluded != 2 {
		t.Errorf("expected 2 checked bags, got %v", fr.CheckedBagsIncluded)
	}
}

func TestParseBagAllowance_NotIncluded(t *testing.T) {
	offer := make([]any, 7)
	offer[6] = []any{1.0, 0.0} // carry-on NOT included, 0 checked bags

	var fr models.FlightResult
	parseBagAllowance(offer, &fr)

	if fr.CarryOnIncluded == nil || *fr.CarryOnIncluded {
		t.Error("expected carry-on NOT included")
	}
	if fr.CheckedBagsIncluded == nil || *fr.CheckedBagsIncluded != 0 {
		t.Errorf("expected 0 checked bags, got %v", fr.CheckedBagsIncluded)
	}
}

func TestParseBagAllowance_NilOffer(t *testing.T) {
	var fr models.FlightResult
	parseBagAllowance(nil, &fr) // should not panic
	if fr.CarryOnIncluded != nil {
		t.Error("expected nil carry-on for nil offer")
	}
}

func TestParseBagAllowance_ShortOffer(t *testing.T) {
	var fr models.FlightResult
	parseBagAllowance([]any{nil, nil}, &fr) // too short, index 6 absent
	if fr.CarryOnIncluded != nil {
		t.Error("expected nil carry-on for short offer")
	}
}

func TestParseBagAllowance_BagArrayTooShort(t *testing.T) {
	offer := make([]any, 7)
	offer[6] = []any{0.0} // only 1 element instead of 2

	var fr models.FlightResult
	parseBagAllowance(offer, &fr)
	if fr.CarryOnIncluded != nil {
		t.Error("expected nil for bag array too short")
	}
}

func TestParseBagAllowance_BagArrayNotArray(t *testing.T) {
	offer := make([]any, 7)
	offer[6] = "not an array"

	var fr models.FlightResult
	parseBagAllowance(offer, &fr)
	if fr.CarryOnIncluded != nil {
		t.Error("expected nil for non-array bag data")
	}
}

// --- flightDeparture ---

func TestFlightDeparture_NoLegs(t *testing.T) {
	f := models.FlightResult{}
	got := flightDeparture(f)
	if !got.IsZero() {
		t.Errorf("expected zero time for no legs, got %v", got)
	}
}

func TestFlightDeparture_ValidTime(t *testing.T) {
	f := models.FlightResult{
		Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:30"}},
	}
	got := flightDeparture(f)
	if got.IsZero() {
		t.Error("expected non-zero time")
	}
	if got.Hour() != 10 || got.Minute() != 30 {
		t.Errorf("expected 10:30, got %v", got)
	}
}

func TestFlightDeparture_InvalidTime(t *testing.T) {
	f := models.FlightResult{
		Legs: []models.FlightLeg{{DepartureTime: "invalid"}},
	}
	got := flightDeparture(f)
	if !got.IsZero() {
		t.Errorf("expected zero time for invalid departure, got %v", got)
	}
}

// --- flightSortKey ---

func TestFlightSortKey_NoLegs(t *testing.T) {
	f := models.FlightResult{}
	if got := flightSortKey(f); got != "" {
		t.Errorf("expected empty sort key, got %q", got)
	}
}

func TestFlightSortKey_MultiLegs(t *testing.T) {
	f := models.FlightResult{
		Legs: []models.FlightLeg{
			{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "FRA"}},
			{DepartureAirport: models.AirportInfo{Code: "FRA"}, ArrivalAirport: models.AirportInfo{Code: "NRT"}},
		},
	}
	got := flightSortKey(f)
	if got != "HEL->FRA->NRT" {
		t.Errorf("sort key = %q, want HEL->FRA->NRT", got)
	}
}

// --- extractKiwiContent ---

func TestExtractKiwiContent_RPCError(t *testing.T) {
	rpc := kiwiRPCResponse{
		Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: -1, Message: "test error"},
	}
	_, err := extractKiwiContent(rpc)
	if err == nil {
		t.Error("expected error for RPC error response")
	}
}

func TestExtractKiwiContent_NilResult(t *testing.T) {
	rpc := kiwiRPCResponse{}
	_, err := extractKiwiContent(rpc)
	if err == nil {
		t.Error("expected error for nil result")
	}
}

func TestExtractKiwiContent_ToolError(t *testing.T) {
	result := json.RawMessage(`{"content":[{"type":"text","text":"error"}],"isError":true}`)
	rpc := kiwiRPCResponse{Result: result}
	_, err := extractKiwiContent(rpc)
	if err == nil {
		t.Error("expected error for tool error response")
	}
}

func TestExtractKiwiContent_NoTextContent(t *testing.T) {
	result := json.RawMessage(`{"content":[{"type":"image","text":""}]}`)
	rpc := kiwiRPCResponse{Result: result}
	_, err := extractKiwiContent(rpc)
	if err == nil {
		t.Error("expected error for no text content")
	}
}

func TestExtractKiwiContent_EmptyText(t *testing.T) {
	result := json.RawMessage(`{"content":[{"type":"text","text":""}]}`)
	rpc := kiwiRPCResponse{Result: result}
	_, err := extractKiwiContent(rpc)
	if err == nil {
		t.Error("expected error for empty text content")
	}
}

func TestExtractKiwiContent_ValidText(t *testing.T) {
	result := json.RawMessage(`{"content":[{"type":"text","text":"[{\"price\":100}]"}]}`)
	rpc := kiwiRPCResponse{Result: result}
	got, err := extractKiwiContent(rpc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != `[{"price":100}]` {
		t.Errorf("got %q", string(got))
	}
}

func TestExtractKiwiContent_TooLargeText(t *testing.T) {
	// Build text > 512KB
	bigText := make([]byte, 513*1024)
	for i := range bigText {
		bigText[i] = 'x'
	}
	result := json.RawMessage(fmt.Sprintf(`{"content":[{"type":"text","text":"%s"}]}`, string(bigText)))
	rpc := kiwiRPCResponse{Result: result}
	_, err := extractKiwiContent(rpc)
	if err == nil {
		t.Error("expected error for too-large text")
	}
}

func TestExtractKiwiContent_InvalidResultJSON(t *testing.T) {
	result := json.RawMessage(`not valid json`)
	rpc := kiwiRPCResponse{Result: result}
	_, err := extractKiwiContent(rpc)
	if err == nil {
		t.Error("expected error for invalid result JSON")
	}
}

// --- parseKiwiRPCResponse ---

func TestParseKiwiRPCResponse_DirectJSON(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"result":{"test":true}}`)
	rpc, err := parseKiwiRPCResponse(body)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if rpc.Result == nil {
		t.Error("expected non-nil result")
	}
}

func TestParseKiwiRPCResponse_SSEFormat(t *testing.T) {
	body := []byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"test\":true}}\n\n")
	rpc, err := parseKiwiRPCResponse(body)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if rpc.Result == nil {
		t.Error("expected non-nil result from SSE")
	}
}

func TestParseKiwiRPCResponse_SSEMultipleFrames(t *testing.T) {
	// Multiple SSE data frames; should take the last one with result.
	body := []byte("data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"first\":true}}\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"last\":true}}\n\n")
	rpc, err := parseKiwiRPCResponse(body)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if rpc.ID != 2 {
		t.Errorf("expected last frame (id=2), got id=%d", rpc.ID)
	}
}

func TestParseKiwiRPCResponse_SSEWithDone(t *testing.T) {
	body := []byte("data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n\ndata: [DONE]\n\n")
	rpc, err := parseKiwiRPCResponse(body)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if rpc.Result == nil {
		t.Error("expected result, [DONE] should be skipped")
	}
}

func TestParseKiwiRPCResponse_NoUsableResponse(t *testing.T) {
	body := []byte("event: ping\ndata: [DONE]\n\n")
	_, err := parseKiwiRPCResponse(body)
	if err == nil {
		t.Error("expected error for no usable response")
	}
}

func TestParseKiwiRPCResponse_SSEWithError(t *testing.T) {
	body := []byte(`data: {"jsonrpc":"2.0","id":1,"error":{"code":-1,"message":"fail"}}` + "\n\n")
	rpc, err := parseKiwiRPCResponse(body)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if rpc.Error == nil {
		t.Error("expected error in RPC response")
	}
}

// --- mergeFlightResults ---

func TestMergeFlightResults_Dedup(t *testing.T) {
	google := []models.FlightResult{
		{Price: 200, Duration: 120, Provider: "google_flights"},
	}
	kiwi := []models.FlightResult{
		{Price: 180, Duration: 130, Provider: "kiwi"},
	}
	got := mergeFlightResults(google, kiwi, nil, SearchOptions{})
	if len(got) != 2 {
		t.Errorf("expected 2 merged flights, got %d", len(got))
	}
	// Should be sorted by price (default)
	if got[0].Price > got[1].Price {
		t.Error("expected cheaper flight first")
	}
}

func TestMergeFlightResults_WithFilters(t *testing.T) {
	all := []models.FlightResult{
		{Price: 100, Duration: 120, Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00"}}},
		{Price: 600, Duration: 120, Legs: []models.FlightLeg{{DepartureTime: "2026-07-01T10:00"}}},
	}
	got := mergeFlightResults(all, nil, nil, SearchOptions{MaxPrice: 500})
	if len(got) != 1 {
		t.Errorf("expected 1 flight after merge+filter, got %d", len(got))
	}
}

// --- mapKiwiItinerary ---

func TestMapKiwiItinerary_Direct(t *testing.T) {
	itinerary := kiwiItinerary{
		FlyFrom:           "HEL",
		FlyTo:             "NRT",
		CityFrom:          "Helsinki",
		CityTo:            "Tokyo",
		Departure:         kiwiDateTime{UTC: "2026-06-15T10:00:00Z", Local: "2026-06-15T13:00:00"},
		Arrival:           kiwiDateTime{UTC: "2026-06-16T01:00:00Z", Local: "2026-06-16T10:00:00"},
		DurationInSeconds: 54000,
		Price:             450,
		Currency:          "EUR",
		DeepLink:          "https://kiwi.com/booking/123",
	}
	fr := mapKiwiItinerary(itinerary, "USD")
	if fr.Price != 450 {
		t.Errorf("price = %v, want 450", fr.Price)
	}
	if fr.Currency != "EUR" {
		t.Errorf("currency = %q, want EUR", fr.Currency)
	}
	if fr.Provider != "kiwi" {
		t.Errorf("provider = %q, want kiwi", fr.Provider)
	}
	if fr.Duration != 900 {
		t.Errorf("duration = %d, want 900", fr.Duration)
	}
	if fr.SelfConnect {
		t.Error("expected no self-connect for direct flight")
	}
	if len(fr.Legs) != 1 {
		t.Errorf("expected 1 leg, got %d", len(fr.Legs))
	}
}

func TestMapKiwiItinerary_WithLayover(t *testing.T) {
	itinerary := kiwiItinerary{
		FlyFrom:           "HEL",
		FlyTo:             "NRT",
		CityFrom:          "Helsinki",
		CityTo:            "Tokyo",
		Departure:         kiwiDateTime{UTC: "2026-06-15T10:00:00Z"},
		Arrival:           kiwiDateTime{UTC: "2026-06-16T01:00:00Z"},
		DurationInSeconds: 54000,
		Price:             350,
		Currency:          "",
		Layovers: []kiwiLayover{
			{
				At:        "FRA",
				City:      "Frankfurt",
				Arrival:   kiwiDateTime{UTC: "2026-06-15T13:00:00Z"},
				Departure: kiwiDateTime{UTC: "2026-06-15T15:00:00Z"},
			},
		},
	}
	fr := mapKiwiItinerary(itinerary, "EUR")
	if !fr.SelfConnect {
		t.Error("expected self-connect with layover")
	}
	if len(fr.Warnings) == 0 {
		t.Error("expected warning for self-connect")
	}
	if fr.Currency != "EUR" {
		t.Errorf("expected fallback currency EUR, got %q", fr.Currency)
	}
	if len(fr.Legs) != 2 {
		t.Errorf("expected 2 legs, got %d", len(fr.Legs))
	}
	if fr.Stops != 1 {
		t.Errorf("expected 1 stop, got %d", fr.Stops)
	}
}

func TestMapKiwiItinerary_TotalDurationFallback(t *testing.T) {
	itinerary := kiwiItinerary{
		FlyFrom:                "HEL",
		FlyTo:                  "NRT",
		DurationInSeconds:      0,
		TotalDurationInSeconds: 7200,
		Departure:              kiwiDateTime{UTC: "2026-06-15T10:00:00Z"},
		Arrival:                kiwiDateTime{UTC: "2026-06-15T12:00:00Z"},
		Price:                  100,
	}
	fr := mapKiwiItinerary(itinerary, "EUR")
	if fr.Duration != 120 {
		t.Errorf("duration = %d, want 120 (from TotalDurationInSeconds)", fr.Duration)
	}
}

// --- parseOneFlight with emissions ---

func TestParseOneFlight_WithEmissions(t *testing.T) {
	flightInfo := make([]any, 13)
	flightInfo[12] = float64(450000) // 450kg CO2
	entry := []any{flightInfo, []any{[]any{nil, 200.0}}}

	fr, err := parseOneFlight(entry)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if fr.Emissions != 450000 {
		t.Errorf("emissions = %d, want 450000", fr.Emissions)
	}
}

func TestParseOneFlight_ZeroEmissions(t *testing.T) {
	flightInfo := make([]any, 13)
	flightInfo[12] = float64(0)
	entry := []any{flightInfo, []any{[]any{nil, 200.0}}}

	fr, err := parseOneFlight(entry)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if fr.Emissions != 0 {
		t.Errorf("emissions = %d, want 0", fr.Emissions)
	}
}

// --- parseFlights with mixed entries ---

func TestParseFlights_MixedValid(t *testing.T) {
	flightInfo := make([]any, 10)
	validEntry := []any{flightInfo, []any{[]any{nil, 100.0}}}

	raw := []any{
		validEntry,
		"not an array",
		nil,
		[]any{}, // too short (< 2 elements)
		validEntry,
	}
	got := parseFlights(raw)
	if len(got) != 2 {
		t.Errorf("expected 2 valid flights, got %d", len(got))
	}
}

// --- parseOneLeg with aircraft ---

func TestParseOneLeg_WithAircraft(t *testing.T) {
	leg := make([]any, 23)
	leg[3] = "HEL"
	leg[6] = "NRT"
	leg[17] = "Airbus A350"
	fl := parseOneLeg(leg)
	if fl.Aircraft != "Airbus A350" {
		t.Errorf("aircraft = %q, want Airbus A350", fl.Aircraft)
	}
}

// --- computeLayovers ---

func TestComputeLayovers_ThreeLegs(t *testing.T) {
	legs := []models.FlightLeg{
		{ArrivalTime: "2026-07-01T12:00"},
		{DepartureTime: "2026-07-01T14:30", ArrivalTime: "2026-07-01T18:00"},
		{DepartureTime: "2026-07-01T20:00"},
	}
	computeLayovers(legs)
	if legs[1].LayoverMinutes != 150 { // 2h30m
		t.Errorf("leg[1] layover = %d, want 150", legs[1].LayoverMinutes)
	}
	if legs[2].LayoverMinutes != 120 { // 2h
		t.Errorf("leg[2] layover = %d, want 120", legs[2].LayoverMinutes)
	}
}

func TestComputeLayovers_MissingArrival(t *testing.T) {
	legs := []models.FlightLeg{
		{ArrivalTime: ""},
		{DepartureTime: "2026-07-01T14:30"},
	}
	computeLayovers(legs)
	if legs[1].LayoverMinutes != 0 {
		t.Errorf("expected 0 layover for missing arrival, got %d", legs[1].LayoverMinutes)
	}
}

func TestComputeLayovers_InvalidTimes(t *testing.T) {
	legs := []models.FlightLeg{
		{ArrivalTime: "not-a-time"},
		{DepartureTime: "2026-07-01T14:30"},
	}
	computeLayovers(legs)
	if legs[1].LayoverMinutes != 0 {
		t.Errorf("expected 0 layover for invalid times, got %d", legs[1].LayoverMinutes)
	}
}

// --- toString ---

func TestToString_Nil(t *testing.T) {
	if got := toString(nil); got != "" {
		t.Errorf("toString(nil) = %q, want empty", got)
	}
}

func TestToString_Float64Integer(t *testing.T) {
	if got := toString(float64(42)); got != "42" {
		t.Errorf("toString(42.0) = %q, want 42", got)
	}
}

func TestToString_Float64Decimal(t *testing.T) {
	got := toString(float64(3.14))
	if got != "3.14" {
		t.Errorf("toString(3.14) = %q, want 3.14", got)
	}
}

// --- CalendarOptions defaults ---

func TestCalendarOptions_DefaultsFull(t *testing.T) {
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

func TestCalendarOptions_RoundTripDefaults(t *testing.T) {
	opts := CalendarOptions{RoundTrip: true}
	opts.defaults()
	if opts.TripLength != 7 {
		t.Errorf("TripLength = %d, want 7", opts.TripLength)
	}
}

// --- GridOptions defaults ---

func TestGridOptions_DefaultsFull(t *testing.T) {
	opts := GridOptions{}
	opts.defaults()
	if opts.Adults != 1 {
		t.Errorf("Adults = %d, want 1", opts.Adults)
	}
	if opts.DepartFrom == "" {
		t.Error("DepartFrom should be set")
	}
	if opts.DepartTo == "" {
		t.Error("DepartTo should be set")
	}
	if opts.ReturnFrom == "" {
		t.Error("ReturnFrom should be set")
	}
	if opts.ReturnTo == "" {
		t.Error("ReturnTo should be set")
	}
}

// --- buildFilters round-trip ---

func TestBuildFilters_RoundTripSegments(t *testing.T) {
	opts := SearchOptions{Adults: 1, ReturnDate: "2026-06-22"}
	filters := buildFilters("HEL", "NRT", "2026-06-15", opts)

	data, _ := json.Marshal(filters)
	var arr []any
	json.Unmarshal(data, &arr)

	settings := arr[1].([]any)
	// Trip type should be 1 (round-trip)
	tripType := int(settings[2].(float64))
	if tripType != 1 {
		t.Errorf("trip type = %d, want 1 (round-trip)", tripType)
	}

	// Should have 2 segments
	segments := settings[13].([]any)
	if len(segments) != 2 {
		t.Errorf("expected 2 segments for round-trip, got %d", len(segments))
	}
}

// --- buildFilters with max price ---

func TestBuildFilters_MaxPrice(t *testing.T) {
	opts := SearchOptions{Adults: 1, MaxPrice: 500}
	filters := buildFilters("HEL", "NRT", "2026-06-15", opts)

	data, _ := json.Marshal(filters)
	var arr []any
	json.Unmarshal(data, &arr)

	settings := arr[1].([]any)
	maxPrice := settings[7].(float64)
	if int(maxPrice) != 500 {
		t.Errorf("max price = %v, want 500", maxPrice)
	}
}

// --- buildFilters with bags ---

func TestBuildFilters_WithBags(t *testing.T) {
	opts := SearchOptions{Adults: 1, CarryOnBags: 1, CheckedBags: 2}
	filters := buildFilters("HEL", "NRT", "2026-06-15", opts)

	data, _ := json.Marshal(filters)
	var arr []any
	json.Unmarshal(data, &arr)

	settings := arr[1].([]any)
	bags := settings[10].([]any)
	if int(bags[0].(float64)) != 1 || int(bags[1].(float64)) != 2 {
		t.Errorf("bags = %v, want [1,2]", bags)
	}
}

// --- buildFilters with exclude basic ---

func TestBuildFilters_ExcludeBasic(t *testing.T) {
	opts := SearchOptions{Adults: 1, ExcludeBasic: true}
	filters := buildFilters("HEL", "NRT", "2026-06-15", opts)

	data, _ := json.Marshal(filters)
	var arr []any
	json.Unmarshal(data, &arr)

	settings := arr[1].([]any)
	exclude := int(settings[28].(float64))
	if exclude != 1 {
		t.Errorf("exclude basic = %d, want 1", exclude)
	}
}

// --- buildFilters with alliances ---

func TestBuildFilters_WithAlliancesInSegment(t *testing.T) {
	opts := SearchOptions{Adults: 1, Alliances: []string{"star_alliance"}}
	filters := buildFilters("HEL", "NRT", "2026-06-15", opts)

	data, _ := json.Marshal(filters)
	var arr []any
	json.Unmarshal(data, &arr)

	settings := arr[1].([]any)
	segments := settings[13].([]any)
	seg := segments[0].([]any)
	alliances := seg[5].([]any)
	if len(alliances) != 1 || alliances[0] != "STAR_ALLIANCE" {
		t.Errorf("alliances = %v, want [STAR_ALLIANCE]", alliances)
	}
}

// --- buildFilters with depart time ---

func TestBuildFilters_WithDepartTime(t *testing.T) {
	opts := SearchOptions{Adults: 1, DepartAfter: "08:00", DepartBefore: "18:00"}
	filters := buildFilters("HEL", "NRT", "2026-06-15", opts)

	data, _ := json.Marshal(filters)
	var arr []any
	json.Unmarshal(data, &arr)

	settings := arr[1].([]any)
	segments := settings[13].([]any)
	seg := segments[0].([]any)
	timeWindow := seg[2].([]any)
	if int(timeWindow[0].(float64)) != 8 || int(timeWindow[1].(float64)) != 18 {
		t.Errorf("time window = %v, want [8, 18]", timeWindow)
	}
}

// --- buildFilters with emissions ---

func TestBuildFilters_LessEmissions(t *testing.T) {
	opts := SearchOptions{Adults: 1, LessEmissions: true}
	filters := buildFilters("HEL", "NRT", "2026-06-15", opts)

	data, _ := json.Marshal(filters)
	var arr []any
	json.Unmarshal(data, &arr)

	settings := arr[1].([]any)
	segments := settings[13].([]any)
	seg := segments[0].([]any)
	emissions := seg[13].([]any)
	if len(emissions) != 1 || emissions[0].(float64) != 1 {
		t.Errorf("emissions = %v, want [1]", emissions)
	}
}

// --- buildFilters with duration limit ---

func TestBuildFilters_DurationLimit(t *testing.T) {
	opts := SearchOptions{Adults: 1, MaxDuration: 720}
	filters := buildFilters("HEL", "NRT", "2026-06-15", opts)

	data, _ := json.Marshal(filters)
	var arr []any
	json.Unmarshal(data, &arr)

	settings := arr[1].([]any)
	segments := settings[13].([]any)
	seg := segments[0].([]any)
	duration := int(seg[7].(float64))
	if duration != 720 {
		t.Errorf("duration limit = %d, want 720", duration)
	}
}

// --- departTimeWindow ---

func TestDepartTimeWindow_OnlyAfterBound(t *testing.T) {
	got := departTimeWindow("08:00", "")
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected array, got %T", got)
	}
	if arr[0] != 8 || arr[1] != 24 {
		t.Errorf("expected [8, 24], got %v", arr)
	}
}

func TestDepartTimeWindow_OnlyBeforeBound(t *testing.T) {
	got := departTimeWindow("", "18:00")
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected array, got %T", got)
	}
	if arr[0] != 0 || arr[1] != 18 {
		t.Errorf("expected [0, 18], got %v", arr)
	}
}

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

func TestParsePrice_DefaultCurrency(t *testing.T) {
	// Price present but no currency info -> defaults to USD
	raw := []any{[]any{nil, float64(100)}}
	price, currency := parsePrice(raw)
	if price != 100 {
		t.Errorf("price = %v, want 100", price)
	}
	if currency != "USD" {
		t.Errorf("currency = %q, want USD", currency)
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
	// Should return some currency (from parsed flights or fallback)
	if got == "" {
		t.Error("expected non-empty currency")
	}
}

func TestDetectSourceCurrencyWithClient_ServerError(t *testing.T) {
	ts := flightsTestServer(t, 500, []byte("error"))
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	got := detectSourceCurrencyWithClient(t.Context(), client, "HEL", "NRT", "2026-06-15")
	if got != "EUR" {
		t.Errorf("expected EUR fallback on error, got %q", got)
	}
}

func TestDetectSourceCurrencyWithClient_BadBody(t *testing.T) {
	ts := flightsTestServer(t, 200, []byte(")]}'\nnot json"))
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	got := detectSourceCurrencyWithClient(t.Context(), client, "HEL", "NRT", "2026-06-15")
	if got != "EUR" {
		t.Errorf("expected EUR fallback on bad body, got %q", got)
	}
}

func TestDetectSourceCurrencyWithClient_EmptyFlights(t *testing.T) {
	// Valid structure but no flights -> EUR fallback
	inner := make([]any, 2)
	innerJSON, _ := json.Marshal(inner)
	outer := []any{[]any{nil, nil, string(innerJSON)}}
	outerJSON, _ := json.Marshal(outer)
	body := append([]byte(")]}'\n"), outerJSON...)

	ts := flightsTestServer(t, 200, body)
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	got := detectSourceCurrencyWithClient(t.Context(), client, "HEL", "NRT", "2026-06-15")
	if got != "EUR" {
		t.Errorf("expected EUR for empty flights, got %q", got)
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

	// First call populates cache
	first := DetectSourceCurrencyWithClient(t.Context(), client, "HEL", "NRT")
	if first == "" {
		t.Fatal("expected non-empty currency from first call")
	}

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
