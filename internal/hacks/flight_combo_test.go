package hacks

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// --- DetectFlightCombo input validation ---

func TestDetectFlightCombo_emptyInput(t *testing.T) {
	hacks := DetectFlightCombo(context.Background(), FlightComboInput{})
	if len(hacks) != 0 {
		t.Errorf("expected nil for empty input, got %d hacks", len(hacks))
	}
}

func TestDetectFlightCombo_missingOrigin(t *testing.T) {
	hacks := DetectFlightCombo(context.Background(), FlightComboInput{
		Destination: "BCN",
		DepartDate:  "2026-05-01",
		ReturnDate:  "2026-05-08",
	})
	if len(hacks) != 0 {
		t.Errorf("expected nil for missing origin, got %d hacks", len(hacks))
	}
}

func TestDetectFlightCombo_missingDestination(t *testing.T) {
	hacks := DetectFlightCombo(context.Background(), FlightComboInput{
		Origin:     "HEL",
		DepartDate: "2026-05-01",
		ReturnDate: "2026-05-08",
	})
	if len(hacks) != 0 {
		t.Errorf("expected nil for missing destination, got %d hacks", len(hacks))
	}
}

func TestDetectFlightCombo_missingDates(t *testing.T) {
	hacks := DetectFlightCombo(context.Background(), FlightComboInput{
		Origin:      "HEL",
		Destination: "BCN",
	})
	if len(hacks) != 0 {
		t.Errorf("expected nil for missing dates and no trips, got %d hacks", len(hacks))
	}
}

func TestDetectFlightCombo_missingReturnDate(t *testing.T) {
	hacks := DetectFlightCombo(context.Background(), FlightComboInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-05-01",
	})
	if len(hacks) != 0 {
		t.Errorf("expected nil for missing return date, got %d hacks", len(hacks))
	}
}

func TestDetectFlightCombo_tripsOverrideDates(t *testing.T) {
	// When Trips is provided, DepartDate/ReturnDate should be ignored.
	// With empty Trips slice AND missing dates, should return nil.
	hacks := DetectFlightCombo(context.Background(), FlightComboInput{
		Origin:      "HEL",
		Destination: "BCN",
		Trips:       []TripLeg{},
	})
	if len(hacks) != 0 {
		t.Errorf("expected nil for empty trips slice, got %d hacks", len(hacks))
	}
}

func TestDetectFlightCombo_defaultCurrency(t *testing.T) {
	// Just verify it doesn't panic with no currency set.
	// Actual API calls will fail but we're testing input handling.
	_ = DetectFlightCombo(context.Background(), FlightComboInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-05-01",
		ReturnDate:  "2026-05-08",
	})
}

// --- cheapestFlightInfo helper ---

func TestCheapestFlightInfo_nil(t *testing.T) {
	price, cur, airline, _ := cheapestFlightInfo(nil, nil)
	if price != 0 || cur != "" || airline != "" {
		t.Errorf("expected zeros for nil result, got price=%v cur=%q airline=%q", price, cur, airline)
	}
}

func TestCheapestFlightInfo_error(t *testing.T) {
	price, _, _, _ := cheapestFlightInfo(nil, context.DeadlineExceeded)
	if price != 0 {
		t.Errorf("expected 0 price on error, got %v", price)
	}
}

func TestCheapestFlightInfo_unsuccessful(t *testing.T) {
	price, _, _, _ := cheapestFlightInfo(&models.FlightSearchResult{Success: false}, nil)
	if price != 0 {
		t.Errorf("expected 0 price for unsuccessful result, got %v", price)
	}
}

func TestCheapestFlightInfo_emptyFlights(t *testing.T) {
	price, _, _, _ := cheapestFlightInfo(&models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{},
	}, nil)
	if price != 0 {
		t.Errorf("expected 0 price for empty flights, got %v", price)
	}
}

func TestCheapestFlightInfo_singleFlight(t *testing.T) {
	price, cur, airline, _ := cheapestFlightInfo(&models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{
				Price:    199.50,
				Currency: "EUR",
				Legs:     []models.FlightLeg{{Airline: "Finnair", AirlineCode: "AY"}},
			},
		},
	}, nil)
	if price != 199.50 {
		t.Errorf("expected 199.50, got %v", price)
	}
	if cur != "EUR" {
		t.Errorf("expected EUR, got %q", cur)
	}
	if airline != "Finnair" {
		t.Errorf("expected Finnair, got %q", airline)
	}
}

func TestCheapestFlightInfo_picksCheapest(t *testing.T) {
	price, _, airline, _ := cheapestFlightInfo(&models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Price: 300, Currency: "EUR", Legs: []models.FlightLeg{{Airline: "Lufthansa"}}},
			{Price: 150, Currency: "EUR", Legs: []models.FlightLeg{{Airline: "Ryanair"}}},
			{Price: 250, Currency: "EUR", Legs: []models.FlightLeg{{Airline: "Finnair"}}},
		},
	}, nil)
	if price != 150 {
		t.Errorf("expected 150, got %v", price)
	}
	if airline != "Ryanair" {
		t.Errorf("expected Ryanair, got %q", airline)
	}
}

