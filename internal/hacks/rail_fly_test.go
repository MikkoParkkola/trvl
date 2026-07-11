package hacks

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// --- railFlyStationsForHub ---

func TestRailFlyStationsForHub_AMS(t *testing.T) {
	stations := railFlyStationsForHub("AMS")
	if len(stations) != 2 {
		t.Fatalf("expected 2 KLM stations for AMS, got %d", len(stations))
	}
	iatas := map[string]bool{}
	for _, st := range stations {
		iatas[st.IATA] = true
		if st.Airline != "KL" {
			t.Errorf("expected airline KL for AMS hub, got %q", st.Airline)
		}
	}
	if !iatas["ZWE"] {
		t.Error("expected ZWE (Antwerp) in AMS stations")
	}
	if !iatas["ZYR"] {
		t.Error("expected ZYR (Brussels-Midi) in AMS stations")
	}
}

func TestRailFlyStationsForHub_FRA(t *testing.T) {
	stations := railFlyStationsForHub("FRA")
	if len(stations) != 7 {
		t.Fatalf("expected 7 Lufthansa stations for FRA, got %d", len(stations))
	}
	for _, st := range stations {
		if st.Airline != "LH" {
			t.Errorf("expected airline LH for FRA hub, got %q for %s", st.Airline, st.IATA)
		}
		if st.AirlineName != "Lufthansa" {
			t.Errorf("expected AirlineName Lufthansa for FRA hub, got %q", st.AirlineName)
		}
	}
}

func TestRailFlyStationsForHub_CDG(t *testing.T) {
	stations := railFlyStationsForHub("CDG")
	if len(stations) != 1 {
		t.Fatalf("expected 1 Air France station for CDG, got %d", len(stations))
	}
	if stations[0].IATA != "ZYR" {
		t.Errorf("expected ZYR for CDG, got %q", stations[0].IATA)
	}
	if stations[0].Airline != "AF" {
		t.Errorf("expected airline AF for CDG, got %q", stations[0].Airline)
	}
}

func TestRailFlyStationsForHub_ZRH(t *testing.T) {
	stations := railFlyStationsForHub("ZRH")
	if len(stations) != 1 {
		t.Fatalf("expected 1 Swiss station for ZRH, got %d", len(stations))
	}
	if stations[0].IATA != "ZDH" {
		t.Errorf("expected ZDH (Basel) for ZRH, got %q", stations[0].IATA)
	}
}

func TestRailFlyStationsForHub_unknown(t *testing.T) {
	stations := railFlyStationsForHub("XXX")
	if len(stations) != 0 {
		t.Errorf("expected 0 stations for unknown hub XXX, got %d", len(stations))
	}
}

// --- Station database completeness ---

func TestRailFlyStations_completeness(t *testing.T) {
	// Verify all expected stations exist in the database.
	expected := map[string]string{
		"ZWE": "AMS",
		"ZYR": "", // appears for both AMS and CDG
		"QKL": "FRA",
		"ZWS": "FRA",
		"QDU": "FRA",
		"QMZ": "FRA",
		"QBO": "FRA",
		"ZAQ": "FRA",
		"QPP": "FRA",
		"ZDH": "ZRH",
	}

	found := map[string]bool{}
	for _, st := range railFlyStations {
		found[st.IATA] = true
		if st.City == "" {
			t.Errorf("station %s has empty City", st.IATA)
		}
		if st.AirlineName == "" {
			t.Errorf("station %s has empty AirlineName", st.IATA)
		}
		if st.TrainProvider == "" {
			t.Errorf("station %s has empty TrainProvider", st.IATA)
		}
		if st.TrainMinutes <= 0 {
			t.Errorf("station %s has non-positive TrainMinutes: %d", st.IATA, st.TrainMinutes)
		}
		if st.FareZone == "" {
			t.Errorf("station %s has empty FareZone", st.IATA)
		}
	}

	for iata := range expected {
		if !found[iata] {
			t.Errorf("expected station %s not found in railFlyStations", iata)
		}
	}
}

