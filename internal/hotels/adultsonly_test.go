package hotels

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestAdultsOnlyMarking verifies the centralised marking applied in
// SearchHotels (via models.IsAdultsOnly) flags adults-only properties from
// either the name or the description, and leaves family-friendly ones alone.
// This mirrors the inline loop in SearchHotels without requiring network.
func TestAdultsOnlyMarking(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "TUI BLUE Madeira Gardens", Description: "Adults only retreat"},
		{Name: "Calm Bay Hotel", Description: "Peaceful; adults recommended."},
		{Name: "Family Fun Resort", Description: "Kids club and pools"},
	}

	for i := range hotels {
		if models.IsAdultsOnly(hotels[i].Name, hotels[i].Description) {
			hotels[i].AdultsOnly = true
		}
	}

	if !hotels[0].AdultsOnly {
		t.Error("expected name-based adults-only detection for index 0")
	}
	if !hotels[1].AdultsOnly {
		t.Error("expected description-based adults-only detection for index 1")
	}
	if hotels[2].AdultsOnly {
		t.Error("family resort must not be flagged adults-only")
	}
}
