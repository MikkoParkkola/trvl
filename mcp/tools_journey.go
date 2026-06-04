package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/MikkoParkkola/trvl/internal/calendar"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/transfer"
)

// handleJourney exposes the Leave-By Scheduler: given a flight departure and how
// the traveller reaches the airport, it answers "when do I leave home to be
// comfortably (not last-minute) at the gate?" with a grounded, conservative
// timeline. It is a smart capability reachable via the `travel` router intent
// `plan_journey`; it is deliberately NOT advertised as a compatibility alias
// (it is a capability, not a legacy tool name).
func handleJourney(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	airport := argString(args, "airport_code")
	date := argString(args, "date")
	depTime := argString(args, "departure_time")
	if airport == "" || date == "" || depTime == "" {
		return nil, nil, fmt.Errorf("airport_code, date, and departure_time are required")
	}
	groundMin := argInt(args, "ground_minutes", 0)
	groundMode := argString(args, "ground_mode")
	if groundMin <= 0 || groundMode == "" {
		return nil, nil, fmt.Errorf("ground_minutes (>0) and ground_mode are required; pick a mode from a transfer search first")
	}

	departure, err := time.Parse("2006-01-02 15:04", date+" "+depTime)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid date/departure_time (want YYYY-MM-DD and HH:MM): %w", err)
	}

	intl := argBool(args, "international", true)
	schedule := transfer.BuildSchedule(transfer.ScheduleInput{
		DepartureLocal: departure,
		AirportCode:    airport,
		International:  intl,
		GroundMinutes:  groundMin,
		GroundMode:     groundMode,
		OriginWalkMin:  argInt(args, "origin_walk_min", 0),
		GroundLabel:    argString(args, "ground_label"),
	})

	summary := fmt.Sprintf("Leave home by %s to reach %s comfortably for your %s departure (confidence: %s).",
		schedule.LeaveHomeBy, airport, depTime, schedule.Confidence)
	for _, row := range schedule.Steps {
		summary += fmt.Sprintf("\n  %s  %s", row.Time, row.Text)
	}
	if len(schedule.Assumptions) > 0 {
		summary += "\n\nAssumptions: "
		for i, a := range schedule.Assumptions {
			if i > 0 {
				summary += "; "
			}
			summary += a
		}
	}

	content := []ContentBlock{
		{Type: "text", Text: summary, Annotations: &ContentAnnotation{Audience: []string{"user"}, Priority: 1.0}},
	}

	// Optional calendar handoff (F.1): when as_ics is set, attach an iCalendar
	// "Leave home" event with a reminder alarm so the user can drop it straight
	// into Apple/Google/Outlook. Additive — does not change the default output.
	if argBool(args, "as_ics", false) {
		ics, icsErr := calendar.ScheduleICS(calendar.ScheduleICSInput{
			Date:          date,
			AirportCode:   airport,
			DepartureTime: depTime,
			ReminderMin:   argInt(args, "reminder_minutes", 30),
			Schedule:      schedule,
		})
		if icsErr == nil {
			return content, journeyResponse{ScheduleTimeline: schedule, ICS: ics}, nil
		}
	}
	return content, schedule, nil
}

// journeyResponse wraps the schedule with an optional iCalendar export when the
// caller requests the calendar handoff via as_ics.
type journeyResponse struct {
	models.ScheduleTimeline
	ICS string `json:"ics,omitempty"`
}
