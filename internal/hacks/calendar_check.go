package hacks

import (
	"context"
	"fmt"
	"time"
)

// holidayPeriod describes a European peak-travel period that drives prices up.
type holidayPeriod struct {
	Name string
	// Start and End are month-day strings "MM-DD". For Easter (which varies
	// yearly) we compute dynamically. YearOffset allows marking "end of June"
	// through "end of August" with a single entry spanning multiple months.
	MonthStart int
	DayStart   int
	MonthEnd   int
	DayEnd     int
}

// staticHolidayPeriods lists recurring fixed-date European holiday windows.
// Easter is handled separately via computeEaster.
var staticHolidayPeriods = []holidayPeriod{
	{Name: "Summer holidays", MonthStart: 6, DayStart: 22, MonthEnd: 8, DayEnd: 31},
	{Name: "Christmas/New Year", MonthStart: 12, DayStart: 20, MonthEnd: 1, DayEnd: 5},
	{Name: "October half-term", MonthStart: 10, DayStart: 12, MonthEnd: 10, DayEnd: 23},
	{Name: "February ski week", MonthStart: 2, DayStart: 12, MonthEnd: 2, DayEnd: 23},
	{Name: "May Day / Ascension cluster", MonthStart: 4, DayStart: 28, MonthEnd: 5, DayEnd: 6},
}

// detectCalendarConflict warns if the requested date falls inside a European
// peak travel period and suggests shifting by ±1 week (if cheaper).
func detectCalendarConflict(ctx context.Context, in DetectorInput) []Hack {
	if !in.valid() || in.Date == "" {
		return nil
	}

	t, err := parseDate(in.Date)
	if err != nil {
		return nil
	}

	period := findPeakPeriod(t)
	if period == "" {
		return nil
	}

	// Try shifting ±7 days and ±14 days outside the peak window.
	alts := []struct {
		delta int
		label string
	}{
		{-7, "one week earlier"},
		{7, "one week later"},
		{-14, "two weeks earlier"},
		{14, "two weeks later"},
	}

	type candidate struct {
		date  string
		label string
	}
	// Find alts that fall outside the peak.
	var outside []candidate
	for _, a := range alts {
		altDate := addDays(in.Date, a.delta)
		if altDate == "" {
			continue
		}
		altT, err := parseDate(altDate)
		if err != nil {
			continue
		}
		if findPeakPeriod(altT) == "" {
			outside = append(outside, candidate{date: altDate, label: a.label})
		}
	}

	if len(outside) == 0 {
		// Still flag the conflict even without a clear off-peak alternative.
		return []Hack{{
			Type:        "calendar_conflict",
			Title:       fmt.Sprintf("Peak period: %s — prices elevated", period),
			Currency:    in.currency(),
			Savings:     0,
			Description: fmt.Sprintf("Your travel date %s falls during %s, a major European holiday period. Prices are typically 20-40%% higher. All nearby dates are also in a peak period.", in.Date, period),
			Risks:       []string{"Flight, hotel, and activity prices elevated during peak periods"},
			Steps: []string{
				"If dates are flexible, try shifting by 2+ weeks",
				"Book as early as possible to lock in lower fares",
				"Consider less popular destinations during this period",
			},
		}}
	}

	// Return the first outside-peak suggestion.
	best := outside[0]
	return []Hack{{
		Type:     "calendar_conflict",
		Title:    fmt.Sprintf("Avoid %s — shift %s", period, best.label),
		Currency: in.currency(),
		Savings:  0, // saving is indicative; actual depends on flight search
		Description: fmt.Sprintf(
			"Your travel date %s falls during %s, a European peak period with elevated prices (typically 20-40%% above normal). "+
				"Travelling %s (%s) avoids this window.",
			in.Date, period, best.label, best.date,
		),
		Risks: []string{
			"Estimated saving is indicative — verify by searching the suggested date",
			"Accommodation and attractions may also be significantly pricier during peak",
		},
		Steps: []string{
			fmt.Sprintf("Try searching for %s instead of %s", best.date, in.Date),
			"Compare prices for both dates before committing",
			"Update hotel bookings if switching dates",
		},
		Citations: []string{
			googleFlightsURL(in.Destination, in.Origin, best.date),
		},
	}}
}

// findPeakPeriod returns the name of the holiday period that contains t, or
// empty string if t is not in a peak period.
func findPeakPeriod(t time.Time) string {
	y := t.Year()

	// Check static periods.
	for _, p := range staticHolidayPeriods {
		start := dateOf(y, p.MonthStart, p.DayStart)
		end := dateOf(y, p.MonthEnd, p.DayEnd)
		// Christmas/New Year spans year boundary.
		if p.MonthStart > p.MonthEnd {
			// e.g. Dec 20 → Jan 5: check Dec part in year y and Jan part in y+1.
			if inRange(t, start, dateOf(y, 12, 31)) || inRange(t, dateOf(y, 1, 1), dateOf(y, p.MonthEnd, p.DayEnd)) {
				return p.Name
			}
			continue
		}
		if inRange(t, start, end) {
			return p.Name
		}
	}

	// Easter: ±10 days around Easter Sunday.
	easter := computeEaster(y)
	if inRange(t, easter.AddDate(0, 0, -10), easter.AddDate(0, 0, 10)) {
		return "Easter"
	}

	return ""
}

// dateOf returns time.Time for year y, month m, day d at midnight UTC.
func dateOf(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

// inRange returns true if t is within [start, end] inclusive (date only).
func inRange(t, start, end time.Time) bool {
	tDate := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return !tDate.Before(start) && !tDate.After(end)
}

// computeEaster computes Easter Sunday for the given year using the
// Anonymous Gregorian algorithm.
func computeEaster(year int) time.Time {
	a := year % 19
	b := year / 100
	c := year % 100
	d := b / 4
	e := b % 4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i := c / 4
	k := c % 4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := ((h + l - 7*m + 114) % 31) + 1
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}
