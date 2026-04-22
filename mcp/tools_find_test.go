package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/tripsearch"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// makeFakeFlightsByCode builds minimal FlightResults whose first-leg departure
// code matches the input string — handy for ordering assertions.
func makeFakeFlightsByCode(codes ...string) []models.FlightResult {
	out := make([]models.FlightResult, 0, len(codes))
	for _, c := range codes {
		out = append(out, models.FlightResult{
			Legs: []models.FlightLeg{{
				DepartureAirport: models.AirportInfo{Code: c},
				ArrivalAirport:   models.AirportInfo{Code: "PRG"},
			}},
		})
	}
	return out
}

func TestFindRequestFromArgs_RoundTrip(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"origin":              "home",
		"destination":         "PRG",
		"departure_date":      "2026-04-23",
		"return_date":         "2026-06-03",
		"cabin":               "economy",
		"min_layover_minutes": float64(720),
		"layover_at":          []any{"AMS", "CDG"},
		"no_early_connection": true,
		"lounge_required":     true,
		"top_n":               float64(3),
	}
	req := findRequestFromArgs(args)
	if req.Origin != "home" || req.Destination != "PRG" || req.Date != "2026-04-23" {
		t.Errorf("core fields missing: %+v", req)
	}
	if req.MinLayoverMinutes != 720 {
		t.Errorf("min_layover_minutes = %d, want 720", req.MinLayoverMinutes)
	}
	if len(req.LayoverAirports) != 2 {
		t.Errorf("layover_at parsed length = %d, want 2", len(req.LayoverAirports))
	}
	if !req.NoEarlyConnection || !req.LoungeRequired {
		t.Errorf("bool flags lost: noEarly=%v lounge=%v", req.NoEarlyConnection, req.LoungeRequired)
	}
	if req.TopN != 3 {
		t.Errorf("top_n = %d, want 3", req.TopN)
	}
}

func TestRelaxOptions_OnlyFiltersThatRanAndDropped(t *testing.T) {
	t.Parallel()
	log := tripsearch.FilterLog{
		LoungeAccess:      tripsearch.FilterStep{Ran: true, Dropped: 5},
		NoEarlyConnection: tripsearch.FilterStep{Ran: true, Dropped: 0}, // ran but dropped nothing
		LongLayover:       tripsearch.FilterStep{Ran: false},            // did not run
	}
	opts := relaxOptions(log)
	have := map[string]bool{}
	for _, o := range opts {
		have[o] = true
	}
	if !have["drop_lounge_required"] {
		t.Errorf("should offer drop_lounge_required when it dropped 5; got %v", opts)
	}
	if have["drop_no_early_connection"] {
		t.Errorf("should not offer drop_no_early_connection when it dropped 0; got %v", opts)
	}
	if have["drop_long_layover"] {
		t.Errorf("should not offer drop_long_layover when it did not run; got %v", opts)
	}
	if !have["cancel"] {
		t.Errorf("cancel option must always be present; got %v", opts)
	}
}

func TestFilterImpactText_OmitsFiltersThatDidntRun(t *testing.T) {
	t.Parallel()
	log := tripsearch.FilterLog{
		LoungeAccess: tripsearch.FilterStep{Ran: true, Dropped: 3},
	}
	got := filterImpactText(log)
	if !strings.Contains(got, "lounge −3") {
		t.Errorf("expected 'lounge −3' in %q", got)
	}
	if strings.Contains(got, "long-layover") || strings.Contains(got, "no-early-connection") {
		t.Errorf("should omit non-running filters in %q", got)
	}
}

func TestPlanFlightBundleTool_SchemaShape(t *testing.T) {
	t.Parallel()
	tool := planFlightBundleTool()
	if tool.Name != "plan_flight_bundle" {
		t.Errorf("name = %q", tool.Name)
	}
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Errorf("ReadOnlyHint must be true for plan_flight_bundle")
	}
	required := map[string]bool{"origin": true, "destination": true, "departure_date": true}
	for _, r := range tool.InputSchema.Required {
		delete(required, r)
	}
	if len(required) != 0 {
		t.Errorf("missing required fields: %v", required)
	}
	// OutputSchema must serialise as JSON and have type=object.
	data, err := json.Marshal(tool.OutputSchema)
	if err != nil {
		t.Fatalf("OutputSchema JSON: %v", err)
	}
	var schema map[string]any
	_ = json.Unmarshal(data, &schema)
	if schema["type"] != "object" {
		t.Errorf("OutputSchema.type = %v", schema["type"])
	}
}

func TestReorderFlightsFirst_HoistsPicked(t *testing.T) {
	t.Parallel()
	result := &tripsearch.Result{
		Flights: makeFakeFlightsByCode("AMS", "BRU", "CDG", "DUS"),
	}
	reorderFlightsFirst(result, 2) // move "CDG" to index 0
	if got := result.Flights[0].Legs[0].DepartureAirport.Code; got != "CDG" {
		t.Errorf("hoisted bundle should be first, got %q", got)
	}
	if len(result.Flights) != 4 {
		t.Errorf("length changed: %d", len(result.Flights))
	}
}
