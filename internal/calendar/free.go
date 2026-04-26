package calendar

// MIK-3066: free-window detection for the opportunity watcher.
//
// The watcher needs to know which contiguous date stretches in the
// next N days are clear of busy events. This file converts the
// busy-interval list emitted by internal/calendarbusy (Apple icalBuddy
// + gws calendar) into a list of FreeWindow{Start, End, Nights}.
//
// Pure logic lives in ComputeFreeWindows so the cross-cutting concerns
// (shelling out, OAuth, OS detection) stay in calendarbusy and we only
// reason about Interval slices here. That keeps this file portable
// across macOS/Linux/CI without build tags.

import (
	"context"
	"regexp"
	"sort"
	"time"

	"github.com/MikkoParkkola/trvl/internal/calendarbusy"
)

// FreeWindow is a contiguous run of free calendar days. Start is the
// first free day; End is the last free day (inclusive); Nights is the
// number of free days in the run, which is also the number of nights a
// traveller could sleep at a destination before having to return home
// for a busy event. A 3-day free run [May 1, May 2, May 3] yields
// Nights=3 — depart morning of May 1, sleep May 1/2/3 nights, return
// morning of May 4 (the next busy day).
type FreeWindow struct {
	Start  time.Time
	End    time.Time
	Nights int
}

// Adapter is the abstraction the opportunity watcher consumes. The
// default implementation is calendarbusy-backed; tests inject fakes.
type Adapter interface {
	FreeWindows(ctx context.Context, from time.Time, days, minNights int) ([]FreeWindow, error)
}

// DefaultAdapter wires the calendarbusy queries to the FreeWindow
// computation. Construct via NewDefaultAdapter().
type DefaultAdapter struct {
	queryFunc func(ctx context.Context, days int) ([]calendarbusy.Interval, error)
}

// NewDefaultAdapter returns an Adapter backed by calendarbusy.Query
// (the production wiring: Apple Calendar via icalBuddy + Google via gws).
func NewDefaultAdapter() *DefaultAdapter {
	return &DefaultAdapter{queryFunc: calendarbusy.Query}
}

// FreeWindows fetches busy intervals via the configured queryFunc, then
// returns FreeWindow stretches of at least minNights nights inside
// [from, from+days].
func (a *DefaultAdapter) FreeWindows(ctx context.Context, from time.Time, days, minNights int) ([]FreeWindow, error) {
	if a == nil || a.queryFunc == nil {
		return nil, nil
	}
	busy, err := a.queryFunc(ctx, days)
	if err != nil {
		return nil, err
	}
	return ComputeFreeWindows(busy, from, days, minNights), nil
}

// travelTitlePattern is the heuristic that recognises "this is a trip"
// all-day events (Booking confirmations, "Trip to ...", "BCN-AMS",
// "Vacation"). Such events are NOT blockers for the watcher — they ARE
// the trip we'd be replicating. Compiled once for hot-path use.
var travelTitlePattern = regexp.MustCompile(`(?i)\b(trip|travel|vacation|holiday|flight|booking|hotel|airbnb|cruise)\b`)

// IsTravelLikeTitle returns true when the event title looks like a
// pre-existing travel block. Such events are excluded from "busy"
// during free-window computation.
func IsTravelLikeTitle(title string) bool {
	if title == "" {
		return false
	}
	return travelTitlePattern.MatchString(title)
}

// ComputeFreeWindows enumerates [from, from+days] day-by-day and
// returns every maximal run of consecutive free dates that is at least
// minNights nights long. A "night" is one calendar-day step inside the
// run, so Start..End spanning 3 dates → 2 nights.
//
// Pure: no I/O. Robust to malformed Interval entries (skipped silently
// when start/end fail to parse as ISO 8601 calendar dates).
func ComputeFreeWindows(busy []calendarbusy.Interval, from time.Time, days, minNights int) []FreeWindow {
	if days <= 0 || minNights < 1 {
		return nil
	}
	// Normalise `from` to its calendar date (UTC). The opportunity
	// watcher reasons in calendar days, not seconds.
	fy, fm, fd := from.Date()
	start := time.Date(fy, fm, fd, 0, 0, 0, 0, time.UTC)

	// Build a fast date-busy lookup. We expand multi-day intervals
	// by walking from Start to End inclusive.
	busySet := make(map[string]struct{}, len(busy)*2)
	for _, b := range busy {
		// Travel-like events are NOT blockers.
		if IsTravelLikeTitle(b.Title) {
			continue
		}
		bs, ok1 := parseDate(b.Start)
		be, ok2 := parseDate(b.End)
		if !ok1 {
			continue
		}
		if !ok2 || be.Before(bs) {
			be = bs
		}
		for d := bs; !d.After(be); d = d.AddDate(0, 0, 1) {
			busySet[d.Format("2006-01-02")] = struct{}{}
		}
	}

	var out []FreeWindow
	var runStart, lastFree time.Time
	count := 0
	flush := func() {
		if count == 0 {
			return
		}
		if count >= minNights {
			out = append(out, FreeWindow{Start: runStart, End: lastFree, Nights: count})
		}
		count = 0
	}

	// Walk exactly `days` calendar dates starting at `from`. We do NOT
	// include `from + days` itself — `days` is a half-open interval
	// length so days=7 covers exactly one week, not eight days.
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i)
		key := d.Format("2006-01-02")
		if _, isBusy := busySet[key]; isBusy {
			flush()
			continue
		}
		if count == 0 {
			runStart = d
		}
		lastFree = d
		count++
	}
	flush()

	// Ensure deterministic order even if busySet iteration changes anything.
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// parseDate accepts the ISO 8601 calendar-date prefix emitted by calendarbusy.
func parseDate(s string) (time.Time, bool) {
	if len(s) < 10 {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", s[:10])
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
