package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/trips"
	"os"
	"fmt"
	"time"
)

func TestHandleSearchHotelByName_InvalidDateRange(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchHotelByName(context.Background(), map[string]any{
		"name": "Hilton Helsinki", "check_in": "2099-06-18", "check_out": "2099-06-15",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for check_out before check_in")
	}
}

func TestHandleHotelRooms_InvalidDateRange_Boost(t *testing.T) {
	t.Parallel()
	_, _, err := handleHotelRooms(context.Background(), map[string]any{
		"hotel_name": "Test Hotel", "check_in": "2099-06-18", "check_out": "2099-06-15",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for check_out before check_in")
	}
}

func TestHandleSearchFlights_PastDate_Boost(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchFlights(context.Background(), map[string]any{
		"origin": "HEL", "destination": "BCN", "departure_date": "2020-01-01",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for past date")
	}
}

func TestHandleSearchFlights_InvalidReturnDate_Boost(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchFlights(context.Background(), map[string]any{
		"origin": "HEL", "destination": "BCN",
		"departure_date": "2099-06-15", "return_date": "not-a-date",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid return_date")
	}
}

func TestHandleSearchFlights_InvalidDestinationIATA(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchFlights(context.Background(), map[string]any{
		"origin": "HEL", "destination": "X", "departure_date": "2099-06-15",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid destination IATA")
	}
}

func TestHandleSearchDates_InvalidDateRange_Boost(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchDates(context.Background(), map[string]any{
		"origin": "HEL", "destination": "BCN",
		"start_date": "2099-06-30", "end_date": "2099-06-01",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for end_date before start_date")
	}
}

func TestHandleSearchDates_InvalidOriginIATA(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchDates(context.Background(), map[string]any{
		"origin": "X", "destination": "BCN",
		"start_date": "2099-06-01", "end_date": "2099-06-30",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid origin IATA")
	}
}

func TestHandleInitialize_WithCapabilities(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := map[string]interface{}{
		"capabilities": map[string]interface{}{
			"sampling": map[string]interface{}{}, "elicitation": map[string]interface{}{},
		},
	}
	raw, _ := json.Marshal(params)
	req := &Request{JSONRPC: "2.0", ID: float64(1), Method: "initialize", Params: json.RawMessage(raw)}
	resp := s.HandleRequest(req)
	if resp == nil || resp.Error != nil {
		t.Fatal("expected successful response")
	}
}

func TestHandlePromptsGet_InvalidParams(t *testing.T) {
	t.Parallel()
	s := NewServer()
	resp := s.HandleRequest(&Request{JSONRPC: "2.0", ID: float64(1), Method: "prompts/get", Params: json.RawMessage(`{bad}`)})
	if resp.Error == nil {
		t.Error("expected error for invalid params JSON")
	}
}

func TestHandleResourcesRead_InvalidParams(t *testing.T) {
	t.Parallel()
	s := NewServer()
	resp := s.HandleRequest(&Request{JSONRPC: "2.0", ID: float64(1), Method: "resources/read", Params: json.RawMessage(`{bad}`)})
	if resp.Error == nil {
		t.Error("expected error for invalid params JSON")
	}
}

func TestHandleToolsCall_InvalidJSON(t *testing.T) {
	t.Parallel()
	s := NewServer()
	resp := s.HandleRequest(&Request{JSONRPC: "2.0", ID: float64(1), Method: "tools/call", Params: json.RawMessage(`{bad}`)})
	if resp.Error == nil {
		t.Error("expected error for invalid params JSON")
	}
}

func TestMakeElicitFunc_NoCapability(t *testing.T) {
	t.Parallel()
	s := NewServer()
	if s.makeElicitFunc() != nil {
		t.Error("expected nil ElicitFunc when client has no elicitation capability")
	}
}

func TestMakeElicitFunc_CapabilityButNoWriter(t *testing.T) {
	t.Parallel()
	s := NewServer()
	s.clientCapabilities.Elicitation = &ElicitationCapability{}
	if s.makeElicitFunc() != nil {
		t.Error("expected nil ElicitFunc when no notifyWriter")
	}
}

func TestMakeElicitFunc_CapabilityAndWriterButNoReader(t *testing.T) {
	t.Parallel()
	s := NewServer()
	s.clientCapabilities.Elicitation = &ElicitationCapability{}
	s.notifyMu.Lock()
	s.notifyWriter = &bytes.Buffer{}
	s.notifyMu.Unlock()
	if s.makeElicitFunc() != nil {
		t.Error("expected nil ElicitFunc when no elicitReader")
	}
}

func TestWriteJSON_MarshalError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if writeJSON(&buf, make(chan int)) == nil {
		t.Error("expected error for non-marshalable value")
	}
}

func TestArgStringSliceOrJSON_NativeSlice(t *testing.T) {
	t.Parallel()
	result := argStringSliceOrJSON(map[string]any{"k": []any{"HEL", "AMS"}}, "k")
	if len(result) != 2 {
		t.Errorf("expected 2, got %d", len(result))
	}
}

func TestArgStringSliceOrJSON_JSONString(t *testing.T) {
	t.Parallel()
	result := argStringSliceOrJSON(map[string]any{"k": `["HEL","AMS"]`}, "k")
	if len(result) != 2 {
		t.Errorf("expected 2 from JSON, got %d", len(result))
	}
}

func TestArgStringSliceOrJSON_CommaString(t *testing.T) {
	t.Parallel()
	result := argStringSliceOrJSON(map[string]any{"k": "HEL,AMS,BCN"}, "k")
	if len(result) != 3 {
		t.Errorf("expected 3, got %d", len(result))
	}
}

func TestArgStringSliceOrJSON_MissingKey(t *testing.T) {
	t.Parallel()
	if argStringSliceOrJSON(map[string]any{"x": "y"}, "k") != nil {
		t.Error("expected nil for missing key")
	}
}

func TestArgStringSliceOrJSON_NonStringNonSlice(t *testing.T) {
	t.Parallel()
	if argStringSliceOrJSON(map[string]any{"k": 42}, "k") != nil {
		t.Error("expected nil for int value")
	}
}

func TestArgStringSliceOrJSON_EmptyString(t *testing.T) {
	t.Parallel()
	if argStringSliceOrJSON(map[string]any{"k": ""}, "k") != nil {
		t.Error("expected nil for empty string")
	}
}

func TestMergeDistricts_MapInput(t *testing.T) {
	t.Parallel()
	p := &preferences.Preferences{}
	mergeDistricts(p, map[string]any{"Helsinki": []any{"Kallio", "Kamppi"}})
	if len(p.PreferredDistricts["Helsinki"]) != 2 {
		t.Errorf("expected 2 districts, got %d", len(p.PreferredDistricts["Helsinki"]))
	}
}

func TestMergeDistricts_JSONString(t *testing.T) {
	t.Parallel()
	p := &preferences.Preferences{}
	mergeDistricts(p, `{"Helsinki":["Kallio"]}`)
	if len(p.PreferredDistricts) != 1 {
		t.Errorf("expected 1 city, got %d", len(p.PreferredDistricts))
	}
}

func TestMergeDistricts_EmptyString(t *testing.T) {
	t.Parallel()
	p := &preferences.Preferences{PreferredDistricts: map[string][]string{"K": {"D"}}}
	mergeDistricts(p, "")
	if len(p.PreferredDistricts) != 1 {
		t.Error("expected unchanged")
	}
}

func TestMergeDistricts_InvalidJSON(t *testing.T) {
	t.Parallel()
	p := &preferences.Preferences{PreferredDistricts: map[string][]string{"K": {"D"}}}
	mergeDistricts(p, "bad-json")
	if len(p.PreferredDistricts) != 1 {
		t.Error("expected unchanged")
	}
}

func TestMergeDistricts_NonMapNonString(t *testing.T) {
	t.Parallel()
	p := &preferences.Preferences{}
	mergeDistricts(p, 42)
	if p.PreferredDistricts != nil {
		t.Error("expected nil")
	}
}

func TestHTTPHandler_CORS_127001(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServer(0)
	body, _ := json.Marshal(Request{JSONRPC: "2.0", ID: float64(1), Method: "ping"})
	rr := httptest.NewRecorder()
	httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	httpReq.Header.Set("Origin", "http://127.0.0.1:8080")
	hs.handleMCP(rr, httpReq)
	if rr.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:8080" {
		t.Error("expected CORS header for 127.0.0.1:8080")
	}
}

func TestHTTPHandler_POST_EmptyBody(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServer(0)
	rr := httptest.NewRecorder()
	hs.handleMCP(rr, httptest.NewRequest("POST", "/mcp", bytes.NewReader(nil)))
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestReadOnboarding_NoPrefs(t *testing.T) {
	t.Parallel()
	result, err := readOnboarding()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Error("expected content")
	}
}

func TestHandleAddBookingWithPath_WithRouteAndPrice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")
	content, _, err := handleAddBookingWithPath(map[string]any{
		"type": "hotel", "provider": "Booking.com",
		"from": "Helsinki", "to": "Helsinki",
		"price": float64(120), "currency": "EUR",
		"nights": 3, "stars": 4, "date": "2099-06-15",
	}, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, cb := range content {
		if strings.Contains(cb.Text, "Booking.com") {
			found = true
		}
	}
	if !found {
		t.Error("expected summary to mention provider name")
	}
}

func TestServeStdio_BatchRequest(t *testing.T) {
	t.Parallel()
	s := NewServer()
	var in bytes.Buffer
	r1, _ := json.Marshal(Request{JSONRPC: "2.0", ID: float64(1), Method: "ping"})
	r2, _ := json.Marshal(Request{JSONRPC: "2.0", ID: float64(2), Method: "tools/list"})
	in.Write(r1)
	in.WriteString("\n")
	in.Write(r2)
	in.WriteString("\n")
	var out bytes.Buffer
	if err := s.ServeStdio(&in, &out); err != nil {
		t.Fatalf("ServeStdio error: %v", err)
	}
	if len(strings.Split(strings.TrimSpace(out.String()), "\n")) != 2 {
		t.Error("expected 2 response lines")
	}
}

func TestBuildProfileSummary_AllOptionalFieldsFilled(t *testing.T) {
	t.Parallel()
	p := &preferences.Preferences{
		HomeAirports: []string{"HEL"}, HomeCities: []string{"Helsinki"},
		DisplayCurrency: "EUR", Nationality: "FI",
		FrequentFlyerPrograms: []preferences.FrequentFlyerStatus{
			{Alliance: "oneworld", Tier: "sapphire", AirlineCode: "AY"},
		},
		LoyaltyAirlines: []string{"AY"}, LoungeCards: []string{"Priority Pass"},
		LoyaltyHotels: []string{"Marriott Bonvoy"},
		BudgetPerNightMin: 80, BudgetPerNightMax: 200, BudgetFlightMax: 500,
	}
	s := buildProfileSummary(p)
	for _, want := range []string{"HEL", "EUR", "oneworld", "Priority Pass"} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q in summary", want)
		}
	}
}

// ============================================================
// readTripsUpcoming -- with trip data (22.6% to higher)
// ============================================================

func TestReadTripsUpcoming_WithTrip(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") != "1" {
		t.Skip("hits live external APIs; set TRVL_TEST_LIVE_INTEGRATIONS=1 to run. Tracked in #45")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Create a trips directory and add a trip with legs.
	tripsDir := filepath.Join(tmp, ".trvl")
	os.MkdirAll(tripsDir, 0o755)
	store := trips.NewStore(tripsDir)
	store.Load()

	futureDate := time.Now().Add(7 * 24 * time.Hour)
	_, err := store.Add(trips.Trip{
		Name:   "Barcelona Trip",
		Status: "booked",
		Legs: []trips.TripLeg{
			{
				Type:      "flight",
				From:      "HEL",
				To:        "BCN",
				Provider:  "Finnair",
				StartTime: futureDate.Format(time.RFC3339),
				EndTime:   futureDate.Add(4 * time.Hour).Format(time.RFC3339),
				Price:     199,
				Currency:  "EUR",
				Confirmed: true,
				Reference: "AY123",
			},
		},
	})
	if err != nil {
		t.Fatalf("add trip: %v", err)
	}

	s := NewServer()
	result, err := s.readTripsUpcoming()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
	text := result.Contents[0].Text
	if strings.Contains(text, "No upcoming trips") {
		t.Error("expected upcoming trip info, got no-trips message")
	}
	if !strings.Contains(text, "Barcelona") {
		t.Error("expected trip name in output")
	}
}

// ============================================================
// readTripByURI -- with trip data (54.5%)
// ============================================================

func TestReadTripByURI_WithTrip(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") != "1" {
		t.Skip("hits live external APIs; set TRVL_TEST_LIVE_INTEGRATIONS=1 to run. Tracked in #45")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	tripsDir := filepath.Join(tmp, ".trvl")
	os.MkdirAll(tripsDir, 0o755)
	store := trips.NewStore(tripsDir)
	store.Load()

	id, err := store.Add(trips.Trip{
		Name:   "Tokyo Trip",
		Status: "planning",
	})
	if err != nil {
		t.Fatalf("add trip: %v", err)
	}

	s := NewServer()
	uri := fmt.Sprintf("trvl://trips/%s", id)
	result, err := s.readTripByURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
	if !strings.Contains(result.Contents[0].Text, "Tokyo") {
		t.Error("expected trip data in output")
	}
}

func TestReadTripByURI_NotFound(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	tripsDir := filepath.Join(tmp, ".trvl")
	os.MkdirAll(tripsDir, 0o755)

	s := NewServer()
	_, err := s.readTripByURI("trvl://trips/nonexistent-id")
	if err == nil {
		t.Error("expected error for non-existent trip")
	}
}

// ============================================================
// handleToolsCall -- error returned by handler (73.1%)
// ============================================================

func TestHandleToolsCall_HandlerError(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params, _ := json.Marshal(ToolCallParams{
		Name:      "search_flights",
		Arguments: map[string]any{"origin": "HEL"},
	})
	req := &Request{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  json.RawMessage(params),
	}
	resp := s.HandleRequest(req)
	if resp == nil {
		t.Fatal("expected response")
	}
	// The handler should return an error result (missing destination/date).
	resultJSON, _ := json.Marshal(resp.Result)
	var result ToolCallResult
	json.Unmarshal(resultJSON, &result)
	if !result.IsError {
		t.Error("expected IsError=true for missing required params")
	}
}
