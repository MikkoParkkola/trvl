package calendar

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/trips"
)

func TestWriteICS_EmitsDayGraphEvent(t *testing.T) {
	// GIVEN a trip carrying one leg and one day-graph DayPlan in its workspace.
	trip := &trips.Trip{
		ID:   "trip_daygraph",
		Name: "Krakow Weekend",
		Legs: []trips.TripLeg{
			{
				Type:      "flight",
				From:      "Helsinki",
				To:        "Krakow",
				Provider:  "Finnair",
				StartTime: "2026-06-16T07:30:00",
				EndTime:   "2026-06-16T10:15:00",
				Confirmed: true,
			},
		},
		Workspace: &trips.Workspace{
			Days: []trips.DayPlan{
				{
					ID:                    "day_1",
					Date:                  "2026-06-16",
					Title:                 "Old Town walk",
					PlaceIDs:              []string{"p1", "p2"},
					EstimatedRouteMinutes: 42,
				},
			},
		},
	}

	// WHEN rendering the ICS.
	ics := WriteICS(trip)

	// THEN the day-graph event is present, all-day, and the leg event survives.
	if !strings.Contains(ics, "Old Town walk") {
		t.Fatalf("rendered ICS missing day-graph title:\n%s", ics)
	}
	if !strings.Contains(ics, "trvl-trip_daygraph-day-0-") {
		t.Fatalf("rendered ICS missing day-graph UID:\n%s", ics)
	}
	if !strings.Contains(ics, "DTSTART;VALUE=DATE:20260616") {
		t.Fatalf("day-graph event is not an all-day event:\n%s", ics)
	}
	if !strings.Contains(ics, "Helsinki → Krakow") {
		t.Fatalf("existing leg event was broken:\n%s", ics)
	}
	if !strings.Contains(ics, "~42 min") {
		t.Fatalf("day-graph description missing route minutes:\n%s", ics)
	}
}

func TestWriteICS_DayWithoutDateSkipped(t *testing.T) {
	// GIVEN a trip whose only DayPlan has no parseable date.
	trip := &trips.Trip{
		ID:   "trip_nodate_day",
		Name: "No-date day",
		Legs: []trips.TripLeg{
			{Type: "flight", From: "A", To: "B", StartTime: "2026-06-16T07:30:00"},
		},
		Workspace: &trips.Workspace{
			Days: []trips.DayPlan{{ID: "bad", Date: "", Title: "Floating day", PlaceIDs: []string{"p1"}}},
		},
	}

	// WHEN rendering.
	ics := WriteICS(trip)

	// THEN the undated day is skipped (no day-graph event) but the leg remains.
	if strings.Contains(ics, "Floating day") {
		t.Fatalf("undated day should have been skipped:\n%s", ics)
	}
	if !strings.Contains(ics, "BEGIN:VEVENT") {
		t.Fatalf("leg event missing:\n%s", ics)
	}
}

func TestWriteICS_DayTitleFallback(t *testing.T) {
	// GIVEN a dated DayPlan with no title and no route estimate.
	trip := &trips.Trip{
		ID:   "trip_fallback",
		Name: "Fallback",
		Legs: []trips.TripLeg{
			{Type: "flight", From: "A", To: "B", StartTime: "2026-06-16T07:30:00"},
		},
		Workspace: &trips.Workspace{
			Days: []trips.DayPlan{{ID: "d", Date: "2026-06-16", PlaceIDs: []string{"p1", "p2", "p3"}}},
		},
	}

	// WHEN rendering.
	ics := WriteICS(trip)

	// THEN a place-count fallback title is used.
	if !strings.Contains(ics, "Day plan (3 places)") {
		t.Fatalf("expected fallback day title:\n%s", ics)
	}
}
