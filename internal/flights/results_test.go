package flights

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestMergeFlightResults_ProfileMaxPriceHintWouldTruncateMultiProvider
// reproduces, at the real merge/filter pipeline, the exact symptom reported
// in issue #452: search_flights via MCP returned only 4 kiwi-only flights for
// FCO->HRG while the CLI and a direct flights.SearchFlights() call returned
// 106 flights across every provider. The root cause was mcp.handleSearchFlights
// silently setting opts.MaxPrice from a profile-derived AvgFlightPrice*1.5
// hint whenever the caller omitted max_price — a hard filter applied here,
// inside mergeFlightResults/filterFlightResults, AFTER each provider's raw
// fetch count is already recorded in provider_statuses (so "google_flights:
// ok, 91 results" and a 4-flight final list could coexist with no visible
// signal of truncation).
//
// This test locks in both directions:
//   - opts.MaxPrice == 0 (the fixed mcp.applyFlightProfileHints never sets
//     it from a profile hint) must preserve every provider's flights.
//   - opts.MaxPrice set to a stale/low ceiling (simulating the pre-fix bug)
//     DOES collapse the merge down to the cheap provider only — proving this
//     is genuinely the mechanism that produced the reported truncation, not
//     just a hypothetical.
func TestMergeFlightResults_ProfileMaxPriceHintWouldTruncateMultiProvider(t *testing.T) {
	kiwiFlights := []models.FlightResult{
		{Price: 120, Currency: "EUR", Provider: "kiwi", Legs: []models.FlightLeg{{DepartureAirport: models.AirportInfo{Code: "FCO"}, ArrivalAirport: models.AirportInfo{Code: "HRG"}, DepartureTime: "2026-08-10T06:00", ArrivalTime: "2026-08-10T10:00"}}},
		{Price: 140, Currency: "EUR", Provider: "kiwi", Legs: []models.FlightLeg{{DepartureAirport: models.AirportInfo{Code: "FCO"}, ArrivalAirport: models.AirportInfo{Code: "HRG"}, DepartureTime: "2026-08-10T09:00", ArrivalTime: "2026-08-10T13:00"}}},
	}
	googleFlights := []models.FlightResult{
		{Price: 410, Currency: "EUR", Provider: "google_flights", Legs: []models.FlightLeg{{DepartureAirport: models.AirportInfo{Code: "FCO"}, ArrivalAirport: models.AirportInfo{Code: "HRG"}, AirlineCode: "MS", DepartureTime: "2026-08-10T11:00", ArrivalTime: "2026-08-10T15:00"}}},
		{Price: 455, Currency: "EUR", Provider: "google_flights", Legs: []models.FlightLeg{{DepartureAirport: models.AirportInfo{Code: "FCO"}, ArrivalAirport: models.AirportInfo{Code: "HRG"}, AirlineCode: "LH", DepartureTime: "2026-08-10T14:00", ArrivalTime: "2026-08-10T20:00"}}},
	}

	t.Run("fixed: no MaxPrice hint preserves every provider", func(t *testing.T) {
		merged := mergeFlightResults(googleFlights, kiwiFlights, nil, SearchOptions{})
		if len(merged) != len(kiwiFlights)+len(googleFlights) {
			t.Fatalf("merged count = %d, want %d (kiwi + google_flights must both survive when no price ceiling is set — issue #452)",
				len(merged), len(kiwiFlights)+len(googleFlights))
		}
		var sawGoogle, sawKiwi bool
		for _, f := range merged {
			switch f.Provider {
			case "google_flights":
				sawGoogle = true
			case "kiwi":
				sawKiwi = true
			}
		}
		if !sawGoogle || !sawKiwi {
			t.Fatalf("merged providers incomplete: sawGoogle=%v sawKiwi=%v, want both true", sawGoogle, sawKiwi)
		}
	})

	t.Run("mechanism proof: a stale MaxPrice ceiling collapses to one provider", func(t *testing.T) {
		// 300 mirrors AvgFlightPrice(200)*1.5 from a user profile whose
		// historical average is far below this route's real fares — the
		// exact shape of the bug that shipped before the fix.
		merged := mergeFlightResults(googleFlights, kiwiFlights, nil, SearchOptions{MaxPrice: 300})
		if len(merged) != len(kiwiFlights) {
			t.Fatalf("merged count = %d, want %d (a stale price ceiling should reproduce the pre-fix truncation to kiwi-only)",
				len(merged), len(kiwiFlights))
		}
		for _, f := range merged {
			if f.Provider != "kiwi" {
				t.Fatalf("unexpected provider %q survived the ceiling; want kiwi-only collapse (proves the truncation mechanism)", f.Provider)
			}
		}
	})
}

