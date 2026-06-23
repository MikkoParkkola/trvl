package mcp

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestOfferAccommodationTypeNoGuessing proves the honesty rule: an unspecified
// accommodation type with no property-type evidence resolves to "" (not
// evidenced), never a guessed "hotel_room". Evidence and an explicit caller
// preference still win, in that order.
func TestOfferAccommodationTypeNoGuessing(t *testing.T) {
	tests := []struct {
		name  string
		hotel models.HotelResult
		need  models.AccommodationNeed
		want  string
	}{
		{
			name:  "evidence wins",
			hotel: models.HotelResult{Name: "Sunset Apartments"},
			want:  models.AccommodationTypeEntireApartment,
		},
		{
			name:  "explicit preference when no evidence",
			hotel: models.HotelResult{Name: "The Place"},
			need:  models.AccommodationNeed{AccommodationType: models.AccommodationTypeHotelRoom},
			want:  models.AccommodationTypeHotelRoom,
		},
		{
			name:  "no evidence and no preference stays empty",
			hotel: models.HotelResult{Name: "The Place"},
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := offerAccommodationType(tt.hotel, tt.need); got != tt.want {
				t.Errorf("offerAccommodationType = %q, want %q", got, tt.want)
			}
		})
	}
}
