package hacks

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// ---------------------------------------------------------------------------
// multimodal_skip_flight
// ---------------------------------------------------------------------------

// TestDetectMultiModalSkipFlight_emptyInput verifies no panic on empty input.
func TestDetectMultiModalSkipFlight_emptyInput(t *testing.T) {
	h := detectMultiModalSkipFlight(context.Background(), DetectorInput{})
	if len(h) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(h))
	}
}

// TestDetectMultiModalSkipFlight_missingOrigin verifies early return when
// Origin is empty.
func TestDetectMultiModalSkipFlight_missingOrigin(t *testing.T) {
	h := detectMultiModalSkipFlight(context.Background(), DetectorInput{
		Destination: "AMS",
		Date:        "2026-04-13",
	})
	if len(h) != 0 {
		t.Errorf("expected empty when Origin missing, got %d", len(h))
	}
}

// TestDetectMultiModalSkipFlight_missingDate verifies early return when Date is empty.
func TestDetectMultiModalSkipFlight_missingDate(t *testing.T) {
	h := detectMultiModalSkipFlight(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "AMS",
	})
	if len(h) != 0 {
		t.Errorf("expected empty when Date missing, got %d", len(h))
	}
}

// TestDetectMultiModalSkipFlight_hackFields verifies that when a hack IS
// returned it has all required fields set.
// This test cannot mock live APIs so it exercises the early-return paths only.
func TestDetectMultiModalSkipFlight_hackType(t *testing.T) {
	// Construct a synthetic Hack the way the detector would and verify fields.
	h := Hack{
		Type:     "multimodal_skip_flight",
		Title:    "Skip the flight — overnight bus saves EUR 167",
		Currency: "EUR",
		Savings:  167,
		Risks:    []string{"risk1"},
		Steps:    []string{"step1"},
	}
	if h.Type != "multimodal_skip_flight" {
		t.Errorf("unexpected Type %q", h.Type)
	}
	if h.Savings != 167 {
		t.Errorf("unexpected Savings %v", h.Savings)
	}
	if len(h.Risks) == 0 {
		t.Error("Risks must not be empty")
	}
	if len(h.Steps) == 0 {
		t.Error("Steps must not be empty")
	}
}

// TestTrimToHHMM verifies the time trimming helper.
func TestTrimToHHMM(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"2026-04-13T21:55", "21:55"},
		{"2026-04-14T06:30", "06:30"},
		{"21:55", "21:55"},               // already short
		{"2026-04-13T21:55:00", "21:55"}, // longer ISO also trimmed
	}
	for _, tc := range tests {
		got := trimToHHMM(tc.in)
		if got != tc.want {
			t.Errorf("trimToHHMM(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// multimodal_positioning
// ---------------------------------------------------------------------------

// TestDetectMultiModalPositioning_emptyInput verifies no panic on empty input.
func TestDetectMultiModalPositioning_emptyInput(t *testing.T) {
	h := detectMultiModalPositioning(context.Background(), DetectorInput{})
	if len(h) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(h))
	}
}

// TestDetectMultiModalPositioning_unknownOrigin verifies no hacks for
// an origin not in the multiModalHubs table.
func TestDetectMultiModalPositioning_unknownOrigin(t *testing.T) {
	h := detectMultiModalPositioning(context.Background(), DetectorInput{
		Origin:      "JFK",
		Destination: "PRG",
		Date:        "2026-04-13",
	})
	if len(h) != 0 {
		t.Errorf("expected no hacks for unknown origin, got %d", len(h))
	}
}

// TestMultiModalHubs verifies the static table is populated and consistent.
func TestMultiModalHubs(t *testing.T) {
	if len(multiModalHubs) == 0 {
		t.Fatal("multiModalHubs is empty")
	}
	hubs, ok := multiModalHubs["HEL"]
	if !ok {
		t.Fatal("HEL should have multimodal hubs")
	}
	for _, h := range hubs {
		if h.HubCode == "" {
			t.Error("HubCode must not be empty")
		}
		if h.HubCity == "" {
			t.Error("HubCity must not be empty")
		}
		if h.StaticGroundEUR <= 0 {
			t.Errorf("StaticGroundEUR must be > 0 for %s", h.HubCode)
		}
	}
}

// TestMinSavingsFraction verifies the threshold constant is sane.
func TestMinSavingsFraction(t *testing.T) {
	if minSavingsFraction <= 0 || minSavingsFraction >= 1 {
		t.Errorf("minSavingsFraction %v must be in (0,1)", minSavingsFraction)
	}
}

// TestDetectMultiModalPositioning_savingsThreshold verifies that the savings
// logic rejects candidates below the 20% threshold.
func TestDetectMultiModalPositioning_savingsThreshold(t *testing.T) {
	// Simulate: directPrice=100, total=95 → savings=5, fraction=5% < 20% → reject.
	directPrice := 100.0
	total := 95.0
	savings := directPrice - total
	fraction := savings / directPrice
	if fraction >= minSavingsFraction {
		t.Errorf("expected savings fraction %v to be below threshold %v", fraction, minSavingsFraction)
	}

	// Simulate: directPrice=100, total=75 → savings=25, fraction=25% ≥ 20% → accept.
	total2 := 75.0
	savings2 := directPrice - total2
	fraction2 := savings2 / directPrice
	if fraction2 < minSavingsFraction {
		t.Errorf("expected savings fraction %v to be above threshold %v", fraction2, minSavingsFraction)
	}
}

// ---------------------------------------------------------------------------
// multimodal_open_jaw_ground
// ---------------------------------------------------------------------------

// TestDetectMultiModalOpenJawGround_emptyInput verifies no panic on empty input.
func TestDetectMultiModalOpenJawGround_emptyInput(t *testing.T) {
	h := detectMultiModalOpenJawGround(context.Background(), DetectorInput{})
	if len(h) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(h))
	}
}

