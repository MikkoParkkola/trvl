package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/MikkoParkkola/trvl/internal/calendar"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/transfer"
	"github.com/MikkoParkkola/trvl/internal/trip"
)

// journeyTransferSearch is a test seam over the home->airport ground search so
// the default suite stays deterministic (no live network). Production points at
// trip.SearchAirportTransfers, which already resolves airport codes to cities
// and merges transit + ground providers + a taxi estimate.
var journeyTransferSearch = trip.SearchAirportTransfers

// handleJourney exposes the Leave-By Scheduler: given a flight departure and how
// the traveller reaches the airport, it answers "when do I leave home to be
// comfortably (not last-minute) at the gate?" with a grounded, conservative
// timeline. It is a smart capability reachable via the `travel` router intent
// `plan_journey`; it is deliberately NOT advertised as a compatibility alias
// (it is a capability, not a legacy tool name).
//
// Ground leg: pass ground_minutes + ground_mode explicitly (from a transfer
// search), OR pass origin to auto-compute the home->airport leg — when origin is
// set and no explicit ground option is given, plan_journey searches the leg,
// returns the full comparison card (so the traveller can still choose), and
// schedules from the best-value option.
func handleJourney(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	airport := argString(args, "airport_code")
	date := argString(args, "date")
	depTime := argString(args, "departure_time")
	if airport == "" || date == "" || depTime == "" {
		return nil, nil, fmt.Errorf("airport_code, date, and departure_time are required")
	}
	groundMin := argInt(args, "ground_minutes", 0)
	groundMode := argString(args, "ground_mode")
	groundLabel := argString(args, "ground_label")

	// Auto-stitch the home->airport leg (B.1-phase2) when an origin is given and
	// no explicit ground option was passed.
	var comparison *models.TransferComparison
	if origin := argString(args, "origin"); origin != "" && (groundMin <= 0 || groundMode == "") {
		res, serr := journeyTransferSearch(ctx, trip.AirportTransferInput{
			AirportCode: airport,
			Destination: origin,
			Date:        date,
			Currency:    argString(args, "currency"),
		})
		if serr == nil && res != nil && res.Success && len(res.Routes) > 0 {
			card := transfer.BuildOptions(res.Routes, airport, origin, airport)
			comparison = &card
			if best := pickScheduledOption(card); best != nil {
				groundMin = best.DoorToDoorMin
				groundMode = best.Mode
				if groundLabel == "" {
					groundLabel = best.Label
				}
			}
		}
	}

	if groundMin <= 0 || groundMode == "" {
		return nil, nil, fmt.Errorf("provide ground_minutes (>0) + ground_mode from a transfer search, or pass origin to auto-compute the home-to-airport leg")
	}

	departure, err := time.Parse("2006-01-02 15:04", date+" "+depTime)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid date or departure_time (date as 4-digit-year-month-day, time as HH:MM): %w", err)
	}

	intl := argBool(args, "international", true)
	schedule := transfer.BuildSchedule(transfer.ScheduleInput{
		DepartureLocal: departure,
		AirportCode:    airport,
		International:  intl,
		GroundMinutes:  groundMin,
		GroundMode:     groundMode,
		OriginWalkMin:  argInt(args, "origin_walk_min", 0),
		GroundLabel:    groundLabel,
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
	if comparison != nil && len(comparison.Options) > 1 {
		summary += "\n\nGround leg scheduled with the best-value option; the full comparison is attached so you can choose another."
	}

	content := []ContentBlock{
		{Type: "text", Text: summary, Annotations: &ContentAnnotation{Audience: []string{"user"}, Priority: 1.0}},
	}

	// Optional calendar handoff (F.1): when as_ics is set, attach an iCalendar
	// "Leave home" event with a reminder alarm so the user can drop it straight
	// into Apple/Google/Outlook. Additive — does not change the default output.
	var ics string
	if argBool(args, "as_ics", false) {
		if out, icsErr := calendar.ScheduleICS(calendar.ScheduleICSInput{
			Date:          date,
			AirportCode:   airport,
			DepartureTime: depTime,
			ReminderMin:   argInt(args, "reminder_minutes", 30),
			Schedule:      schedule,
		}); icsErr == nil {
			ics = out
		}
	}

	if ics != "" || comparison != nil {
		return content, journeyResponse{ScheduleTimeline: schedule, ICS: ics, GroundComparison: comparison}, nil
	}
	return content, schedule, nil
}

// pickScheduledOption chooses the default ground option to schedule from: the
// best-value mode when labelled, else the first option. The full card is still
// returned so the traveller can re-run with a specific mode.
func pickScheduledOption(card models.TransferComparison) *models.TransferOption {
	if len(card.Options) == 0 {
		return nil
	}
	if card.BestValue != "" {
		for i := range card.Options {
			if card.Options[i].Mode == card.BestValue {
				return &card.Options[i]
			}
		}
	}
	return &card.Options[0]
}

// journeyResponse wraps the schedule with an optional iCalendar export and the
// optional auto-computed home->airport comparison card.
type journeyResponse struct {
	models.ScheduleTimeline
	ICS              string                     `json:"ics,omitempty"`
	GroundComparison *models.TransferComparison `json:"ground_comparison,omitempty"`
}
