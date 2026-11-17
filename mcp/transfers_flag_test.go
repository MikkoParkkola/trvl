package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// itemProperties digs the per-item properties out of an array schema.
func itemProperties(t *testing.T, schema interface{}) map[string]interface{} {
	t.Helper()
	items, _ := schema.(map[string]interface{})["items"].(map[string]interface{})
	props, _ := items["properties"].(map[string]interface{})
	if props == nil {
		t.Fatalf("schema has no item properties: %v", schema)
	}
	return props
}

// A ground route or itinerary whose change count is not known says so in the
// text reply, never "direct", and its flag is in the output schema.
func TestTransfersFlag_GroundAndRouteReplies(t *testing.T) {
	routes := []models.GroundRoute{{Provider: "oebb", Type: "train", Price: 30, Currency: "EUR", Duration: 311,
		TransfersUnknown: true,
		Departure:        models.GroundStop{City: "Prague", Time: "2026-11-20T10:21:00"},
		Arrival:          models.GroundStop{City: "Vienna", Time: "2026-11-20T15:32:00"}}}
	if s := buildGroundRouteSummary("Ground", routes, 10); strings.Contains(s, "direct") || !strings.Contains(s, "transfers unknown") {
		t.Errorf("ground summary = %q, want \"transfers unknown\" and no \"direct\"", s)
	}

	result := &models.RouteSearchResult{Success: true, Count: 1, Itineraries: []models.RouteItinerary{
		{TotalPrice: 30, Currency: "EUR", TotalDuration: 311, TransfersUnknown: true}}}
	if s := buildRouteSummary(result); strings.Contains(s, "direct") || !strings.Contains(s, "transfers unknown") {
		t.Errorf("route summary = %q, want \"transfers unknown\" and no \"direct\"", s)
	}

	if _, ok := itemProperties(t, groundRoutesOutputSchema())["transfers_unknown"]; !ok {
		t.Error("ground route schema lacks transfers_unknown")
	}
	if _, ok := itemProperties(t, routeItinerariesOutputSchema())["transfers_unknown"]; !ok {
		t.Error("itinerary schema lacks transfers_unknown")
	}
}

// The compacted search_ground route keeps the flag only when it is set.
func TestCompactGroundRoute_TransfersFlag(t *testing.T) {
	for _, set := range []bool{false, true} {
		b, err := json.Marshal(compactGroundRoute(models.GroundRoute{Provider: "oebb", Type: "train", TransfersUnknown: set}))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(b), `"transfers_unknown":true`); got != set {
			t.Errorf("flag %v encodes as %s", set, b)
		}
		if !set && strings.Contains(string(b), "transfers_unknown") {
			t.Errorf("flag false still emits the key: %s", b)
		}
	}
}
