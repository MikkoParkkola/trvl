// Package calendarbusy provides a lightweight busy-interval lookup so
// `trvl find` can skip Saturdays that clash with existing calendar events.
//
// Two providers are supported out of the box (both opt-in, silent fallback
// to empty when unavailable):
//
//   - Google Workspace via the `gws calendar agenda` CLI. Chosen because
//     gws is already part of Mikko's environment (see ~/.claude/CLAUDE.md).
//     We never embed OAuth — we shell out to the pre-authenticated CLI.
//
//   - Local macOS Calendar via `icalBuddy`. Present by default on macOS;
//     zero-config. Covers iCloud-mirrored work + family calendars.
//
// When neither provider is available, Query returns an empty slice and
// nil error — callers treat "no busy data" as "every Saturday is free".
package calendarbusy

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

// Interval is a closed calendar range [Start, End] in ISO 8601 calendar
// date form. Multi-day events span multiple dates; single-day events have
// Start == End.
type Interval struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Title string `json:"title,omitempty"`
}

// Execer is the shell-out injection point used by tests.
type Execer func(ctx context.Context, name string, args ...string) ([]byte, error)

// defaultExecer runs the command through os/exec.
var defaultExecer Execer = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// Query returns the union of busy intervals across all available calendar
// providers for the next `days` days. Each provider is best-effort; errors
// from a single source are silently dropped so one misconfigured provider
// does not block the rest.
func Query(ctx context.Context, days int) ([]Interval, error) {
	return QueryWithExec(ctx, days, defaultExecer)
}

// QueryWithExec is the testable variant — pass a fake Execer to stub the
// external CLIs.
func QueryWithExec(ctx context.Context, days int, exec Execer) ([]Interval, error) {
	var all []Interval
	if out, err := exec(ctx, "gws", "calendar", "agenda", "--days", itoa(days), "--format", "json"); err == nil {
		all = append(all, parseGwsAgenda(out)...)
	}
	if out, err := exec(ctx, "icalBuddy", "-f", "-n", "-ea", "-nc", "-b", "", "-iep", "title,datetime", "-po", "title,datetime", "eventsFrom:today", "to:today+"+itoa(days)); err == nil {
		all = append(all, parseICalBuddy(out)...)
	}
	return all, nil
}

// Overlaps reports whether `date` (an ISO 8601 calendar date) falls within
// any of the given busy intervals. Used by the date-inference path.
func Overlaps(date string, busy []Interval) bool {
	for _, b := range busy {
		if date >= b.Start && date <= b.End {
			return true
		}
	}
	return false
}

// parseGwsAgenda parses the JSON output of `gws calendar agenda`. The
// expected shape is a list of events with start/end fields. Robust to
// missing or non-object elements — invalid entries are skipped silently.
func parseGwsAgenda(raw []byte) []Interval {
	var events []map[string]any
	if err := json.Unmarshal(raw, &events); err != nil {
		// Try the wrapped-object shape some gws versions emit:
		var wrapped struct {
			Events []map[string]any `json:"events"`
		}
		if json.Unmarshal(raw, &wrapped) == nil {
			events = wrapped.Events
		}
	}
	out := make([]Interval, 0, len(events))
	for _, ev := range events {
		start := extractGwsDate(ev, "start")
		end := extractGwsDate(ev, "end")
		if start == "" {
			continue
		}
		if end == "" {
			end = start
		}
		title, _ := ev["summary"].(string)
		if title == "" {
			title, _ = ev["title"].(string)
		}
		out = append(out, Interval{Start: start, End: end, Title: title})
	}
	return out
}

// extractGwsDate pulls a calendar date from a gws event map. Supports
// both the all-day shape (pure date) and the timed shape (ISO 8601
// datetime with timezone).
func extractGwsDate(ev map[string]any, key string) string {
	v, ok := ev[key]
	if !ok {
		return ""
	}
	// Direct string form.
	if s, ok := v.(string); ok {
		return datePrefix(s)
	}
	// Nested object {"date": "..."} or {"dateTime": "..."}.
	if m, ok := v.(map[string]any); ok {
		if s, _ := m["date"].(string); s != "" {
			return datePrefix(s)
		}
		if s, _ := m["dateTime"].(string); s != "" {
			return datePrefix(s)
		}
	}
	return ""
}

// datePrefix returns the first 10 chars of a date-or-datetime string when
// it looks like ISO 8601, else empty.
func datePrefix(s string) string {
	if len(s) < 10 {
		return ""
	}
	// Cheap validation: positions 4 and 7 must be dashes.
	if s[4] != '-' || s[7] != '-' {
		return ""
	}
	return s[:10]
}

// parseICalBuddy parses `icalBuddy` output. icalBuddy emits plaintext with
// one event per line after the title (e.g. `at 2026-05-02 14:00 - 15:00`
// or simply `at 2026-05-02`). We only need the date part.
func parseICalBuddy(raw []byte) []Interval {
	lines := strings.Split(string(raw), "\n")
	var out []Interval
	var currentTitle string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Lines starting "at " are datetime lines; everything else is a title.
		if strings.HasPrefix(line, "at ") {
			rest := strings.TrimPrefix(line, "at ")
			date := datePrefix(rest)
			if date == "" {
				continue
			}
			out = append(out, Interval{Start: date, End: date, Title: currentTitle})
			currentTitle = ""
		} else {
			currentTitle = line
		}
	}
	return out
}

// itoa is a tiny local variant so we avoid strconv in hot paths. Given
// days is bounded to [0, 365], this is strictly safe.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		n = -n
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// NextFreeSaturday walks forward from `from` and returns the first
// Saturday that is at least `minBufferDays` days out and does NOT overlap
// any busy interval. When no free Saturday is found within `maxLookDays`,
// returns the Saturday at the buffer boundary anyway so the caller has
// something to work with.
//
// Returned date is an ISO 8601 calendar date.
func NextFreeSaturday(from time.Time, minBufferDays, maxLookDays int, busy []Interval) string {
	target := from.AddDate(0, 0, minBufferDays)
	offset := (int(time.Saturday) - int(target.Weekday()) + 7) % 7
	target = target.AddDate(0, 0, offset)
	fallback := target.Format("2006-01-02")
	deadline := from.AddDate(0, 0, maxLookDays)
	for !target.After(deadline) {
		date := target.Format("2006-01-02")
		if !Overlaps(date, busy) {
			return date
		}
		target = target.AddDate(0, 0, 7)
	}
	return fallback
}
