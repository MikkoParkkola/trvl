package flights

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

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
	if err := json.Unmarshal(data, &arr); err != nil {
		t.Fatalf("Unmarshal filters: %v", err)
	}

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
	if err := json.Unmarshal(data, &arr); err != nil {
		t.Fatalf("Unmarshal filters: %v", err)
	}

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
	if err := json.Unmarshal(data, &arr); err != nil {
		t.Fatalf("Unmarshal filters: %v", err)
	}

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
	_ = json.Unmarshal(data, &arr)

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
	_ = json.Unmarshal(data, &arr)

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
	_ = json.Unmarshal(data, &arr)

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
	_ = json.Unmarshal(data, &arr)

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
	_ = json.Unmarshal(data, &arr)

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
