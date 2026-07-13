package hacks

import (
	"context"
	"testing"
)

func TestDetectRailCompetition_emptyInput(t *testing.T) {
	hacks := detectRailCompetition(context.Background(), DetectorInput{})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for empty input, got %d", len(hacks))
	}
}

func TestDetectRailCompetition_missingOrigin(t *testing.T) {
	hacks := detectRailCompetition(context.Background(), DetectorInput{
		Destination: "BCN",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for missing origin, got %d", len(hacks))
	}
}

func TestDetectRailCompetition_missingDestination(t *testing.T) {
	hacks := detectRailCompetition(context.Background(), DetectorInput{
		Origin: "MAD",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for missing destination, got %d", len(hacks))
	}
}

func TestDetectRailCompetition_nonApplicableRoute(t *testing.T) {
	hacks := detectRailCompetition(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "AMS",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for non-applicable route, got %d", len(hacks))
	}
}

func TestDetectRailCompetition_madridBarcelona(t *testing.T) {
	hacks := detectRailCompetition(context.Background(), DetectorInput{
		Origin:      "MAD",
		Destination: "BCN",
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack for MAD→BCN, got %d", len(hacks))
	}
	h := hacks[0]
	if h.Type != "rail_competition" {
		t.Errorf("type = %q, want rail_competition", h.Type)
	}
	if h.Title == "" {
		t.Error("title is empty")
	}
	if h.Description == "" {
		t.Error("description is empty")
	}
	if len(h.Steps) == 0 {
		t.Error("steps are empty")
	}
	if len(h.Risks) == 0 {
		t.Error("risks are empty")
	}
	// Should mention 4 operators.
	if !containsSubstring(h.Description, "4 competing") {
		t.Error("expected 4 competing operators in description for MAD→BCN")
	}
	// Advisory hack — savings 0 without flight price.
	if h.Savings != 0 {
		t.Errorf("advisory hack should have 0 savings without NaivePrice, got %.0f", h.Savings)
	}
}

func TestDetectRailCompetition_reverseDirection(t *testing.T) {
	// BCN→MAD should also match (bidirectional).
	hacks := detectRailCompetition(context.Background(), DetectorInput{
		Origin:      "BCN",
		Destination: "MAD",
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack for BCN→MAD (reverse), got %d", len(hacks))
	}
	if hacks[0].Type != "rail_competition" {
		t.Errorf("type = %q, want rail_competition", hacks[0].Type)
	}
}

func TestDetectRailCompetition_italyRoute(t *testing.T) {
	hacks := detectRailCompetition(context.Background(), DetectorInput{
		Origin:      "MXP",
		Destination: "FCO",
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack for MXP→FCO, got %d", len(hacks))
	}
	// Should mention duopoly.
	if !containsSubstring(hacks[0].Description, "Trenitalia") {
		t.Error("expected Trenitalia in description for MXP→FCO")
	}
	if !containsSubstring(hacks[0].Description, "Italo") {
		t.Error("expected Italo in description for MXP→FCO")
	}
}

func TestDetectRailCompetition_czechRoute(t *testing.T) {
	hacks := detectRailCompetition(context.Background(), DetectorInput{
		Origin:      "PRG",
		Destination: "VIE",
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack for PRG→VIE, got %d", len(hacks))
	}
	if !containsSubstring(hacks[0].Description, "RegioJet") {
		t.Error("expected RegioJet in description for PRG→VIE")
	}
}

func TestDetectRailCompetition_caseInsensitive(t *testing.T) {
	hacks := detectRailCompetition(context.Background(), DetectorInput{
		Origin:      "mad",
		Destination: "bcn",
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack for lowercase mad→bcn, got %d", len(hacks))
	}
}

func TestDetectRailCompetition_withNaivePrice(t *testing.T) {
	// When NaivePrice is set, should compute savings.
	hacks := detectRailCompetition(context.Background(), DetectorInput{
		Origin:      "MAD",
		Destination: "BCN",
		NaivePrice:  80,
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack, got %d", len(hacks))
	}
	// MAD→BCN min fare = 7, naive = 80, savings = 73.
	if hacks[0].Savings != 73 {
		t.Errorf("savings = %.0f, want 73", hacks[0].Savings)
	}
}

func TestDetectRailCompetition_noSavingsWhenCheaper(t *testing.T) {
	// NaivePrice below rail fare — savings stays 0.
	hacks := detectRailCompetition(context.Background(), DetectorInput{
		Origin:      "MAD",
		Destination: "BCN",
		NaivePrice:  5,
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack, got %d", len(hacks))
	}
	// Flight is cheaper than rail — no savings.
	if hacks[0].Savings != 0 {
		t.Errorf("savings = %.0f, want 0 when flight cheaper than rail", hacks[0].Savings)
	}
}

func TestDetectRailCompetition_currencyDefault(t *testing.T) {
	hacks := detectRailCompetition(context.Background(), DetectorInput{
		Origin:      "MAD",
		Destination: "BCN",
	})
	if len(hacks) == 0 {
		t.Fatal("expected at least one hack")
	}
	if hacks[0].Currency != "EUR" {
		t.Errorf("currency = %q, want EUR", hacks[0].Currency)
	}
}

// TestDetectRailCompetition_nonEURTarget_suppressedWhenInconvertible proves
// case (b): a non-EUR target currency that can't be honestly converted (XXX
// is the ISO 4217 "no currency" placeholder — it will never appear in a
// real exchange-rate table) suppresses the hack rather than labeling the
// EUR-denominated MinFareEUR with the wrong currency. Deterministic — no
// live network required: a fake convertCurrency is injected via the seam in
// currency.go so the "can't convert" contract is exercised offline instead
// of dialing the live rates table. No t.Parallel — the seam var is shared
// package state, set/restored sequentially like railGroundSearcher.
func TestDetectRailCompetition_nonEURTarget_suppressedWhenInconvertible(t *testing.T) {
	orig := convertCurrency
	convertCurrency = func(_ context.Context, amount float64, from, to string) (float64, string) {
		if from == to {
			return amount, to
		}
		return amount, from // can't convert — same contract as the real function
	}
	t.Cleanup(func() { convertCurrency = orig })

	hacks := detectRailCompetition(context.Background(), DetectorInput{
		Origin:      "MAD",
		Destination: "BCN",
		Currency:    "XXX",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for inconvertible target currency, got %d", len(hacks))
	}
}

// TestDetectRailCompetition_eurTarget_labelsEURAndConverts proves cases (a)
// and (c) explicitly, including the NaivePrice comparison path: EUR passes
// through untouched (no network — ConvertCurrency short-circuits on
// from==to) and the surfaced hack's Currency matches the target.
func TestDetectRailCompetition_eurTarget_labelsEURAndConverts(t *testing.T) {
	hacks := detectRailCompetition(context.Background(), DetectorInput{
		Origin:      "MAD",
		Destination: "BCN",
		Currency:    "EUR",
		NaivePrice:  80,
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack, got %d", len(hacks))
	}
	if hacks[0].Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", hacks[0].Currency)
	}
	if hacks[0].Savings != 73 {
		t.Errorf("Savings = %.0f, want 73", hacks[0].Savings)
	}
}

// --- Static data tests ---

func TestCompetitiveCorridors_populated(t *testing.T) {
	if len(competitiveCorridors) == 0 {
		t.Fatal("competitiveCorridors is empty")
	}
	for i, c := range competitiveCorridors {
		if c.From == "" {
			t.Errorf("[%d] From is empty", i)
		}
		if c.To == "" {
			t.Errorf("[%d] To is empty", i)
		}
		if len(c.Operators) < 2 {
			t.Errorf("[%d] %s→%s has fewer than 2 operators", i, c.From, c.To)
		}
		if c.MinFareEUR <= 0 {
			t.Errorf("[%d] %s→%s MinFareEUR must be > 0, got %.2f", i, c.From, c.To, c.MinFareEUR)
		}
		if c.Country == "" {
			t.Errorf("[%d] %s→%s Country is empty", i, c.From, c.To)
		}
	}
}

func TestRailCityMap_populated(t *testing.T) {
	if len(railCityMap) == 0 {
		t.Fatal("railCityMap is empty")
	}
	// Spot check key entries.
	tests := []struct {
		code string
		city string
	}{
		{"MAD", "Madrid"},
		{"BCN", "Barcelona"},
		{"FCO", "Rome"},
		{"PRG", "Prague"},
	}
	for _, tc := range tests {
		got, ok := railCityMap[tc.code]
		if !ok {
			t.Errorf("railCityMap missing %s", tc.code)
			continue
		}
		if got != tc.city {
			t.Errorf("railCityMap[%s] = %q, want %q", tc.code, got, tc.city)
		}
	}
}