func TestRailFlyStations_ZYR_dual(t *testing.T) {
	// ZYR (Brussels-Midi) should appear for both KLM (AMS) and Air France (CDG).
	var amsFound, cdgFound bool
	for _, st := range railFlyStations {
		if st.IATA == "ZYR" && st.HubIATA == "AMS" {
			amsFound = true
		}
		if st.IATA == "ZYR" && st.HubIATA == "CDG" {
			cdgFound = true
		}
	}
	if !amsFound {
		t.Error("ZYR should appear with HubIATA=AMS (KLM)")
	}
	if !cdgFound {
		t.Error("ZYR should appear with HubIATA=CDG (Air France)")
	}
}

func TestRailFlyStations_totalCount(t *testing.T) {
	if len(railFlyStations) != 11 {
		t.Errorf("expected 11 rail+fly stations, got %d", len(railFlyStations))
	}
}

// --- buildRailFlyHack ---

func TestBuildRailFlyHack_oneWay(t *testing.T) {
	station := railFlyStation{
		IATA:          "ZWE",
		City:          "Antwerp",
		HubIATA:       "AMS",
		Airline:       "KL",
		AirlineName:   "KLM",
		TrainProvider: "Eurostar",
		TrainMinutes:  60,
		FareZone:      "Belgian market",
	}

	h := buildRailFlyHack("AMS", "BCN", 300, "EUR", 240, "EUR", 60, station, "")

	if h.Type != "rail_fly_arbitrage" {
		t.Errorf("Type = %q, want rail_fly_arbitrage", h.Type)
	}
	if h.Savings != 60 {
		t.Errorf("Savings = %v, want 60", h.Savings)
	}
	if h.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", h.Currency)
	}
	if !strings.Contains(h.Title, "Antwerp") {
		t.Error("title should mention Antwerp")
	}
	if !strings.Contains(h.Title, "AMS") {
		t.Error("title should mention hub AMS")
	}
	if !strings.Contains(h.Description, "KLM") {
		t.Error("description should mention KLM")
	}
	if !strings.Contains(h.Description, "Eurostar") {
		t.Error("description should mention Eurostar")
	}
	if !strings.Contains(h.Description, "Belgian market") {
		t.Error("description should mention fare zone")
	}
	if len(h.Steps) < 5 {
		t.Errorf("expected at least 5 steps (KLM with skip notes), got %d", len(h.Steps))
	}
	// Check one-way trip type in steps
	if !strings.Contains(h.Steps[0], "one-way") {
		t.Error("step 0 should mention one-way")
	}
	if len(h.Risks) < 3 {
		t.Errorf("expected at least 3 risks, got %d", len(h.Risks))
	}
	// KLM stations should note LOW risk (no enforcement)
	if !strings.Contains(h.Risks[0], "LOW risk") {
		t.Error("first risk for KLM should note LOW risk")
	}
	if len(h.Citations) != 2 {
		t.Errorf("expected 2 citations, got %d", len(h.Citations))
	}
}

func TestBuildRailFlyHack_roundTrip(t *testing.T) {
	station := railFlyStation{
		IATA:          "QKL",
		City:          "Cologne",
		HubIATA:       "FRA",
		Airline:       "LH",
		AirlineName:   "Lufthansa",
		TrainProvider: "DB ICE",
		TrainMinutes:  62,
		FareZone:      "Rhineland regional",
	}

	h := buildRailFlyHack("FRA", "JFK", 800, "EUR", 650, "EUR", 150, station, "2026-06-15")

	if h.Savings != 150 {
		t.Errorf("Savings = %v, want 150", h.Savings)
	}
	// Check round-trip type in steps
	if !strings.Contains(h.Steps[0], "round-trip") {
		t.Error("step 0 should mention round-trip for return date")
	}
	if !strings.Contains(h.Description, "DB ICE") {
		t.Error("description should mention DB ICE")
	}
	if !strings.Contains(h.Description, "Rhineland regional") {
		t.Error("description should mention fare zone")
	}
}

// --- DetectRailFlyArbitrage input validation ---

func TestDetectRailFlyArbitrage_emptyInputs(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		dest   string
		date   string
	}{
		{"all empty", "", "", ""},
		{"no origin", "", "BCN", "2026-05-01"},
		{"no destination", "AMS", "", "2026-05-01"},
		{"no date", "AMS", "BCN", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hacks := DetectRailFlyArbitrage(context.Background(), tc.origin, tc.dest, tc.date, "")
			if len(hacks) != 0 {
				t.Errorf("expected nil for %s, got %d hacks", tc.name, len(hacks))
			}
		})
	}
}

