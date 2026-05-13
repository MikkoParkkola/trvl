package itinerary

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/trips"
)

func TestOptimizeOrdersPlacesByDistance(t *testing.T) {
	tr := trips.NormalizeWorkspace(trips.Trip{
		Name:   "Tokyo",
		Status: "planning",
		Workspace: &trips.Workspace{
			Places: []trips.Place{
				{ID: "a", Name: "A", Lat: 35.6812, Lon: 139.7671},
				{ID: "c", Name: "C", Lat: 35.7101, Lon: 139.8107},
				{ID: "b", Name: "B", Lat: 35.6895, Lon: 139.6917},
			},
		},
	})
	got := Optimize(tr, Options{MaxRouteMinutesPerDay: 120, AverageKPH: 20})
	if len(got.Days) != 1 {
		t.Fatalf("days = %#v", got.Days)
	}
	if got.Days[0].PlaceIDs[0] != "a" || got.Days[0].PlaceIDs[1] != "c" {
		t.Fatalf("unexpected order: %#v", got.Days[0].PlaceIDs)
	}
	if got.Days[0].EstimatedRouteMinutes <= 0 {
		t.Fatalf("route minutes not estimated: %#v", got.Days[0])
	}
}

func TestOptimizeWarnsOnOverpackedDay(t *testing.T) {
	tr := trips.NormalizeWorkspace(trips.Trip{
		Name:   "Europe",
		Status: "planning",
		Workspace: &trips.Workspace{
			Places: []trips.Place{
				{ID: "hel", Name: "Helsinki", Lat: 60.1699, Lon: 24.9384},
				{ID: "prg", Name: "Prague", Lat: 50.0755, Lon: 14.4378},
			},
		},
	})
	got := Optimize(tr, Options{MaxRouteMinutesPerDay: 30, AverageKPH: 20})
	if len(got.Warnings) == 0 {
		t.Fatalf("expected overpacked warning, got %#v", got)
	}
}
