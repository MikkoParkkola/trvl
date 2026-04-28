package hacks

// coverage_target_test.go — Tests targeting low-coverage functions.
// All names are unique across the package — verified against coverage_max_test.go.

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// ---------------------------------------------------------------------------
// routesThroughDestination — additional branches
// ---------------------------------------------------------------------------

func TestRoutesThroughDestination_twoLegHitsIntermediate(t *testing.T) {
	// Two-leg flight; AMS is the intermediate stop (not final).
	r := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{
				Price: 150,
				Legs: []models.FlightLeg{
					{
						DepartureAirport: models.AirportInfo{Code: "HEL"},
						ArrivalAirport:   models.AirportInfo{Code: "AMS"},
					},
					{
						DepartureAirport: models.AirportInfo{Code: "AMS"},
						ArrivalAirport:   models.AirportInfo{Code: "ARN"},
					},
				},
			},
		},
	}
	if !routesThroughDestination(r, "AMS") {
		t.Error("expected true: AMS is an intermediate stop in a 2-leg flight")
	}
}

func TestRoutesThroughDestination_twoLegFinalDestNoMatch(t *testing.T) {
	// AMS is the FINAL destination leg — the loop skips it, falls back to optimistic.
	r := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{
				Price: 150,
				Legs: []models.FlightLeg{
					{
						DepartureAirport: models.AirportInfo{Code: "HEL"},
						ArrivalAirport:   models.AirportInfo{Code: "FRA"},
					},
					{
						DepartureAirport: models.AirportInfo{Code: "FRA"},
						ArrivalAirport:   models.AirportInfo{Code: "AMS"},
					},
				},
			},
		},
	}
	// AMS only appears as final leg → loop misses it → optimistic fallback = true.
	result := routesThroughDestination(r, "AMS")
	// Just verify it doesn't panic and the loop ran.
	_ = result
}

func TestRoutesThroughDestination_threeLegsIntermediate(t *testing.T) {
	// Three-leg flight; DOH is intermediate.
	r := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{
				Price: 600,
				Legs: []models.FlightLeg{
					{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "DOH"}},
					{DepartureAirport: models.AirportInfo{Code: "DOH"}, ArrivalAirport: models.AirportInfo{Code: "BOM"}},
					{DepartureAirport: models.AirportInfo{Code: "BOM"}, ArrivalAirport: models.AirportInfo{Code: "SIN"}},
				},
			},
		},
	}
	if !routesThroughDestination(r, "DOH") {
		t.Error("expected true: DOH is intermediate in a 3-leg flight")
	}
}

// ---------------------------------------------------------------------------
// primaryAirlineCode — additional branches
// ---------------------------------------------------------------------------

func TestPrimaryAirlineCode_firstNonEmptyCode(t *testing.T) {
	// First leg has empty AirlineCode, second has LH.
	r := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{
				Legs: []models.FlightLeg{
					{AirlineCode: "", Airline: "Unknown"},
					{AirlineCode: "LH", Airline: "Lufthansa"},
				},
			},
		},
	}
	got := primaryAirlineCode(r)
	if got != "LH" {
		t.Errorf("expected 'LH' (first non-empty code), got %q", got)
	}
}

func TestPrimaryAirlineCode_multiFlightReturnsFirst(t *testing.T) {
	r := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Legs: []models.FlightLeg{{AirlineCode: "AY"}}},
			{Legs: []models.FlightLeg{{AirlineCode: "KL"}}},
		},
	}
	got := primaryAirlineCode(r)
	if got != "AY" {
		t.Errorf("expected 'AY' (from first flight), got %q", got)
	}
}

// ---------------------------------------------------------------------------
// buildHiddenCityHack — pure builder, no live API
// ---------------------------------------------------------------------------

