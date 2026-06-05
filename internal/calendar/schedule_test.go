package calendar

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func sampleSchedule() models.ScheduleTimeline {
	return models.ScheduleTimeline{
		LeaveHomeBy:   "06:45",
		BufferMinutes: 120,
		Confidence:    "high",
		Assumptions:   []string{"120-min airport buffer (check-in + security (international))", "15-min safety margin"},
		Steps: []models.SchedRow{
			{Time: "06:45", Text: "Leave home — Train I to Helsinki Airport"},
			{Time: "07:50", Text: "Arrive at airport — allow 120 min for check-in + security (international)"},
			{Time: "09:40", Text: "Scheduled departure"},
		},
	}
}

func TestScheduleICS_Success(t *testing.T) {
	ics, err := ScheduleICS(ScheduleICSInput{
		Date:          "2026-07-18",
		AirportCode:   "HEL",
		DepartureTime: "09:40",
		Schedule:      sampleSchedule(),
	})
	if err != nil {
		t.Fatalf("ScheduleICS error: %v", err)
	}
	for _, want := range []string{
		"BEGIN:VCALENDAR",
		"END:VCALENDAR",
		"BEGIN:VEVENT",
		"DTSTART:20260718T064500", // floating local leave-by time
		"BEGIN:VALARM",
		"TRIGGER:-PT30M", // default reminder
		"ACTION:DISPLAY",
		"Leave home for HEL",
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("ICS missing %q", want)
		}
	}
	// RFC 5545 CRLF line endings.
	if !strings.Contains(ics, "\r\n") {
		t.Error("ICS must use CRLF line endings")
	}
}

func TestScheduleICS_CustomReminder(t *testing.T) {
	ics, err := ScheduleICS(ScheduleICSInput{
		Date:          "2026-07-18",
		AirportCode:   "BCN",
		DepartureTime: "12:00",
		ReminderMin:   45,
		Schedule:      sampleSchedule(),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(ics, "TRIGGER:-PT45M") {
		t.Errorf("expected custom 45-min reminder trigger")
	}
}

func TestScheduleICS_RequiresDateAndLeaveBy(t *testing.T) {
	if _, err := ScheduleICS(ScheduleICSInput{Date: "", Schedule: sampleSchedule()}); err == nil {
		t.Error("expected error when date is missing")
	}
	if _, err := ScheduleICS(ScheduleICSInput{Date: "2026-07-18", Schedule: models.ScheduleTimeline{}}); err == nil {
		t.Error("expected error when leave_home_by is empty")
	}
}
