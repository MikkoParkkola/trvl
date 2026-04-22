package calendarbusy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestOverlaps_Boundaries(t *testing.T) {
	t.Parallel()
	busy := []Interval{
		{Start: "2026-05-10", End: "2026-05-12", Title: "Amsterdam workshop"},
	}
	cases := []struct {
		date string
		want bool
	}{
		{"2026-05-09", false},
		{"2026-05-10", true}, // inclusive start
		{"2026-05-11", true},
		{"2026-05-12", true}, // inclusive end
		{"2026-05-13", false},
	}
	for _, c := range cases {
		if got := Overlaps(c.date, busy); got != c.want {
			t.Errorf("Overlaps(%q) = %v, want %v", c.date, got, c.want)
		}
	}
}

func TestNextFreeSaturday_SkipsBusySaturday(t *testing.T) {
	t.Parallel()
	// Wed 2026-05-06. 14d buffer lands on Wed 2026-05-20 → next Saturday is
	// 2026-05-23. Mark that Saturday busy; expected pick is 2026-05-30.
	wed := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	busy := []Interval{{Start: "2026-05-23", End: "2026-05-23", Title: "conference"}}
	got := NextFreeSaturday(wed, 14, 60, busy)
	if got != "2026-05-30" {
		t.Errorf("NextFreeSaturday skipping 05-23 = %q, want 2026-05-30", got)
	}
}

func TestNextFreeSaturday_EverythingBusyReturnsFallback(t *testing.T) {
	t.Parallel()
	wed := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	// Mark all Saturdays inside the next 60 days as busy.
	busy := []Interval{{Start: "2026-05-01", End: "2026-07-31"}}
	got := NextFreeSaturday(wed, 14, 60, busy)
	if got != "2026-05-23" {
		t.Errorf("no free Saturday in window should fall back to the first candidate (2026-05-23), got %q", got)
	}
}

func TestParseGwsAgenda_HandlesAllDayAndTimedShapes(t *testing.T) {
	t.Parallel()
	raw := []byte(`[
		{"summary":"Team offsite","start":{"date":"2026-05-10"},"end":{"date":"2026-05-12"}},
		{"summary":"Coffee","start":{"dateTime":"2026-05-14T09:00:00+02:00"},"end":{"dateTime":"2026-05-14T10:00:00+02:00"}},
		{"summary":"Missing start","end":"2026-05-20"},
		"not-an-object"
	]`)
	got := parseGwsAgenda(raw)
	if len(got) != 2 {
		t.Fatalf("expected 2 valid intervals, got %d: %v", len(got), got)
	}
	if got[0].Start != "2026-05-10" || got[0].End != "2026-05-12" || got[0].Title != "Team offsite" {
		t.Errorf("all-day parse mismatch: %+v", got[0])
	}
	if got[1].Start != "2026-05-14" || got[1].End != "2026-05-14" || got[1].Title != "Coffee" {
		t.Errorf("timed parse mismatch: %+v", got[1])
	}
}

func TestParseGwsAgenda_WrappedShape(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"events":[{"title":"X","start":"2026-06-01","end":"2026-06-01"}]}`)
	got := parseGwsAgenda(raw)
	if len(got) != 1 || got[0].Start != "2026-06-01" || got[0].Title != "X" {
		t.Errorf("wrapped shape not parsed: %v", got)
	}
}

func TestParseICalBuddy_SingleDay(t *testing.T) {
	t.Parallel()
	raw := []byte("Flight to ARN\n    at 2026-05-10 09:00 - 11:00\nDentist\n    at 2026-05-12")
	got := parseICalBuddy(raw)
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(got), got)
	}
	if got[0].Title != "Flight to ARN" || got[0].Start != "2026-05-10" {
		t.Errorf("first event mismatch: %+v", got[0])
	}
	if got[1].Title != "Dentist" || got[1].Start != "2026-05-12" {
		t.Errorf("second event mismatch: %+v", got[1])
	}
}

func TestQueryWithExec_BothProvidersMissingReturnsEmpty(t *testing.T) {
	t.Parallel()
	execer := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, errors.New("command not found")
	}
	got, err := QueryWithExec(context.Background(), 30, execer)
	if err != nil {
		t.Errorf("expected nil error when providers missing, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result when no providers, got %v", got)
	}
}

func TestQueryWithExec_UnionsAcrossProviders(t *testing.T) {
	t.Parallel()
	execer := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "gws":
			return []byte(`[{"summary":"gws","start":"2026-05-10","end":"2026-05-10"}]`), nil
		case "icalBuddy":
			return []byte("Apple\n    at 2026-05-17"), nil
		}
		return nil, errors.New("unexpected")
	}
	got, err := QueryWithExec(context.Background(), 30, execer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events across both providers, got %d: %v", len(got), got)
	}
	titles := []string{got[0].Title, got[1].Title}
	if !contains(titles, "gws") || !contains(titles, "Apple") {
		t.Errorf("expected both titles, got %v", titles)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.EqualFold(s, needle) {
			return true
		}
	}
	return false
}