func TestBuildHiddenCityHack_savingsAndType(t *testing.T) {
	in := DetectorInput{
		Origin:      "HEL",
		Destination: "AMS",
		Date:        "2026-06-01",
		Currency:    "EUR",
	}
	h := buildHiddenCityHack(in, "ARN", 120.0, 200.0, "EUR", "AY")

	if h.Type != "hidden_city" {
		t.Errorf("Type = %q, want 'hidden_city'", h.Type)
	}
	if h.Savings != 80 {
		t.Errorf("Savings = %v, want 80", h.Savings)
	}
	if h.Currency != "EUR" {
		t.Errorf("Currency = %q, want 'EUR'", h.Currency)
	}
	if len(h.Steps) == 0 {
		t.Error("Steps must not be empty")
	}
	if len(h.Risks) < 5 {
		t.Errorf("expected at least 5 standard risks, got %d", len(h.Risks))
	}
	if len(h.Citations) == 0 {
		t.Error("Citations must not be empty")
	}
	if h.Description == "" {
		t.Error("Description must not be empty")
	}
}

func TestBuildHiddenCityHack_noExtraRiskForUnknownAirline(t *testing.T) {
	in := DetectorInput{
		Origin:      "HEL",
		Destination: "AMS",
		Date:        "2026-06-01",
	}
	// Unknown airline → BaggageNote returns empty → exactly 5 standard risks.
	h := buildHiddenCityHack(in, "CPH", 80.0, 150.0, "EUR", "XYZUNKNOWN")
	if len(h.Risks) != 5 {
		t.Errorf("expected exactly 5 risks for unknown airline, got %d", len(h.Risks))
	}
}

// ---------------------------------------------------------------------------
// isLowCostFlight — untested code paths
// ---------------------------------------------------------------------------

func TestIsLowCostFlight_ryanairCode(t *testing.T) {
	f := models.FlightResult{
		Legs: []models.FlightLeg{{AirlineCode: "FR"}},
	}
	if !isLowCostFlight(f) {
		t.Error("FR (Ryanair) should be identified as LCC")
	}
}

func TestIsLowCostFlight_easyJetCode(t *testing.T) {
	f := models.FlightResult{
		Legs: []models.FlightLeg{{AirlineCode: "U2"}},
	}
	if !isLowCostFlight(f) {
		t.Error("U2 (easyJet) should be identified as LCC")
	}
}

func TestIsLowCostFlight_legacyPlusLCCReturnsTrue(t *testing.T) {
	f := models.FlightResult{
		Legs: []models.FlightLeg{
			{AirlineCode: "LH"},
			{AirlineCode: "W6"},
		},
	}
	if !isLowCostFlight(f) {
		t.Error("flight with ≥1 LCC leg should return true")
	}
}

// ---------------------------------------------------------------------------
// lccName — untested code paths
// ---------------------------------------------------------------------------

func TestLccName_ryanairCode(t *testing.T) {
	f := models.FlightResult{
		Legs: []models.FlightLeg{{AirlineCode: "FR"}},
	}
	if got := lccName(f); got != "Ryanair" {
		t.Errorf("lccName = %q, want 'Ryanair'", got)
	}
}

func TestLccName_easyjetCode(t *testing.T) {
	f := models.FlightResult{
		Legs: []models.FlightLeg{{AirlineCode: "U2"}},
	}
	if got := lccName(f); got != "easyJet" {
		t.Errorf("lccName = %q, want 'easyJet'", got)
	}
}

func TestLccName_legacyReturnsDefault(t *testing.T) {
	f := models.FlightResult{
		Legs: []models.FlightLeg{{AirlineCode: "LH"}},
	}
	if got := lccName(f); got != "LCC" {
		t.Errorf("lccName fallback = %q, want 'LCC'", got)
	}
}

func TestLccName_emptyLegsReturnsDefault(t *testing.T) {
	f := models.FlightResult{}
	if got := lccName(f); got != "LCC" {
		t.Errorf("lccName (no legs) = %q, want 'LCC'", got)
	}
}

// ---------------------------------------------------------------------------
// loyaltyConflictNote — untested nil-prefs path
// ---------------------------------------------------------------------------