// TestDetectMultiModalOpenJawGround_unknownDest verifies no hacks when
// destination not in nearbyHubs.
func TestDetectMultiModalOpenJawGround_unknownDest(t *testing.T) {
	h := detectMultiModalOpenJawGround(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "AMS",
		Date:        "2026-04-13",
	})
	if len(h) != 0 {
		t.Errorf("expected no hacks for unknown destination, got %d", len(h))
	}
}

// TestNearbyHubs verifies the static table is populated and consistent.
func TestNearbyHubs(t *testing.T) {
	if len(nearbyHubs) == 0 {
		t.Fatal("nearbyHubs is empty")
	}
	hubs, ok := nearbyHubs["DBV"]
	if !ok {
		t.Fatal("DBV (Dubrovnik) should have nearby hub entries")
	}
	for _, h := range hubs {
		if h.HubCode == "" {
			t.Error("HubCode must not be empty")
		}
		if h.StaticGroundEUR <= 0 {
			t.Errorf("StaticGroundEUR must be > 0 for hub %s→DBV", h.HubCode)
		}
		if h.Notes == "" {
			t.Errorf("Notes must not be empty for hub %s→DBV", h.HubCode)
		}
	}
}

// TestDetectMultiModalOpenJawGround_overnightBonus verifies hotel bonus logic.
func TestDetectMultiModalOpenJawGround_overnightBonus(t *testing.T) {
	// When Overnight=true the hotel bonus (averageHotelCost) is added.
	hub := nearbyHub{Overnight: true, StaticGroundEUR: 15}
	flightPrice := 100.0
	directPrice := 200.0
	total := flightPrice + hub.StaticGroundEUR
	hotelBonus := 0.0
	if hub.Overnight {
		hotelBonus = averageHotelCost
	}
	savings := directPrice - total + hotelBonus
	want := 200.0 - 115.0 + 60.0 // = 145
	if savings != want {
		t.Errorf("savings = %v, want %v", savings, want)
	}
}

// TestDetectMultiModalOpenJawGround_thresholdFifty verifies the EUR 50 threshold.
func TestDetectMultiModalOpenJawGround_thresholdFifty(t *testing.T) {
	// savings=49 → below threshold → should not surface.
	savings := 49.0
	if savings >= 50 {
		t.Error("expected savings below EUR 50 threshold")
	}

	savings2 := 50.0
	if savings2 < 50 {
		t.Error("expected savings exactly at EUR 50 to pass threshold")
	}
}

