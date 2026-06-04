package transfer

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tm
}

func TestBuildSchedule_Success_GroundedAirport(t *testing.T) {
	// HEL is in the KB (intl buffer 120). Train ground leg, low variance.
	in := ScheduleInput{
		DepartureLocal: mustParse(t, "2026-07-18 09:40"),
		AirportCode:    "HEL",
		International:  true,
		GroundMinutes:  30,
		GroundMode:     "train",
		OriginWalkMin:  5,
		GroundLabel:    "Train I to Helsinki Airport",
	}
	s := BuildSchedule(in)

	if s.BufferMinutes != 120 {
		t.Errorf("buffer = %d, want 120 (HEL intl KB)", s.BufferMinutes)
	}
	if s.Confidence != "high" {
		t.Errorf("confidence = %q, want high (grounded buffer + low-variance train)", s.Confidence)
	}
	// leave_by = 09:40 - (120 + 30 + 5 variance + 5 walk + 15 safety) = -175 min => 06:45
	if s.LeaveHomeBy != "06:45" {
		t.Errorf("leave_home_by = %q, want 06:45", s.LeaveHomeBy)
	}
	if len(s.Steps) < 3 {
		t.Errorf("expected >=3 timeline rows, got %d", len(s.Steps))
	}
	if s.Steps[len(s.Steps)-1].Time != "09:40" {
		t.Errorf("last row should be the departure at 09:40, got %q", s.Steps[len(s.Steps)-1].Time)
	}
}

// TestBuildSchedule_NeverOptimistic is the safety invariant: leave_by must
// always be earlier than departure minus the airport buffer alone — i.e. the
// schedule never assumes you can arrive later than the check-in buffer allows.
func TestBuildSchedule_NeverOptimistic(t *testing.T) {
	cases := []ScheduleInput{
		{DepartureLocal: mustParse(t, "2026-07-18 09:40"), AirportCode: "HEL", International: true, GroundMinutes: 30, GroundMode: "train"},
		{DepartureLocal: mustParse(t, "2026-07-18 23:55"), AirportCode: "ZZZ", International: true, GroundMinutes: 50, GroundMode: "taxi", OriginWalkMin: 3},
		{DepartureLocal: mustParse(t, "2026-07-18 06:10"), AirportCode: "BCN", International: false, GroundMinutes: 20, GroundMode: "bus"},
	}
	for i, in := range cases {
		s := BuildSchedule(in)
		leaveBy := mustParse(t, "2026-07-18 "+s.LeaveHomeBy)
		latestSafe := in.DepartureLocal.Add(-time.Duration(s.BufferMinutes) * time.Minute).Add(-time.Duration(in.GroundMinutes) * time.Minute)
		if !leaveBy.Before(latestSafe) && !leaveBy.Equal(latestSafe) {
			t.Errorf("case %d: leave_by %s is optimistic — not earlier than departure - buffer - ground (%s)", i, s.LeaveHomeBy, latestSafe.Format("15:04"))
		}
	}
}

func TestBuildSchedule_UnknownAirportIsConservativeAndLowerConfidence(t *testing.T) {
	in := ScheduleInput{
		DepartureLocal: mustParse(t, "2026-07-18 12:00"),
		AirportCode:    "ZZZ", // not in KB
		International:  true,
		GroundMinutes:  40,
		GroundMode:     "taxi",
	}
	s := BuildSchedule(in)
	if s.BufferMinutes != defaultIntlBufferMin {
		t.Errorf("unknown airport must use conservative default %d, got %d", defaultIntlBufferMin, s.BufferMinutes)
	}
	if s.Confidence == "high" {
		t.Errorf("unknown buffer + taxi must not be high confidence, got %q", s.Confidence)
	}
}

func TestTransferVariance_RailSteadierThanRoad(t *testing.T) {
	if transferVarianceMin("train") >= transferVarianceMin("taxi") {
		t.Errorf("train variance (%d) must be less than taxi (%d)", transferVarianceMin("train"), transferVarianceMin("taxi"))
	}
}
