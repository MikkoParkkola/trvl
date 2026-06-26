package mcp

import "testing"

// TestBuildHotelSearchRequest_EnrichRoomsDefaultOn asserts the MCP hotel search
// enables real room-level price enrichment by default (drilling top results for
// per-room rates instead of headline teasers), and that callers can opt out with
// enrich_rooms=false for a faster headline-only search.
func TestBuildHotelSearchRequest_EnrichRoomsDefaultOn(t *testing.T) {
	base := map[string]any{
		"location":  "Lisbon",
		"check_in":  "2030-08-10",
		"check_out": "2030-08-12",
	}

	req, err := buildHotelSearchRequest(base)
	if err != nil {
		t.Fatalf("buildHotelSearchRequest(default): %v", err)
	}
	if !req.Options.EnrichRooms {
		t.Error("EnrichRooms should default to true so users get real room-level prices")
	}

	optOut := map[string]any{
		"location":     "Lisbon",
		"check_in":     "2030-08-10",
		"check_out":    "2030-08-12",
		"enrich_rooms": false,
	}
	req2, err := buildHotelSearchRequest(optOut)
	if err != nil {
		t.Fatalf("buildHotelSearchRequest(opt-out): %v", err)
	}
	if req2.Options.EnrichRooms {
		t.Error("enrich_rooms=false should disable room-level enrichment")
	}
}