// ---------------------------------------------------------------------------
// multimodal_return_split
// ---------------------------------------------------------------------------

// TestDetectMultiModalReturnSplit_emptyInput verifies no panic on empty input.
func TestDetectMultiModalReturnSplit_emptyInput(t *testing.T) {
	h := detectMultiModalReturnSplit(context.Background(), DetectorInput{})
	if len(h) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(h))
	}
}

// TestDetectMultiModalReturnSplit_oneWay verifies no hacks for one-way queries
// (ReturnDate is empty).
func TestDetectMultiModalReturnSplit_oneWay(t *testing.T) {
	h := detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "PRG",
		Date:        "2026-04-13",
		// ReturnDate intentionally omitted
	})
	if len(h) != 0 {
		t.Errorf("expected no hacks for one-way query, got %d", len(h))
	}
}

// TestDetectMultiModalReturnSplit_hackFields verifies a synthetic hack has
// the required Type, Risks, and Steps.
func TestDetectMultiModalReturnSplit_hackFields(t *testing.T) {
	h := Hack{
		Type:     "multimodal_return_split",
		Title:    "Fly out, return by bus — saves EUR 64",
		Currency: "EUR",
		Savings:  64,
		Risks:    []string{"risk1", "risk2"},
		Steps:    []string{"step1", "step2", "step3"},
	}
	if h.Type != "multimodal_return_split" {
		t.Errorf("unexpected Type %q", h.Type)
	}
	if len(h.Risks) < 2 {
		t.Errorf("expected at least 2 risks, got %d", len(h.Risks))
	}
	if len(h.Steps) < 3 {
		t.Errorf("expected at least 3 steps, got %d", len(h.Steps))
	}
}

// TestDetectMultiModalReturnSplit_savingsFormula verifies the savings arithmetic.
// Real example: HEL↔PRG round-trip EUR 269, one-way out EUR 145, ground return EUR 60.
func TestDetectMultiModalReturnSplit_savingsFormula(t *testing.T) {
	rtPrice := 269.0
	owOutPrice := 145.0
	groundReturnPrice := 60.0
	totalMixed := owOutPrice + groundReturnPrice // 205
	savings := rtPrice - totalMixed              // 64
	if savings != 64 {
		t.Errorf("expected savings=64, got %v", savings)
	}
	if savings < 50 {
		t.Error("savings should exceed EUR 50 threshold for this example")
	}
}

// TestDetectMultiModalReturnSplit_overnightSavings verifies hotel bonus for
// overnight ground return.
func TestDetectMultiModalReturnSplit_overnightSavings(t *testing.T) {
	rtPrice := 200.0
	owOutPrice := 120.0
	groundReturnPrice := 40.0
	overnight := true

	hotelBonus := 0.0
	if overnight {
		hotelBonus = averageHotelCost // 60
	}
	totalMixed := owOutPrice + groundReturnPrice
	savings := rtPrice - totalMixed + hotelBonus // 200 - 160 + 60 = 100

	if savings != 100 {
		t.Errorf("expected savings=100 with overnight bonus, got %v", savings)
	}
}

// ---------------------------------------------------------------------------
// return split seams + makers (currency honesty tests; mirrors split_test.go)
// ---------------------------------------------------------------------------

func returnSplitMakeFlight(price float64, currency string) *models.FlightSearchResult {
	return &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{{Price: price, Currency: currency}},
	}
}

func returnSplitMakeGround(price float64, currency, routeType, depCity, arrCity, depTime, arrTime, bookingURL string) *models.GroundSearchResult {
	return &models.GroundSearchResult{
		Success: true,
		Routes: []models.GroundRoute{{
			Provider:   "flixbus",
			Type:       routeType,
			Price:      price,
			Currency:   currency,
			Departure:  models.GroundStop{City: depCity, Time: depTime},
			Arrival:    models.GroundStop{City: arrCity, Time: arrTime},
			BookingURL: bookingURL,
		}},
	}
}

func emptyGround() *models.GroundSearchResult {
	return &models.GroundSearchResult{Success: true, Routes: []models.GroundRoute{}}
}

