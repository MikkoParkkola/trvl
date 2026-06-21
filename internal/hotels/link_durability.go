package hotels

import (
	"math"
	"net/url"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// Link-durability triage (#168).
//
// Booking links returned by Google Hotels via SerpAPI come in several shapes
// with very different lifetimes. Roberto Reale documented the failure mode in
// his "Budget Travel Pipeline" series and implemented the triage in
// RobertoReale/travel-search (serpapi_verified.py); this is trvl's native
// version of the same rules:
//
//   - google.com/aclk        OTA ad-click redirect — works at search time but
//                            can expire after a day or two. Keep, but mark it
//                            "expiring" and always provide a durable fallback.
//   - google.com/travel/clk  vacation-rental redirect — dead within hours.
//                            Strip it; rely on the durable fallback.
//   - direct OTA / property   stable. Keep as-is.
//
// Regardless of provider link, attach a durable Booking.com property+date
// deep-link that never 404s, so an agent always has one link that lands on a
// bookable page for the right property and dates.

const (
	linkStable   = "stable"
	linkExpiring = "expiring"
)

// touristTaxNote is the descriptive, non-numeric caveat surfaced on price
// results so an agent budgets for the local tourist/city tax separately. See
// #169 and RobertoReale's "Budget Travel Pipeline" (rule 2).
const touristTaxNote = "A local tourist or city tax may be payable in cash at the property and is typically not included in any online total. Confirm the rate locally and budget it as a separate cash cost."

// isDeadRentalRedirect reports whether a URL is a vacation-rental click
// redirect that expires within hours and should be dropped.
func isDeadRentalRedirect(rawURL string) bool {
	low := strings.ToLower(rawURL)
	return strings.Contains(low, "google.com/travel/clk") ||
		strings.Contains(low, "/travel/clk")
}

// isExpiringAdRedirect reports whether a URL is a Google ad-click redirect that
// works now but may expire after a day or two.
func isExpiringAdRedirect(rawURL string) bool {
	low := strings.ToLower(rawURL)
	return strings.Contains(low, "google.com/aclk") || strings.Contains(low, "/aclk?")
}

// ClassifyLinkDurability classifies a single booking URL for callers outside the
// price path (e.g. room-level booking-readiness). Returns "stable" for a durable
// link, "expiring" for an ad-click or dead rental redirect that may not survive
// to booking time, and "" for an empty URL.
func ClassifyLinkDurability(rawURL string) string {
	if strings.TrimSpace(rawURL) == "" {
		return ""
	}
	if isDeadRentalRedirect(rawURL) || isExpiringAdRedirect(rawURL) {
		return linkExpiring
	}
	return linkStable
}

// durableBookingURL builds a Booking.com search deep-link for a property and
// date range. It lands on a bookable page for the right property and never
// 404s, unlike an expiring ad-click redirect. Returns "" when there is not
// enough information to build a useful link.
func durableBookingURL(propertyName, checkIn, checkOut string) string {
	name := strings.TrimSpace(propertyName)
	if name == "" {
		return ""
	}
	q := url.Values{}
	q.Set("ss", name)
	if checkIn != "" {
		q.Set("checkin", checkIn)
	}
	if checkOut != "" {
		q.Set("checkout", checkOut)
	}
	q.Set("group_adults", "2")
	return "https://www.booking.com/searchresults.html?" + q.Encode()
}

// pricesEqual reports whether two prices are equal within a one-cent epsilon,
// used to detect when a shown total matches its pre-tax figure (#171).
func pricesEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.01
}

// applyLinkDurability triages every provider link on a price result: it strips
// dead vacation-rental redirects, tags the remaining links as "stable" or
// "expiring", and attaches a durable Booking.com fallback. Safe to call on any
// HotelPriceResult, including one with no providers.
func applyLinkDurability(result *models.HotelPriceResult) {
	if result == nil {
		return
	}
	for i := range result.Providers {
		p := &result.Providers[i]
		switch {
		case p.ProviderURL == "":
			p.LinkDurability = ""
		case isDeadRentalRedirect(p.ProviderURL):
			// Dead within hours — strip it; the durable fallback covers it.
			p.ProviderURL = ""
			p.LinkDurability = ""
		case isExpiringAdRedirect(p.ProviderURL):
			p.LinkDurability = linkExpiring
		default:
			p.LinkDurability = linkStable
		}
	}
	if result.BookingFallbackURL == "" {
		result.BookingFallbackURL = durableBookingURL(result.Name, result.CheckIn, result.CheckOut)
	}
	// #169: surface the local tourist/city tax as a separate cash caveat when
	// there are bookable prices. Descriptive only — never an estimate, never
	// folded into ranking (it is roughly equal across candidates).
	if len(result.Providers) > 0 && result.TouristTaxNote == "" {
		result.TouristTaxNote = touristTaxNote
	}
}
