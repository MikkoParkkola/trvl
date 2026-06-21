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

// SameDayAlternative finds a genuine, actionable saving within an
// already-returned flight list: when the headline result (flights[0], i.e. what
// the current sort puts first — by duration, departure time, etc.) is pricier
// than the cheapest same-day fare, choosing the cheapest captures a real saving.
//
// This is honest by construction: when the list is already sorted cheapest-first
// the headline IS the cheapest, the delta is zero, and nil is returned — no
// illusory "saving" is fabricated. Zero new provider calls (pure spread of data
// the search already returned).
func SameDayAlternative(flights []models.FlightResult, minDelta float64, asOf time.Time) *Saving {
	if len(flights) < 2 {
		return nil
	}
	headline := flights[0]
	if headline.Price <= 0 {
		return nil
	}
	cheapest := headline
	for _, f := range flights[1:] {
		if f.Price > 0 && f.Price < cheapest.Price {
			cheapest = f
		}
	}
	delta := headline.Price - cheapest.Price
	if delta < minDelta {
		return nil // headline already is (within minDelta of) the cheapest
	}
	return &Saving{
		Kind:        KindSameDay,
		Description: fmt.Sprintf("The cheapest same-day fare (%.0f %s) saves %.0f %s over the top-listed result (%.0f %s)", cheapest.Price, cheapest.Currency, delta, headline.Currency, headline.Price, headline.Currency),
		Amount:      delta,
		Currency:    cheapest.Currency,
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