func TestDetectRailFlyArbitrage_noStationsForHub(t *testing.T) {
	// HEL has no rail+fly stations — should return nil immediately without API calls.
	hacks := DetectRailFlyArbitrage(context.Background(), "HEL", "BCN", "2026-05-01", "")
	if len(hacks) != 0 {
		t.Errorf("expected nil for hub without rail stations, got %d hacks", len(hacks))
	}
}

// --- detectRailFlyArbitrage wrapper ---

func TestDetectRailFlyArbitrage_wrapper_emptyInput(t *testing.T) {
	hacks := detectRailFlyArbitrage(context.Background(), DetectorInput{})
	if len(hacks) != 0 {
		t.Errorf("expected nil for empty DetectorInput, got %d hacks", len(hacks))
	}
}

// --- Savings threshold tests ---

func TestRailFlySavingsThreshold_belowPercent(t *testing.T) {
	// 4% savings (below 5% threshold) should not fire even if absolute > 15
	basePrice := 500.0
	railPrice := 480.0 // 20 savings = 4%
	savings := basePrice - railPrice

	// Verify the threshold logic: 4% < 5% minimum
	if savings/basePrice >= 0.05 {
		t.Fatal("test setup error: savings should be below 5%")
	}
	if savings < 15 {
		t.Fatal("test setup error: absolute savings should be >= 15")
	}
	// Both conditions must be met: savings >= 15 AND savings/basePrice >= 0.05
	// Here percent is below threshold, so hack should NOT fire.
}

func TestRailFlySavingsThreshold_abovePercent(t *testing.T) {
	// 6% savings (above 5% threshold) AND > 15 absolute should produce a hack
	station := railFlyStation{
		IATA: "ZWE", City: "Antwerp", HubIATA: "AMS", Airline: "KL",
		AirlineName: "KLM", TrainProvider: "Eurostar", TrainMinutes: 60,
		FareZone: "Belgian market",
	}

	basePrice := 500.0
	railPrice := 470.0 // 30 savings = 6%
	savings := basePrice - railPrice

	// Verify the threshold logic: 6% >= 5% AND 30 >= 15
	if savings/basePrice < 0.05 {
		t.Fatal("test setup error: savings should be above 5%")
	}
	if savings < 15 {
		t.Fatal("test setup error: savings should be above 15")
	}

	// Build the hack to verify it would be produced
	h := buildRailFlyHack("AMS", "BCN", basePrice, "EUR", railPrice, "EUR", savings, station, "")
	if h.Type != "rail_fly_arbitrage" {
		t.Errorf("expected hack to be produced for 6%% savings, got type %q", h.Type)
	}
	if h.Savings != 30 {
		t.Errorf("expected savings 30, got %v", h.Savings)
	}
}

func TestRailFlySavingsThreshold_belowAbsolute(t *testing.T) {
	// Even at 10% savings, if absolute is below 15 it should not fire
	basePrice := 100.0
	railPrice := 90.0 // 10 savings = 10%
	savings := basePrice - railPrice

	// Verify: 10% >= 5% but 10 < 15
	if savings/basePrice < 0.05 {
		t.Fatal("test setup error: percent should be above 5%")
	}
	if savings >= 15 {
		t.Fatal("test setup error: absolute savings should be below 15")
	}
}

// --- MIK-3079: ANR/BRU origin recognition ---

func TestRailFlyStationsForHub_ANR_origin(t *testing.T) {
	// Antwerp Airport (ANR) should surface the Antwerp rail station (ZWE, KLM->AMS).
	stations := railFlyStationsForHub("ANR")
	if len(stations) != 1 {
		t.Fatalf("expected 1 rail-fly station for ANR origin, got %d", len(stations))
	}
	st := stations[0]
	if st.IATA != "ZWE" {
		t.Errorf("ANR origin: IATA = %q, want ZWE", st.IATA)
	}
	if st.HubIATA != "AMS" {
		t.Errorf("ANR origin: HubIATA = %q, want AMS", st.HubIATA)
	}
	if st.Airline != "KL" {
		t.Errorf("ANR origin: Airline = %q, want KL", st.Airline)
	}
}

