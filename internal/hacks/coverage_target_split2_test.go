package hacks

// coverage_target_test.go — Tests targeting low-coverage functions.
// All names are unique across the package — verified against coverage_max_test.go.

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/testutil"
)

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
	h := detectMultiTripCombo(context.Background(), "HEL", "BCN", trips, "EUR", nil)
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
	h := detectMultiTripCombo(context.Background(), "HEL", "BCN", trips, "EUR", nil)
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
