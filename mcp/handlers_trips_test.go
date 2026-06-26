package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// ============================================================
// handleListTrips / handleGetTrip / handleCreateTrip / handleAddTripLeg /
// handleMarkTripBooked — error paths (missing params, nil args)
// ============================================================

func TestHandleGetTrip_MissingID(t *testing.T) {
	t.Parallel()
	_, _, err := handleGetTrip(context.Background(), map[string]any{}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing id")
	}
}

func TestHandleGetTrip_NilArgs(t *testing.T) {
	t.Parallel()
	_, _, err := handleGetTrip(context.Background(), nil, nil, nil, nil)
	if err == nil {
		t.Error("expected error for nil args")
	}
}

func TestHandleCreateTrip_MissingName(t *testing.T) {
	t.Parallel()
	_, _, err := handleCreateTrip(context.Background(), map[string]any{}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestHandleCreateTrip_EmptyName(t *testing.T) {
	t.Parallel()
	_, _, err := handleCreateTrip(context.Background(), map[string]any{"name": ""}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for empty name")
	}
}

func TestHandleAddTripLeg_MissingTripID(t *testing.T) {
	t.Parallel()
	_, _, err := handleAddTripLeg(context.Background(), map[string]any{
		"type": "flight", "from": "HEL", "to": "BCN",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing trip_id")
	}
}

func TestHandleAddTripLeg_NilArgs(t *testing.T) {
	t.Parallel()
	_, _, err := handleAddTripLeg(context.Background(), nil, nil, nil, nil)
	if err == nil {
		t.Error("expected error for nil args (trip_id missing)")
	}
}

func TestHandleMarkTripBooked_MissingAll(t *testing.T) {
	t.Parallel()
	_, _, err := handleMarkTripBooked(context.Background(), map[string]any{}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing trip_id/provider/reference")
	}
}

func TestHandleMarkTripBooked_MissingProvider(t *testing.T) {
	t.Parallel()
	_, _, err := handleMarkTripBooked(context.Background(), map[string]any{
		"trip_id": "trip_1", "reference": "ABC123",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing provider")
	}
}

func TestHandleMarkTripBooked_MissingReference(t *testing.T) {
	t.Parallel()
	_, _, err := handleMarkTripBooked(context.Background(), map[string]any{
		"trip_id": "trip_1", "provider": "KLM",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing reference")
	}
}

// ============================================================
// handleSearchDeals — origins handling
// ============================================================
//
// origins is optional: missing/nil/empty origins returns deals from all origins
// (mirrors the CLI `--from` flag). These behaviors, plus origins filtering and
// currency conversion, are covered deterministically (and via live probes) in
// tools_deals_test.go.

// ============================================================
// handleSearchRoute — error paths
// ============================================================

func TestHandleSearchRoute_MissingAll(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchRoute(context.Background(), map[string]any{}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing origin/destination/date")
	}
}

func TestHandleSearchRoute_MissingDest(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchRoute(context.Background(), map[string]any{
		"origin": "HEL", "date": "2026-07-01",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing destination")
	}
}

func TestHandleSearchRoute_NilArgs(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchRoute(context.Background(), nil, nil, nil, nil)
	if err == nil {
		t.Error("expected error for nil args")
	}
}

// ============================================================
// buildRouteSummary
// ============================================================

func TestBuildRouteSummary_NoItineraries(t *testing.T) {
	t.Parallel()
	result := &models.RouteSearchResult{
		Success:     true,
		Origin:      "Helsinki",
		Destination: "Dubrovnik",
		Date:        "2026-07-01",
		Count:       0,
	}
	got := buildRouteSummary(result)
	if !strings.Contains(got, "Found 0") {
		t.Errorf("expected 'Found 0' in summary, got %q", got)
	}
	if !strings.Contains(got, "Helsinki") {
		t.Errorf("expected origin in summary, got %q", got)
	}
}

func TestBuildRouteSummary_DirectRoute(t *testing.T) {
	t.Parallel()
	result := &models.RouteSearchResult{
		Success:     true,
		Origin:      "Helsinki",
		Destination: "Tallinn",
		Date:        "2026-07-01",
		Count:       1,
		Itineraries: []models.RouteItinerary{
			{
				TotalPrice:    25,
				Currency:      "EUR",
				TotalDuration: 150,
				Transfers:     0,
				Legs: []models.RouteLeg{
					{Mode: "ferry", From: "Helsinki", To: "Tallinn", Provider: "Eckeroline", Price: 25, Currency: "EUR"},
				},
			},
		},
	}
	got := buildRouteSummary(result)
	if !strings.Contains(got, "direct") {
		t.Errorf("expected 'direct' for 0 transfers, got %q", got)
	}
	if !strings.Contains(got, "ferry") {
		t.Errorf("expected 'ferry' in summary, got %q", got)
	}
}

func TestBuildRouteSummary_TruncatesAt10(t *testing.T) {
	t.Parallel()
	itineraries := make([]models.RouteItinerary, 15)
	for i := range itineraries {
		itineraries[i] = models.RouteItinerary{
			TotalPrice: float64(100 + i*10), Currency: "EUR",
			TotalDuration: 120, Transfers: 1,
			Legs: []models.RouteLeg{{Mode: "bus", From: "A", To: "B", Provider: "FlixBus"}},
		}
	}
	result := &models.RouteSearchResult{
		Success: true, Origin: "A", Destination: "B", Date: "2026-07-01",
		Count: 15, Itineraries: itineraries,
	}
	got := buildRouteSummary(result)
	if !strings.Contains(got, "5 more") {
		t.Errorf("expected overflow count, got %q", got)
	}
}

func TestBuildRouteSummary_LegWithZeroPrice(t *testing.T) {
	t.Parallel()
	result := &models.RouteSearchResult{
		Success: true, Origin: "A", Destination: "B", Date: "2026-07-01",
		Count: 1,
		Itineraries: []models.RouteItinerary{
			{
				TotalPrice: 50, Currency: "EUR", TotalDuration: 60, Transfers: 0,
				Legs: []models.RouteLeg{
					{Mode: "train", From: "A", To: "B", Provider: "DB", Price: 0, Currency: "EUR"},
				},
			},
		},
	}
	got := buildRouteSummary(result)
	if !strings.Contains(got, "-") {
		t.Errorf("expected '-' for zero-price leg, got %q", got)
	}
}
