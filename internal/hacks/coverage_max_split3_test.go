package hacks

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

func TestIsDirectLCCRoute_unknownPair(t *testing.T) {
	if isDirectLCCRoute("HEL", "XXX") {
		t.Error("expected false for unknown pair")
	}
}

func TestIsDirectLCCRoute_sameAirport(t *testing.T) {
	if isDirectLCCRoute("STN", "STN") {
		t.Error("expected false for same airport")
	}
}

func TestIsDirectLCCRoute_originInMapDestNot(t *testing.T) {
	// STN is in lccDirectRoutes but HEL is not a dest for STN.
	if isDirectLCCRoute("STN", "HEL") {
		t.Error("expected false for STN→HEL (not in LCC routes)")
	}
}

func TestIsDirectLCCRoute_reverseOnlyMatch(t *testing.T) {
	// DUB→STN: DUB has STN in its map.
	if !isDirectLCCRoute("DUB", "STN") {
		t.Error("expected true for DUB→STN")
	}
}

// --- primaryAirlineCode with various results ---

func TestPrimaryAirlineCode_unsuccessfulResult(t *testing.T) {
	result := &models.FlightSearchResult{Success: false}
	if code := primaryAirlineCode(result); code != "" {
		t.Errorf("expected empty for unsuccessful result, got %q", code)
	}
}

func TestPrimaryAirlineCode_noLegs(t *testing.T) {
	result := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Price: 100},
		},
	}
	if code := primaryAirlineCode(result); code != "" {
		t.Errorf("expected empty for no legs, got %q", code)
	}
}

func TestPrimaryAirlineCode_findsFirstLeg(t *testing.T) {
	result := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{
				Price: 100,
				Legs: []models.FlightLeg{
					{AirlineCode: "AY", Airline: "Finnair"},
				},
			},
		},
	}
	if code := primaryAirlineCode(result); code != "AY" {
		t.Errorf("expected AY, got %q", code)
	}
}

// --- routesThroughDestination with various inputs ---

func TestRoutesThroughDestination_unsuccessfulResult(t *testing.T) {
	result := &models.FlightSearchResult{Success: false}
	if routesThroughDestination(result, "AMS") {
		t.Error("expected false for unsuccessful result")
	}
}

func TestRoutesThroughDestination_singleLeg(t *testing.T) {
	result := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{
				Legs: []models.FlightLeg{
					{ArrivalAirport: models.AirportInfo{Code: "AMS"}},
				},
			},
		},
	}
	// Single-leg flights can't be hidden-city, but function returns true optimistically
	// when no multi-leg flight with intermediate stop is found.
	if !routesThroughDestination(result, "AMS") {
		t.Error("expected true (optimistic) for single-leg flight")
	}
}

func TestRoutesThroughDestination_multiLegMatch(t *testing.T) {
	result := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{
				Legs: []models.FlightLeg{
					{ArrivalAirport: models.AirportInfo{Code: "AMS"}},
					{ArrivalAirport: models.AirportInfo{Code: "HEL"}},
				},
			},
		},
	}
	if !routesThroughDestination(result, "AMS") {
		t.Error("expected true: flight stops at AMS as intermediate")
	}
}

func TestRoutesThroughDestination_multiLegNoMatch(t *testing.T) {
	result := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{
				Legs: []models.FlightLeg{
					{ArrivalAirport: models.AirportInfo{Code: "FRA"}},
					{ArrivalAirport: models.AirportInfo{Code: "HEL"}},
				},
			},
		},
	}
	// No intermediate stop at AMS, but optimistic return for non-empty flights.
	if !routesThroughDestination(result, "AMS") {
		t.Error("expected true (optimistic fallback)")
	}
}

func TestRoutesThroughDestination_emptyFlights(t *testing.T) {
	result := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{},
	}
	if routesThroughDestination(result, "AMS") {
		t.Error("expected false for empty flights")
	}
}

// --- isHomeAirport with matches ---

func TestIsHomeAirport_found(t *testing.T) {
	prefs := &preferences.Preferences{
		HomeAirports: []string{"HEL", "TLL"},
	}
	if !isHomeAirport("HEL", prefs) {
		t.Error("expected true for HEL in home airports")
	}
}

func TestIsHomeAirport_notFound(t *testing.T) {
	prefs := &preferences.Preferences{
		HomeAirports: []string{"HEL", "TLL"},
	}
	if isHomeAirport("AMS", prefs) {
		t.Error("expected false for AMS not in home airports")
	}
}

func TestIsHomeAirport_emptyHomeAirports(t *testing.T) {
	prefs := &preferences.Preferences{
		HomeAirports: []string{},
	}
	if isHomeAirport("HEL", prefs) {
		t.Error("expected false for empty home airports")
	}
}