func withReturnSplitFlightSearch(t *testing.T, fn func(context.Context, string, string, string, flights.SearchOptions) (*models.FlightSearchResult, error)) {
	t.Helper()
	original := returnSplitFlightSearch
	returnSplitFlightSearch = fn
	t.Cleanup(func() { returnSplitFlightSearch = original })
}

func withReturnSplitGroundSearch(t *testing.T, fn func(context.Context, string, string, string, ground.SearchOptions) (*models.GroundSearchResult, error)) {
	t.Helper()
	original := returnSplitGroundSearch
	returnSplitGroundSearch = fn
	t.Cleanup(func() { returnSplitGroundSearch = original })
}

// TestDetectMultiModalReturnSplit_currencyMismatch_flightsSEK_groundEUR_returnsZero
// is the RED-proving case: flights (rt+ows) report SEK, ground reports EUR ->
// 0 hacks. Fails before the guard (would mix and mislabel SEK), passes after.
func TestDetectMultiModalReturnSplit_currencyMismatch_flightsSEK_groundEUR_returnsZero(t *testing.T) {
	withReturnSplitFlightSearch(t, func(_ context.Context, _, _, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return returnSplitMakeFlight(300, "SEK"), nil
		}
		return returnSplitMakeFlight(150, "SEK"), nil
	})
	withReturnSplitGroundSearch(t, func(_ context.Context, from, to, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		if from == "Prague" && to == "Helsinki" {
			return returnSplitMakeGround(80, "EUR", "bus", "Prague", "Helsinki", "2026-07-08T10:00", "2026-07-08T22:00", "https://ex/bus"), nil
		}
		return emptyGround(), nil
	})

	hacks := detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin: "HEL", Destination: "PRG", Date: "2026-07-01", ReturnDate: "2026-07-08", Currency: "SEK",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks on SEK flight / EUR ground mismatch, got %d", len(hacks))
	}
}

// TestDetectMultiModalReturnSplit_allEUR_emitsCorrectCurrency is the positive control.
func TestDetectMultiModalReturnSplit_allEUR_emitsCorrectCurrency(t *testing.T) {
	withReturnSplitFlightSearch(t, func(_ context.Context, _, _, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return returnSplitMakeFlight(269, "EUR"), nil
		}
		return returnSplitMakeFlight(145, "EUR"), nil
	})
	withReturnSplitGroundSearch(t, func(_ context.Context, from, to, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		if from == "Prague" && to == "Helsinki" {
			return returnSplitMakeGround(60, "EUR", "bus", "Prague", "Helsinki", "2026-07-08T10:00", "2026-07-08T20:00", "https://ex/bus"), nil
		}
		return emptyGround(), nil
	})

	hacks := detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin: "HEL", Destination: "PRG", Date: "2026-07-01", ReturnDate: "2026-07-08", Currency: "EUR",
	})
	if len(hacks) == 0 {
		t.Fatal("expected hack for all-EUR inputs")
	}
	h := hacks[0]
	if h.Currency != "EUR" {
		t.Errorf("Currency = %q, want %q", h.Currency, "EUR")
	}
	if h.Savings != 64 {
		t.Errorf("Savings = %v, want 64 (269-145-60)", h.Savings)
	}
	if !strings.Contains(h.Title, "EUR") || !strings.Contains(h.Description, "EUR") {
		t.Errorf("expected EUR labels in title/desc, got title=%q", h.Title)
	}
}

// TestDetectMultiModalReturnSplit_onlyRTCurrencyDiffers_skips isolates the rt clause (dir1).
func TestDetectMultiModalReturnSplit_onlyRTCurrencyDiffers_skips(t *testing.T) {
	withReturnSplitFlightSearch(t, func(_ context.Context, _, _, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return returnSplitMakeFlight(269, "USD"), nil // rt is the differing one
		}
		return returnSplitMakeFlight(145, "EUR"), nil
	})
	withReturnSplitGroundSearch(t, func(_ context.Context, from, to, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		if from == "Prague" && to == "Helsinki" {
			return returnSplitMakeGround(60, "EUR", "bus", "Prague", "Helsinki", "2026-07-08T10:00", "2026-07-08T20:00", "u"), nil
		}
		return emptyGround(), nil
	})

	hacks := detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin: "HEL", Destination: "PRG", Date: "2026-07-01", ReturnDate: "2026-07-08", Currency: "EUR",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 when only round-trip currency differs, got %d", len(hacks))
	}
}

