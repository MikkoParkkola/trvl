package nlsearch

import (
	"slices"
	"testing"
)

// TestBuildPlan_Table exercises a dozen real phrasings asserting correct mode,
// origin/destination, date(s), and constraints. `today` is fixed at a Saturday
// (2026-06-20) so relative-date resolution is deterministic.
func TestBuildPlan_Table(t *testing.T) {
	const today = "2026-06-20"

	cases := []struct {
		name            string
		query           string
		wantMode        string
		wantOrigin      string
		wantDest        string
		wantDate        string // "" = don't assert exact value
		wantDateNonZero bool   // assert a date was resolved (when exact value is relative)
		wantReturn      bool   // assert ReturnDate non-empty
		wantConstraints []string
		wantBudget      float64
	}{
		{
			name:            "tromso cheapest multimodal avoiding redeye",
			query:           "cheapest way to Tromso next month avoiding red-eyes",
			wantMode:        ModeMultimodal,
			wantOrigin:      "",
			wantDest:        "TOS",
			wantDateNonZero: true,
			wantConstraints: []string{ConstraintCheapest, ConstraintNoRedeye},
		},
		{
			name:            "round trip HEL NRT next week",
			query:           "HEL to NRT and back next week",
			wantMode:        ModeFlight,
			wantOrigin:      "HEL",
			wantDest:        "NRT",
			wantDateNonZero: true,
			wantReturn:      true,
			wantConstraints: []string{},
		},
		{
			name:            "explicit flight from to with iso date",
			query:           "cheapest flight from Helsinki to Prague on 2026-05-07",
			wantMode:        ModeFlight,
			wantOrigin:      "HEL",
			wantDest:        "PRG",
			wantDate:        "2026-05-07",
			wantConstraints: []string{ConstraintCheapest},
		},
		{
			name:            "nonstop direct flight",
			query:           "nonstop flight from HEL to LHR next week",
			wantMode:        ModeFlight,
			wantOrigin:      "HEL",
			wantDest:        "LHR",
			wantDateNonZero: true,
			wantConstraints: []string{ConstraintNonstop},
		},
		{
			name:            "train ground mode",
			query:           "train from Amsterdam to Berlin next week",
			wantMode:        ModeGround,
			wantOrigin:      "AMS",
			wantDest:        "BER",
			wantDateNonZero: true,
			wantConstraints: []string{},
		},
		{
			name:            "ferry ground mode",
			query:           "ferry from Helsinki to Tallinn next week",
			wantMode:        ModeGround,
			wantOrigin:      "HEL",
			wantDest:        "TLL",
			wantDateNonZero: true,
			wantConstraints: []string{},
		},
		{
			name:            "hotel with budget",
			query:           "hotel in Prague for 3 nights under EUR 120",
			wantMode:        ModeHotel,
			wantDest:        "PRG",
			wantConstraints: []string{"budget<=120"},
			wantBudget:      120,
		},
		{
			name:            "multimodal best way phrasing",
			query:           "best way from Helsinki to Barcelona next month",
			wantMode:        ModeMultimodal,
			wantOrigin:      "HEL",
			wantDest:        "BCN",
			wantDateNonZero: true,
			wantConstraints: []string{},
		},
		{
			name:            "fastest constraint",
			query:           "fastest flight from HEL to CDG next week",
			wantMode:        ModeFlight,
			wantOrigin:      "HEL",
			wantDest:        "CDG",
			wantDateNonZero: true,
			wantConstraints: []string{ConstraintFastest},
		},
		{
			name:            "hacks mode hidden city",
			query:           "find hidden city tricks from HEL to NRT next week",
			wantMode:        ModeHacks,
			wantOrigin:      "HEL",
			wantDest:        "NRT",
			wantDateNonZero: true,
			wantConstraints: []string{},
		},
		{
			name:            "budget with currency only and cheapest",
			query:           "cheapest flight from HEL to BCN under 300 next week",
			wantMode:        ModeFlight,
			wantOrigin:      "HEL",
			wantDest:        "BCN",
			wantDateNonZero: true,
			wantConstraints: []string{ConstraintCheapest, "budget<=300"},
			wantBudget:      300,
		},
		{
			name:            "overnight avoidance maps to no-redeye",
			query:           "flight from HEL to JFK next week, no overnight layovers",
			wantMode:        ModeFlight,
			wantOrigin:      "HEL",
			wantDest:        "JFK",
			wantDateNonZero: true,
			wantConstraints: []string{ConstraintNoRedeye},
		},
		{
			name:            "default endpoints to flight",
			query:           "HEL to BCN on 2026-07-01",
			wantMode:        ModeFlight,
			wantOrigin:      "HEL",
			wantDest:        "BCN",
			wantDate:        "2026-07-01",
			wantConstraints: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := BuildPlan(tc.query, today)
			if p.Mode != tc.wantMode {
				t.Errorf("Mode = %q, want %q", p.Mode, tc.wantMode)
			}
			if p.Origin != tc.wantOrigin {
				t.Errorf("Origin = %q, want %q", p.Origin, tc.wantOrigin)
			}
			if p.Destination != tc.wantDest {
				t.Errorf("Destination = %q, want %q", p.Destination, tc.wantDest)
			}
			if tc.wantDate != "" && p.Date != tc.wantDate {
				t.Errorf("Date = %q, want %q", p.Date, tc.wantDate)
			}
			if tc.wantDateNonZero && p.Date == "" {
				t.Errorf("Date should be resolved, got empty")
			}
			if tc.wantReturn && p.ReturnDate == "" {
				t.Errorf("ReturnDate should be set for round-trip, got empty")
			}
			if tc.wantBudget != 0 && p.MaxBudget != tc.wantBudget {
				t.Errorf("MaxBudget = %v, want %v", p.MaxBudget, tc.wantBudget)
			}
			if !slices.Equal(p.Constraints, tc.wantConstraints) {
				t.Errorf("Constraints = %v, want %v", p.Constraints, tc.wantConstraints)
			}
			if len(p.Searches) == 0 {
				t.Errorf("Searches should never be empty")
			}
		})
	}
}

