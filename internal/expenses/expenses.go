// Package expenses computes per-traveller reconciliation for a trip:
// given a slice of priced Bookings (each with a payer + a traveller
// split), returns a Settlement that says who owes whom how much.
// Pure functions — no I/O — so the math stays trivially auditable
// and the inbox-parser / day-graph wiring can compose on top of it.
//
// MIK-3088 (partial). Inbox parser via gws gmail and the day-graph
// composer ship as separate changes against the same booking shape.
package expenses

import (
	"fmt"
	"sort"
	"strings"
)

// Booking is one priced line item attached to the trip. Category lets
// the reconciliation surface a per-bucket roll-up (flights vs hotels
// vs activities). Payer is the traveller who actually paid the
// supplier; the split lists every traveller who consumed the item
// along with their share weight (default 1.0 == equal split). The
// canonical "two travellers, dad pays for daughter's seat too" case
// is expressed as Payer=dad, Split=[dad:1, daughter:1].
type Booking struct {
	ID       string
	Category string
	Currency string
	Amount   float64
	Payer    string
	Split    []ShareEntry
}

// ShareEntry is one traveller's claim on a Booking. Weight defaults
// to 1.0 when zero — letting callers omit it for an equal split.
type ShareEntry struct {
	Traveller string
	Weight    float64
}

// Settlement is the per-traveller reconciliation produced by
// Reconcile. PerTraveller carries each traveller's net position
// (positive = others owe them; negative = they owe others). Transfers
// is the minimum-flow set of "X pays Y EUR Z" instructions that
// settle the group in the fewest moves possible.
type Settlement struct {
	PerTraveller []TravellerBalance
	Transfers    []Transfer
	ByCategory   []CategoryTotal
	Currency     string
	Total        float64
}

// TravellerBalance is one row of the reconciliation grid.
type TravellerBalance struct {
	Traveller string
	Paid      float64 // sum of bookings they fronted
	Owed      float64 // sum of share-weighted line items
	Net       float64 // Paid - Owed; positive = others owe them
}

// Transfer is one settlement instruction.
type Transfer struct {
	From   string
	To     string
	Amount float64
}

// CategoryTotal is the per-category roll-up across the trip.
type CategoryTotal struct {
	Category string
	Total    float64
}

// Reconcile computes the Settlement from a slice of Bookings. The
// currency on the first booking with a non-empty Currency field wins;
// callers must pre-normalise to a single currency or the math is
// nonsense. Bookings with zero/negative amount are skipped silently.
// Returns a zero-value Settlement when no usable bookings remain.
func Reconcile(bookings []Booking) Settlement {
	usable := make([]Booking, 0, len(bookings))
	for _, b := range bookings {
		if b.Amount <= 0 {
			continue
		}
		if strings.TrimSpace(b.Payer) == "" {
			continue
		}
		if len(b.Split) == 0 {
			continue
		}
		usable = append(usable, b)
	}
	if len(usable) == 0 {
		return Settlement{}
	}
	currency := pickCurrency(usable)
	paid := map[string]float64{}
	owed := map[string]float64{}
	cats := map[string]float64{}
	var total float64
	for _, b := range usable {
		paid[b.Payer] += b.Amount
		total += b.Amount
		cats[b.Category] += b.Amount
		totalWeight := 0.0
		for _, s := range b.Split {
			totalWeight += weightOf(s)
		}
		if totalWeight <= 0 {
			continue
		}
		for _, s := range b.Split {
			share := b.Amount * (weightOf(s) / totalWeight)
			owed[strings.TrimSpace(s.Traveller)] += share
		}
	}
	balances := buildBalances(paid, owed)
	transfers := minimumFlow(balances)
	return Settlement{
		PerTraveller: balances,
		Transfers:    transfers,
		ByCategory:   buildCategories(cats),
		Currency:     currency,
		Total:        total,
	}
}

func pickCurrency(b []Booking) string {
	for _, x := range b {
		if c := strings.TrimSpace(x.Currency); c != "" {
			return c
		}
	}
	return ""
}

func weightOf(s ShareEntry) float64 {
	if s.Weight <= 0 {
		return 1
	}
	return s.Weight
}

