package hacks

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// --- MIK-3079: rail leg cost pricing ---

// withRailGroundSearcher swaps the package-level ground searcher for the
// duration of a test and restores it afterwards.
func withRailGroundSearcher(t *testing.T, fn func(ctx context.Context, from, to, date string, opts ground.SearchOptions) (*models.GroundSearchResult, error)) {
	t.Helper()
	orig := railGroundSearcher
	railGroundSearcher = fn
	t.Cleanup(func() { railGroundSearcher = orig })
}

// TestRailFlyInjectsConcreteCandidateViaSeam verifies that when the rail
// station search seam returns a real flight, DetectRailFlyArbitrage populates
// ConcreteCandidates (for later injection as ranked bookable result).
func TestRailFlyInjectsConcreteCandidateViaSeam(t *testing.T) {
	withRailFlyFlightSearcher(t, func(_ context.Context, _ *batchexec.Client, origin, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
		if origin == "AMS" {
			return &models.FlightSearchResult{Success: true, Flights: []models.FlightResult{{Price: 280, Currency: "EUR", Legs: []models.FlightLeg{{DepartureAirport: models.AirportInfo{Code: "AMS"}}}}}}, nil
		}
		// rail origin returns a concrete cheaper itinerary
		return &models.FlightSearchResult{Success: true, Flights: []models.FlightResult{{Price: 210, Currency: "EUR", Legs: []models.FlightLeg{{DepartureAirport: models.AirportInfo{Code: "ZWE"}}}}}}, nil
	})
	withRailGroundSearcher(t, func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		return &models.GroundSearchResult{Success: true, Count: 1, Routes: []models.GroundRoute{{Provider: "eurostar", Type: "train", Price: 0, Currency: "EUR"}}}, nil
	})

	hacks := DetectRailFlyArbitrage(context.Background(), "AMS", "BCN", "2026-07-01", "")
	if len(hacks) == 0 {
		t.Fatal("expected rail+fly hack for AMS")
	}
	if len(hacks[0].ConcreteCandidates) == 0 {
		t.Fatal("expected ConcreteCandidates populated via rail searcher seam")
	}
	if hacks[0].ConcreteCandidates[0].Legs[0].DepartureAirport.Code != "ZWE" {
		t.Errorf("candidate origin = %q, want ZWE (rail station)", hacks[0].ConcreteCandidates[0].Legs[0].DepartureAirport.Code)
	}
}

var testRailFlyStation = railFlyStation{
	IATA: "ZWE", City: "Antwerp", HubIATA: "AMS", Airline: "KL",
	AirlineName: "KLM", TrainProvider: "Eurostar", TrainMinutes: 60,
	FareZone: "Belgian market",
}

func TestResolveRailLegCost_bundledFreeWithLiveStandalone(t *testing.T) {
	// Hub origin (AMS == station.HubIATA): live ground quote available -> standalone
	// note carries the live price, headline cost stays 0 (bundled in the ticket).
	withRailGroundSearcher(t, func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		return &models.GroundSearchResult{
			Success: true,
			Count:   1,
			Routes: []models.GroundRoute{
				{Provider: "eurostar", Type: "train", Price: 39, Currency: "EUR"},
			},
		}, nil
	})

	leg := resolveRailLegCost(context.Background(), "AMS", testRailFlyStation, "2026-05-01")
	if leg.Cost != 0 {
		t.Errorf("bundled rail leg Cost = %v, want 0 (included in ticket)", leg.Cost)
	}
	if !strings.Contains(leg.Note, "included in airline ticket") {
		t.Errorf("note should state the bundle, got %q", leg.Note)
	}
	if !strings.Contains(leg.Note, "live quote") || !strings.Contains(leg.Note, "39") {
		t.Errorf("note should carry the live standalone quote, got %q", leg.Note)
	}
}