// TestBuildPlan_TromsoFlagship locks the headline acceptance case end-to-end,
// including the dispatched search shape.
func TestBuildPlan_TromsoFlagship(t *testing.T) {
	p := BuildPlan("cheapest way to Tromso next month avoiding red-eyes", "2026-06-20")
	if p.Mode != ModeMultimodal {
		t.Fatalf("Mode = %q, want multimodal", p.Mode)
	}
	if p.Destination != "TOS" {
		t.Fatalf("Destination = %q, want TOS", p.Destination)
	}
	if p.Date == "" {
		t.Fatal("Date should be resolved from 'next month'")
	}
	if !slices.Contains(p.Constraints, ConstraintCheapest) || !slices.Contains(p.Constraints, ConstraintNoRedeye) {
		t.Fatalf("Constraints = %v, want cheapest + no-redeye", p.Constraints)
	}
	// Multimodal plans dispatch both a route and a flight comparison.
	if len(p.Searches) != 2 {
		t.Fatalf("Searches = %d, want 2 (route + flight)", len(p.Searches))
	}
	tools := []string{p.Searches[0].Tool, p.Searches[1].Tool}
	if !slices.Contains(tools, "search_route") || !slices.Contains(tools, "search_flights") {
		t.Fatalf("Searches tools = %v, want search_route + search_flights", tools)
	}
}

// TestBuildPlan_RoundTripReturnDate verifies that round-trip phrasing without an
// explicit return date still produces a return leg one week out.
func TestBuildPlan_RoundTripReturnDate(t *testing.T) {
	p := BuildPlan("HEL to NRT and back next week", "2026-06-20")
	if !p.RoundTrip {
		t.Fatal("RoundTrip should be true for 'and back'")
	}
	if p.ReturnDate == "" {
		t.Fatal("ReturnDate should be derived for round-trip")
	}
	if p.Date == "" || p.ReturnDate <= p.Date {
		t.Fatalf("ReturnDate (%q) should be after Date (%q)", p.ReturnDate, p.Date)
	}
}

// TestBuildPlan_BudgetParsing covers the budget extraction variants.
func TestBuildPlan_BudgetParsing(t *testing.T) {
	cases := []struct {
		query string
		want  float64
	}{
		{"flight HEL BCN under EUR 500 next week", 500},
		{"flight HEL BCN budget of 1,200 next week", 1200},
		{"flight HEL BCN max 300 next week", 300},
		{"flight HEL BCN below $250 next week", 250},
		{"flight HEL BCN next week", 0},
	}
	for _, tc := range cases {
		p := BuildPlan(tc.query, "2026-06-20")
		if p.MaxBudget != tc.want {
			t.Errorf("query %q: MaxBudget = %v, want %v", tc.query, p.MaxBudget, tc.want)
		}
	}
}

// TestBuildPlan_NeverErrorsOnGarbage ensures a nonsense query yields a usable
// best-effort plan rather than panicking.
func TestBuildPlan_NeverErrorsOnGarbage(t *testing.T) {
	p := BuildPlan("asdf qwerty zzz", "2026-06-20")
	if p.Mode == "" {
		t.Fatal("Mode should always be set")
	}
	if p.Constraints == nil || p.Searches == nil {
		t.Fatal("Constraints and Searches should be non-nil slices")
	}
}