func buildBalances(paid, owed map[string]float64) []TravellerBalance {
	names := map[string]struct{}{}
	for k := range paid {
		names[k] = struct{}{}
	}
	for k := range owed {
		names[k] = struct{}{}
	}
	out := make([]TravellerBalance, 0, len(names))
	for name := range names {
		out = append(out, TravellerBalance{
			Traveller: name,
			Paid:      paid[name],
			Owed:      owed[name],
			Net:       paid[name] - owed[name],
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Traveller < out[j].Traveller
	})
	return out
}

func buildCategories(cats map[string]float64) []CategoryTotal {
	out := make([]CategoryTotal, 0, len(cats))
	for k, v := range cats {
		out = append(out, CategoryTotal{Category: k, Total: v})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Category < out[j].Category
	})
	return out
}

// minimumFlow walks the balance list and emits the smallest set of
// transfers that zeros every net. Greedy match cheapest-debt to
// largest-credit until both columns drain — produces at most N-1
// transfers for N travellers, matching the textbook minimum.
//
// Tolerance of 0.005 collapses cents-level rounding noise into the
// last transfer so a perfectly-split trip never emits a "X pays Y
// 0.01" instruction the user would find annoying.
func minimumFlow(balances []TravellerBalance) []Transfer {
	const tolerance = 0.005
	debtors := make([]TravellerBalance, 0, len(balances))
	creditors := make([]TravellerBalance, 0, len(balances))
	for _, b := range balances {
		switch {
		case b.Net < -tolerance:
			debtors = append(debtors, b)
		case b.Net > tolerance:
			creditors = append(creditors, b)
		}
	}
	// Largest absolute debts settle first so we prune travellers as
	// quickly as possible.
	sort.SliceStable(debtors, func(i, j int) bool {
		return debtors[i].Net < debtors[j].Net
	})
	sort.SliceStable(creditors, func(i, j int) bool {
		return creditors[i].Net > creditors[j].Net
	})
	var out []Transfer
	for len(debtors) > 0 && len(creditors) > 0 {
		d := &debtors[0]
		c := &creditors[0]
		amount := -d.Net
		if c.Net < amount {
			amount = c.Net
		}
		if amount > tolerance {
			out = append(out, Transfer{From: d.Traveller, To: c.Traveller, Amount: round2(amount)})
		}
		d.Net += amount
		c.Net -= amount
		if d.Net > -tolerance {
			debtors = debtors[1:]
		}
		if c.Net < tolerance {
			creditors = creditors[1:]
		}
	}
	return out
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

// Render emits a fixed-format settlement summary the CLI can show
// without pulling in a templating layer. Pure formatter — separate
// from Reconcile so renderer tweaks do not perturb the math.
func Render(s Settlement) string {
	if len(s.PerTraveller) == 0 {
		return "no bookings to reconcile"
	}
	var b strings.Builder
	cur := s.Currency
	if cur == "" {
		cur = "EUR"
	}
	_, _ = fmt.Fprintf(&b, "Trip total: %.2f %s across %d travellers\n", s.Total, cur, len(s.PerTraveller))
	_, _ = fmt.Fprintln(&b, "Per traveller:")
	for _, t := range s.PerTraveller {
		_, _ = fmt.Fprintf(&b, "  %s — paid %.2f, owes %.2f, net %+.2f\n", t.Traveller, t.Paid, t.Owed, t.Net)
	}
	if len(s.Transfers) == 0 {
		_, _ = fmt.Fprintln(&b, "Already balanced; no transfers needed.")
	} else {
		_, _ = fmt.Fprintln(&b, "Settlement:")
		for _, t := range s.Transfers {
			_, _ = fmt.Fprintf(&b, "  %s -> %s: %.2f %s\n", t.From, t.To, t.Amount, cur)
		}
	}
	if len(s.ByCategory) > 0 {
		_, _ = fmt.Fprintln(&b, "By category:")
		for _, c := range s.ByCategory {
			cat := c.Category
			if cat == "" {
				cat = "uncategorised"
			}
			_, _ = fmt.Fprintf(&b, "  %s: %.2f\n", cat, c.Total)
		}
	}
	return b.String()
}