func TestCheapestFlightInfo_skipsZeroPrice(t *testing.T) {
	price, _, _, _ := cheapestFlightInfo(&models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Price: 0, Currency: "EUR"},
			{Price: 200, Currency: "EUR", Legs: []models.FlightLeg{{Airline: "SAS"}}},
		},
	}, nil)
	if price != 200 {
		t.Errorf("expected 200, got %v", price)
	}
}

func TestCheapestFlightInfo_allZero(t *testing.T) {
	price, _, _, _ := cheapestFlightInfo(&models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Price: 0},
			{Price: 0},
		},
	}, nil)
	if price != 0 {
		t.Errorf("expected 0 for all-zero prices, got %v", price)
	}
}

func TestCheapestFlightInfo_noLegs(t *testing.T) {
	_, _, airline, _ := cheapestFlightInfo(&models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Price: 100, Currency: "EUR", Legs: nil},
		},
	}, nil)
	if airline != "" {
		t.Errorf("expected empty airline for no legs, got %q", airline)
	}
}

func TestCheapestFlightInfo_fallbackToAirlineCode(t *testing.T) {
	_, _, airline, _ := cheapestFlightInfo(&models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Price: 100, Currency: "EUR", Legs: []models.FlightLeg{{Airline: "", AirlineCode: "AY"}}},
		},
	}, nil)
	if airline != "AY" {
		t.Errorf("expected AY as fallback, got %q", airline)
	}
}

// --- permutations ---

func TestPermutations_zero(t *testing.T) {
	result := permutations(0)
	if result != nil {
		t.Errorf("expected nil for n=0, got %v", result)
	}
}

func TestPermutations_one(t *testing.T) {
	result := permutations(1)
	if len(result) != 1 {
		t.Fatalf("expected 1 permutation for n=1, got %d", len(result))
	}
	if !reflect.DeepEqual(result[0], []int{0}) {
		t.Errorf("expected [0], got %v", result[0])
	}
}

func TestPermutations_two(t *testing.T) {
	result := permutations(2)
	if len(result) != 2 {
		t.Fatalf("expected 2 permutations for n=2, got %d", len(result))
	}
	// Should contain [0,1] and [1,0].
	found := map[string]bool{}
	for _, p := range result {
		key := ""
		for _, v := range p {
			key += string(rune('0' + v))
		}
		found[key] = true
	}
	if !found["01"] || !found["10"] {
		t.Errorf("missing expected permutations in %v", result)
	}
}

func TestPermutations_three(t *testing.T) {
	result := permutations(3)
	if len(result) != 6 {
		t.Fatalf("expected 6 permutations for n=3, got %d", len(result))
	}

	// Verify all are unique.
	seen := map[string]bool{}
	for _, p := range result {
		key := ""
		for _, v := range p {
			key += string(rune('0' + v))
		}
		if seen[key] {
			t.Errorf("duplicate permutation: %v", p)
		}
		seen[key] = true
	}
}

func TestPermutations_four(t *testing.T) {
	result := permutations(4)
	if len(result) != 24 {
		t.Fatalf("expected 24 permutations for n=4, got %d", len(result))
	}
}

func TestPermutations_five_capped(t *testing.T) {
	result := permutations(5)
	if result != nil {
		t.Errorf("expected nil for n=5 (exceeds cap), got %d permutations", len(result))
	}
}

// --- isIdentityPerm ---

func TestIsIdentityPerm_identity(t *testing.T) {
	if !isIdentityPerm([]int{0, 1, 2, 3}) {
		t.Error("expected [0,1,2,3] to be identity")
	}
}

func TestIsIdentityPerm_single(t *testing.T) {
	if !isIdentityPerm([]int{0}) {
		t.Error("expected [0] to be identity")
	}
}

func TestIsIdentityPerm_swapped(t *testing.T) {
	if isIdentityPerm([]int{1, 0}) {
		t.Error("expected [1,0] to NOT be identity")
	}
}

func TestIsIdentityPerm_empty(t *testing.T) {
	if !isIdentityPerm([]int{}) {
		t.Error("expected empty slice to be identity")
	}
}

// --- sortTrips ---

func TestSortTrips(t *testing.T) {
	trips := []TripLeg{
		{DepartDate: "2026-06-01", ReturnDate: "2026-06-08"},
		{DepartDate: "2026-05-01", ReturnDate: "2026-05-08"},
		{DepartDate: "2026-07-01", ReturnDate: "2026-07-08"},
	}
	sortTrips(trips)
	if trips[0].DepartDate != "2026-05-01" {
		t.Errorf("expected first trip to be May, got %s", trips[0].DepartDate)
	}
	if trips[1].DepartDate != "2026-06-01" {
		t.Errorf("expected second trip to be June, got %s", trips[1].DepartDate)
	}
	if trips[2].DepartDate != "2026-07-01" {
		t.Errorf("expected third trip to be July, got %s", trips[2].DepartDate)
	}
}

