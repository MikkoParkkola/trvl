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

// nightlyOnly is the nightly stay term, applied by the core search before
// pricing. It leaves out hotels whose only price is a monthly rent (a monthly
// rent shown as a night's price is off by about thirty times) and strips the
// monthly sources from hotels that also have a nightly one, so a monthly rent
// never competes for the headline, savings or cheapest_source. It returns the
// kept hotels and how many were left out. The input slice is not mutated.
func nightlyOnly(in []models.HotelResult) ([]models.HotelResult, int) {
	kept := make([]models.HotelResult, 0, len(in))
	omitted := 0
	for _, h := range in {
		if monthlyOnly(h) {
			omitted++
			continue
		}
		var sources []models.PriceSource
		for _, src := range h.Sources {
			if src.PriceBasis != models.PriceBasisMonthly {
				sources = append(sources, src)
				continue
			}
			// The rental's page is not a nightly booking link; finalize or the
			// fallback search link supplies one.
			if src.BookingURL != "" && h.BookingURL == src.BookingURL {
				h.BookingURL = ""
			}
		}
		if len(sources) != len(h.Sources) {
			h.Sources = sources
			if h.PriceBasis == models.PriceBasisMonthly {
				h.PriceBasis = "" // finalize takes the basis from the headline source
			}
		}
		kept = append(kept, h)
	}
	return kept, omitted
}

func monthlyOnly(h models.HotelResult) bool {
	if len(h.Sources) == 0 {
		return h.PriceBasis == models.PriceBasisMonthly
	}
	for _, s := range h.Sources {
		if s.PriceBasis != models.PriceBasisMonthly {
			return false
		}
	}
	return true
}

// StayTerm says which prices a hotel search returns.
type StayTerm int

const (
	// StayNightly is the default: nightly prices only. Results whose only
	// price is a monthly rent are left out and counted in MonthlyOmitted, so
	// no caller shows or multiplies a monthly rent as a night's price.
	StayNightly StayTerm = iota
	// StayIncludeMonthly also returns monthly rentals, labelled with the
	// monthly price basis. Only the long-stay accommodation search uses it.
	StayIncludeMonthly
)
