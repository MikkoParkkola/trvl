package transfer

import (
	"fmt"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/transfer/airportkb"
)

// Default conservative buffers (minutes) used when the airport KB has no entry.
// Deliberately generous: the scheduler errs toward "leave earlier", never
// optimistic. A missing profile lowers confidence, never shortens the buffer.
const (
	defaultIntlBufferMin     = 150 // 2h30 — conservative when airport unknown
	defaultDomesticBufferMin = 90
	safetyMarginMin          = 15 // fixed floor added on top of everything
)

// transferVarianceMin returns a padding (minutes) for the chosen ground mode,
// reflecting real-world reliability: rail is steady, road is not.
func transferVarianceMin(mode string) int {
	switch mode {
	case "train", "metro", "airport_express":
		return 5
	case "bus", "transit", "mixed":
		return 12
	case "taxi", "ride_hail":
		return 20 // traffic-exposed
	case "private_transfer":
		return 10
	default:
		return 12
	}
}

// ScheduleInput configures a Leave-By computation for the outbound airport leg.
type ScheduleInput struct {
	DepartureLocal time.Time // flight (or train) scheduled departure, local time
	AirportCode    string    // IATA, for the curated buffer KB
	International  bool      // intl vs domestic check-in/security buffer
	GroundMinutes  int       // chosen ground option door-to-door minutes (home -> airport)
	GroundMode     string    // chosen ground mode (for variance)
	OriginWalkMin  int       // walk from door to the first stop, if any
	GroundLabel    string    // e.g. "Train I to Helsinki Airport"
}

// BuildSchedule computes the Leave-By timeline by backward induction from the
// departure anchor. Every term is grounded or conservative and surfaced in
// Assumptions; the result is never optimistic (see invariant test).
func BuildSchedule(in ScheduleInput) models.ScheduleTimeline {
	buffer, bufferGrounded := airportBuffer(in.AirportCode, in.International)
	variance := transferVarianceMin(in.GroundMode)

	totalLead := buffer + in.GroundMinutes + variance + in.OriginWalkMin + safetyMarginMin
	leaveBy := in.DepartureLocal.Add(-time.Duration(totalLead) * time.Minute)
	arriveAirport := in.DepartureLocal.Add(-time.Duration(buffer) * time.Minute)

	rows := []models.SchedRow{
		{Time: clock(leaveBy), Text: "Leave home" + labelSuffix(in.GroundLabel)},
	}
	if in.OriginWalkMin > 0 {
		rows = append(rows, models.SchedRow{
			Time: clock(leaveBy.Add(time.Duration(in.OriginWalkMin) * time.Minute)),
			Text: fmt.Sprintf("Reach first stop (%d-min walk)", in.OriginWalkMin),
		})
	}
	rows = append(rows,
		models.SchedRow{Time: clock(arriveAirport), Text: fmt.Sprintf("Arrive at airport — allow %d min for %s", buffer, checkinPhrase(in.International))},
		models.SchedRow{Time: clock(in.DepartureLocal), Text: "Scheduled departure"},
	)

	assumptions := []string{
		fmt.Sprintf("%d-min airport buffer (%s)", buffer, checkinPhrase(in.International)),
		fmt.Sprintf("%d-min ground transfer + %d-min variance (%s)", in.GroundMinutes, variance, modeWord(in.GroundMode)),
		fmt.Sprintf("%d-min safety margin", safetyMarginMin),
	}
	if in.OriginWalkMin > 0 {
		assumptions = append(assumptions, fmt.Sprintf("%d-min walk to first stop", in.OriginWalkMin))
	}

	conf := confidence(bufferGrounded, in.GroundMode)

	return models.ScheduleTimeline{
		LeaveHomeBy:   clock(leaveBy),
		Steps:         rows,
		BufferMinutes: buffer,
		Assumptions:   assumptions,
		Confidence:    conf,
		Fallback:      "", // populated by the journey planner when a later departure still clears a reduced buffer
	}
}

// airportBuffer returns the recommended check-in+security buffer and whether it
// came from the curated KB (grounded) vs a conservative default.
func airportBuffer(code string, intl bool) (minutes int, grounded bool) {
	if p, ok := airportkb.Lookup(code); ok {
		if intl && p.IntlBufferMin > 0 {
			return p.IntlBufferMin, true
		}
		if !intl && p.DomesticBufferMin > 0 {
			return p.DomesticBufferMin, true
		}
	}
	if intl {
		return defaultIntlBufferMin, false
	}
	return defaultDomesticBufferMin, false
}

func confidence(bufferGrounded bool, mode string) string {
	lowVariance := mode == "train" || mode == "metro" || mode == "airport_express"
	switch {
	case bufferGrounded && lowVariance:
		return "high"
	case bufferGrounded || lowVariance:
		return "medium"
	default:
		return "low"
	}
}

func checkinPhrase(intl bool) string {
	if intl {
		return "check-in + security (international)"
	}
	return "check-in + security (domestic)"
}

func modeWord(mode string) string {
	if mode == "" {
		return "transfer"
	}
	return mode
}

func labelSuffix(label string) string {
	if label == "" {
		return ""
	}
	return " — " + label
}

func clock(t time.Time) string {
	return t.Format("15:04")
}
