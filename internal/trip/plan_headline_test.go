package trip

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestExtractTopHotelsCreditsTheHeadlineProvider: the trip plan names the
// provider whose price it shows.
func TestExtractTopHotelsCreditsTheHeadlineProvider(t *testing.T) {
	got := extractTopHotels([]models.HotelResult{{
		Name: "St Christopher's", Price: 66.83, Currency: "EUR", Rating: 8.1,
		HeadlineProvider: "agoda", CheapestSource: "google_hotels",
	}}, 2, 1)
	if len(got) != 1 || got[0].PriceSource != "agoda" {
		t.Fatalf("plan hotels = %+v, want price source agoda", got)
	}
}