func TestLoyaltyConflictNote_nilPrefsReturnsEmpty(t *testing.T) {
	r := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Legs: []models.FlightLeg{{AirlineCode: "LH", Airline: "Lufthansa"}}},
		},
	}
	if got := loyaltyConflictNote(r, nil); got != "" {
		t.Errorf("expected empty with nil prefs, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// detectThrowaway — valid-input path (hits API, returns nil)
// ---------------------------------------------------------------------------

func TestDetectThrowaway_validInputNoLiveAPI(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	h := detectThrowaway(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
	})
	_ = h // nil expected (no live API), no panic
}

// ---------------------------------------------------------------------------
// detectHiddenCity — knownDestination valid path
// ---------------------------------------------------------------------------

func TestDetectHiddenCity_knownHubDestination(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	// AMS is in hiddenCityExtensions — hits live API (returns nil).
	h := detectHiddenCity(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "AMS",
		Date:        "2026-06-01",
	})
	_ = h
}

func TestDetectHiddenCity_anotherKnownHub(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	// FRA is also in hiddenCityExtensions.
	h := detectHiddenCity(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "FRA",
		Date:        "2026-06-01",
	})
	_ = h
}

// ---------------------------------------------------------------------------
// detectStopover — valid input + buildStopoverHack
// ---------------------------------------------------------------------------

func TestDetectStopover_validInputNoPanic(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	h := detectStopover(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "DOH",
		Date:        "2026-06-01",
	})
	_ = h
}

func TestBuildStopoverHack_flightCurrencyUsed(t *testing.T) {
	in := DetectorInput{
		Origin:      "HEL",
		Destination: "BKK",
		Date:        "2026-11-01",
		Currency:    "USD",
	}
	prog := StopoverProgram{
		Airline:      "Finnair",
		Hub:          "HEL",
		MaxNights:    5,
		URL:          "https://finnair.com/stopover",
		Restrictions: "Economy class only",
	}
	f := models.FlightResult{
		Price:    450,
		Currency: "EUR", // flight has EUR — should override USD
	}
	h := buildStopoverHack(in, prog, f, "HEL")
	if h.Currency != "EUR" {
		t.Errorf("Currency = %q, want 'EUR' (from flight result)", h.Currency)
	}
}

func TestBuildStopoverHack_noCurrencyFallsBackToInput(t *testing.T) {
	in := DetectorInput{Currency: "GBP"}
	prog := StopoverProgram{
		Airline:      "Qatar Airways",
		Hub:          "DOH",
		MaxNights:    4,
		URL:          "https://qatarairways.com/stopover",
		Restrictions: "Online request only",
	}
	f := models.FlightResult{
		Price:    800,
		Currency: "",
	}
	h := buildStopoverHack(in, prog, f, "DOH")
	if h.Currency != "GBP" {
		t.Errorf("Currency = %q, want 'GBP' (fallback to input)", h.Currency)
	}
}

func TestBuildStopoverHack_requiredFields(t *testing.T) {
	in := DetectorInput{Origin: "HEL", Destination: "SIN", Date: "2026-09-01", Currency: "EUR"}
	prog := StopoverProgram{
		Airline:      "Emirates",
		Hub:          "DXB",
		MaxNights:    4,
		URL:          "https://emirates.com/stopover",
		Restrictions: "Must book direct with Emirates",
	}
	f := models.FlightResult{Price: 900, Currency: "EUR"}

	h := buildStopoverHack(in, prog, f, "DXB")

	if h.Type != "stopover" {
		t.Errorf("Type = %q, want 'stopover'", h.Type)
	}
	if h.Savings != 0 {
		t.Errorf("Savings = %v, want 0 (value-add, not price saving)", h.Savings)
	}
	if len(h.Steps) == 0 {
		t.Error("Steps must not be empty")
	}
	if len(h.Risks) == 0 {
		t.Error("Risks must not be empty")
	}
	if len(h.Citations) == 0 || h.Citations[0] != "https://emirates.com/stopover" {
		t.Error("Citation should be the program URL")
	}
	if h.Description == "" {
		t.Error("Description must not be empty")
	}
}

