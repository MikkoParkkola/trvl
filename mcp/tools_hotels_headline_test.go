package mcp

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestAccommodationEvidenceCreditsTheHeadlineProvider: evidence names the
// provider whose quote is the headline, not the cheaper lead-in teaser; the
// room lookup still keys on the Google place ID.
func TestAccommodationEvidenceCreditsTheHeadlineProvider(t *testing.T) {
	h := models.HotelResult{HotelID: "/g/11c0", HeadlineProvider: "agoda", CheapestSource: "google_hotels",
		Sources: []models.PriceSource{{Provider: "google_hotels"}, {Provider: "agoda"}}}
	if got := accommodationHotelProvider(h); got != "agoda" {
		t.Errorf("accommodationHotelProvider = %q, want agoda", got)
	}
	if got := accommodationEvidenceProvider(h, models.AccommodationOffer{}); got != "agoda" {
		t.Errorf("accommodationEvidenceProvider = %q, want agoda", got)
	}
	if !accommodationHotelIDSupportsRoomLookup(h) {
		t.Error("a Google place ID lost its room lookup because the headline came from agoda")
	}
}

// TestHotelSummaryCountsFartherHotels: the summary says how many hotels the
// distance cap left out and how to bring them back.
func TestHotelSummaryCountsFartherHotels(t *testing.T) {
	for name, res := range map[string]*models.HotelSearchResult{
		"some shown": {Success: true, Count: 1, Hotels: []models.HotelResult{{Name: "A", Price: 80, Currency: "EUR"}}, FartherOmitted: 3, MaxDistanceKm: 25},
		"none shown": {Success: true, Count: 0, FartherOmitted: 3, MaxDistanceKm: 25},
	} {
		got := hotelSummary(res, "Vienna")
		for _, want := range []string{"3 hotels", "25 km", "max_distance"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: summary %q lacks %q", name, got, want)
			}
		}
	}
}
