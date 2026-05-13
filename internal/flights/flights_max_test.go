package flights

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func flightsTestServer(t *testing.T, statusCode int, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		_, _ = w.Write(body)
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