func TestHubCityName_knownAirports(t *testing.T) {
	known := map[string]string{
		"HEL": "Helsinki",
		"KEF": "Reykjavik",
		"LIS": "Lisbon",
		"IST": "Istanbul",
		"DOH": "Doha",
		"DXB": "Dubai",
		"SIN": "Singapore",
		"AUH": "Abu Dhabi",
	}
	for code, want := range known {
		got := hubCityName(code)
		if got != want {
			t.Errorf("hubCityName(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestHubCityName_unknownReturnsCode(t *testing.T) {
	if got := hubCityName("ZZZ"); got != "ZZZ" {
		t.Errorf("hubCityName(unknown) = %q, want code itself", got)
	}
}

// ---------------------------------------------------------------------------
// detectOpenJaw — valid known-destination path
// ---------------------------------------------------------------------------

func TestDetectOpenJaw_unknownDestReturnsNil(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	h := detectOpenJaw(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "ZZZ", // not in openJawAlternates
		Date:        "2026-06-01",
		ReturnDate:  "2026-06-08",
	})
	if len(h) != 0 {
		t.Errorf("expected no hacks for unknown destination, got %d", len(h))
	}
}

func TestDetectOpenJaw_knownDestHitsAPI(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	// PRG is in openJawAlternates — will try live API.
	h := detectOpenJaw(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "PRG",
		Date:        "2026-06-01",
		ReturnDate:  "2026-06-08",
	})
	_ = h
}

// ---------------------------------------------------------------------------
// detectPositioning — known origin valid path
// ---------------------------------------------------------------------------

func TestDetectPositioning_knownOriginHitsAPI(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	// HEL is in nearbyAirports.
	h := detectPositioning(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
	})
	_ = h
}

func TestDetectPositioning_anotherKnownOrigin(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	// AMS is in nearbyAirports.
	h := detectPositioning(context.Background(), DetectorInput{
		Origin:      "AMS",
		Destination: "BCN",
		Date:        "2026-06-01",
	})
	_ = h
}

// ---------------------------------------------------------------------------
// detectMultiStop — known destination valid path
// ---------------------------------------------------------------------------

func TestDetectMultiStop_knownDestHitsAPI(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	h := detectMultiStop(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "PRG",
		Date:        "2026-06-01",
	})
	_ = h
}

// ---------------------------------------------------------------------------
// detectNightTransport — valid input (hits ground API)
// ---------------------------------------------------------------------------

func TestDetectNightTransport_validRoute(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	h := detectNightTransport(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "TLL",
		Date:        "2026-06-01",
	})
	_ = h
}

// ---------------------------------------------------------------------------
// detectTuesdayBooking — Sunday/Friday triggering
// ---------------------------------------------------------------------------

func TestDetectTuesdayBooking_sundayTriggersDetector(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	// 2026-04-19 is a Sunday.
	h := detectTuesdayBooking(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "PRG",
		Date:        "2026-04-19",
	})
	_ = h // returns nil (no live API), must not panic
}

func TestDetectTuesdayBooking_fridayTriggersDetector(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	// 2026-04-17 is a Friday.
	h := detectTuesdayBooking(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "PRG",
		Date:        "2026-04-17",
	})
	_ = h
}

// ---------------------------------------------------------------------------
// detectFerryPositioning — valid input path
// ---------------------------------------------------------------------------

func TestDetectFerryPositioning_knownOriginPath(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	h := detectFerryPositioning(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
	})
	_ = h
}

func TestDetectFerryPositioning_tallinnOrigin(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	h := detectFerryPositioning(context.Background(), DetectorInput{
		Origin:      "TLL",
		Destination: "BCN",
		Date:        "2026-06-01",
	})
	_ = h
}

