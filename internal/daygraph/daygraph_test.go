package daygraph

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/trips"
)

// tripSpanning builds a trip whose legs span the given inclusive date range.
func tripSpanning(start, end string) trips.Trip {
	return trips.Trip{
		ID: "trip_test",
		Legs: []trips.TripLeg{
			{Type: "flight", From: "Helsinki", To: "Krakow", StartTime: start + "T07:30:00"},
			{Type: "hotel", From: "Krakow", To: "Krakow", StartTime: start, EndTime: end},
		},
	}
}

func TestCompose_DayCountMatchesSpanAndRouteMinutesPositive(t *testing.T) {
	// GIVEN a 2-day trip and four POIs with coordinates (so at least one day
	// receives two places).
	trip := tripSpanning("2026-06-16", "2026-06-17")
	places := []trips.Place{
		{ID: "p1", Name: "Wawel Castle", Lat: 50.0544, Lon: 19.9354, Category: "activity"},
		{ID: "p2", Name: "Main Square", Lat: 50.0617, Lon: 19.9373, Category: "activity"},
		{ID: "p3", Name: "Hotel Stary", Lat: 50.0619, Lon: 19.9368, Category: "hotel"},
		{ID: "p4", Name: "Schindler Museum", Lat: 50.0475, Lon: 19.9614, Category: "activity"},
	}

	// WHEN composing the day graph.
	days, err := Compose(trip, places)

	// THEN one day per calendar day in the span, each carrying place IDs,
	// and at least one day with >=2 places has a positive route estimate.
	if err != nil {
		t.Fatalf("Compose returned error: %v", err)
	}
	if len(days) != 2 {
		t.Fatalf("day count = %d, want 2 (span 2026-06-16..2026-06-17)", len(days))
	}

	var multiPlaceDay *trips.DayPlan
	totalAssigned := 0
	for i := range days {
		totalAssigned += len(days[i].PlaceIDs)
		if len(days[i].PlaceIDs) >= 2 {
			multiPlaceDay = &days[i]
		}
	}
	if totalAssigned != len(places) {
		t.Fatalf("assigned place count = %d, want %d", totalAssigned, len(places))
	}
	if multiPlaceDay == nil {
		t.Fatalf("expected at least one day with >=2 places")
	}
	if multiPlaceDay.EstimatedRouteMinutes <= 0 {
		t.Fatalf("EstimatedRouteMinutes = %d for a >=2-place day, want > 0",
			multiPlaceDay.EstimatedRouteMinutes)
	}
}

func TestCompose_SinglePlaceDayHasNonZeroEstimate(t *testing.T) {
	// GIVEN a one-day trip and a single POI with coordinates.
	trip := tripSpanning("2026-07-01", "2026-07-01")
	places := []trips.Place{
		{ID: "solo", Name: "Louvre", Lat: 48.8606, Lon: 2.3376},
	}

	// WHEN composing.
	days, err := Compose(trip, places)

	// THEN one day with a non-zero estimate (baseline approach time).
	if err != nil {
		t.Fatalf("Compose returned error: %v", err)
	}
	if len(days) != 1 {
		t.Fatalf("day count = %d, want 1", len(days))
	}
	if days[0].EstimatedRouteMinutes <= 0 {
		t.Fatalf("single-place day estimate = %d, want > 0", days[0].EstimatedRouteMinutes)
	}
}

func TestCompose_MissingCoordsGoToWarningsNotDropped(t *testing.T) {
	// GIVEN a one-day trip with one coordinate-less place and one with coords.
	trip := tripSpanning("2026-08-10", "2026-08-10")
	places := []trips.Place{
		{ID: "nocoord", Name: "Mystery Spot"},
		{ID: "withcoord", Name: "Sagrada Familia", Lat: 41.4036, Lon: 2.1744},
	}

	// WHEN composing.
	days, err := Compose(trip, places)

	// THEN both places remain assigned (not dropped) and the coordinate-less
	// one is named in Warnings.
	if err != nil {
		t.Fatalf("Compose returned error: %v", err)
	}
	if len(days) != 1 {
		t.Fatalf("day count = %d, want 1", len(days))
	}
	if len(days[0].PlaceIDs) != 2 {
		t.Fatalf("PlaceIDs = %d, want 2 (no silent drop)", len(days[0].PlaceIDs))
	}
	if len(days[0].Warnings) != 1 {
		t.Fatalf("Warnings = %d, want 1", len(days[0].Warnings))
	}
	if !strings.Contains(days[0].Warnings[0], "Mystery Spot") {
		t.Fatalf("warning %q does not name the coordinate-less place", days[0].Warnings[0])
	}
}

