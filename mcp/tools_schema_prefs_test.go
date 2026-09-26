package mcp

import "testing"

func TestSearchToolsAdvertiseSeatAndBucket(t *testing.T) {
	if _, ok := searchFlightsTool().InputSchema.Properties["seat_preference"]; !ok {
		t.Fatal("search_flights schema missing seat_preference")
	}
	if _, ok := searchGroundTool().InputSchema.Properties["bucket_list"]; !ok {
		t.Fatal("search_ground schema missing bucket_list")
	}
}
