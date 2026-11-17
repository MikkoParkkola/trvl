package mcp

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// The built-in Booking search tags quotes "booking.com", the provider runtime
// "booking"; the summary counts both (#808).
func TestHotelSummaryText_CountsBothBookingTags(t *testing.T) {
	result := &models.HotelSearchResult{
		Success: true,
		Count:   2,
		Hotels: []models.HotelResult{
			{Name: "Built-in", Price: 100, Currency: "EUR", Sources: []models.PriceSource{{Provider: "booking.com", Price: 100, Currency: "EUR"}}},
			{Name: "Runtime", Price: 110, Currency: "EUR", Sources: []models.PriceSource{{Provider: "booking", Price: 110, Currency: "EUR"}}},
		},
	}
	if got := hotelSummaryText(result, "Vienna"); !strings.Contains(got, "Includes 2 Booking.com matches") {
		t.Fatalf("summary = %q, want it to count both Booking tags", got)
	}
}
