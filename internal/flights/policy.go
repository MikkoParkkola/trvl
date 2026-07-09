package flights

import (
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
	result.Flights = FilterFlightsByTimePreference(result.Flights, prefs.FlightTimeEarliest, prefs.FlightTimeLatest)
	result.Flights = AdjustBagAllowance(result.Flights, prefs.FrequentFlyerPrograms)
	result.Count = len(result.Flights)
}
