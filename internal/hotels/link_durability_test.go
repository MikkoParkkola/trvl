package hotels

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestLinkClassification(t *testing.T) {
	cases := []struct {
		url        string
		deadRental bool
		expiring   bool
	}{
		{"https://www.google.com/travel/clk/abc123", true, false},
		{"https://www.google.com/aclk?sa=l&ai=xyz", false, true},
		{"https://www.booking.com/hotel/it/villa-maria.html", false, false},
		{"", false, false},
	}
	for _, c := range cases {
		if got := isDeadRentalRedirect(c.url); got != c.deadRental {
			t.Errorf("isDeadRentalRedirect(%q) = %v, want %v", c.url, got, c.deadRental)
		}
		if got := isExpiringAdRedirect(c.url); got != c.expiring {
			t.Errorf("isExpiringAdRedirect(%q) = %v, want %v", c.url, got, c.expiring)
		}
	}
}

func TestDurableBookingURL(t *testing.T) {
	u := durableBookingURL("Hotel Villa Maria", "2026-07-30", "2026-08-04")
	if !strings.HasPrefix(u, "https://www.booking.com/searchresults.html?") {
		t.Fatalf("durable URL = %q, want booking.com searchresults", u)
	}
	for _, want := range []string{"ss=Hotel+Villa+Maria", "checkin=2026-07-30", "checkout=2026-08-04"} {
		if !strings.Contains(u, want) {
			t.Errorf("durable URL %q missing %q", u, want)
		}
	}
	if durableBookingURL("", "2026-07-30", "2026-08-04") != "" {
		t.Error("durable URL with empty name should be empty")
	}
}

// #168: dead vacation-rental redirects are stripped, ad-click links are tagged
// expiring, direct links stay stable, and a durable Booking.com fallback is
// always attached. Reference behaviour from RobertoReale/travel-search.
func TestApplyLinkDurability(t *testing.T) {
	result := &models.HotelPriceResult{
		Name:     "Hotel Villa Maria",
		CheckIn:  "2026-07-30",
		CheckOut: "2026-08-04",
		Providers: []models.ProviderPrice{
			{Provider: "VacationRentals", ProviderURL: "https://www.google.com/travel/clk/dead"},
			{Provider: "Booking.com", ProviderURL: "https://www.google.com/aclk?ai=expiring"},
			{Provider: "Hotels.com", ProviderURL: "https://hotels.com/villa-maria"},
			{Provider: "NoLink", ProviderURL: ""},
		},
	}
	applyLinkDurability(result)

	if result.Providers[0].ProviderURL != "" || result.Providers[0].LinkDurability != "" {
		t.Errorf("dead rental redirect must be stripped, got url=%q dur=%q",
			result.Providers[0].ProviderURL, result.Providers[0].LinkDurability)
	}
	if result.Providers[1].LinkDurability != linkExpiring {
		t.Errorf("aclk link must be tagged expiring, got %q", result.Providers[1].LinkDurability)
	}
	if result.Providers[2].LinkDurability != linkStable {
		t.Errorf("direct OTA link must be tagged stable, got %q", result.Providers[2].LinkDurability)
	}
	if result.Providers[3].LinkDurability != "" {
		t.Errorf("empty link must have empty durability, got %q", result.Providers[3].LinkDurability)
	}
	if !strings.Contains(result.BookingFallbackURL, "booking.com/searchresults") {
		t.Errorf("durable fallback must be attached, got %q", result.BookingFallbackURL)
	}
}