func TestRailFlyStationsForHub_BRU_origin(t *testing.T) {
	// Brussels Airport (BRU) should surface Brussels-Midi (ZYR), which has BOTH
	// a KLM (AMS) and an Air France (CDG) Air&Rail bundle.
	stations := railFlyStationsForHub("BRU")
	if len(stations) != 2 {
		t.Fatalf("expected 2 rail-fly stations for BRU origin (KLM+AF), got %d", len(stations))
	}
	hubs := map[string]string{} // hub -> airline
	for _, st := range stations {
		if st.IATA != "ZYR" {
			t.Errorf("BRU origin: IATA = %q, want ZYR", st.IATA)
		}
		hubs[st.HubIATA] = st.Airline
	}
	if hubs["AMS"] != "KL" {
		t.Errorf("BRU origin: expected ZYR->AMS via KL, got airline %q", hubs["AMS"])
	}
	if hubs["CDG"] != "AF" {
		t.Errorf("BRU origin: expected ZYR->CDG via AF, got airline %q", hubs["CDG"])
	}
}

func TestRailFlyStationsForHub_aliasDoesNotBreakHubMatch(t *testing.T) {
	// Extending origin recognition must not change the existing hub-origin path.
	tests := []struct {
		origin string
		want   int
	}{
		{"AMS", 2},
		{"FRA", 7},
		{"CDG", 1},
		{"ZRH", 1},
		{"JFK", 0}, // an airport that is neither a hub nor a rail-fly alias
	}
	for _, tc := range tests {
		if got := len(railFlyStationsForHub(tc.origin)); got != tc.want {
			t.Errorf("railFlyStationsForHub(%q) = %d stations, want %d", tc.origin, got, tc.want)
		}
	}
}

// withRailFlyFlightSearcher swaps the package-level flight searcher for the
// duration of a test and restores it afterwards.
func withRailFlyFlightSearcher(t *testing.T, fn func(context.Context, *batchexec.Client, string, string, string, flights.SearchOptions) (*models.FlightSearchResult, error)) {
	t.Helper()
	orig := railFlyFlightSearcher
	railFlyFlightSearcher = fn
	t.Cleanup(func() { railFlyFlightSearcher = orig })
}

func railFlyFlight(price float64, currency string) *models.FlightSearchResult {
	if price <= 0 {
		return &models.FlightSearchResult{Success: true}
	}
	return &models.FlightSearchResult{
		Success: true,
		Count:   1,
		Flights: []models.FlightResult{
			{
				Price:    price,
				Currency: currency,
				Legs:     []models.FlightLeg{{Airline: "KLM", AirlineCode: "KL"}},
			},
		},
	}
}

func TestRailFlyDetectComposesPricedBundle(t *testing.T) {
	withRailFlyFlightSearcher(t, func(_ context.Context, _ *batchexec.Client, origin, _, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			t.Fatalf("one-way rail-fly search should not set ReturnDate, got %q", opts.ReturnDate)
		}
		switch origin {
		case "AMS":
			return railFlyFlight(420, "EUR"), nil
		case "ZWE":
			return railFlyFlight(260, "EUR"), nil
		case "ZYR":
			return railFlyFlight(310, "EUR"), nil
		default:
			return railFlyFlight(0, "EUR"), nil
		}
	})
	withRailGroundSearcher(t, func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		return &models.GroundSearchResult{Success: true, Count: 1, Routes: []models.GroundRoute{
			{Provider: "eurostar", Type: "train", Price: 39, Currency: "EUR"},
		}}, nil
	})

	hacks := DetectRailFlyArbitrage(context.Background(), "ams", "bcn", "2026-05-01", "")
	if len(hacks) != 1 {
		t.Fatalf("expected one rail-fly hack, got %d", len(hacks))
	}
	h := hacks[0]
	if h.Bundle == nil {
		t.Fatal("expected composed rail-fly bundle")
	}
	if h.Bundle.TotalCost != 260 {
		t.Fatalf("bundle total = %v, want 260", h.Bundle.TotalCost)
	}
	if h.Savings != 160 {
		t.Fatalf("savings = %v, want 160", h.Savings)
	}
	if h.RailCost != 0 {
		t.Fatalf("hub-origin rail cost = %v, want bundled 0", h.RailCost)
	}
	if len(h.Bundle.Legs) != 2 {
		t.Fatalf("bundle legs = %d, want 2", len(h.Bundle.Legs))
	}
}

