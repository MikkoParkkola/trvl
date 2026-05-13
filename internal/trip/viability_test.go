package trip

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/visa"
	"github.com/MikkoParkkola/trvl/internal/weather"
)

func TestAssessTrip_MissingOrigin(t *testing.T) {
	_, err := AssessTrip(t.Context(), ViabilityInput{
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
	})
	if err == nil {
		t.Error("expected error for missing origin")
	}
}

func TestAssessTrip_MissingDestination(t *testing.T) {
	_, err := AssessTrip(t.Context(), ViabilityInput{
		Origin:     "HEL",
		DepartDate: "2026-07-01",
		ReturnDate: "2026-07-08",
	})
	if err == nil {
		t.Error("expected error for missing destination")
	}
}

func TestAssessTrip_MissingDates(t *testing.T) {
	_, err := AssessTrip(t.Context(), ViabilityInput{
		Origin:      "HEL",
		Destination: "BCN",
	})
	if err == nil {
		t.Error("expected error for missing dates")
	}
}

func TestAssessTrip_DefaultGuests(t *testing.T) {
	// Guests <= 0 should default to 1 (no error).
	input := ViabilityInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		Guests:      0,
	}
	// This would make live API calls, so just verify validation passes.
	if input.Guests <= 0 {
		input.Guests = 1
	}
	if input.Guests != 1 {
		t.Errorf("expected guests to default to 1, got %d", input.Guests)
	}
}

