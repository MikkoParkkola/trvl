package fareintel

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestScoreConfidence_EstimatedFareIsUnrated pins D1: an estimated ground
// price (static table, day-best, range low) is never scored as bookable, so
// confidence is unrated with basis "estimated fare" and no live freshness,
// even when the other signals would have produced a rating.
func TestScoreConfidence_EstimatedFareIsUnrated(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	c := ScoreConfidence(ConfidenceInput{
		Price:           22,
		Currency:        "EUR",
		Provider:        "vikingline",
		RetrievedAt:     now,
		Now:             now,
		Sources:         []models.PriceSource{{Provider: "vikingline", Price: 22, Currency: "EUR", RetrievedAt: now}},
		PriceProvenance: models.PriceEstimated,
	})
	if c.Rated || c.Label != models.ConfidenceUnrated {
		t.Errorf("confidence = rated %v label %q, want unrated", c.Rated, c.Label)
	}
	if c.Basis != "estimated fare" {
		t.Errorf("basis = %q, want \"estimated fare\"", c.Basis)
	}
	if c.Freshness == models.FreshnessLive {
		t.Error("an estimated fare carries a live freshness")
	}
}

// TestScoreConfidence_LiveFareStillRated guards the other side: a live fare
// with the same signals keeps its rating.
func TestScoreConfidence_LiveFareStillRated(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	c := ScoreConfidence(ConfidenceInput{
		Price:           22,
		Currency:        "EUR",
		Provider:        "flixbus",
		RetrievedAt:     now,
		Now:             now,
		Sources:         []models.PriceSource{{Provider: "flixbus", Price: 22, Currency: "EUR", RetrievedAt: now}},
		PriceProvenance: models.PriceLive,
	})
	if !c.Rated {
		t.Errorf("live fare confidence unrated: %+v", c)
	}
}