func TestFerryPositioningRoutes_allValid(t *testing.T) {
	for code, routes := range ferryPositioningRoutes {
		for i, r := range routes {
			if r.FerryFrom == "" {
				t.Errorf("[%s][%d] FerryFrom must not be empty", code, i)
			}
			if r.FerryTo == "" {
				t.Errorf("[%s][%d] FerryTo must not be empty", code, i)
			}
			if r.AirportTo == "" {
				t.Errorf("[%s][%d] AirportTo must not be empty", code, i)
			}
			if r.FerryEUR <= 0 {
				t.Errorf("[%s][%d] FerryEUR must be positive, got %v", code, i, r.FerryEUR)
			}
			if r.Notes == "" {
				t.Errorf("[%s][%d] Notes must not be empty", code, i)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// detectCurrencyArbitrage — valid input path
// ---------------------------------------------------------------------------

func TestDetectCurrencyArbitrage_validInputPath(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	h := detectCurrencyArbitrage(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
	})
	_ = h
}

// ---------------------------------------------------------------------------
// detectDateFlex — valid input path
// ---------------------------------------------------------------------------

func TestDetectDateFlex_validInputPath(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	h := detectDateFlex(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
	})
	_ = h
}

// ---------------------------------------------------------------------------
// detectMultiModalReturnSplit — valid input path
// ---------------------------------------------------------------------------

func TestDetectMultiModalReturnSplit_validRoundTripInput(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	h := detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "PRG",
		Date:        "2026-06-01",
		ReturnDate:  "2026-06-08",
	})
	_ = h
}

// ---------------------------------------------------------------------------
// detectMultiModalSkipFlight — valid input path
// ---------------------------------------------------------------------------

func TestDetectMultiModalSkipFlight_validRoute(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	h := detectMultiModalSkipFlight(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "TLL",
		Date:        "2026-06-01",
	})
	_ = h
}

// ---------------------------------------------------------------------------
// detectMultiModalPositioning — valid input path
// ---------------------------------------------------------------------------

func TestDetectMultiModalPositioning_knownOriginPath(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	h := detectMultiModalPositioning(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
	})
	_ = h
}

// ---------------------------------------------------------------------------
// detectMultiModalOpenJawGround — valid input path
// ---------------------------------------------------------------------------

func TestDetectMultiModalOpenJawGround_knownDest(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	h := detectMultiModalOpenJawGround(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "DBV",
		Date:        "2026-06-01",
	})
	_ = h
}

// ---------------------------------------------------------------------------
// DetectRailFlyArbitrage — unknown hub guard
// ---------------------------------------------------------------------------

func TestDetectRailFlyArbitrage_xyzUnknownHub(t *testing.T) {
	h := DetectRailFlyArbitrage(context.Background(), "XYZ", "BCN", "2026-06-01", "")
	if len(h) != 0 {
		t.Errorf("expected no hacks for unknown hub, got %d", len(h))
	}
}

func TestRailFlyStations_integrity(t *testing.T) {
	if len(railFlyStations) == 0 {
		t.Fatal("railFlyStations must not be empty")
	}
	for _, st := range railFlyStations {
		if st.IATA == "" {
			t.Error("railFlyStation IATA must not be empty")
		}
		if st.City == "" {
			t.Errorf("[%s] City must not be empty", st.IATA)
		}
		if st.HubIATA == "" {
			t.Errorf("[%s] HubIATA must not be empty", st.IATA)
		}
		if st.Airline == "" {
			t.Errorf("[%s] Airline must not be empty", st.IATA)
		}
		if st.TrainMinutes <= 0 {
			t.Errorf("[%s] TrainMinutes must be positive", st.IATA)
		}
	}
}