// --- groundCostBetween known pairs ---

func TestGroundCostBetween_knownPair(t *testing.T) {
	v := groundCostBetween("AMS", "BRU")
	if v != 20 {
		t.Errorf("expected 20 for AMS→BRU, got %.0f", v)
	}
}

func TestGroundCostBetween_knownPairCPHARN(t *testing.T) {
	v := groundCostBetween("CPH", "ARN")
	if v != 30 {
		t.Errorf("expected 30 for CPH→ARN, got %.0f", v)
	}
}

func TestGroundCostBetween_knownPairBCNMAD(t *testing.T) {
	v := groundCostBetween("BCN", "MAD")
	if v != 30 {
		t.Errorf("expected 30 for BCN→MAD, got %.0f", v)
	}
}

func TestGroundCostBetween_reverseAMSDUS(t *testing.T) {
	// Forward: AMS→DUS=20, reverse should also work.
	v := groundCostBetween("DUS", "AMS")
	if v != 20 {
		t.Errorf("expected 20 for DUS→AMS (reverse), got %.0f", v)
	}
}

// --- cancelled context tests for API-dependent detectors ---
// These test that a cancelled context causes the detector to return nil
// (covering the error path after the API call).

func TestDetectDateFlex_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	h := detectDateFlex(ctx, DetectorInput{
		Date:        "2026-06-15",
		Origin:      "HEL",
		Destination: "BCN",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

func TestDetectCurrencyArbitrage_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := detectCurrencyArbitrage(ctx, DetectorInput{
		Date:        "2026-06-15",
		Origin:      "HEL",
		Destination: "BCN",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

func TestDetectHiddenCity_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := detectHiddenCity(ctx, DetectorInput{
		Date:        "2026-06-15",
		Origin:      "HEL",
		Destination: "AMS",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

func TestDetectLowCostCarrier_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := detectLowCostCarrier(ctx, DetectorInput{
		Date:        "2026-06-15",
		Origin:      "HEL",
		Destination: "BCN",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

func TestDetectPositioning_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := detectPositioning(ctx, DetectorInput{
		Date:        "2026-06-15",
		Origin:      "HEL",
		Destination: "BCN",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

func TestDetectSplit_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := detectSplit(ctx, DetectorInput{
		Date:        "2026-06-15",
		ReturnDate:  "2026-06-22",
		Origin:      "HEL",
		Destination: "BCN",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

func TestDetectThrowaway_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := detectThrowaway(ctx, DetectorInput{
		Date:        "2026-06-15",
		Origin:      "HEL",
		Destination: "BCN",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

func TestDetectOpenJaw_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := detectOpenJaw(ctx, DetectorInput{
		Date:        "2026-06-15",
		ReturnDate:  "2026-06-22",
		Origin:      "HEL",
		Destination: "PRG",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

func TestDetectStopover_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := detectStopover(ctx, DetectorInput{
		Date:        "2026-06-15",
		Origin:      "HEL",
		Destination: "BCN",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

func TestDetectNightTransport_ReturnsNothingWhenCancelled(t *testing.T) {
	// A cancelled context stops every ground search before it dials out
	// (MIK-8107); the detector must then report no hack.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if h := detectNightTransport(ctx, DetectorInput{
		Date:        "2026-06-15",
		Origin:      "AMS",
		Destination: "BCN",
	}); len(h) != 0 {
		t.Errorf("expected no hack when cancelled, got %v", h)
	}
}

func TestDetectFerryPositioning_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := detectFerryPositioning(ctx, DetectorInput{
		Date:        "2026-06-15",
		Origin:      "HEL",
		Destination: "BCN",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

func TestDetectMultiStop_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := detectMultiStop(ctx, DetectorInput{
		Date:        "2026-06-15",
		Origin:      "HEL",
		Destination: "PRG",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

func TestDetectTuesdayBooking_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := detectTuesdayBooking(ctx, DetectorInput{
		Date:        "2026-06-19", // Friday
		Origin:      "HEL",
		Destination: "BCN",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

func TestDetectMultiModalSkipFlight_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := detectMultiModalSkipFlight(ctx, DetectorInput{
		Date:        "2026-06-15",
		Origin:      "HEL",
		Destination: "TLL",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

func TestDetectMultiModalPositioning_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := detectMultiModalPositioning(ctx, DetectorInput{
		Date:        "2026-06-15",
		Origin:      "HEL",
		Destination: "BCN",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

func TestDetectMultiModalOpenJawGround_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := detectMultiModalOpenJawGround(ctx, DetectorInput{
		Date:        "2026-06-15",
		Origin:      "HEL",
		Destination: "DBV",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

func TestDetectMultiModalReturnSplit_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := detectMultiModalReturnSplit(ctx, DetectorInput{
		Date:        "2026-06-15",
		ReturnDate:  "2026-06-22",
		Origin:      "HEL",
		Destination: "TLL",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

// --- DetectRailFlyArbitrage edge cases ---

func TestDetectRailFlyArbitrage_emptyOrigin(t *testing.T) {
	h := DetectRailFlyArbitrage(context.Background(), "", "BCN", "2026-06-15", "")
	if h != nil {
		t.Error("expected nil for empty origin")
	}
}

func TestDetectRailFlyArbitrage_emptyDestination(t *testing.T) {
	h := DetectRailFlyArbitrage(context.Background(), "AMS", "", "2026-06-15", "")
	if h != nil {
		t.Error("expected nil for empty destination")
	}
}

func TestDetectRailFlyArbitrage_emptyDate(t *testing.T) {
	h := DetectRailFlyArbitrage(context.Background(), "AMS", "BCN", "", "")
	if h != nil {
		t.Error("expected nil for empty date")
	}
}

func TestDetectRailFlyArbitrage_noStationsForOrigin(t *testing.T) {
	h := DetectRailFlyArbitrage(context.Background(), "BCN", "HEL", "2026-06-15", "")
	if h != nil {
		t.Error("expected nil for origin without rail stations")
	}
}

func TestDetectRailFlyArbitrage_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := DetectRailFlyArbitrage(ctx, "AMS", "BCN", "2026-06-15", "")
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}

// --- isLowCostFlight and lccName with more cases ---

func TestIsLowCostFlight_noLegs(t *testing.T) {
	f := models.FlightResult{Price: 100}
	if isLowCostFlight(f) {
		t.Error("expected false for flight with no legs")
	}
}

func TestIsLowCostFlight_legacyCarrier(t *testing.T) {
	f := models.FlightResult{
		Legs: []models.FlightLeg{{AirlineCode: "BA"}},
	}
	if isLowCostFlight(f) {
		t.Error("expected false for legacy carrier BA")
	}
}

func TestIsLowCostFlight_mixedLegs(t *testing.T) {
	f := models.FlightResult{
		Legs: []models.FlightLeg{
			{AirlineCode: "BA"},
			{AirlineCode: "FR"},
		},
	}
	if !isLowCostFlight(f) {
		t.Error("expected true when one leg is LCC (FR)")
	}
}

func TestLccName_noLCCLegs(t *testing.T) {
	f := models.FlightResult{
		Legs: []models.FlightLeg{{AirlineCode: "BA"}},
	}
	if got := lccName(f); got != "LCC" {
		t.Errorf("expected fallback 'LCC', got %q", got)
	}
}

func TestLccName_wizz(t *testing.T) {
	f := models.FlightResult{
		Legs: []models.FlightLeg{{AirlineCode: "W6"}},
	}
	if got := lccName(f); got != "Wizz Air" {
		t.Errorf("expected 'Wizz Air', got %q", got)
	}
}

// --- loyaltyConflictNote edge cases ---

func TestLoyaltyConflictNote_emptyLoyalty(t *testing.T) {
	result := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Legs: []models.FlightLeg{{AirlineCode: "AY", Airline: "Finnair"}}},
		},
	}
	prefs := &preferences.Preferences{
		LoyaltyAirlines: []string{},
	}
	if got := loyaltyConflictNote(result, prefs); got != "" {
		t.Errorf("expected empty for no loyalty airlines, got %q", got)
	}
}

func TestLoyaltyConflictNote_noConflict(t *testing.T) {
	result := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Legs: []models.FlightLeg{{AirlineCode: "AY", Airline: "Finnair"}}},
		},
	}
	prefs := &preferences.Preferences{
		LoyaltyAirlines: []string{"BA"},
	}
	if got := loyaltyConflictNote(result, prefs); got != "" {
		t.Errorf("expected empty for non-matching loyalty, got %q", got)
	}
}

func TestLoyaltyConflictNote_conflict(t *testing.T) {
	result := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Legs: []models.FlightLeg{{AirlineCode: "AY", Airline: "Finnair"}}},
		},
	}
	prefs := &preferences.Preferences{
		LoyaltyAirlines: []string{"AY"},
	}
	got := loyaltyConflictNote(result, prefs)
	if got == "" {
		t.Error("expected loyalty conflict note")
	}
}

// --- DetectFlightCombo cancelled context ---

func TestDetectFlightCombo_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := DetectFlightCombo(ctx, FlightComboInput{
		Origin:      "HEL",
		Destination: "BCN",
		Trips: []TripLeg{
			{DepartDate: "2026-06-15", ReturnDate: "2026-06-22"},
		},
		Currency: "EUR",
	})
	if h != nil {
		t.Error("expected nil for cancelled context")
	}
}