func TestSortTrips_alreadySorted(t *testing.T) {
	trips := []TripLeg{
		{DepartDate: "2026-01-01", ReturnDate: "2026-01-08"},
		{DepartDate: "2026-02-01", ReturnDate: "2026-02-08"},
	}
	sortTrips(trips)
	if trips[0].DepartDate != "2026-01-01" || trips[1].DepartDate != "2026-02-01" {
		t.Error("already-sorted trips should remain unchanged")
	}
}

func TestSortTrips_empty(t *testing.T) {
	trips := []TripLeg{}
	sortTrips(trips) // should not panic
}

// --- buildSplitHack output ---

func TestBuildSplitHack_fields(t *testing.T) {
	hack := buildSplitHack("HEL", "BCN",
		TripLeg{DepartDate: "2026-05-01", ReturnDate: "2026-05-08"},
		"EUR", 400, 150, 180, "Ryanair", "Finnair",
	)

	if hack.Type != "flight_combo" {
		t.Errorf("expected type flight_combo, got %q", hack.Type)
	}
	if hack.Currency != "EUR" {
		t.Errorf("expected EUR, got %q", hack.Currency)
	}
	if hack.Savings != 70 { // 400 - (150+180) = 70
		t.Errorf("expected savings 70, got %v", hack.Savings)
	}
	if len(hack.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(hack.Steps))
	}
	if len(hack.Risks) == 0 {
		t.Error("expected non-empty risks")
	}
	if len(hack.Citations) != 2 {
		t.Errorf("expected 2 citations, got %d", len(hack.Citations))
	}
}

// --- buildNestedHack output ---

func TestBuildNestedHack_fields(t *testing.T) {
	trips := []TripLeg{
		{DepartDate: "2026-05-01", ReturnDate: "2026-05-08"},
		{DepartDate: "2026-06-01", ReturnDate: "2026-06-08"},
	}
	perm := []int{1, 0} // swapped returns

	hack := buildNestedHack("HEL", "BCN", trips, perm, "EUR", 800, 600, 200)

	if hack.Type != "flight_combo" {
		t.Errorf("expected type flight_combo, got %q", hack.Type)
	}
	if hack.Savings != 200 {
		t.Errorf("expected savings 200, got %v", hack.Savings)
	}
	// Steps: 1 header + 2 ticket descriptions
	if len(hack.Steps) != 3 {
		t.Errorf("expected 3 steps, got %d", len(hack.Steps))
	}
	if len(hack.Citations) != 2 {
		t.Errorf("expected 2 citations, got %d", len(hack.Citations))
	}
	if len(hack.Risks) == 0 {
		t.Error("expected non-empty risks")
	}
}

// --- FlightComboResult / TicketOption struct ---

func TestFlightComboResult_struct(t *testing.T) {
	r := FlightComboResult{
		Strategy:     "nested_returns",
		TotalCost:    600,
		BaselineCost: 800,
		Savings:      200,
		Currency:     "EUR",
		Tickets: []TicketOption{
			{Type: "round_trip", Outbound: "HEL->BCN May 1", Return: "BCN->HEL Jun 8", Price: 300, Currency: "EUR"},
			{Type: "round_trip", Outbound: "HEL->BCN Jun 1", Return: "BCN->HEL May 8", Price: 300, Currency: "EUR"},
		},
	}
	if r.Strategy != "nested_returns" {
		t.Errorf("unexpected Strategy: %q", r.Strategy)
	}
	if r.Savings != 200 {
		t.Errorf("unexpected Savings: %v", r.Savings)
	}
	if len(r.Tickets) != 2 {
		t.Errorf("unexpected ticket count: %d", len(r.Tickets))
	}
}

// --- TripLeg struct ---

func TestTripLeg_struct(t *testing.T) {
	leg := TripLeg{DepartDate: "2026-05-01", ReturnDate: "2026-05-08"}
	if leg.DepartDate != "2026-05-01" {
		t.Errorf("unexpected DepartDate: %q", leg.DepartDate)
	}
	if leg.ReturnDate != "2026-05-08" {
		t.Errorf("unexpected ReturnDate: %q", leg.ReturnDate)
	}
}

// --- permutation contents verification ---

func TestPermutations_containsAllElements(t *testing.T) {
	for n := 1; n <= 4; n++ {
		perms := permutations(n)
		for _, p := range perms {
			if len(p) != n {
				t.Fatalf("permutation length %d != n=%d", len(p), n)
			}
			sorted := make([]int, n)
			copy(sorted, p)
			sort.Ints(sorted)
			for i := 0; i < n; i++ {
				if sorted[i] != i {
					t.Errorf("permutation %v missing element %d (n=%d)", p, i, n)
				}
			}
		}
	}
}

// --- maxComboTrips constant ---

func TestMaxComboTrips(t *testing.T) {
	if maxComboTrips != 4 {
		t.Errorf("expected maxComboTrips=4, got %d", maxComboTrips)
	}
}

// --- comboMinSavingsRatio constant ---

func TestComboMinSavingsRatio(t *testing.T) {
	if comboMinSavingsRatio != 0.05 {
		t.Errorf("expected comboMinSavingsRatio=0.05, got %v", comboMinSavingsRatio)
	}
}