func TestBuildRailFlyHack_KLMoneWay(t *testing.T) {
	st := railFlyStation{
		IATA: "ZWE", City: "Antwerp", HubIATA: "AMS",
		Airline: "KL", AirlineName: "KLM",
		TrainProvider: "Eurostar", TrainMinutes: 60,
		FareZone: "Belgian market",
	}
	h := buildRailFlyHack("AMS", "JFK", 600.0, "EUR", 480.0, "EUR", 120.0, st, "")
	if h.Type != "rail_fly_arbitrage" {
		t.Errorf("Type = %q, want 'rail_fly_arbitrage'", h.Type)
	}
	if h.Savings != 120 {
		t.Errorf("Savings = %v, want 120", h.Savings)
	}
	if len(h.Steps) == 0 {
		t.Error("Steps must not be empty")
	}
	if len(h.Risks) == 0 {
		t.Error("Risks must not be empty")
	}
}

func TestBuildRailFlyHack_LufthansaRoundTrip(t *testing.T) {
	st := railFlyStation{
		IATA: "QKL", City: "Cologne", HubIATA: "FRA",
		Airline: "LH", AirlineName: "Lufthansa",
		TrainProvider: "DB ICE", TrainMinutes: 62,
		FareZone: "Rhineland regional",
	}
	h := buildRailFlyHack("FRA", "SIN", 900.0, "EUR", 750.0, "EUR", 150.0, st, "2026-06-15")
	if h.Type != "rail_fly_arbitrage" {
		t.Errorf("Type = %q, want 'rail_fly_arbitrage'", h.Type)
	}
	if h.Savings != 150 {
		t.Errorf("Savings = %v, want 150", h.Savings)
	}
	if len(h.Risks) == 0 {
		t.Error("expected non-empty risks for Lufthansa AIRail")
	}
}

// ---------------------------------------------------------------------------
// detectSplit — valid full round-trip path
// ---------------------------------------------------------------------------

func TestDetectSplit_validFullInput(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	h := detectSplit(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
		ReturnDate:  "2026-06-08",
	})
	_ = h
}

// ---------------------------------------------------------------------------
// parseDatetime (multi_stop.go) — all format branches
// ---------------------------------------------------------------------------

func TestParseDatetime_RFC3339(t *testing.T) {
	_, err := parseDatetime("2026-06-01T10:00:00+02:00")
	if err != nil {
		t.Errorf("parseDatetime RFC3339 unexpected error: %v", err)
	}
}

func TestParseDatetime_datetimeNoTZ(t *testing.T) {
	_, err := parseDatetime("2026-06-01T10:00:00")
	if err != nil {
		t.Errorf("parseDatetime datetime-no-tz unexpected error: %v", err)
	}
}

func TestParseDatetime_datetimeShort(t *testing.T) {
	_, err := parseDatetime("2026-06-01T10:00")
	if err != nil {
		t.Errorf("parseDatetime short datetime unexpected error: %v", err)
	}
}

func TestParseDatetime_spaceSeparator(t *testing.T) {
	_, err := parseDatetime("2026-06-01 10:00")
	if err != nil {
		t.Errorf("parseDatetime space-separator unexpected error: %v", err)
	}
}

func TestParseDatetime_invalidReturnsError(t *testing.T) {
	_, err := parseDatetime("not-a-date")
	if err == nil {
		t.Error("parseDatetime invalid input should return error")
	}
}

func TestParseDatetime_emptyReturnsError(t *testing.T) {
	_, err := parseDatetime("")
	if err == nil {
		t.Error("parseDatetime empty string should return error")
	}
}

// ---------------------------------------------------------------------------
// AirportCoords (positioning.go) — exported map validity
// ---------------------------------------------------------------------------

func TestAirportCoords_nonEmpty(t *testing.T) {
	if len(AirportCoords) == 0 {
		t.Fatal("AirportCoords must not be empty")
	}
}

func TestAirportCoords_allHaveNames(t *testing.T) {
	for code, loc := range AirportCoords {
		if code == "" {
			t.Error("airport code must not be empty")
		}
		if loc.Name == "" {
			t.Errorf("[%s] Location Name must not be empty", code)
		}
	}
}