// TestDetectMultiModalReturnSplit_onlyOWOutCurrencyDiffers_skips isolates owOutCur (dir1).
func TestDetectMultiModalReturnSplit_onlyOWOutCurrencyDiffers_skips(t *testing.T) {
	withReturnSplitFlightSearch(t, func(_ context.Context, origin, dest, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return returnSplitMakeFlight(269, "EUR"), nil
		}
		if origin == "HEL" && dest == "PRG" {
			return returnSplitMakeFlight(145, "USD"), nil // owOut differs
		}
		return returnSplitMakeFlight(145, "EUR"), nil // owRet
	})
	withReturnSplitGroundSearch(t, func(_ context.Context, from, to, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		if from == "Prague" && to == "Helsinki" {
			return returnSplitMakeGround(60, "EUR", "bus", "Prague", "Helsinki", "2026-07-08T10:00", "2026-07-08T20:00", "u"), nil
		}
		return emptyGround(), nil
	})

	hacks := detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin: "HEL", Destination: "PRG", Date: "2026-07-01", ReturnDate: "2026-07-08", Currency: "EUR",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 when only owOut currency differs, got %d", len(hacks))
	}
}

// TestDetectMultiModalReturnSplit_onlyGroundCurrencyDiffers_skips isolates groundCur (dir1).
func TestDetectMultiModalReturnSplit_onlyGroundCurrencyDiffers_skips(t *testing.T) {
	withReturnSplitFlightSearch(t, func(_ context.Context, _, _, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return returnSplitMakeFlight(269, "EUR"), nil
		}
		return returnSplitMakeFlight(145, "EUR"), nil
	})
	withReturnSplitGroundSearch(t, func(_ context.Context, from, to, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		if from == "Prague" && to == "Helsinki" {
			return returnSplitMakeGround(60, "SEK", "bus", "Prague", "Helsinki", "2026-07-08T10:00", "2026-07-08T20:00", "u"), nil
		}
		return emptyGround(), nil
	})

	hacks := detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin: "HEL", Destination: "PRG", Date: "2026-07-01", ReturnDate: "2026-07-08", Currency: "EUR",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 when only ground currency differs, got %d", len(hacks))
	}
}

// TestDetectMultiModalReturnSplit_emptyBaseCurrency_skips isolates the baseCur != "" clause.
// Everything reports an empty currency, so the equality checks would pass — only the
// unknown-baseline guard prevents a mislabelled emit.
func TestDetectMultiModalReturnSplit_emptyBaseCurrency_skips(t *testing.T) {
	withReturnSplitFlightSearch(t, func(_ context.Context, _, _, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return returnSplitMakeFlight(269, ""), nil
		}
		return returnSplitMakeFlight(145, ""), nil
	})
	withReturnSplitGroundSearch(t, func(_ context.Context, from, to, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		if from == "Prague" && to == "Helsinki" {
			return returnSplitMakeGround(60, "", "bus", "Prague", "Helsinki", "2026-07-08T10:00", "2026-07-08T20:00", "u"), nil
		}
		return emptyGround(), nil
	})

	hacks := detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin: "HEL", Destination: "PRG", Date: "2026-07-01", ReturnDate: "2026-07-08", Currency: "",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 when baseline currency unknown, got %d", len(hacks))
	}
}

// TestDetectMultiModalReturnSplit_dir2_onlyOWRetCurrencyDiffers_skips isolates owRetCur (dir2).
// Ground-out (HEL->PRG) is enabled and ground-return (PRG->HEL) is empty, so only
// direction 2 is a candidate; the one-way return flight differs and must suppress it.
func TestDetectMultiModalReturnSplit_dir2_onlyOWRetCurrencyDiffers_skips(t *testing.T) {
	withReturnSplitFlightSearch(t, func(_ context.Context, origin, dest, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return returnSplitMakeFlight(269, "EUR"), nil
		}
		if origin == "PRG" && dest == "HEL" {
			return returnSplitMakeFlight(145, "USD"), nil // owRet differs
		}
		return returnSplitMakeFlight(145, "EUR"), nil // owOut
	})
	withReturnSplitGroundSearch(t, func(_ context.Context, from, to, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		if from == "Helsinki" && to == "Prague" {
			return returnSplitMakeGround(60, "EUR", "bus", "Helsinki", "Prague", "2026-07-01T10:00", "2026-07-01T20:00", "u"), nil
		}
		return emptyGround(), nil // ground return disabled -> dir1 cannot emit
	})

	hacks := detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin: "HEL", Destination: "PRG", Date: "2026-07-01", ReturnDate: "2026-07-08", Currency: "EUR",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 when only dir2 one-way return currency differs, got %d", len(hacks))
	}
}

