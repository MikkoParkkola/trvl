package hacks

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// overnightBus is one live overnight bus: departs 21:00, arrives 07:00.
func overnightBus(price float64, from, to string) ground.SearchFunc {
	return func(context.Context, string, string, string, ground.SearchOptions) (*models.GroundSearchResult, error) {
		return &models.GroundSearchResult{Success: true, Routes: []models.GroundRoute{{
			Provider: "flixbus", Type: "bus", Price: price, Currency: "EUR", PriceProvenance: models.PriceLive,
			Departure: models.GroundStop{City: from, Time: "2027-07-15T21:00"},
			Arrival:   models.GroundStop{City: to, Time: "2027-07-16T07:00"},
		}}}, nil
	}
}

// flightAt returns a flight search stub whose only flight costs price EUR.
func flightAt(price float64) flights.SearchFunc {
	return func(context.Context, string, string, string, flights.SearchOptions) (*models.FlightSearchResult, error) {
		return &models.FlightSearchResult{Success: true, Flights: []models.FlightResult{{Price: price, Currency: "EUR"}}}, nil
	}
}

func withRoster(t *testing.T, fns ...detectFn) {
	t.Helper()
	prev := currentDetectorRoster()
	setDetectorRoster(func() []detectFn { return fns })
	t.Cleanup(func() { setDetectorRoster(prev) })
}

// #819: an overnight bus saves a hotel night, which is not part of any fare.
// DetectAll still lists it with that saving, but BestSaving never subtracts
// it from a EUR 150 flight. On main it attached price 90.
func TestNightTransport_ListedButNeverPricedAgainstAFare(t *testing.T) {
	withRoster(t, detectNightTransport)
	in := DetectorInput{
		Origin: "PRG", Destination: "VIE", Date: "2027-07-15", Currency: "EUR", NaivePrice: 150,
		GroundSearchOverride: overnightBus(30, "Prague", "Vienna"),
	}

	listed, _ := DetectAll(context.Background(), in)
	if len(listed) != 1 || listed[0].Type != "night_transport" || listed[0].Savings != 60 {
		t.Fatalf("DetectAll = %+v, want one night_transport hack saving 60", listed)
	}
	if got := BestSaving(context.Background(), in, nil); got != nil {
		t.Errorf("flight search attached a hotel night as a fare saving: type=%s price=%v naive=%v savings=%v",
			got.Type, got.Price, got.NaivePrice, got.Savings)
	}
}

// #819: a ferry cabin replacing a hotel night is not a cheaper flight. On
// main a EUR 150 flight HEL to ARN became price 65.
func TestFerryCabin_NeverPricedAgainstAFare(t *testing.T) {
	in := DetectorInput{Origin: "HEL", Destination: "ARN", Date: "2027-07-15", Currency: "EUR", NaivePrice: 150}
	if got := BestSaving(context.Background(), in, func(ctx context.Context, in DetectorInput) []Hack {
		return detectFerryCabin(ctx, in)
	}); got != nil {
		t.Errorf("flight search attached a ferry-cabin hotel saving as a fare: type=%s price=%v savings=%v",
			got.Type, got.Price, got.Savings)
	}
}

// #819: skipping a EUR 200 flight for an EUR 80 overnight bus saves 120 on
// fares. The hotel night is described, not added: on main Savings was 180.
func TestMultiModalSkipFlight_SavingIsTheFareGap(t *testing.T) {
	hs := detectMultiModalSkipFlight(context.Background(), DetectorInput{
		Origin: "PRG", Destination: "VIE", Date: "2027-07-15", Currency: "EUR",
		SearchOverride: flightAt(200), GroundSearchOverride: overnightBus(80, "Prague", "Vienna"),
	})
	if len(hs) != 1 {
		t.Fatalf("got %d hacks, want 1", len(hs))
	}
	h := hs[0]
	if h.Savings != 120 {
		t.Errorf("Savings = %v, want 120 (200 flight - 80 bus)", h.Savings)
	}
	if !strings.Contains(h.Title, "EUR 120") || strings.Contains(h.Title, "180") || strings.Contains(h.Description, "180") {
		t.Errorf("text does not match the 120 saving: title=%q description=%q", h.Title, h.Description)
	}
}

// #819: flying HEL to ZAG (EUR 100) and taking the overnight bus to DBV
// (static EUR 15) instead of a EUR 300 direct flight saves 185 on fares. On
// main the hotel night made it 245.
func TestMultiModalOpenJawGround_SavingIsTheFareGap(t *testing.T) {
	hs := detectMultiModalOpenJawGround(context.Background(), DetectorInput{
		Origin: "HEL", Destination: "DBV", Date: "2027-07-15", Currency: "EUR",
		SearchOverride: func(_ context.Context, _, dest, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
			switch dest {
			case "DBV":
				return flightAt(300)(context.Background(), "", "", "", flights.SearchOptions{})
			case "ZAG":
				return flightAt(100)(context.Background(), "", "", "", flights.SearchOptions{})
			}
			return flightAt(400)(context.Background(), "", "", "", flights.SearchOptions{}) // SPU: no saving
		},
		GroundSearchOverride: func(context.Context, string, string, string, ground.SearchOptions) (*models.GroundSearchResult, error) {
			return &models.GroundSearchResult{Success: true}, nil // no live quote: static EUR 15
		},
	})
	if len(hs) != 1 {
		t.Fatalf("got %d hacks, want 1 (via Zagreb)", len(hs))
	}
	if hs[0].Savings != 185 {
		t.Errorf("Savings = %v, want 185 (300 direct - 100 flight - 15 bus)", hs[0].Savings)
	}
}

// #819: a EUR 269 round trip against EUR 145 out plus an EUR 60 overnight
// bus back saves 64 on fares. On main the hotel night made it 124.
func TestMultiModalReturnSplit_OvernightSavingIsTheFareGap(t *testing.T) {
	withReturnSplitFlightSearch(t, func(_ context.Context, _, _, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if opts.ReturnDate != "" {
			return returnSplitMakeFlight(269, "EUR"), nil
		}
		return returnSplitMakeFlight(145, "EUR"), nil
	})
	withReturnSplitGroundSearch(t, func(_ context.Context, from, to, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		if from == "Prague" && to == "Helsinki" {
			return returnSplitMakeGround(60, "EUR", "bus", "Prague", "Helsinki", "2026-07-08T21:00", "2026-07-09T07:00", "https://ex/bus"), nil
		}
		return emptyGround(), nil
	})
	hs := detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin: "HEL", Destination: "PRG", Date: "2026-07-01", ReturnDate: "2026-07-08", Currency: "EUR",
	})
	if len(hs) == 0 {
		t.Fatal("expected a return-split hack")
	}
	if hs[0].Savings != 64 {
		t.Errorf("Savings = %v, want 64 (269 - 145 - 60)", hs[0].Savings)
	}
}

// A flight-fare saving still attaches to a flight search.
func TestBestSaving_FlightFareSavingStillAttaches(t *testing.T) {
	in := DetectorInput{Origin: "STN", Destination: "VIE", Date: "2027-07-15", Currency: "EUR", NaivePrice: 150}
	got := BestSaving(context.Background(), in, func(context.Context, DetectorInput) []Hack {
		return []Hack{{Type: "split", Savings: 30, Currency: "EUR"}}
	})
	if got == nil || got.Price != 120 {
		t.Fatalf("flight saving lost: %+v", got)
	}
}
