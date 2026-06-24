// Package dealradar builds a daily "deal-radar" digest from the savings
// surfaced by trvl's existing value engines (length-of-stay rate flips,
// hotel value stack, points/award value).
//
// The aggregator is deliberately pure: it performs no network I/O and reads
// no clocks for the rendered body, so the same inputs always render the same
// text. Callers (the `trvl digest` command, scheduled by launchd) collect the
// value-engine outputs and hand them to BuildDigest, then render and mail the
// result.
package dealradar

import (
	"fmt"
	"sort"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/los"
)

// Item is one savings line in the digest. It is intentionally engine-agnostic:
// each value engine maps its native output onto this shape so the renderer
// stays simple and the ordering is stable.
type Item struct {
	// Source labels the originating value engine, e.g. "los", "hotel",
	// "points". Used for grouping and deterministic ordering.
	Source string
	// Title is a short human-readable headline for the saving.
	Title string
	// Detail is an optional one-line explanation.
	Detail string
	// Savings is the absolute amount saved, in Currency. Higher sorts first
	// within a source.
	Savings float64
	// Currency is the ISO currency code for Savings (e.g. "EUR").
	Currency string
}

// Digest is the aggregated, rendered-ready set of savings items.
type Digest struct {
	Items []Item
}

// FromFlips maps length-of-stay rate flips onto digest items. Only flips with
// genuine absolute savings (negative TotalDelta) are surfaced; better-rate
// flips that cost more in total are skipped because the digest reports money
// saved, not nightly-rate curiosities. Pure: no clock, no network.
func FromFlips(currency string, flips []los.Flip) []Item {
	if currency == "" {
		currency = "EUR"
	}
	out := make([]Item, 0, len(flips))
	for _, f := range flips {
		saved := f.BaselineTotal - f.AlternativeTotal
		if saved <= 0 {
			continue
		}
		out = append(out, Item{
			Source:   "los",
			Title:    fmt.Sprintf("Length-of-stay flip: %d→%d nights", f.BaselineNights, f.AlternativeNights),
			Detail:   f.Reason,
			Savings:  saved,
			Currency: currency,
		})
	}
	return out
}

// BuildDigest collects items from every supplied source into a single digest
// with a stable, deterministic ordering. Items are sorted by source name, then
// by descending savings, then by title — so equal inputs always render
// identically regardless of caller-side ordering.
func BuildDigest(itemSets ...[]Item) Digest {
	var all []Item
	for _, set := range itemSets {
		all = append(all, set...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Source != all[j].Source {
			return all[i].Source < all[j].Source
		}
		if all[i].Savings != all[j].Savings {
			return all[i].Savings > all[j].Savings
		}
		return all[i].Title < all[j].Title
	})
	return Digest{Items: all}
}

// TotalSavings sums the absolute savings across all items. When items carry
// mixed currencies the sum is still returned (callers that need
// currency-correct totals should group first); the common case is a single
// operator currency.
func (d Digest) TotalSavings() float64 {
	var t float64
	for _, it := range d.Items {
		t += it.Savings
	}
	return t
}

// Empty reports whether the digest has no savings items.
func (d Digest) Empty() bool { return len(d.Items) == 0 }

// Subject renders a stable email subject line for the digest.
func (d Digest) Subject() string {
	if d.Empty() {
		return "trvl deal-radar: no flips today"
	}
	cur := d.Items[0].Currency
	if cur == "" {
		cur = "EUR"
	}
	return fmt.Sprintf("trvl deal-radar: %d deals, save up to %.0f %s",
		len(d.Items), d.Items[0].Savings, cur)
}

// Render produces the deterministic plain-text body of the digest. It contains
// no timestamps so body comparisons in tests are stable.
func (d Digest) Render() string {
	var b strings.Builder
	b.WriteString("trvl deal-radar\n")
	b.WriteString("===============\n\n")
	if d.Empty() {
		b.WriteString("No rate-flips or value deals surfaced today.\n")
		return b.String()
	}
	for i, it := range d.Items {
		cur := it.Currency
		if cur == "" {
			cur = "EUR"
		}
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, it.Source, it.Title)
		fmt.Fprintf(&b, "   save %.2f %s\n", it.Savings, cur)
		if it.Detail != "" {
			fmt.Fprintf(&b, "   %s\n", it.Detail)
		}
		b.WriteString("\n")
	}
	cur := d.Items[0].Currency
	if cur == "" {
		cur = "EUR"
	}
	fmt.Fprintf(&b, "Total potential savings: %.2f %s across %d deals.\n",
		d.TotalSavings(), cur, len(d.Items))
	return b.String()
}