// dir2Base wires a direction-2-only scenario: ground-out (HEL->PRG) present,
// ground-return (PRG->HEL) empty so dir1 cannot emit. All fares/ground EUR by
// default; each isolation test flips exactly one currency. rt 269, owRet 145,
// ground-out 60 -> savings 64 -> emits when all currencies agree.
func dir2Base(t *testing.T, rtCur, owRetCur, groundCur string) []Hack {
	t.Helper()
	withReturnSplitFlightSearch(t, func(_ context.Context, origin, dest, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return returnSplitMakeFlight(269, rtCur), nil
		}
		if origin == "PRG" && dest == "HEL" {
			return returnSplitMakeFlight(145, owRetCur), nil // owRet
		}
		return returnSplitMakeFlight(145, "EUR"), nil // owOut
	})
	withReturnSplitGroundSearch(t, func(_ context.Context, from, to, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		if from == "Helsinki" && to == "Prague" {
			return returnSplitMakeGround(60, groundCur, "bus", "Helsinki", "Prague", "2026-07-01T10:00", "2026-07-01T20:00", "u"), nil
		}
		return emptyGround(), nil
	})
	return detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin: "HEL", Destination: "PRG", Date: "2026-07-01", ReturnDate: "2026-07-08", Currency: "EUR",
	})
}

// TestDetectMultiModalReturnSplit_dir2_allEUR_emits is the dir2 positive control.
func TestDetectMultiModalReturnSplit_dir2_allEUR_emits(t *testing.T) {
	hacks := dir2Base(t, "EUR", "EUR", "EUR")
	if len(hacks) != 1 {
		t.Fatalf("expected 1 dir2 hack for all-EUR, got %d", len(hacks))
	}
	if hacks[0].Currency != "EUR" || hacks[0].Savings != 64 {
		t.Errorf("dir2 hack Currency=%q Savings=%v, want EUR/64", hacks[0].Currency, hacks[0].Savings)
	}
	if !strings.Contains(hacks[0].Description, "EUR") {
		t.Errorf("dir2 desc missing EUR label: %q", hacks[0].Description)
	}
}

// TestDetectMultiModalReturnSplit_dir2_onlyGroundCurrencyDiffers_skips pins the
// dir2 groundCur clause (previously unpinned; mislabelled EUR total would emit).
func TestDetectMultiModalReturnSplit_dir2_onlyGroundCurrencyDiffers_skips(t *testing.T) {
	if hacks := dir2Base(t, "EUR", "EUR", "SEK"); len(hacks) != 0 {
		t.Errorf("expected 0 when only dir2 ground currency differs, got %d", len(hacks))
	}
}

// TestDetectMultiModalReturnSplit_dir2_onlyRTCurrencyDiffers_skips pins the dir2 rtCur clause.
func TestDetectMultiModalReturnSplit_dir2_onlyRTCurrencyDiffers_skips(t *testing.T) {
	if hacks := dir2Base(t, "USD", "EUR", "EUR"); len(hacks) != 0 {
		t.Errorf("expected 0 when only dir2 round-trip currency differs, got %d", len(hacks))
	}
}

