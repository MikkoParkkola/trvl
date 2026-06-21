package main

import (
	"fmt"
	"io"
	"time"

	"github.com/MikkoParkkola/trvl/internal/counterfactual"
	"github.com/MikkoParkkola/trvl/internal/dategrid"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/probecache"
)

// probeCacheFreshness bounds how recent a Tier-1 cached probe must be to be
// served. Beyond it the cached fan-out result is treated as stale and skipped.
const probeCacheFreshness = 12 * time.Hour

// gridFreshness is how recent a persisted price grid must be to drive a
// call-free shift-day counterfactual. Beyond this it is treated as stale and
// not surfaced (TRVL.CF.5: never present stale data as live).
const gridFreshness = 24 * time.Hour

// shiftDaySavings computes call-free shift-day counterfactuals for a single-date
// flight search by reading a PERSISTED price grid (written by the watch
// scheduler). It issues no provider calls and returns nothing when no fresh grid
// covers the route — honest silence over a fabricated or stale claim.
func shiftDaySavings(origin, destination, date string, now time.Time) []counterfactual.Saving {
	store, err := dategrid.DefaultStore()
	if err != nil || store.Load() != nil {
		return nil
	}
	g, ok := store.Get(dategrid.RouteKey(origin, destination))
	if !ok || !g.Fresh(now, gridFreshness) {
		return nil
	}
	grid := make([]models.DatePriceResult, 0, len(g.Points))
	for _, p := range g.Points {
		grid = append(grid, models.DatePriceResult{
			Date:       p.Date,
			ReturnDate: p.ReturnDate,
			Price:      p.Price,
			Currency:   p.Currency,
		})
	}
	return counterfactual.ShiftDay(grid, date, 10, g.UpdatedAt)
}

// tier1CachedSavings returns Tier-1 counterfactual savings pre-computed by the
// watch scheduler for this route (MIK-6234). Served call-free from the probe
// cache; nothing is returned when no fresh entry exists. No provider calls.
func tier1CachedSavings(origin, destination string, now time.Time) []counterfactual.Saving {
	store, err := probecache.DefaultStore()
	if err != nil || store.Load() != nil {
		return nil
	}
	e, ok := store.Get(probecache.RouteKey(origin, destination))
	if !ok || !e.Fresh(now, probeCacheFreshness) {
		return nil
	}
	return e.Savings
}

// printSavings renders counterfactual savings, split by whether they were free.
// Call-free savings (MIK-6234 Tier 0) carry the explicit "no extra searches"
// guarantee; probed savings (Tier 2) are labelled as found by a deeper search so
// the output never implies a fan-out that did not happen, or hides one that did.
// Each line carries an "as of" age when its data is not from this moment
// (TRVL.CF.5 honesty).
func printSavings(w io.Writer, savings []counterfactual.Saving, now time.Time) {
	if len(savings) == 0 {
		return
	}
	var free, probed []counterfactual.Saving
	for _, s := range savings {
		if s.CallFree {
			free = append(free, s)
		} else {
			probed = append(probed, s)
		}
	}
	if len(free) > 0 {
		fmt.Fprintln(w, "\nSavings you could capture (no extra searches):")
		for _, s := range free {
			fmt.Fprintf(w, "  • %s%s\n", s.Description, asOfSuffix(s.AsOf, now))
		}
	}
	if len(probed) > 0 {
		fmt.Fprintln(w, "\nFound by a deeper search (extra provider calls):")
		for _, s := range probed {
			fmt.Fprintf(w, "  • %s%s\n", s.Description, asOfSuffix(s.AsOf, now))
		}
	}
}

// asOfSuffix returns a human "(as of N ago)" marker when the data is at least an
// hour old, so a persisted-grid saving is never silently presented as live.
func asOfSuffix(asOf, now time.Time) string {
	if asOf.IsZero() {
		return ""
	}
	age := now.Sub(asOf)
	if age < time.Hour {
		return ""
	}
	switch {
	case age < 24*time.Hour:
		return fmt.Sprintf(" (as of %dh ago)", int(age.Hours()))
	default:
		return fmt.Sprintf(" (as of %dd ago)", int(age.Hours())/24)
	}
}

// cheapestFlightPrice returns the lowest positive fare in a result set, or 0.
func cheapestFlightPrice(flights []models.FlightResult) float64 {
	var best float64
	for _, f := range flights {
		if f.Price > 0 && (best == 0 || f.Price < best) {
			best = f.Price
		}
	}
	return best
}
