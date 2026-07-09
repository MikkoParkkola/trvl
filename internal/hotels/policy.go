package hotels

import "github.com/MikkoParkkola/trvl/internal/models"

// ApplySharedHotelPolicy applies the post-search preference chain that BOTH the
// CLI (cmd/trvl/hotels) and the MCP surface (mcp/tools_hotels, and by reuse
// tools_accommodations) must run IDENTICALLY. It is the single source of truth
// for that chain so the two surfaces cannot drift.
//
// This was extracted (trvl #452 follow-up, the hotels analogue of
// flights.ApplySharedFlightPolicy) because the surfaces had already diverged:
// the adults-only exclusion below lived only in the CLI, so the MCP surface
// returned adults-only properties to a party that included children — the same
// class of CLI/MCP parity bug behind the #452 MaxPrice truncation. One
// implementation cannot drift.
//
// childrenPresent: pass true when the party includes children (CLI: children>0;
// MCP: len(opts.ChildrenAges)>0). When true, properties flagged AdultsOnly are
// removed and Count is kept consistent with the filtered set. Returns the number
// of properties hidden so callers can surface a transparency note (the CLI does;
// the MCP surface reflects it in the structured Count).
func ApplySharedHotelPolicy(result *models.HotelSearchResult, childrenPresent bool) (hidden int) {
	if result == nil {
		return 0
	}
	if childrenPresent {
		result.Hotels, hidden = excludeAdultsOnly(result.Hotels)
		result.Count = len(result.Hotels)
	}
	return hidden
}

// excludeAdultsOnly returns the hotels with every adults-only property removed,
// plus the count removed. The input slice is not mutated.
func excludeAdultsOnly(in []models.HotelResult) (kept []models.HotelResult, hidden int) {
	kept = make([]models.HotelResult, 0, len(in))
	for _, h := range in {
		if h.AdultsOnly {
			hidden++
			continue
		}
		kept = append(kept, h)
	}
	return kept, hidden
}