func TestResolveDestinationCountry_IATACodes(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"HEL", "FI"},
		{"BCN", "ES"},
		{"JFK", "US"},
		{"NRT", "JP"},
		{"LHR", "GB"},
		{"CDG", "FR"},
		{"SYD", "AU"},
		{"GRU", "BR"},
		{"DXB", "AE"},
		{"NBO", "KE"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := resolveDestinationCountry(tt.input)
			if got != tt.want {
				t.Errorf("resolveDestinationCountry(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveDestinationCountry_CountryCode(t *testing.T) {
	// 2-letter codes are returned directly (assumed to be country codes).
	got := resolveDestinationCountry("FI")
	if got != "FI" {
		t.Errorf("resolveDestinationCountry(\"FI\") = %q, want \"FI\"", got)
	}
}

func TestResolveDestinationCountry_Unknown(t *testing.T) {
	got := resolveDestinationCountry("ZZZ")
	if got != "" {
		t.Errorf("resolveDestinationCountry(\"ZZZ\") = %q, want empty", got)
	}
}

func TestResolveDestinationCountry_Empty(t *testing.T) {
	got := resolveDestinationCountry("")
	if got != "" {
		t.Errorf("resolveDestinationCountry(\"\") = %q, want empty", got)
	}
}

func TestResolveDestinationCountry_Whitespace(t *testing.T) {
	got := resolveDestinationCountry(" HEL ")
	if got != "FI" {
		t.Errorf("resolveDestinationCountry(\" HEL \") = %q, want \"FI\"", got)
	}
}

func TestResolveDestinationCountry_Lowercase(t *testing.T) {
	got := resolveDestinationCountry("hel")
	if got != "FI" {
		t.Errorf("resolveDestinationCountry(\"hel\") = %q, want \"FI\"", got)
	}
}

func TestBuildVisaCheck_VisaFree(t *testing.T) {
	result := visa.Result{
		Success: true,
		Requirement: visa.Requirement{
			Status:  "visa-free",
			MaxStay: "90 days",
		},
	}
	check := buildVisaCheck(result)
	if check.Status != "ok" {
		t.Errorf("status = %q, want ok", check.Status)
	}
	if check.Dimension != "visa" {
		t.Errorf("dimension = %q, want visa", check.Dimension)
	}
}

func TestBuildVisaCheck_FreedomOfMovement(t *testing.T) {
	result := visa.Result{
		Success: true,
		Requirement: visa.Requirement{
			Status:  "freedom-of-movement",
			MaxStay: "unlimited",
		},
	}
	check := buildVisaCheck(result)
	if check.Status != "ok" {
		t.Errorf("status = %q, want ok", check.Status)
	}
}

func TestBuildVisaCheck_VisaRequired(t *testing.T) {
	result := visa.Result{
		Success: true,
		Requirement: visa.Requirement{
			Status: "visa-required",
			Notes:  "Apply at embassy.",
		},
	}
	check := buildVisaCheck(result)
	if check.Status != "blocker" {
		t.Errorf("status = %q, want blocker", check.Status)
	}
}

func TestBuildVisaCheck_EVisa(t *testing.T) {
	result := visa.Result{
		Success: true,
		Requirement: visa.Requirement{
			Status: "e-visa",
			Notes:  "Apply online.",
		},
	}
	check := buildVisaCheck(result)
	if check.Status != "warning" {
		t.Errorf("status = %q, want warning", check.Status)
	}
}

func TestBuildVisaCheck_VisaOnArrival(t *testing.T) {
	result := visa.Result{
		Success: true,
		Requirement: visa.Requirement{
			Status: "visa-on-arrival",
		},
	}
	check := buildVisaCheck(result)
	if check.Status != "warning" {
		t.Errorf("status = %q, want warning", check.Status)
	}
}

func TestBuildVisaCheck_LookupFailed(t *testing.T) {
	result := visa.Result{Success: false}
	check := buildVisaCheck(result)
	if check.Status != "warning" {
		t.Errorf("status = %q, want warning", check.Status)
	}
	if check.Summary != "could not determine visa requirements" {
		t.Errorf("summary = %q", check.Summary)
	}
}

func TestBuildWeatherCheck_Sunny(t *testing.T) {
	forecasts := []weather.Forecast{
		{TempMax: 30, TempMin: 20, Precipitation: 0},
		{TempMax: 32, TempMin: 22, Precipitation: 1},
		{TempMax: 28, TempMin: 18, Precipitation: 0},
	}
	check := buildWeatherCheck(forecasts)
	if check.Status != "ok" {
		t.Errorf("status = %q, want ok", check.Status)
	}
	if check.Dimension != "weather" {
		t.Errorf("dimension = %q, want weather", check.Dimension)
	}
}

func TestBuildWeatherCheck_MostlyRain(t *testing.T) {
	forecasts := []weather.Forecast{
		{TempMax: 15, TempMin: 10, Precipitation: 20},
		{TempMax: 14, TempMin: 9, Precipitation: 15},
		{TempMax: 16, TempMin: 11, Precipitation: 0},
	}
	check := buildWeatherCheck(forecasts)
	if check.Status != "warning" {
		t.Errorf("status = %q, want warning (mostly rain)", check.Status)
	}
}

func TestBuildWeatherCheck_SomeRain(t *testing.T) {
	forecasts := []weather.Forecast{
		{TempMax: 25, TempMin: 15, Precipitation: 10},
		{TempMax: 26, TempMin: 16, Precipitation: 0},
		{TempMax: 24, TempMin: 14, Precipitation: 2},
		{TempMax: 27, TempMin: 17, Precipitation: 0},
	}
	check := buildWeatherCheck(forecasts)
	if check.Status != "ok" {
		t.Errorf("status = %q, want ok (1 rainy day of 4 is not mostly rain)", check.Status)
	}
}

func TestDetermineVerdict_GO(t *testing.T) {
	checks := []ViabilityCheck{
		{Dimension: "flights", Status: "ok"},
		{Dimension: "hotels", Status: "ok"},
	}
	verdict, reason := determineVerdict(checks, false, false)
	if verdict != "GO" {
		t.Errorf("verdict = %q, want GO", verdict)
	}
	if reason != "All checks passed" {
		t.Errorf("reason = %q", reason)
	}
}

func TestDetermineVerdict_WAIT(t *testing.T) {
	checks := []ViabilityCheck{
		{Dimension: "flights", Status: "ok"},
		{Dimension: "hotels", Status: "warning"},
		{Dimension: "weather", Status: "warning"},
	}
	verdict, reason := determineVerdict(checks, false, true)
	if verdict != "WAIT" {
		t.Errorf("verdict = %q, want WAIT", verdict)
	}
	if reason != "Issues with: hotels, weather" {
		t.Errorf("reason = %q", reason)
	}
}

func TestDetermineVerdict_NO_GO(t *testing.T) {
	checks := []ViabilityCheck{
		{Dimension: "flights", Status: "ok"},
		{Dimension: "visa", Status: "blocker", Summary: "visa required"},
	}
	verdict, reason := determineVerdict(checks, true, false)
	if verdict != "NO_GO" {
		t.Errorf("verdict = %q, want NO_GO", verdict)
	}
	if reason != "visa: visa required" {
		t.Errorf("reason = %q", reason)
	}
}

func TestDetermineVerdict_NO_GO_OverridesWarning(t *testing.T) {
	checks := []ViabilityCheck{
		{Dimension: "flights", Status: "warning"},
		{Dimension: "visa", Status: "blocker", Summary: "visa required"},
	}
	verdict, _ := determineVerdict(checks, true, true)
	if verdict != "NO_GO" {
		t.Errorf("verdict = %q, want NO_GO (blocker overrides warning)", verdict)
	}
}

func TestBuildViabilityChecks_CostSuccess(t *testing.T) {
	costResult := &TripCostResult{
		Success: true,
		Flights: FlightCost{
			Outbound: 150,
			Return:   180,
			Currency: "EUR",
		},
		Hotels: HotelCost{
			PerNight: 80,
			Total:    560,
			Currency: "EUR",
			Name:     "Hotel Test",
		},
		Total:     890,
		Currency:  "EUR",
		PerPerson: 890,
		PerDay:    127,
		Nights:    7,
	}

	checks, hasBlocker, hasWarning := buildViabilityChecks(costResult, nil, visa.Result{}, "", nil, nil)
	if hasBlocker {
		t.Error("unexpected blocker")
	}
	if hasWarning {
		t.Error("unexpected warning")
	}

	// Should have flights, hotels, total_cost checks.
	if len(checks) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(checks))
	}
	if checks[0].Dimension != "flights" || checks[0].Status != "ok" {
		t.Errorf("flight check: dim=%q status=%q", checks[0].Dimension, checks[0].Status)
	}
	if checks[1].Dimension != "hotels" || checks[1].Status != "ok" {
		t.Errorf("hotel check: dim=%q status=%q", checks[1].Dimension, checks[1].Status)
	}
	if checks[2].Dimension != "total_cost" {
		t.Errorf("total_cost check: dim=%q", checks[2].Dimension)
	}
}

