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
	"strings"
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
	// KindProbe: a saving found by an explicit, budgeted fan-out probe (Tier 2).
	// Unlike the other kinds this is NOT call-free; CallFree is false on these.
	KindProbe Kind = "probe"
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
			currency = strings.ToUpper(strings.TrimSpace(d.Currency))
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
		cur := strings.ToUpper(strings.TrimSpace(d.Currency))
		// Round 24's guard here only rejected a KNOWN mismatch (both sides
		// labeled and different), so an empty-currency row still slipped
		// through unchecked and got compared by raw magnitude against the
		// reference's labeled currency -- the exact fabricated-saving bug
		// class round 24 meant to close, just reachable via the
		// empty-string escape hatch. Round 25 requires exact equality
		// after normalization instead: one blank and one labeled is
		// rejected, and any known mismatch is still rejected. Found
		// independently by both GPT and Grok second-opinion review,
		// 2026-07-31 (round 25).
		//
		// Round 26 (trvl#549) also rejects BOTH blank, which round 25 had
		// treated as compatible on the pre-existing unknown-unknown display
		// convention. Two rows can be currencyless for unrelated reasons --
		// one provider omitting the currency on a EUR quote, another on a JPY
		// one -- so "both blank" is not evidence they share a unit; it is the
		// absence of evidence either way.
		//
		// Saving.Amount is documented as money saved IN Currency. With no
		// currency the number has no defined unit and cannot honestly be shown:
		// the description rendered "save 123 " with a trailing space. Refusing
		// costs the rare genuinely-comparable pair a signal that could not have
		// been displayed anyway.
		if cur == "" || cur != currency {
			continue
		}
		delta := current - d.Price
		if delta < minDelta {
			continue
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
	headlineCur := strings.ToUpper(strings.TrimSpace(headline.Currency))
	cheapest := headline
	for _, f := range flights[1:] {
		if f.Price <= 0 || f.Price >= cheapest.Price {
			continue
		}
		// Round 25: the round-24 guard below had the same empty-currency
		// escape hatch as ShiftDay's -- an unlabeled candidate fare could
		// still win against a labeled headline by raw magnitude. Require
		// exact equality after normalization (both blank is compatible,
		// one blank one labeled is rejected, known mismatch is rejected).
		// Found independently by both GPT and Grok second-opinion review,
		// 2026-07-31 (round 25).
		fCur := strings.ToUpper(strings.TrimSpace(f.Currency))
		// Round 26 (trvl#549): an unknown currency is not a match for another
		// unknown one. Same reasoning as ShiftDay above, and it bites harder
		// here -- this input is a flight result list, which can span several
		// providers, so two blank rows are more likely to be genuinely
		// different currencies than in a single date grid.
		if fCur == "" || fCur != headlineCur {
			continue
		}
		cheapest = f
	}
	delta := headline.Price - cheapest.Price
	if delta < minDelta {
		return nil // headline already is (within minDelta of) the cheapest
	}
	// Round 25: use the normalized currency, not the raw field, for both the
	// returned Saving and the description -- ShiftDay already normalizes
	// (see cur above); SameDayAlternative returning cheapest.Currency raw
	// meant "eur" passed the equality gate above but was then emitted
	// lowercase downstream, inconsistent with ShiftDay's always-uppercase
	// output for the same bug class.
	cheapestCur := strings.ToUpper(strings.TrimSpace(cheapest.Currency))
	return &Saving{
		Kind:        KindSameDay,
		Description: fmt.Sprintf("The cheapest same-day fare (%.0f %s) saves %.0f %s over the top-listed result (%.0f %s)", cheapest.Price, cheapestCur, delta, cheapestCur, headline.Price, cheapestCur),
		Amount:      delta,
		Currency:    cheapestCur,
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