func TestResolveRailLegCost_aliasOriginChargesRealLeg(t *testing.T) {
	// Alias origin (ANR != station.HubIATA AMS): the rail leg is a real,
	// separately-paid cost -> headline Cost carries the live quote, not 0.
	withRailGroundSearcher(t, func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		return &models.GroundSearchResult{
			Success: true,
			Count:   1,
			Routes: []models.GroundRoute{
				{Provider: "eurostar", Type: "train", Price: 39, Currency: "EUR"},
			},
		}, nil
	})

	leg := resolveRailLegCost(context.Background(), "ANR", testRailFlyStation, "2026-05-01")
	if leg.Cost != 39 {
		t.Errorf("alias-origin rail leg Cost = %v, want 39 (real paid leg)", leg.Cost)
	}
	if leg.Estimated {
		t.Error("with a live quote the alias leg should not be an estimate")
	}
	if leg.Provider != "eurostar" {
		t.Errorf("alias-origin leg Provider = %q, want eurostar", leg.Provider)
	}
}

func TestResolveRailLegCost_aliasOriginErrorEstimateIsCharged(t *testing.T) {
	// Alias origin with a provider error -> labelled estimate is the real cost.
	withRailGroundSearcher(t, func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		return nil, errors.New("ground provider timeout")
	})

	leg := resolveRailLegCost(context.Background(), "ANR", testRailFlyStation, "2026-05-01")
	if !leg.Estimated {
		t.Error("alias-origin provider error should degrade to a charged estimate")
	}
	if leg.Cost <= 0 {
		t.Errorf("estimate cost should be a positive conservative value, got %v", leg.Cost)
	}
}

func TestStandaloneRailQuote_liveQuoteWins(t *testing.T) {
	withRailGroundSearcher(t, func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		return &models.GroundSearchResult{
			Success: true,
			Count:   2,
			Routes: []models.GroundRoute{
				{Provider: "eurostar", Type: "train", Price: 59, Currency: "EUR"},
				{Provider: "ns", Type: "train", Price: 42, Currency: "EUR"}, // cheapest
			},
		}, nil
	})

	leg := standaloneRailQuote(context.Background(), testRailFlyStation, "2026-05-01")
	if leg.Estimated {
		t.Error("expected a live (non-estimated) quote")
	}
	if leg.Cost != 42 {
		t.Errorf("expected cheapest live price 42, got %v", leg.Cost)
	}
	if leg.Provider != "ns" {
		t.Errorf("expected provider ns (cheapest), got %q", leg.Provider)
	}
}

func TestStandaloneRailQuote_providerErrorDegradesToEstimate(t *testing.T) {
	// A ground-provider error must NEVER crash — it degrades to a labelled estimate.
	withRailGroundSearcher(t, func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		return nil, errors.New("ground provider timeout")
	})

	leg := standaloneRailQuote(context.Background(), testRailFlyStation, "2026-05-01")
	if !leg.Estimated {
		t.Error("provider error should degrade to an estimate (Estimated=true)")
	}
	if leg.Cost <= 0 {
		t.Errorf("estimate cost should be a positive conservative value, got %v", leg.Cost)
	}
	if !strings.Contains(leg.Note, "estimate") {
		t.Errorf("estimate must be labelled, got %q", leg.Note)
	}
}

func TestStandaloneRailQuote_emptyResultDegradesToEstimate(t *testing.T) {
	withRailGroundSearcher(t, func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		return &models.GroundSearchResult{Success: true, Count: 0, Routes: nil}, nil
	})

	leg := standaloneRailQuote(context.Background(), testRailFlyStation, "2026-05-01")
	if !leg.Estimated {
		t.Error("empty route list should degrade to an estimate")
	}
	if leg.Cost <= 0 {
		t.Errorf("estimate cost should be positive, got %v", leg.Cost)
	}
}