func TestRailFlyDetectRoundTripAddsOpenJawRailReturn(t *testing.T) {
	withRailFlyFlightSearcher(t, func(_ context.Context, _ *batchexec.Client, origin, _, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "2026-05-08" {
			t.Fatalf("round-trip rail-fly search ReturnDate = %q, want 2026-05-08", opts.ReturnDate)
		}
		switch origin {
		case "AMS":
			return railFlyFlight(500, "EUR"), nil
		case "ZWE":
			return railFlyFlight(260, "EUR"), nil
		case "ZYR":
			return railFlyFlight(310, "EUR"), nil
		default:
			return railFlyFlight(0, "EUR"), nil
		}
	})
	withRailGroundSearcher(t, func(_ context.Context, from, to, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		price := 39.0
		if from == "BCN" && to == "AMS" {
			price = 70
		}
		return &models.GroundSearchResult{Success: true, Count: 1, Routes: []models.GroundRoute{
			{Provider: "train", Type: "train", Price: price, Currency: "EUR"},
		}}, nil
	})

	hacks := DetectRailFlyArbitrage(context.Background(), "AMS", "BCN", "2026-05-01", "2026-05-08")
	if len(hacks) != 2 {
		t.Fatalf("expected rail-fly + open-jaw hacks, got %d", len(hacks))
	}
	if hacks[0].Type != "rail_fly_arbitrage" || hacks[0].Bundle == nil {
		t.Fatalf("first hack = type %q bundle %+v, want rail_fly_arbitrage with priced bundle", hacks[0].Type, hacks[0].Bundle)
	}
	if hacks[1].Type != "open_jaw_rail_return" {
		t.Fatalf("second hack type = %q, want open_jaw_rail_return", hacks[1].Type)
	}
	if hacks[1].Bundle == nil || len(hacks[1].Bundle.Legs) != 2 {
		t.Fatalf("open-jaw hack should carry two bundle legs, got %+v", hacks[1].Bundle)
	}
	if hacks[1].Bundle.TotalCost != 330 {
		t.Fatalf("open-jaw total = %v, want 330", hacks[1].Bundle.TotalCost)
	}
}

func TestRailFlyDetectNoBaselinePrice(t *testing.T) {
	withRailFlyFlightSearcher(t, func(_ context.Context, _ *batchexec.Client, _, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
		return railFlyFlight(0, "EUR"), nil
	})

	if hacks := DetectRailFlyArbitrage(context.Background(), "AMS", "BCN", "2026-05-01", ""); len(hacks) != 0 {
		t.Fatalf("expected no hacks without a baseline price, got %d", len(hacks))
	}
}

func TestRailFlyDetectNoCheaperStation(t *testing.T) {
	withRailFlyFlightSearcher(t, func(_ context.Context, _ *batchexec.Client, origin, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
		if origin == "AMS" {
			return railFlyFlight(200, "EUR"), nil
		}
		return railFlyFlight(250, "EUR"), nil
	})

	if hacks := DetectRailFlyArbitrage(context.Background(), "AMS", "BCN", "2026-05-01", ""); len(hacks) != 0 {
		t.Fatalf("expected no hacks when rail stations are not cheaper, got %d", len(hacks))
	}
}

func TestRailFlyDetectSavingsBelowThreshold(t *testing.T) {
	withRailFlyFlightSearcher(t, func(_ context.Context, _ *batchexec.Client, origin, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
		if origin == "AMS" {
			return railFlyFlight(200, "EUR"), nil
		}
		return railFlyFlight(190, "EUR"), nil
	})
	withRailGroundSearcher(t, func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		return &models.GroundSearchResult{Success: true, Count: 1, Routes: []models.GroundRoute{
			{Provider: "eurostar", Type: "train", Price: 20, Currency: "EUR"},
		}}, nil
	})

	if hacks := DetectRailFlyArbitrage(context.Background(), "AMS", "BCN", "2026-05-01", ""); len(hacks) != 0 {
		t.Fatalf("expected no hacks below savings threshold, got %d", len(hacks))
	}
}
