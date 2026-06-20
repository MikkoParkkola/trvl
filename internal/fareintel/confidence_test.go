package fareintel

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/dealquality"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// highSignal: live API price corroborated by several sources should score high.
func TestScoreConfidence_HighSignal(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	in := ConfidenceInput{
		Price:       120,
		Currency:    "EUR",
		Provider:    "kiwi", // structured API
		RetrievedAt: now,    // just fetched -> live
		Now:         now,
		Sources: []models.PriceSource{
			{Provider: "kiwi", Price: 120, RetrievedAt: now},
			{Provider: "ryanair", Price: 125, RetrievedAt: now},
			{Provider: "skiplagged", Price: 130, RetrievedAt: now},
		},
	}
	c := ScoreConfidence(in)
	if !c.Rated {
		t.Fatalf("expected rated, got unrated: %+v", c)
	}
	if c.Label != models.ConfidenceHigh {
		t.Fatalf("expected high label, got %q (score %.3f)", c.Label, c.Score)
	}
	if c.Freshness != models.FreshnessLive {
		t.Errorf("expected live freshness, got %q", c.Freshness)
	}
	if c.Percent() < 75 {
		t.Errorf("expected >=75%%, got %d", c.Percent())
	}
	if c.Basis == "" {
		t.Error("expected a non-empty basis explanation")
	}
}

// staleLowSignal: a stale scraped single-source price should score low.
func TestScoreConfidence_StaleLowSignal(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	// booking has StaleMinutes=720 (12h); 13h old -> stale.
	retrieved := now.Add(-13 * time.Hour)
	in := ConfidenceInput{
		Price:       300,
		Currency:    "EUR",
		Provider:    "booking", // scrape
		RetrievedAt: retrieved,
		Now:         now,
		Sources:     []models.PriceSource{{Provider: "booking", Price: 300, RetrievedAt: retrieved}},
	}
	c := ScoreConfidence(in)
	if !c.Rated {
		t.Fatalf("expected rated, got unrated: %+v", c)
	}
	if c.Label != models.ConfidenceLow {
		t.Fatalf("expected low label, got %q (score %.3f)", c.Label, c.Score)
	}
	if c.Freshness != models.FreshnessStale {
		t.Errorf("expected stale freshness, got %q", c.Freshness)
	}
}

// noSignal: no provider, no freshness, no sources -> honest unrated, never faked.
func TestScoreConfidence_UnratedWhenNoSignal(t *testing.T) {
	c := ScoreConfidence(ConfidenceInput{Price: 100, Currency: "EUR"})
	if c.Rated {
		t.Fatalf("expected unrated, got rated: %+v", c)
	}
	if c.Label != models.ConfidenceUnrated {
		t.Errorf("expected unrated label, got %q", c.Label)
	}
	if c.Score != 0 {
		t.Errorf("unrated score must be 0 (never fabricated), got %.3f", c.Score)
	}
	if c.Percent() != 0 {
		t.Errorf("unrated Percent() must be 0, got %d", c.Percent())
	}
}

// noPrice: a missing price can never be assessed.
func TestScoreConfidence_UnratedWhenNoPrice(t *testing.T) {
	c := ScoreConfidence(ConfidenceInput{Price: 0, Provider: "kiwi"})
	if c.Rated || c.Label != models.ConfidenceUnrated {
		t.Fatalf("expected unrated for zero price, got %+v", c)
	}
}

// singleScrapeFresh: a just-fetched single scraped source is plausibly bookable
// but not high — should land medium.
func TestScoreConfidence_SingleScrapeFreshIsMedium(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	c := ScoreConfidence(ConfidenceInput{
		Price:       200,
		Currency:    "EUR",
		Provider:    "google_flights", // scrape
		RetrievedAt: now,
		Now:         now,
		Sources:     []models.PriceSource{{Provider: "google_flights", Price: 200, RetrievedAt: now}},
	})
	if !c.Rated {
		t.Fatalf("expected rated, got %+v", c)
	}
	if c.Label != models.ConfidenceMedium {
		t.Errorf("expected medium, got %q (score %.3f)", c.Label, c.Score)
	}
}

// separateTickets: a synthetic book-direct itinerary is discounted vs the same
// signals without the separate-tickets risk.
func TestScoreConfidence_SeparateTicketsDiscount(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	base := ConfidenceInput{
		Price:       120,
		Currency:    "EUR",
		Provider:    "kiwi",
		RetrievedAt: now,
		Now:         now,
		Sources: []models.PriceSource{
			{Provider: "kiwi", Price: 120, RetrievedAt: now},
			{Provider: "ryanair", Price: 125, RetrievedAt: now},
		},
	}
	whole := ScoreConfidence(base)
	base.SeparateTickets = true
	split := ScoreConfidence(base)
	if split.Score >= whole.Score {
		t.Fatalf("separate-tickets score %.3f should be < whole-fare %.3f", split.Score, whole.Score)
	}
	if !containsSignal(split.Signals, "separate_tickets_risk") {
		t.Error("expected separate_tickets_risk signal tag")
	}
}

// dealHistory: a healthy historical price band contributes a deal-position
// signal via dealquality without being faked when history is too sparse.
func TestScoreConfidence_DealPositionSignal(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	samples := make([]dealquality.Sample, 0, 12)
	for i := 0; i < 12; i++ {
		samples = append(samples, dealquality.Sample{Route: "HEL-BCN", Season: "Q2", Date: "2026-05-01", Price: 100 + float64(i*5), Kind: "flight"})
	}
	in := ConfidenceInput{
		Price:       120, // mid-band -> plausible
		Currency:    "EUR",
		Provider:    "kiwi",
		RetrievedAt: now,
		Now:         now,
		Sources:     []models.PriceSource{{Provider: "kiwi", Price: 120, RetrievedAt: now}},
		DealSamples: samples,
	}
	c := ScoreConfidence(in)
	if !c.Rated {
		t.Fatalf("expected rated, got %+v", c)
	}
	if !hasSignalPrefix(c.Signals, "deal:") {
		t.Errorf("expected a deal: signal from dealquality, got %v", c.Signals)
	}

	// Sparse history must NOT fabricate a deal signal.
	in.DealSamples = samples[:3]
	c2 := ScoreConfidence(in)
	if hasSignalPrefix(c2.Signals, "deal:") {
		t.Errorf("sparse history must not produce a deal signal, got %v", c2.Signals)
	}
}

// fareHistory: fareintel.Analyze is reused as the price-position signal when
// fare history (not dealquality samples) is supplied.
func TestScoreConfidence_FareHistorySignal(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	hist := []watch.PricePoint{
		Point(100, "EUR", now.Add(-72*time.Hour)),
		Point(110, "EUR", now.Add(-48*time.Hour)),
		Point(105, "EUR", now.Add(-24*time.Hour)),
		Point(108, "EUR", now.Add(-12*time.Hour)),
	}
	c := ScoreConfidence(ConfidenceInput{
		Price:       104,
		Currency:    "EUR",
		Provider:    "kiwi",
		RetrievedAt: now,
		Now:         now,
		Sources:     []models.PriceSource{{Provider: "kiwi", Price: 104, RetrievedAt: now}},
		FareHistory: hist,
	})
	if !hasSignalPrefix(c.Signals, "fare:") {
		t.Errorf("expected a fare: signal from fareintel.Analyze, got %v", c.Signals)
	}
}

func containsSignal(sigs []string, want string) bool {
	for _, s := range sigs {
		if s == want {
			return true
		}
	}
	return false
}

func hasSignalPrefix(sigs []string, prefix string) bool {
	for _, s := range sigs {
		if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
