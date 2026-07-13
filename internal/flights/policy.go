package flights

import (
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// ApplySharedFlightPolicy applies the preference-derived post-search filter
// chain that BOTH the CLI (cmd/trvl) and the MCP surface (mcp/tools_flights)
// must run IDENTICALLY: budget ceiling, time-of-day window, and frequent-flyer
// bag-allowance adjustment. Count is kept consistent with the filtered set.
//
// This is the single source of truth for that policy. It was extracted (trvl
// #452 follow-up) because the two surfaces previously hand-maintained separate
// copies of this chain, and the copies drifted — the exact class of bug behind
// the #452 MaxPrice truncation, the earlier PreferredAlliance merge-zero
// regression, and the CabinClass parity gap. One implementation cannot drift.
//
// skipBudgetPref: pass true when the caller already applied an EXPLICIT price
// ceiling (CLI `--max-price` flag / MCP `max_price` arg). In that case the
// preference-derived budget filter is skipped to avoid double-constraining the
// result; the time-of-day and bag adjustments still apply. This matches the
// CLI's long-standing behavior and brings the MCP surface into line with it.
func ApplySharedFlightPolicy(result *models.FlightSearchResult, prefs *preferences.Preferences, skipBudgetPref bool) {
	if prefs == nil || result == nil || !result.Success {
		return
	}
	if !skipBudgetPref {
		result.Flights = FilterFlightsByBudget(result.Flights, prefs.BudgetFlightMax)
	}
	// #473: split the time-of-day window into a SOFT window plus a hard-floor
	// tolerance. Flights just outside the window are KEPT (near-misses) rather
	// than silently dropped; only flights beyond the tolerance are removed. The
	// near-misses are down-ranked by the scoring factor
	// FactorTimeHardFloorCompliance, so the honest set is surfaced without a
	// synthetic provider status muddying the raw provider counts.
	kept, _, _ := FilterFlightsByTimeWindow(
		result.Flights, prefs.FlightTimeEarliest, prefs.FlightTimeLatest, prefs.FlightTimeHardFloor)
	result.Flights = kept
	result.Flights = AdjustBagAllowance(result.Flights, prefs.FrequentFlyerPrograms)

	// #469: inject concrete, bookable hack-derived candidates (e.g. rail+fly
	// from rail-station origins) into the ranked Flights when the HackSaving
	// carries real results (never estimates) and the category is not
	// suppressed via preferences. Advisory-only savings stay out of the
	// bookable list.
	if hs := result.HackSaving; hs != nil && len(hs.Candidates) > 0 {
		if !prefs.HackSuppressed(hs.Type) {
			for _, c := range hs.Candidates {
				f := c // copy
				// Ensure the tradeoff annotation is present on promoted candidate.
				hasNote := false
				for _, w := range f.Warnings {
					if strings.Contains(w, "[rail+fly]") {
						hasNote = true
						break
					}
				}
				if !hasNote {
					f.Warnings = append(f.Warnings, "[rail+fly] throwaway train leg; board/exit at the hub airport")
				}
				result.Flights = append(result.Flights, f)
			}
		}
	}

	result.Count = len(result.Flights)
}
