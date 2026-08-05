package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/fareintel"
	"github.com/MikkoParkkola/trvl/internal/trips"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

func TestTripWorkspaceImportReservationMergesIntoTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	_, created, err := handleCreateTrip(context.Background(), map[string]any{"name": "Amsterdam"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}
	tripID := created.(map[string]string)["id"]

	_, structured, err := handleTripWorkspace(context.Background(), map[string]any{
		"action":  "import_reservation",
		"trip_id": tripID,
		"subject": "Booking confirmation - KLM",
		"body":    "Your flight has been confirmed.\nFlight: HEL -> AMS\nDeparture: 2026-07-01\nTotal: EUR 189.00\nBooking reference: ABC123",
		"source":  "email",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("import reservation: %v", err)
	}
	result := structured.(map[string]any)
	summary := result["summary"].(trips.MergeSummary)
	if summary.ImportedRecordsAdded != 1 || summary.LegsAdded != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	trip := result["trip"].(trips.Trip)
	if len(trip.Workspace.ImportedRecords) != 1 || len(trip.Legs) != 1 {
		t.Fatalf("trip = %#v", trip)
	}
}

func TestTripWorkspaceFareIntelligence(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	history := []watch.PricePoint{
		{Price: 100, Currency: "EUR", Timestamp: now.AddDate(0, 0, -5)},
		{Price: 110, Currency: "EUR", Timestamp: now.AddDate(0, 0, -4)},
		{Price: 120, Currency: "EUR", Timestamp: now.AddDate(0, 0, -3)},
		{Price: 130, Currency: "EUR", Timestamp: now.AddDate(0, 0, -2)},
		{Price: 140, Currency: "EUR", Timestamp: now.AddDate(0, 0, -1)},
	}
	_, structured, err := handleTripWorkspace(context.Background(), map[string]any{
		"action":   "fare_intelligence",
		"price":    90.0,
		"currency": "EUR",
		"history":  history,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("fare intelligence: %v", err)
	}
	result := structured.(fareintel.Result)
	if result.Verdict != "buy" {
		t.Fatalf("result = %#v", result)
	}
}

func TestTravelRoutesWorkspaceActionToTripWorkspace(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	_, created, err := handleCreateTrip(context.Background(), map[string]any{"name": "Candidate"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}
	tripID := created.(map[string]string)["id"]

	s := NewServer()
	_, structured, err := s.handleTravel(context.Background(), map[string]any{
		"intent": "trip_workspace",
		"action": "save_candidate",
		"params": map[string]any{
			"trip_id":    tripID,
			"type":       "hotel",
			"title":      "Central stay",
			"provider":   "Google Hotels",
			"price":      120.0,
			"currency":   "EUR",
			"url":        "https://example.com/hotel",
			"checked_at": time.Now().Format(time.RFC3339),
		},
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("travel workspace route: %v", err)
	}
	routed := structured.(travelSmartResult)
	if routed.DispatchedTo != "trip_workspace" {
		t.Fatalf("routed = %#v", routed)
	}
	if !strings.Contains(routed.Intent, "trip_workspace") {
		t.Fatalf("intent = %q", routed.Intent)
	}
}

func TestTripWorkspaceGetExportImportAndReadiness(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	_, created, err := handleCreateTrip(context.Background(), map[string]any{"name": "Workspace"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}
	tripID := created.(map[string]string)["id"]

	_, saved, err := handleTripWorkspace(context.Background(), map[string]any{
		"action":     "save_candidate",
		"trip_id":    tripID,
		"type":       "hotel",
		"title":      "Central stay",
		"provider":   "Google Hotels",
		"price":      120.0,
		"currency":   "EUR",
		"url":        "https://example.com/hotel",
		"checked_at": time.Now().Format(time.RFC3339),
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("save candidate: %v", err)
	}
	candidate := saved.(map[string]any)["candidate"].(trips.BookingCandidate)

	_, gotStructured, err := handleTripWorkspace(context.Background(), map[string]any{
		"action":  "get",
		"trip_id": tripID,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	gotTrip := gotStructured.(*trips.Trip)
	if gotTrip.ID != tripID || len(gotTrip.Workspace.Candidates) != 1 {
		t.Fatalf("workspace trip = %#v", gotTrip)
	}

	_, readinessStructured, err := handleTripWorkspace(context.Background(), map[string]any{
		"action":       "booking_ready",
		"trip_id":      tripID,
		"candidate_id": candidate.ID,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("booking readiness: %v", err)
	}
	readiness := readinessStructured.(map[string]any)
	if ready, _ := readiness["ready"].(bool); !ready {
		t.Fatalf("readiness = %#v, want ready", readiness)
	}

	_, exportedStructured, err := handleTripWorkspace(context.Background(), map[string]any{
		"action":  "export_json",
		"trip_id": tripID,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("export json: %v", err)
	}
	exported := exportedStructured.(map[string]any)
	rawJSON := exported["content"].(string)
	if !strings.Contains(rawJSON, "Central stay") {
		t.Fatalf("exported json missing candidate: %s", rawJSON)
	}

	_, markdownStructured, err := handleTripWorkspace(context.Background(), map[string]any{
		"action":  "export_markdown",
		"trip_id": tripID,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("export markdown: %v", err)
	}
	if !strings.Contains(markdownStructured.(map[string]any)["content"].(string), "Workspace") {
		t.Fatalf("markdown export = %#v", markdownStructured)
	}

	_, importedStructured, err := handleTripWorkspace(context.Background(), map[string]any{
		"action": "import_json",
		"json":   rawJSON,
		"name":   "Imported workspace",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("import json new trip: %v", err)
	}
	imported := importedStructured.(map[string]any)
	if imported["trip_id"].(string) == "" {
		t.Fatalf("imported = %#v", imported)
	}

	_, mergedStructured, err := handleTripWorkspace(context.Background(), map[string]any{
		"action":  "import_json",
		"trip_id": tripID,
		"json":    rawJSON,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("import json merge: %v", err)
	}
	if mergedStructured.(map[string]any)["trip_id"].(string) != tripID {
		t.Fatalf("merged = %#v", mergedStructured)
	}
}

func TestTripWorkspaceReadinessReportsBlockersAndMissingCandidate(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	_, created, err := handleCreateTrip(context.Background(), map[string]any{"name": "Blocked candidate"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}
	tripID := created.(map[string]string)["id"]

	_, _, err = handleTripWorkspace(context.Background(), map[string]any{
		"action":  "save_candidate",
		"trip_id": tripID,
		"type":    "flight",
		"title":   "Unpriced option",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("save incomplete candidate: %v", err)
	}

	_, structured, err := handleTripWorkspace(context.Background(), map[string]any{
		"action":  "booking_ready",
		"trip_id": tripID,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("booking readiness: %v", err)
	}
	result := structured.(map[string]any)
	if ready, _ := result["ready"].(bool); ready {
		t.Fatalf("readiness = %#v, want blockers", result)
	}
	if blockers := result["blockers"].([]string); len(blockers) < 2 {
		t.Fatalf("blockers = %#v, want missing URL and price", blockers)
	}

	_, _, err = handleTripWorkspace(context.Background(), map[string]any{
		"action":       "booking_ready",
		"trip_id":      tripID,
		"candidate_id": "missing",
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected missing candidate error")
	}
}

func TestTripWorkspaceOptimizeItinerary(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	_, created, err := handleCreateTrip(context.Background(), map[string]any{"name": "Itinerary"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}
	tripID := created.(map[string]string)["id"]

	_, structured, err := handleTripWorkspace(context.Background(), map[string]any{
		"action":            "optimize_itinerary",
		"trip_id":           tripID,
		"max_route_minutes": 90,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("optimize itinerary: %v", err)
	}
	if structured == nil {
		t.Fatal("expected optimization result")
	}
}
