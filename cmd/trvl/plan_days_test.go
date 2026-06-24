package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/trips"
)

// sampleTrip builds a two-day trip with two coordinate-bearing places and one
// without, so composeDays exercises both the route estimate and the warning.
func sampleTrip() trips.Trip {
	return trips.Trip{
		Name:   "Krakow",
		Status: "planning",
		Legs: []trips.TripLeg{{
			Type:      "flight",
			From:      "AMS",
			To:        "KRK",
			StartTime: "2026-06-16T07:30",
			EndTime:   "2026-06-17T09:05",
		}},
		Workspace: &trips.Workspace{
			Places: []trips.Place{
				{Name: "Wawel Castle", City: "Krakow", Lat: 50.0540, Lon: 19.9354},
				{Name: "Main Square", City: "Krakow", Lat: 50.0617, Lon: 19.9373},
				{Name: "Mystery spot", City: "Krakow"}, // no coords -> warning
			},
		},
	}
}

func TestComposeDays_ProducesDaysWithRouteEstimate(t *testing.T) {
	res, err := composeDays(sampleTrip())
	if err != nil {
		t.Fatalf("composeDays: %v", err)
	}

	// Span 2026-06-16 .. 2026-06-17 inclusive = 2 days.
	if len(res.Days) != 2 {
		t.Fatalf("Days = %d, want 2", len(res.Days))
	}

	totalPlaces := 0
	sawRoute := false
	sawWarning := false
	for _, d := range res.Days {
		totalPlaces += len(d.PlaceIDs)
		if d.EstimatedRouteMinutes > 0 {
			sawRoute = true
		}
		if len(d.Warnings) > 0 {
			sawWarning = true
		}
	}
	if totalPlaces != 3 {
		t.Errorf("assigned places = %d, want 3", totalPlaces)
	}
	if !sawRoute {
		t.Error("expected at least one day with a non-zero route estimate")
	}
	if !sawWarning {
		t.Error("expected a warning for the coordinate-less place")
	}
}

func TestComposeDays_NoDateSpanErrors(t *testing.T) {
	t.Parallel()
	// A trip with no parseable leg date has no resolvable day span.
	_, err := composeDays(trips.Trip{Name: "x", Status: "planning"})
	if err == nil {
		t.Error("expected error for trip with no date span")
	}
}

func TestComposeDays_NilWorkspace(t *testing.T) {
	t.Parallel()
	trip := sampleTrip()
	trip.Workspace = nil // must not panic; days have no places
	res, err := composeDays(trip)
	if err != nil {
		t.Fatalf("composeDays: %v", err)
	}
	if len(res.Days) != 2 {
		t.Errorf("Days = %d, want 2", len(res.Days))
	}
}

func TestPlanDaysCmd_RendersItinerary(t *testing.T) {
	setTestHome(t, t.TempDir())

	store, err := loadTripStore()
	if err != nil {
		t.Fatalf("loadTripStore: %v", err)
	}
	id, err := store.Add(sampleTrip())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	cmd := planDaysCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{id})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Itinerary") {
		t.Errorf("output missing itinerary header:\n%s", got)
	}
	if !strings.Contains(got, "2026-06-16") {
		t.Errorf("output missing first day date:\n%s", got)
	}
}

func TestPlanDaysCmd_MissingTripErrors(t *testing.T) {
	setTestHome(t, t.TempDir())

	cmd := planDaysCmd()
	cmd.SetArgs([]string{"trip_does_not_exist"})

	if err := cmd.Execute(); err == nil {
		t.Error("expected error for a trip id that is not present")
	}
}

func TestPlanDaysCmd_JSONFormat(t *testing.T) {
	setTestHome(t, t.TempDir())
	t.Cleanup(func() { format = "" })
	format = "json"

	store, err := loadTripStore()
	if err != nil {
		t.Fatalf("loadTripStore: %v", err)
	}
	id, err := store.Add(sampleTrip())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	cmd := planDaysCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{id})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "\"days\"") {
		t.Errorf("JSON output missing days key:\n%s", out.String())
	}
}