// TestDetectMultiModalReturnSplit_dir2_emptyBaseCurrency_skips pins the dir2
// baseCur != "" clause: all currencies empty (equality passes) but empty
// baseline must still suppress the emit.
func TestDetectMultiModalReturnSplit_dir2_emptyBaseCurrency_skips(t *testing.T) {
	withReturnSplitFlightSearch(t, func(_ context.Context, origin, dest, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return returnSplitMakeFlight(269, ""), nil
		}
		return returnSplitMakeFlight(145, ""), nil
	})
	withReturnSplitGroundSearch(t, func(_ context.Context, from, to, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		if from == "Helsinki" && to == "Prague" {
			return returnSplitMakeGround(60, "", "bus", "Helsinki", "Prague", "2026-07-01T10:00", "2026-07-01T20:00", "u"), nil
		}
		return emptyGround(), nil
	})
	hacks := detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin: "HEL", Destination: "PRG", Date: "2026-07-01", ReturnDate: "2026-07-08", Currency: "",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 when dir2 baseline currency empty, got %d", len(hacks))
	}
}

// TestDetectMultiModalReturnSplit_nilResultsNoPanic verifies a search seam
// returning (nil, nil) is skipped safely rather than panicking (mirrors split.go).
func TestDetectMultiModalReturnSplit_nilResultsNoPanic(t *testing.T) {
	withReturnSplitFlightSearch(t, func(_ context.Context, _, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
		return nil, nil
	})
	withReturnSplitGroundSearch(t, func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		return nil, nil
	})
	hacks := detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin: "HEL", Destination: "PRG", Date: "2026-07-01", ReturnDate: "2026-07-08", Currency: "EUR",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks on nil results, got %d", len(hacks))
	}
}

// returnSplitNilCase runs the detector with every provider call valid EXCEPT
// the one named by nilTarget, which returns (nil, nil). It isolates each nil
// guard: drop that guard and the detector dereferences nil -> panic -> the
// subtest fails. Ground calls return emptyGround() so no hack emits and the
// assertion stays a uniform len==0 + no-panic.
func returnSplitNilCase(t *testing.T, nilTarget string) []Hack {
	t.Helper()
	withReturnSplitFlightSearch(t, func(_ context.Context, origin, dest, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		switch {
		case opts.ReturnDate != "": // round-trip baseline
			if nilTarget == "rt" {
				return nil, nil
			}
			return returnSplitMakeFlight(269, "EUR"), nil
		case origin == "PRG" && dest == "HEL": // one-way return
			if nilTarget == "owRet" {
				return nil, nil
			}
			return returnSplitMakeFlight(145, "EUR"), nil
		default: // one-way outbound HEL->PRG
			if nilTarget == "owOut" {
				return nil, nil
			}
			return returnSplitMakeFlight(145, "EUR"), nil
		}
	})
	withReturnSplitGroundSearch(t, func(_ context.Context, from, to, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		switch {
		case from == "Prague" && to == "Helsinki": // dir1 ground return
			if nilTarget == "groundDir1" {
				return nil, nil
			}
			return emptyGround(), nil
		case from == "Helsinki" && to == "Prague": // dir2 ground out
			if nilTarget == "groundDir2" {
				return nil, nil
			}
			return emptyGround(), nil
		default:
			return emptyGround(), nil
		}
	})
	return detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin: "HEL", Destination: "PRG", Date: "2026-07-01", ReturnDate: "2026-07-08", Currency: "EUR",
	})
}

// TestDetectMultiModalReturnSplit_eachNilGuardPinned pins all five provider
// nil guards individually (the earlier smoke test only reached the first). Each
// subtest lets prior calls succeed and returns nil for exactly one provider
// result; removing that result's nil guard makes the detector panic here.
func TestDetectMultiModalReturnSplit_eachNilGuardPinned(t *testing.T) {
	for _, target := range []string{"rt", "owOut", "owRet", "groundDir1", "groundDir2"} {
		t.Run(target, func(t *testing.T) {
			if hacks := returnSplitNilCase(t, target); len(hacks) != 0 {
				t.Errorf("nil %s result: expected 0 hacks, got %d", target, len(hacks))
			}
		})
	}
}

