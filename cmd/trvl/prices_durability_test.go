package main

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestFormatPricesTable_SurfacesLinkDurability verifies that the provider price
// table surfaces per-link stability, the durable fallback, and the tourist-tax
// note in default CLI output — so a save-and-book-later traveller can tell which
// links expire (Roberto Reale's link-durability feedback, blog issue #1).
func TestFormatPricesTable_SurfacesLinkDurability(t *testing.T) {
	result := &models.HotelPriceResult{
		HotelID:  "0x133b6ab82c204df7:0x437369f021e5e869",
		CheckIn:  "2026-07-30",
		CheckOut: "2026-08-04",
		Providers: []models.ProviderPrice{
			{Provider: "Booking.com", Price: 980, Currency: "EUR", LinkDurability: "stable"},
			{Provider: "Expedia", Price: 1402, Currency: "EUR", LinkDurability: "expiring"},
		},
		BookingFallbackURL: "https://www.booking.com/searchresults.html?ss=Hotel+Continental+Mare",
		TouristTaxNote:     "A local tourist or city tax may be payable in cash at the property.",
	}

	out := captureStdout(t, func() {
		if err := formatPricesTable(result); err != nil {
			t.Fatalf("formatPricesTable: %v", err)
		}
	})

	for _, want := range []string{
		"stable",                    // stable link tag
		"expiring",                  // expiring link tag
		"book promptly",             // expiring-links warning
		"Durable link",              // durable fallback surfaced
		"booking.com/searchresults", // the fallback URL
		"tourist or city tax",       // tourist-tax note
	} {
		if !strings.Contains(out, want) {
			t.Errorf("prices table output missing %q:\n%s", want, out)
		}
	}
}
