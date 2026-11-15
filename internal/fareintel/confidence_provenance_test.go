package fareintel

import (
	"slices"
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

// TestScoreConfidence_CorroborationCountsLiveSourcesOnly pins that an
// estimated or unavailable ground source never corroborates a live fare;
// sources without a provenance (flights, hotels) still count.
func TestScoreConfidence_CorroborationCountsLiveSourcesOnly(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	c := ScoreConfidence(ConfidenceInput{
		Price: 22, Currency: "EUR", Provider: "flixbus", RetrievedAt: now, Now: now,
		PriceProvenance: models.PriceLive,
		Sources: []models.PriceSource{
			{Provider: "flixbus", Price: 22, Currency: "EUR", RetrievedAt: now, Provenance: models.PriceLive},
			{Provider: "rome2rio", Price: 18, Currency: "EUR", RetrievedAt: now, Provenance: models.PriceEstimated},
			{Provider: "transitous", Currency: "EUR", RetrievedAt: now, Provenance: models.PriceUnavailable},
		},
	})
	if !slices.Contains(c.Signals, "sources:1") {
		t.Errorf("signals = %v, want sources:1 (only the live source corroborates)", c.Signals)
	}
	flight := ScoreConfidence(ConfidenceInput{
		Price: 120, Currency: "EUR", Provider: "google_flights", RetrievedAt: now, Now: now,
		Sources: []models.PriceSource{{Provider: "google_flights", Price: 120, RetrievedAt: now}, {Provider: "kiwi", Price: 125, RetrievedAt: now}},
	})
	found := false
	for _, s := range flight.Signals {
		found = found || s == "sources:2"
	}
	if !found {
		t.Errorf("flight signals = %v, want sources:2 (no provenance still counts)", flight.Signals)
	}
}
