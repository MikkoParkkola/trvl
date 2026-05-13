package opportunity

// MIK-3065 (partial): tests for the opportunity score composer.

import (
	"math"
	"strings"
	"testing"
)

func sampleCandidate() Candidate {
	return Candidate{
		Destination: "PRG",
		DepartDate:  "2026-06-15",
		ReturnDate:  "2026-06-22",
		Nights:      7,
		Signals:     Signals{ProfileMatch: 90, RequestMatch: 70, DealQuality: 95},
	}
}

func TestScore_DefaultWeightsMatchAC(t *testing.T) {
	got := Score(sampleCandidate(), Weights{})
	// 0.4·90 + 0.2·70 + 0.4·95 = 36 + 14 + 38 = 88
	want := 88.0
	if math.Abs(got.Overall-want) > 0.01 {
		t.Errorf("Overall=%.2f, want %.2f", got.Overall, want)
	}
}

func TestScore_CustomWeights(t *testing.T) {
	got := Score(sampleCandidate(), Weights{ProfileMatch: 1, RequestMatch: 0, DealQuality: 0})
	if math.Abs(got.Overall-90) > 0.01 {
		t.Errorf("Overall=%.2f, want 90 (pure profile match)", got.Overall)
	}
}

func TestScore_ClampUpper(t *testing.T) {
	c := Candidate{Signals: Signals{ProfileMatch: 200, RequestMatch: 200, DealQuality: 200}}
	got := Score(c, Weights{ProfileMatch: 1, RequestMatch: 1, DealQuality: 1})
	if got.Overall != 100 {
		t.Errorf("Overall=%.2f, want 100 (clamped)", got.Overall)
	}
}

func TestScore_ClampLower(t *testing.T) {
	c := Candidate{Signals: Signals{ProfileMatch: -10, RequestMatch: -10, DealQuality: -10}}
	got := Score(c, Weights{})
	if got.Overall != 0 {
		t.Errorf("Overall=%.2f, want 0 (clamped)", got.Overall)
	}
}

func TestScore_ReasonBands(t *testing.T) {
	cases := []struct {
		signals Signals
		want    string
	}{
		{Signals{ProfileMatch: 100, RequestMatch: 100, DealQuality: 100}, "unicorn"},
		{Signals{ProfileMatch: 90, RequestMatch: 80, DealQuality: 85}, "excellent"},
		{Signals{ProfileMatch: 70, RequestMatch: 70, DealQuality: 70}, "solid"},
		{Signals{ProfileMatch: 30, RequestMatch: 30, DealQuality: 30}, "below-threshold"},
	}
	for _, tc := range cases {
		got := Score(Candidate{Signals: tc.signals}, Weights{})
		if !strings.Contains(got.Reason, tc.want) {
			t.Errorf("signals=%v Reason=%q want band %q", tc.signals, got.Reason, tc.want)
		}
	}
}

func TestFilterAndRank_DropsBelowMinScore(t *testing.T) {
	in := []Candidate{
		{Destination: "A", Signals: Signals{ProfileMatch: 90, RequestMatch: 80, DealQuality: 90}},    // overall 88
		{Destination: "B", Signals: Signals{ProfileMatch: 50, RequestMatch: 50, DealQuality: 50}},    // overall 50
		{Destination: "C", Signals: Signals{ProfileMatch: 100, RequestMatch: 100, DealQuality: 100}}, // overall 100
	}
	got := FilterAndRank(in, Weights{}, 85)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2 above min_score 85", len(got))
	}
	if got[0].Destination != "C" {
		t.Errorf("first=%q, want C (overall 100 should sort first)", got[0].Destination)
	}
}

func TestFilterAndRank_NilOnEmpty(t *testing.T) {
	if got := FilterAndRank(nil, Weights{}, 85); got != nil {
		t.Errorf("nil input should yield nil, got %v", got)
	}
}

func TestFilterAndRank_NilWhenAllBelow(t *testing.T) {
	in := []Candidate{
		{Destination: "A", Signals: Signals{ProfileMatch: 30, RequestMatch: 30, DealQuality: 30}},
	}
	if got := FilterAndRank(in, Weights{}, 85); got != nil {
		t.Errorf("all-below should yield nil, got %v", got)
	}
}

func TestFilterAndRank_MinScoreZeroKeepsAll(t *testing.T) {
	in := []Candidate{
		{Destination: "A", Signals: Signals{ProfileMatch: 10, RequestMatch: 10, DealQuality: 10}},
		{Destination: "B", Signals: Signals{ProfileMatch: 5, RequestMatch: 5, DealQuality: 5}},
	}
	got := FilterAndRank(in, Weights{}, 0)
	if len(got) != 2 {
		t.Errorf("min_score=0 should keep all, got %d", len(got))
	}
}

func TestFavouritesFromPreferences_UnionAndIntersection(t *testing.T) {
	got := FavouritesFromPreferences(
		[]string{"PRG", "KRK"},        // bucket list always
		[]string{"VIE", "BCN", "ZRH"}, // previous trips, only those with affinity >= 3
		map[string]int{"VIE": 5, "BCN": 2, "ZRH": 4},
		3,
	)
	want := []string{"KRK", "PRG", "VIE", "ZRH"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, code := range want {
		if got[i] != code {
			t.Errorf("got[%d]=%q, want %q", i, got[i], code)
		}
	}
}

func TestFavouritesFromPreferences_DefaultsAndCaseFolding(t *testing.T) {
	got := FavouritesFromPreferences(
		[]string{"prg", "  prg  "}, // dedup + case fold + trim
		[]string{"vie"},
		map[string]int{"vie": 5}, // case-insensitive lookup
		0,                        // 0 falls back to default 3
	)
	if len(got) != 2 || got[0] != "PRG" || got[1] != "VIE" {
		t.Errorf("got %v, want [PRG VIE]", got)
	}
}

func TestFavouritesFromPreferences_BelowAffinityExcluded(t *testing.T) {
	got := FavouritesFromPreferences(
		nil,
		[]string{"VIE", "BCN"},
		map[string]int{"VIE": 5, "BCN": 1},
		3,
	)
	if len(got) != 1 || got[0] != "VIE" {
		t.Errorf("got %v, want only [VIE] (BCN affinity below threshold)", got)
	}
}

func TestDefaultWeights_MatchAC(t *testing.T) {
	w := DefaultWeights()
	if w.ProfileMatch != 0.4 || w.RequestMatch != 0.2 || w.DealQuality != 0.4 {
		t.Errorf("DefaultWeights=%+v, want ProfileMatch=0.4 RequestMatch=0.2 DealQuality=0.4", w)
	}
	if w.ProfileMatch+w.RequestMatch+w.DealQuality != 1.0 {
		t.Errorf("weights should sum to 1.0; got %.2f", w.ProfileMatch+w.RequestMatch+w.DealQuality)
	}
}