func TestMergeFlightResults_SortsCheapestAndFiltersStops(t *testing.T) {
	googleFlights := []models.FlightResult{
		{
			Price:    200,
			Currency: "EUR",
			Duration: 120,
			Stops:    0,
			Provider: "google_flights",
			Legs: []models.FlightLeg{
				{
					DepartureAirport: models.AirportInfo{Code: "HEL"},
					ArrivalAirport:   models.AirportInfo{Code: "DBV"},
					DepartureTime:    "2026-07-01T08:00",
					ArrivalTime:      "2026-07-01T10:00",
				},
			},
		},
	}
	kiwiFlights := []models.FlightResult{
		{
			Price:    150,
			Currency: "EUR",
			Duration: 300,
			Stops:    2,
			Provider: "kiwi",
			Legs: []models.FlightLeg{
				{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "ARN"}, DepartureTime: "2026-07-01T06:00", ArrivalTime: "2026-07-01T07:00"},
				{DepartureAirport: models.AirportInfo{Code: "ARN"}, ArrivalAirport: models.AirportInfo{Code: "WAW"}, DepartureTime: "2026-07-01T08:00", ArrivalTime: "2026-07-01T09:00"},
				{DepartureAirport: models.AirportInfo{Code: "WAW"}, ArrivalAirport: models.AirportInfo{Code: "DBV"}, DepartureTime: "2026-07-01T10:00", ArrivalTime: "2026-07-01T11:00"},
			},
		},
		{
			Price:    175,
			Currency: "EUR",
			Duration: 180,
			Stops:    1,
			Provider: "kiwi",
			Legs: []models.FlightLeg{
				{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "ARN"}, DepartureTime: "2026-07-01T07:00", ArrivalTime: "2026-07-01T08:00"},
				{DepartureAirport: models.AirportInfo{Code: "ARN"}, ArrivalAirport: models.AirportInfo{Code: "DBV"}, DepartureTime: "2026-07-01T09:00", ArrivalTime: "2026-07-01T10:00"},
			},
		},
	}

	merged := mergeFlightResults(googleFlights, kiwiFlights, nil, SearchOptions{
		MaxStops: models.OneStop,
		SortBy:   models.SortCheapest,
	})

	if len(merged) != 2 {
		t.Fatalf("merged count = %d, want 2", len(merged))
	}
	if merged[0].Price != 175 {
		t.Fatalf("first price = %.0f, want 175", merged[0].Price)
	}
	if merged[1].Price != 200 {
		t.Fatalf("second price = %.0f, want 200", merged[1].Price)
	}
}

// TestSortFlightResults_ZeroPriceRanksLast proves a price-less native round-trip
// fare (Google prices the return "at booking", so Price==0) never outranks a
// real-priced result. A 0/nil ranking price must sort BELOW all positive-priced
// results instead of being treated as the cheapest (EUR 0). Regression for the
// operator-visible "#1 is a Price: - round_trip" bug.
func TestSortFlightResults_ZeroPriceRanksLast(t *testing.T) {
	leg := func(dep, arr string) []models.FlightLeg {
		return []models.FlightLeg{{
			DepartureAirport: models.AirportInfo{Code: dep},
			ArrivalAirport:   models.AirportInfo{Code: arr},
			DepartureTime:    "2026-07-10T08:00",
			ArrivalTime:      "2026-07-10T10:00",
		}}
	}
	cases := []struct {
		name       string
		flights    []models.FlightResult
		wantFirst  string // provider of #1
		wantLastPx float64
	}{
		{
			name: "priceless native round-trip does not outrank real fares",
			flights: []models.FlightResult{
				{Price: 0, Currency: "EUR", Provider: "google_flights", FareType: models.FareRoundTrip, Legs: leg("HEL", "BCN")},
				{Price: 334, Currency: "EUR", Provider: "kiwi", Legs: leg("HEL", "BCN")},
				{Price: 330, Currency: "EUR", Provider: "skiplagged", Legs: leg("HEL", "BCN")},
			},
			wantFirst:  "skiplagged",
			wantLastPx: 0,
		},
		{
			name: "all real prices unaffected (cheapest still first)",
			flights: []models.FlightResult{
				{Price: 334, Currency: "EUR", Provider: "kiwi", Legs: leg("HEL", "BCN")},
				{Price: 330, Currency: "EUR", Provider: "skiplagged", Legs: leg("HEL", "BCN")},
			},
			wantFirst:  "skiplagged",
			wantLastPx: 334,
		},
		{
			name: "multiple priceless fares all sink below real ones",
			flights: []models.FlightResult{
				{Price: 0, Currency: "EUR", Provider: "google_flights", FareType: models.FareRoundTrip, Legs: leg("HEL", "BCN")},
				{Price: 0, Currency: "EUR", Provider: "kiwi", FareType: models.FareRoundTrip, Legs: leg("HEL", "BCN")},
				{Price: 290, Currency: "EUR", Provider: "ryanair", Legs: leg("HEL", "BCN")},
			},
			wantFirst:  "ryanair",
			wantLastPx: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sortFlightResults(tc.flights, models.SortCheapest)
			if got := tc.flights[0].Provider; got != tc.wantFirst {
				t.Errorf("#1 provider = %q (price %.0f), want %q", got, tc.flights[0].PriceForRanking(), tc.wantFirst)
			}
			if got := tc.flights[len(tc.flights)-1].PriceForRanking(); got != tc.wantLastPx {
				t.Errorf("last ranking price = %.0f, want %.0f", got, tc.wantLastPx)
			}
			// No positive-priced result may sit below a non-positive one.
			for i := 1; i < len(tc.flights); i++ {
				prev := tc.flights[i-1].PriceForRanking()
				cur := tc.flights[i].PriceForRanking()
				if prev <= 0 && cur > 0 {
					t.Errorf("priceless result at index %d outranks real-priced result at %d", i-1, i)
				}
			}
		})
	}
}

