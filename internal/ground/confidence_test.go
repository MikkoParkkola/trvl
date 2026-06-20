package ground

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// annotateGroundConfidence must attach an honest Confidence to every route.
// A just-fetched route from a known provider is rated; a route with no
// provider signal is left unrated rather than faked.
func TestAnnotateGroundConfidence(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	routes := []models.GroundRoute{
		{
			Provider: "flixbus",
			Type:     "bus",
			Price:    19.99,
			Currency: "EUR",
			Sources: []models.PriceSource{
				{Provider: "flixbus", Price: 19.99, RetrievedAt: now},
				{Provider: "omio", Price: 21.50, RetrievedAt: now},
			},
		},
		{
			// No provider, no sources -> unrated, never faked.
			Type:     "bus",
			Price:    25,
			Currency: "EUR",
		},
	}
	annotateGroundConfidence(routes, now)

	if routes[0].Confidence == nil || !routes[0].Confidence.Rated {
		t.Fatalf("route 0 should be rated, got %+v", routes[0].Confidence)
	}
	if routes[1].Confidence == nil || routes[1].Confidence.Rated {
		t.Fatalf("route 1 should be unrated, got %+v", routes[1].Confidence)
	}
	if routes[1].Confidence.Label != models.ConfidenceUnrated {
		t.Errorf("route 1 expected unrated label, got %q", routes[1].Confidence.Label)
	}
}
