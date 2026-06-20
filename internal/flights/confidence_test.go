package flights

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// annotateFlightConfidence must attach an honest Confidence to every flight:
// a live multi-source API fare scores high; a synthetic separate-tickets
// itinerary is discounted relative to the same signals.
func TestAnnotateFlightConfidence(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	flights := []models.FlightResult{
		{
			Price:    120,
			Currency: "EUR",
			Provider: "kiwi",
			Sources: []models.PriceSource{
				{Provider: "kiwi", Price: 120, RetrievedAt: now},
				{Provider: "ryanair", Price: 125, RetrievedAt: now},
				{Provider: "skiplagged", Price: 130, RetrievedAt: now},
			},
		},
		{
			Price:      140,
			Currency:   "EUR",
			Provider:   "kiwi",
			BookDirect: true,
			Sources: []models.PriceSource{
				{Provider: "kiwi", Price: 140, RetrievedAt: now},
				{Provider: "ryanair", Price: 145, RetrievedAt: now},
			},
		},
	}
	annotateFlightConfidence(flights, now)

	if flights[0].Confidence == nil || !flights[0].Confidence.Rated {
		t.Fatalf("flight 0 should be rated, got %+v", flights[0].Confidence)
	}
	if flights[0].Confidence.Label != models.ConfidenceHigh {
		t.Errorf("flight 0 expected high, got %q", flights[0].Confidence.Label)
	}
	if flights[1].Confidence == nil || flights[1].Confidence.Score >= flights[0].Confidence.Score {
		t.Errorf("separate-tickets flight should score below the whole fare")
	}
}