// TestApplyComparableBaseline_LCCBagRanking proves an LCC bare fare ranks by its
// all-in (fare + carry-on fee) so it no longer unfairly beats an included fare.
func TestApplyComparableBaseline_LCCBagRanking(t *testing.T) {
	leg := func(code string) []models.FlightLeg {
		return []models.FlightLeg{{AirlineCode: code, DepartureTime: "2026-06-01T08:00"}}
	}
	// FR (Ryanair) is OverheadOnly -> +EUR15 carry-on; AY (Finnair) includes bag.
	flights := []models.FlightResult{
		{Price: 45, Currency: "EUR", Provider: "ryanair", Legs: leg("FR")},
		{Price: 55, Currency: "EUR", Provider: "finnair", Legs: leg("AY")},
	}
	applyComparableBaseline(flights)
	if flights[0].ComparablePrice == 0 {
		t.Fatal("Ryanair comparable price not set (expected carry-on fee added)")
	}
	if flights[0].ComparablePrice <= 45 {
		t.Errorf("Ryanair comparable %v should exceed base 45", flights[0].ComparablePrice)
	}
	// Finnair includes the bag -> no uplift -> ranking value stays 55.
	if flights[1].PriceForRanking() != 55 {
		t.Errorf("Finnair ranking value = %v, want 55", flights[1].PriceForRanking())
	}
	// If Ryanair all-in (45+15=60) now exceeds Finnair 55, the sort must reflect it.
	sortFlightResults(flights, models.SortCheapest)
	if flights[0].Provider != "finnair" {
		t.Errorf("after all-in ranking, cheapest should be finnair, got %s (FR comparable=%v)", flights[0].Provider, comparableOf(flights, "ryanair"))
	}
}

func comparableOf(flights []models.FlightResult, provider string) float64 {
	for _, f := range flights {
		if f.Provider == provider {
			return f.PriceForRanking()
		}
	}
	return 0
}

func TestNormalizeFlightCurrencies(t *testing.T) {
	// Stub converter: USD->EUR at 0.9, no network.
	conv := func(_ context.Context, amount float64, from, to string) (float64, string) {
		if from == "USD" && to == "EUR" {
			return amount * 0.9, "EUR"
		}
		if from == to {
			return amount, to
		}
		return amount, from // other pairs: leave unchanged
	}
	flights := []models.FlightResult{
		{Price: 100, Currency: "USD", Provider: "skiplagged"},
		{Price: 90, Currency: "EUR", Provider: "google"},
		{Price: 50, Currency: "GBP", Provider: "x"}, // unconvertible -> unchanged
	}
	normalizeFlightCurrencies(context.Background(), flights, "EUR", conv)
	if flights[0].Currency != "EUR" || flights[0].Price != 90 {
		t.Errorf("USD->EUR failed: %v %s", flights[0].Price, flights[0].Currency)
	}
	if flights[2].Currency != "GBP" || flights[2].Price != 50 {
		t.Errorf("unconvertible should stay GBP 50, got %v %s", flights[2].Price, flights[2].Currency)
	}
	normalizeFlightCurrencies(context.Background(), flights, "", conv)
	normalizeFlightCurrencies(context.Background(), flights, "EUR", nil)
}
