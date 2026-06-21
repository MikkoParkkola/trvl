// Package counterfactual computes "you could save X by ..." savings from data
// trvl has ALREADY fetched — never by issuing fresh provider searches (MIK-6234
// Tier 0). Every function here is pure: it takes already-fetched grids,
// itinerary lists, or persisted history and returns deltas. The structural
// guarantee is that a Tier-0 counterfactual costs ZERO marginal provider calls,
// because nothing in this package can reach the network.
//
// Tiers 1 (scheduler-amortized speculative probe) and 2 (interactive cold-route
// fan-out) live behind the probebudget two-lane firewall and are intentionally
// out of scope here.
package counterfactual

import (
	"fmt"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/pricesignal"
)

// Kind identifies the counterfactual lever.
type Kind string

const (
	// KindShiftDay: a different departure date in an already-fetched grid is cheaper.
	KindShiftDay Kind = "shift_day"
	// KindSameDay: a cheaper itinerary on the SAME date (carrier/time) than the headline.
	KindSameDay Kind = "same_day_alternative"
	// KindVsHistory: the current price relative to this route's own history.
	KindVsHistory Kind = "vs_history"
)

// Saving is one call-free counterfactual finding. Amount is the money saved
// versus the reference price, in Currency. AsOf records when the underlying data
// was observed, so a stale finding is never presented as live (TRVL.CF.5).
type Saving struct {
	Kind        Kind      `json:"kind"`
	Description string    `json:"description"`
	Amount      float64   `json:"amount"`
	Currency    string    `json:"currency,omitempty"`
	AsOf        time.Time `json:"as_of"`
	// CallFree is always true for Tier-0 findings: a guarantee, surfaced in the
	// payload, that producing this saving issued no provider call.
	CallFree bool `json:"call_free"`
}

// ShiftDay finds dates in an already-fetched price grid that beat the price on
// currentDate by at least minDelta. grid is the output of a date search the user
// already ran; asOf is when that grid was fetched. Results are the cheapest
// alternatives first. Zero provider calls.
func ShiftDay(grid []models.DatePriceResult, currentDate string, minDelta float64, asOf time.Time) []Saving {
	var current float64
	var currency string
	for _, d := range grid {
		if d.Date == currentDate {
			current = d.Price
			currency = d.Currency
			break
		}
	}
	if current <= 0 {
		return nil
	}
	var out []Saving
	for _, d := range grid {
		if d.Date == currentDate || d.Price <= 0 {
			continue
		}
		delta := current - d.Price
		if delta < minDelta {
			continue
		}
		cur := d.Currency
		if cur == "" {
			cur = currency
		}
		out = append(out, Saving{
			Kind:        KindShiftDay,
			Description: fmt.Sprintf("Depart %s instead of %s to save %.0f %s", d.Date, currentDate, delta, cur),
			Amount:      delta,
			Currency:    cur,
			AsOf:        asOf,
			CallFree:    true,
		})
	}
	sortByAmountDesc(out)
	return out
}

// SameDayAlternative finds itineraries in an already-returned flight list that
// are cheaper than the headline (cheapest is the reference) by at least
// minDelta. Because a single flight search already returns every same-day
// carrier/time, this is pure spread analysis with zero new calls.
//
// In practice the cheapest is usually the headline, so this surfaces the case
// where a non-headline option trades a small premium for a better time/carrier
// the renderer wants to mention — here we report genuine savings vs the most
// expensive shown option, framed honestly as the spread available on the day.
func SameDayAlternative(flights []models.FlightResult, minDelta float64, asOf time.Time) *Saving {
	if len(flights) < 2 {
		return nil
	}
	var lo, hi float64
	var currency string
	for _, f := range flights {
		if f.Price <= 0 {
			continue
		}
		if lo == 0 || f.Price < lo {
			lo = f.Price
			currency = f.Currency
		}
		if f.Price > hi {
			hi = f.Price
		}
	}
	delta := hi - lo
	if lo <= 0 || delta < minDelta {
		return nil
	}
	return &Saving{
		Kind:        KindSameDay,
		Description: fmt.Sprintf("Same-day fares range %.0f–%.0f %s; the cheapest saves %.0f %s over the dearest shown", lo, hi, currency, delta, currency),
		Amount:      delta,
		Currency:    currency,
		AsOf:        asOf,
		CallFree:    true,
	}
}

// VsHistory expresses a confident price-position as a counterfactual saving:
// how far below this route's median the current price sits. Returns nil when the
// position is not confident (honesty: no history-based claim under the floor) or
// when the current price is at/above median.
func VsHistory(pos *pricesignal.Position, currency string, asOf time.Time) *Saving {
	if pos == nil || !pos.Confident || pos.Median <= 0 {
		return nil
	}
	delta := pos.Median - pos.Current
	if delta <= 0 {
		return nil
	}
	return &Saving{
		Kind:        KindVsHistory,
		Description: fmt.Sprintf("Current price is %.0f %s below this route's typical (median %.0f over %d obs)", delta, currency, pos.Median, pos.Observations),
		Amount:      delta,
		Currency:    currency,
		AsOf:        asOf,
		CallFree:    true,
	}
}

func sortByAmountDesc(s []Saving) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].Amount > s[j-1].Amount; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