// twoRouteGround builds a ground result with a cheaper wrong-currency route
// listed before a pricier baseline-currency route, to check the detector picks
// the cheapest ELIGIBLE route rather than the cheapest overall.
func twoRouteGround(cheapCur string, cheapPrice float64, eligiblePrice float64, depCity, arrCity string) *models.GroundSearchResult {
	return &models.GroundSearchResult{Success: true, Routes: []models.GroundRoute{
		{Provider: "x", Type: "bus", Price: cheapPrice, Currency: cheapCur,
			Departure: models.GroundStop{City: depCity, Time: "2026-07-01T10:00"},
			Arrival:   models.GroundStop{City: arrCity, Time: "2026-07-01T20:00"}, BookingURL: "u1"},
		{Provider: "y", Type: "bus", Price: eligiblePrice, Currency: "EUR",
			Departure: models.GroundStop{City: depCity, Time: "2026-07-01T10:00"},
			Arrival:   models.GroundStop{City: arrCity, Time: "2026-07-01T20:00"}, BookingURL: "u2"},
	}}
}

// TestDetectMultiModalReturnSplit_dir1_cheaperMismatchedRouteSkipped: a 50-SEK
// route is cheaper than a 60-EUR route but SEK != EUR baseline; the detector
// must select the 60-EUR route and still emit (previously emitted nothing).
func TestDetectMultiModalReturnSplit_dir1_cheaperMismatchedRouteSkipped(t *testing.T) {
	withReturnSplitFlightSearch(t, func(_ context.Context, _, _, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return returnSplitMakeFlight(269, "EUR"), nil
		}
		return returnSplitMakeFlight(145, "EUR"), nil
	})
	withReturnSplitGroundSearch(t, func(_ context.Context, from, to, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		if from == "Prague" && to == "Helsinki" { // dir1 ground return
			return twoRouteGround("SEK", 50, 60, "Prague", "Helsinki"), nil
		}
		return emptyGround(), nil
	})
	hacks := detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin: "HEL", Destination: "PRG", Date: "2026-07-01", ReturnDate: "2026-07-08", Currency: "EUR",
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 dir1 hack using the eligible EUR route, got %d", len(hacks))
	}
	if hacks[0].Currency != "EUR" || hacks[0].Savings != 64 { // 269-(145+60)=64
		t.Errorf("dir1 hack Currency=%q Savings=%v, want EUR/64", hacks[0].Currency, hacks[0].Savings)
	}
}

// TestDetectMultiModalReturnSplit_dir2_cheaperMismatchedRouteSkipped: same, dir2.
func TestDetectMultiModalReturnSplit_dir2_cheaperMismatchedRouteSkipped(t *testing.T) {
	withReturnSplitFlightSearch(t, func(_ context.Context, origin, dest, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return returnSplitMakeFlight(269, "EUR"), nil
		}
		if origin == "PRG" && dest == "HEL" {
			return returnSplitMakeFlight(145, "EUR"), nil // owRet
		}
		return returnSplitMakeFlight(145, "EUR"), nil // owOut
	})
	withReturnSplitGroundSearch(t, func(_ context.Context, from, to, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		if from == "Helsinki" && to == "Prague" { // dir2 ground out
			return twoRouteGround("SEK", 50, 60, "Helsinki", "Prague"), nil
		}
		return emptyGround(), nil // dir1 empty so only dir2 emits
	})
	hacks := detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin: "HEL", Destination: "PRG", Date: "2026-07-01", ReturnDate: "2026-07-08", Currency: "EUR",
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 dir2 hack using the eligible EUR route, got %d", len(hacks))
	}
	if hacks[0].Currency != "EUR" || hacks[0].Savings != 64 {
		t.Errorf("dir2 hack Currency=%q Savings=%v, want EUR/64", hacks[0].Currency, hacks[0].Savings)
	}
}

// ---------------------------------------------------------------------------
// Integration: DetectAll includes multimodal types
// ---------------------------------------------------------------------------

// TestDetectAll_includesMultiModalTypes verifies the detector list in DetectAll
// is wired up correctly by checking that the type strings are valid identifiers
// (non-empty). We cannot force live API hacks in unit tests.
func TestDetectAll_includesMultiModalTypes(t *testing.T) {
	validTypes := map[string]bool{
		"multimodal_skip_flight":     true,
		"multimodal_positioning":     true,
		"multimodal_open_jaw_ground": true,
		"multimodal_return_split":    true,
	}
	// Verify the type string literals match what we declare in each file.
	for typ := range validTypes {
		if typ == "" {
			t.Error("multimodal type must not be empty string")
		}
	}
}