func TestAttachRailLegCost_setsFieldsAndStep(t *testing.T) {
	h := buildRailFlyHack("AMS", "BCN", 300, "EUR", 240, "EUR", 60, testRailFlyStation, "")
	stepsBefore := len(h.Steps)

	attachRailLegCost(&h, railLegCost{
		Cost: 0, Currency: "EUR", Provider: "Eurostar",
		Estimated: false, Note: "included in airline ticket; standalone leg live quote 39 EUR via eurostar",
	})

	if h.RailCost != 0 {
		t.Errorf("RailCost = %v, want 0", h.RailCost)
	}
	if h.RailCostCurrency != "EUR" {
		t.Errorf("RailCostCurrency = %q, want EUR", h.RailCostCurrency)
	}
	if h.RailProvider != "Eurostar" {
		t.Errorf("RailProvider = %q, want Eurostar", h.RailProvider)
	}
	if h.RailCostEstimated {
		t.Error("RailCostEstimated should be false for a bundled+live leg")
	}
	if h.RailCostNote == "" {
		t.Error("RailCostNote should be set")
	}
	if len(h.Steps) != stepsBefore+1 {
		t.Errorf("expected one rail-cost step appended, steps before=%d after=%d", stepsBefore, len(h.Steps))
	}
	if !strings.Contains(h.Steps[len(h.Steps)-1], "Rail leg cost") {
		t.Errorf("last step should describe the rail leg cost, got %q", h.Steps[len(h.Steps)-1])
	}
}

func TestAttachRailLegCost_estimateIsLabelled(t *testing.T) {
	h := buildRailFlyHack("AMS", "BCN", 300, "EUR", 240, "EUR", 60, testRailFlyStation, "")
	attachRailLegCost(&h, railLegCost{
		Cost: 25, Currency: "EUR", Provider: "", Estimated: true, Note: "estimate ~25 EUR",
	})
	if !h.RailCostEstimated {
		t.Error("RailCostEstimated should be true for the estimate path")
	}
	if !strings.Contains(h.RailCostNote, "estimate") {
		t.Errorf("estimate note should be labelled, got %q", h.RailCostNote)
	}
}

func TestAttachRailLegCost_nilHackNoPanic(t *testing.T) {
	// Must not panic on a nil hack pointer.
	attachRailLegCost(nil, railLegCost{Cost: 0, Currency: "EUR"})
}

func TestBuildRailFlyHack_aliasOriginIsHonestAboutPaidLeg(t *testing.T) {
	// Alias origin (ANR != hub AMS): the hack text must NOT claim the train is
	// free, and must flag the separate rail ticket.
	h := buildRailFlyHack("ANR", "BCN", 300, "EUR", 250, "EUR", 35, testRailFlyStation, "")

	if strings.Contains(h.Title, "is free") {
		t.Errorf("alias-origin title must not claim the train is free, got %q", h.Title)
	}
	if !strings.Contains(h.Title, "net saving") {
		t.Errorf("alias-origin title should mention net saving, got %q", h.Title)
	}
	if strings.Contains(h.Description, "included free") {
		t.Errorf("alias-origin description must not say included free, got %q", h.Description)
	}
	if !strings.Contains(h.Description, "separate rail ticket") {
		t.Errorf("alias-origin description should flag the separate rail ticket, got %q", h.Description)
	}
	foundRiskNote := false
	for _, r := range h.Risks {
		if strings.Contains(r, "separate ticket") {
			foundRiskNote = true
		}
	}
	if !foundRiskNote {
		t.Error("alias-origin hack should carry a risk noting the rail leg is a separate ticket")
	}
}

func TestBuildRailFlyHack_hubOriginUnchangedBundledText(t *testing.T) {
	// Hub origin (AMS == hub): existing bundled wording is preserved.
	h := buildRailFlyHack("AMS", "BCN", 300, "EUR", 240, "EUR", 60, testRailFlyStation, "")
	if !strings.Contains(h.Title, "is free") {
		t.Errorf("hub-origin title should keep the bundled 'is free' wording, got %q", h.Title)
	}
	if !strings.Contains(h.Description, "included free in the ticket") {
		t.Errorf("hub-origin description should keep bundled wording, got %q", h.Description)
	}
}
