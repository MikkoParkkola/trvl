package mcp

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestAccommodationPriceDisplay pins the labeled price rendering. Providers
// disagree on whether they quote a nightly rate or a stay total, so the
// human-facing string must always say which number it is showing.
func TestAccommodationPriceDisplay(t *testing.T) {
	cases := []struct {
		name  string
		offer models.AccommodationOffer
		want  string
	}{
		{
			name:  "nightly and total both shown and labeled",
			offer: models.AccommodationOffer{Currency: "EUR", NightlyPrice: 120, TotalPrice: 360},
			want:  "EUR 120/night (EUR 360 total)",
		},
		{
			name:  "single night collapses to nightly only",
			offer: models.AccommodationOffer{Currency: "EUR", NightlyPrice: 120, TotalPrice: 120},
			want:  "EUR 120/night",
		},
		{
			name:  "nightly only is labeled per night",
			offer: models.AccommodationOffer{Currency: "USD", NightlyPrice: 99},
			want:  "USD 99/night",
		},
		{
			name:  "total only is labeled total",
			offer: models.AccommodationOffer{Currency: "GBP", TotalPrice: 500},
			want:  "GBP 500 total",
		},
		{
			name:  "no price yields empty string",
			offer: models.AccommodationOffer{Currency: "EUR"},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := accommodationPriceDisplay(tc.offer); got != tc.want {
				t.Fatalf("accommodationPriceDisplay = %q, want %q", got, tc.want)
			}
		})
	}
}