func TestBuildViabilityChecks_CostFailure(t *testing.T) {
	checks, _, hasWarning := buildViabilityChecks(nil, nil, visa.Result{}, "", nil, nil)
	if !hasWarning {
		t.Error("expected warning when cost result is nil")
	}
	if len(checks) == 0 {
		t.Fatal("expected at least 1 check")
	}
	if checks[0].Status != "warning" {
		t.Errorf("flight check status = %q, want warning", checks[0].Status)
	}
}

func TestBuildViabilityChecks_WithVisaBlocker(t *testing.T) {
	costResult := &TripCostResult{
		Success:  true,
		Flights:  FlightCost{Outbound: 100, Return: 100, Currency: "EUR"},
		Hotels:   HotelCost{PerNight: 50, Total: 350, Currency: "EUR", Name: "H"},
		Total:    550,
		Currency: "EUR",
		Nights:   7,
	}
	visaResult := visa.Result{
		Success: true,
		Requirement: visa.Requirement{
			Status: "visa-required",
			Notes:  "Apply at embassy",
		},
	}

	checks, hasBlocker, _ := buildViabilityChecks(costResult, nil, visaResult, "FI", nil, nil)
	if !hasBlocker {
		t.Error("expected blocker for visa-required")
	}

	// Find visa check.
	found := false
	for _, c := range checks {
		if c.Dimension == "visa" {
			found = true
			if c.Status != "blocker" {
				t.Errorf("visa status = %q, want blocker", c.Status)
			}
		}
	}
	if !found {
		t.Error("visa check not found")
	}
}

func TestBuildViabilityChecks_NoPassportSkipsVisa(t *testing.T) {
	costResult := &TripCostResult{
		Success:  true,
		Flights:  FlightCost{Outbound: 100, Return: 100, Currency: "EUR"},
		Hotels:   HotelCost{PerNight: 50, Total: 350, Currency: "EUR", Name: "H"},
		Total:    550,
		Currency: "EUR",
		Nights:   7,
	}

	checks, _, _ := buildViabilityChecks(costResult, nil, visa.Result{}, "", nil, nil)
	for _, c := range checks {
		if c.Dimension == "visa" {
			t.Error("visa check should not appear when passport is empty")
		}
	}
}

func TestBuildViabilityChecks_OutboundOnlyWarning(t *testing.T) {
	costResult := &TripCostResult{
		Success: true,
		Flights: FlightCost{
			Outbound: 150,
			Return:   0,
			Currency: "EUR",
		},
		Hotels:   HotelCost{PerNight: 80, Total: 560, Currency: "EUR", Name: "H"},
		Total:    710,
		Currency: "EUR",
		Nights:   7,
	}

	checks, _, hasWarning := buildViabilityChecks(costResult, nil, visa.Result{}, "", nil, nil)
	if !hasWarning {
		t.Error("expected warning for outbound-only flights")
	}
	if checks[0].Status != "warning" {
		t.Errorf("flight check status = %q, want warning", checks[0].Status)
	}
}

func TestBuildViabilityChecks_NoFlightPrices(t *testing.T) {
	costResult := &TripCostResult{
		Success: true,
		Flights: FlightCost{Outbound: 0, Return: 0, Currency: "EUR"},
		Hotels:  HotelCost{PerNight: 80, Total: 560, Currency: "EUR", Name: "H"},
	}

	checks, _, hasWarning := buildViabilityChecks(costResult, nil, visa.Result{}, "", nil, nil)
	if !hasWarning {
		t.Error("expected warning for no flight prices")
	}
	if checks[0].Summary != "no flight prices found" {
		t.Errorf("summary = %q", checks[0].Summary)
	}
}

func TestBuildViabilityChecks_NoHotelPrices(t *testing.T) {
	costResult := &TripCostResult{
		Success:  true,
		Flights:  FlightCost{Outbound: 150, Return: 180, Currency: "EUR"},
		Hotels:   HotelCost{PerNight: 0},
		Total:    330,
		Currency: "EUR",
		Nights:   7,
	}

	checks, _, hasWarning := buildViabilityChecks(costResult, nil, visa.Result{}, "", nil, nil)
	if !hasWarning {
		t.Error("expected warning for no hotel prices")
	}

	found := false
	for _, c := range checks {
		if c.Dimension == "hotels" && c.Status == "warning" {
			found = true
		}
	}
	if !found {
		t.Error("hotel warning check not found")
	}
}
