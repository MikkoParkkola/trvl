// Package los implements the length-of-stay rate-flip scanner that
// hotel booking engines use to detect "stay one extra night, save
// €40" cliffs. Hotels often price weekly windows (5/7/14/28 nights)
// at a per-night discount that makes a longer stay strictly cheaper
// in absolute terms — this package surfaces those crossover points
// from a slice of priced LOSQuote fixtures so the caller can render
// "consider 8 nights instead of 7 to save".
//
// MIK-3087 (partial). Pure function — no I/O — so the math stays
// trivially auditable. Active-holds tracker, last-minute mode, and
// the hotel-points engine ship as separate changes against the same
// preferences surface.
package los

import (
	"fmt"
	"sort"
)

// LOSQuote is one priced length-of-stay candidate. Nights is the
// requested stay length; PricePerNight is the average, TotalPrice is
// the contracted total (caller decides which to populate; if both are
// set the package trusts TotalPrice and ignores PricePerNight to keep
// the comparison apples-to-apples). Refundable matters because a
// non-refundable longer-stay flip is not actionable for a user who
// might shorten the trip.
type LOSQuote struct {
	Nights        int
	PricePerNight float64
	TotalPrice    float64
	Currency      string
	Refundable    bool
}

// FlipKind labels how the scanner classified the crossover.
type FlipKind string

const (
	// FlipExtendForSavings — the longer stay is cheaper in absolute
	// terms than the requested stay. Pay for an extra night, owe less.
	FlipExtendForSavings FlipKind = "extend_for_savings"
	// FlipShortenSafe — the shorter stay is cheaper AND the per-night
	// rate is lower (weekly-rate cliff worked against the user).
	FlipShortenSafe FlipKind = "shorten_safe"
	// FlipExtendBetterRate — the longer stay costs more in absolute
	// terms but the per-night rate is materially lower; useful when
	// the user is flexible on duration and optimising for nightly cost.
	FlipExtendBetterRate FlipKind = "extend_better_rate"
)

// Flip is one detected rate-flip alternative.
type Flip struct {
	Kind              FlipKind
	BaselineNights    int
	BaselineTotal     float64
	AlternativeNights int
	AlternativeTotal  float64
	NightlyDelta      float64 // per-night price difference (alt - baseline). Negative = better rate.
	TotalDelta        float64 // total price difference (alt - baseline). Negative = absolute savings.
	Reason            string
	Refundable        bool
}

// ScanLengthOfStay takes the user's baseline Nights and a slice of
// alternative quotes (typically N-2, N-1, N+1, N+2) and returns the
// flips worth surfacing. Pure function. Quotes with non-positive
// totals are skipped silently so callers do not have to pre-clean.
//
// Returns nil when the baseline is missing from quotes; we cannot
// score relative flips without it.
func ScanLengthOfStay(baselineNights int, quotes []LOSQuote) []Flip {
	if baselineNights <= 0 {
		return nil
	}
	baseline, ok := findQuote(quotes, baselineNights)
	if !ok {
		return nil
	}
	baseTotal := totalOf(baseline)
	if baseTotal <= 0 {
		return nil
	}
	baseNightly := baseTotal / float64(baselineNights)

	out := make([]Flip, 0, len(quotes))
	for _, q := range quotes {
		if q.Nights == baselineNights {
			continue
		}
		altTotal := totalOf(q)
		if altTotal <= 0 || q.Nights <= 0 {
			continue
		}
		altNightly := altTotal / float64(q.Nights)
		f := Flip{
			BaselineNights:    baselineNights,
			BaselineTotal:     baseTotal,
			AlternativeNights: q.Nights,
			AlternativeTotal:  altTotal,
			NightlyDelta:      altNightly - baseNightly,
			TotalDelta:        altTotal - baseTotal,
			Refundable:        q.Refundable,
		}
		switch {
		case q.Nights > baselineNights && altTotal < baseTotal:
			// Longer stay strictly cheaper — the canonical weekly-rate
			// cliff hotels create when they lock 7-night windows below
			// what 5-night windows price at on the same room.
			f.Kind = FlipExtendForSavings
			f.Reason = fmt.Sprintf("stay %d nights instead of %d to save %.2f", q.Nights, baselineNights, baseTotal-altTotal)
			out = append(out, f)
		case q.Nights < baselineNights && altTotal < baseTotal && altNightly < baseNightly:
			// Shorter stay cheaper AND the nightly rate is lower —
			// flag so the user knows the hotel did not punish them
			// for shortening (sometimes the weekly rate is *required*
			// and shortening forfeits the discount).
			f.Kind = FlipShortenSafe
			f.Reason = fmt.Sprintf("shorten to %d nights and save %.2f at a lower nightly rate", q.Nights, baseTotal-altTotal)
			out = append(out, f)
		case q.Nights > baselineNights && altNightly < baseNightly:
			// Longer total but better per-night rate — only surfaced
			// when the gap is material (>= 5%% of baseline nightly).
			if (baseNightly-altNightly)/baseNightly >= 0.05 {
				f.Kind = FlipExtendBetterRate
				f.Reason = fmt.Sprintf("longer total but %.1f%% lower per-night rate", (baseNightly-altNightly)/baseNightly*100)
				out = append(out, f)
			}
		}
	}
	sortFlips(out)
	return out
}

// totalOf prefers TotalPrice when set; otherwise derives it from
// PricePerNight × Nights. Avoids double-counting when the caller
// supplies both fields.
func totalOf(q LOSQuote) float64 {
	if q.TotalPrice > 0 {
		return q.TotalPrice
	}
	if q.PricePerNight > 0 && q.Nights > 0 {
		return q.PricePerNight * float64(q.Nights)
	}
	return 0
}

func findQuote(quotes []LOSQuote, nights int) (LOSQuote, bool) {
	for _, q := range quotes {
		if q.Nights == nights {
			return q, true
		}
	}
	return LOSQuote{}, false
}

// sortFlips orders by absolute savings descending so the most
// compelling flip lands first. ExtendForSavings and ShortenSafe are
// real money in pocket; ExtendBetterRate is informational and ranks
// last on equal totals.
func sortFlips(f []Flip) {
	sort.SliceStable(f, func(i, j int) bool {
		// Negative TotalDelta = saved money; sort by lowest delta first.
		if f[i].TotalDelta != f[j].TotalDelta {
			return f[i].TotalDelta < f[j].TotalDelta
		}
		// Tie-break: cheaper per-night first.
		return f[i].NightlyDelta < f[j].NightlyDelta
	})
}