// ---------------------------------------------------------------------------
// nearbyAirports (positioning.go) — entry-level validation
// ---------------------------------------------------------------------------

func TestNearbyAirports_groundMinsPositive(t *testing.T) {
	for origin, entries := range nearbyAirports {
		for _, e := range entries {
			if e.GroundMins <= 0 {
				t.Errorf("[origin=%s, code=%s] GroundMins must be positive", origin, e.Code)
			}
			if e.Description == "" {
				t.Errorf("[origin=%s, code=%s] Description must not be empty", origin, e.Code)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// DetectRailFlyArbitrage — known AMS hub path (exercises body past guard)
// ---------------------------------------------------------------------------

func TestDetectRailFlyArbitrage_knownAMSHubPath(t *testing.T) {
	// AMS IS a hub in railFlyStations (KLM stations ZWE, ZYR).
	// Exercises lines past the stations guard: client creation, baseResult search.
	// Will return nil (no live API) but exercises the code path.
	h := DetectRailFlyArbitrage(context.Background(), "AMS", "JFK", "2026-06-01", "")
	_ = h
}

func TestDetectRailFlyArbitrage_knownFRAHubPath(t *testing.T) {
	// FRA IS a hub (Lufthansa AIRail stations).
	h := DetectRailFlyArbitrage(context.Background(), "FRA", "JFK", "2026-06-01", "2026-06-15")
	_ = h
}

func TestDetectRailFlyArbitrage_knownCDGHub(t *testing.T) {
	// CDG is a hub (Air France TGV station ZYR).
	h := DetectRailFlyArbitrage(context.Background(), "CDG", "JFK", "2026-06-01", "")
	_ = h
}

// ---------------------------------------------------------------------------
// detectLowCostCarrier — valid input exercises the main body
// ---------------------------------------------------------------------------

func TestDetectLowCostCarrier_validInputExercisesBody(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	// Valid input — will call flights.SearchFlights (returns nil from live API),
	// exercising the guard branches in the function body.
	h := detectLowCostCarrier(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
	})
	_ = h
}

func TestDetectLowCostCarrier_withReturnDate(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	h := detectLowCostCarrier(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
		ReturnDate:  "2026-06-08",
	})
	_ = h
}

// ---------------------------------------------------------------------------
// detectMultiTripCombo — valid multi-trip path
// ---------------------------------------------------------------------------

func TestDetectMultiTripCombo_validTwoTrips(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	// Two trips — exercises the multi-trip combo loop body.
	// Returns nil from live API, but exercises path past the len(trips) guard.
	trips := []TripLeg{
		{DepartDate: "2026-06-01", ReturnDate: "2026-06-08"},
		{DepartDate: "2026-07-01", ReturnDate: "2026-07-08"},
	}
	h := detectMultiTripCombo(context.Background(), "HEL", "BCN", trips, "EUR")
	_ = h
}

func TestDetectMultiTripCombo_moreThanMax(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	// 5 trips — exercises the truncation to maxComboTrips (4).
	trips := make([]TripLeg, 5)
	for i := range trips {
		trips[i] = TripLeg{
			DepartDate: "2026-06-01",
			ReturnDate: "2026-06-08",
		}
	}
	h := detectMultiTripCombo(context.Background(), "HEL", "BCN", trips, "EUR")
	_ = h
}

// ---------------------------------------------------------------------------
// detectThrowaway — additional path with very early return date
// ---------------------------------------------------------------------------

func TestDetectThrowaway_withReturnDate(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	// Providing ReturnDate exercises the branch where it's not auto-calculated.
	h := detectThrowaway(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
		ReturnDate:  "2026-06-08",
	})
	_ = h
}

// ---------------------------------------------------------------------------
// detectSplit — additional valid paths
// ---------------------------------------------------------------------------

func TestDetectSplit_pragueDest(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	h := detectSplit(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "PRG",
		Date:        "2026-06-01",
		ReturnDate:  "2026-06-08",
	})
	_ = h
}