func TestCompose_NoDatesReturnsError(t *testing.T) {
	// GIVEN a trip whose legs carry no parseable date.
	trip := trips.Trip{
		ID:   "trip_nodate",
		Legs: []trips.TripLeg{{Type: "flight", From: "A", To: "B"}},
	}

	// WHEN composing.
	_, err := Compose(trip, nil)

	// THEN an error is returned (no panic).
	if err == nil {
		t.Fatalf("expected error for trip with no parseable dates")
	}
}

func TestCompose_DeterministicAcrossInputOrder(t *testing.T) {
	// GIVEN the same places in two different input orders.
	trip := tripSpanning("2026-09-01", "2026-09-02")
	a := []trips.Place{
		{ID: "z", Name: "Z", Lat: 1, Lon: 1},
		{ID: "a", Name: "A", Lat: 2, Lon: 2},
		{ID: "m", Name: "M", Lat: 3, Lon: 3},
	}
	b := []trips.Place{a[2], a[0], a[1]}

	// WHEN composing each.
	da, err := Compose(trip, a)
	if err != nil {
		t.Fatalf("Compose(a) error: %v", err)
	}
	db, err := Compose(trip, b)
	if err != nil {
		t.Fatalf("Compose(b) error: %v", err)
	}

	// THEN the day plans are identical.
	if len(da) != len(db) {
		t.Fatalf("day counts differ: %d vs %d", len(da), len(db))
	}
	for i := range da {
		if da[i].EstimatedRouteMinutes != db[i].EstimatedRouteMinutes {
			t.Fatalf("day %d minutes differ: %d vs %d", i, da[i].EstimatedRouteMinutes, db[i].EstimatedRouteMinutes)
		}
		if strings.Join(da[i].PlaceIDs, ",") != strings.Join(db[i].PlaceIDs, ",") {
			t.Fatalf("day %d place order differs: %v vs %v", i, da[i].PlaceIDs, db[i].PlaceIDs)
		}
	}
}

func TestCompose_DerivesPlaceIDWhenAbsent(t *testing.T) {
	// GIVEN a place with no explicit ID.
	trip := tripSpanning("2026-10-05", "2026-10-05")
	places := []trips.Place{{Name: "Unnamed POI", City: "Lisbon", Lat: 38.7, Lon: -9.1}}

	// WHEN composing.
	days, err := Compose(trip, places)

	// THEN a stable ID is derived and assigned.
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	if len(days[0].PlaceIDs) != 1 || days[0].PlaceIDs[0] == "" {
		t.Fatalf("expected one derived place ID, got %v", days[0].PlaceIDs)
	}
}

func TestCompose_CoincidentCoordsClampToOneMinute(t *testing.T) {
	// GIVEN two places at the exact same coordinates.
	trip := tripSpanning("2026-11-01", "2026-11-01")
	places := []trips.Place{
		{ID: "x1", Name: "Lobby", Lat: 60.1699, Lon: 24.9384},
		{ID: "x2", Name: "Bar", Lat: 60.1699, Lon: 24.9384},
	}

	// WHEN composing.
	days, err := Compose(trip, places)

	// THEN the estimate is clamped to a minimum of 1 minute (still non-zero).
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	if days[0].EstimatedRouteMinutes != 1 {
		t.Fatalf("coincident-coords estimate = %d, want 1", days[0].EstimatedRouteMinutes)
	}
}

func TestCompose_EmptyDayGetsPlaceholderTitle(t *testing.T) {
	// GIVEN more days than places, so at least one day receives no place.
	trip := tripSpanning("2026-12-01", "2026-12-03")
	places := []trips.Place{{ID: "only", Name: "Cathedral", Lat: 40.0, Lon: -3.0}}

	// WHEN composing.
	days, err := Compose(trip, places)

	// THEN an empty day still carries a non-empty placeholder title.
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	var emptyDay *trips.DayPlan
	for i := range days {
		if len(days[i].PlaceIDs) == 0 {
			emptyDay = &days[i]
		}
	}
	if emptyDay == nil {
		t.Fatalf("expected at least one empty day")
	}
	if !strings.Contains(emptyDay.Title, "Day in") {
		t.Fatalf("empty-day title = %q, want a placeholder", emptyDay.Title)
	}
	if emptyDay.EstimatedRouteMinutes != 0 {
		t.Fatalf("empty-day estimate = %d, want 0", emptyDay.EstimatedRouteMinutes)
	}
}

func TestCompose_LabelFallsBackToIDWhenNameAbsent(t *testing.T) {
	// GIVEN a coordinate-less place with no name (only an ID), so it lands in
	// Warnings, which exercises the label fallback path.
	trip := tripSpanning("2027-01-01", "2027-01-01")
	places := []trips.Place{{ID: "bare-id"}}

	// WHEN composing.
	days, err := Compose(trip, places)

	// THEN the warning references the place's ID as its label.
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}
	if len(days[0].Warnings) != 1 || !strings.Contains(days[0].Warnings[0], "bare-id") {
		t.Fatalf("expected warning naming the ID, got %v", days[0].Warnings)
	}
}
