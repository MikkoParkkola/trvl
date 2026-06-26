package trip

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/visa"
	"github.com/MikkoParkkola/trvl/internal/weather"
)

// TestPlanTrip_ValidationGate is table-driven over the input-validation contract
// that runs before any network call. The key one-way change is asserted here: a
// missing return date is NOT a validation error (unlike a missing depart date),
// while a malformed or non-future return date is still rejected. These cases all
// return from the early-validation block, so the test is deterministic and offline.
func TestPlanTrip_ValidationGate(t *testing.T) {
	cases := []struct {
		name    string
		input   PlanInput
		wantErr string // substring the error must contain
	}{
		{
			name:    "missing origin",
			input:   PlanInput{Destination: "BCN", DepartDate: "2026-07-01", Guests: 1},
			wantErr: "origin and destination",
		},
		{
			name:    "missing depart date",
			input:   PlanInput{Origin: "HEL", Destination: "BCN", ReturnDate: "2026-07-08", Guests: 1},
			wantErr: "depart date is required",
		},
		{
			name:    "zero guests",
			input:   PlanInput{Origin: "HEL", Destination: "BCN", DepartDate: "2026-07-01", Guests: 0},
			wantErr: "guests must be at least 1",
		},
		{
			name:    "malformed return date is rejected",
			input:   PlanInput{Origin: "HEL", Destination: "BCN", DepartDate: "2026-07-01", ReturnDate: "not-a-date", Guests: 1},
			wantErr: "invalid return date",
		},
		{
			name:    "return date not after depart is rejected",
			input:   PlanInput{Origin: "HEL", Destination: "BCN", DepartDate: "2026-07-08", ReturnDate: "2026-07-01", Guests: 1},
			wantErr: "return date must be after depart date",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PlanTrip(context.Background(), tc.input)
			if err == nil {
				t.Fatalf("expected validation error containing %q, got nil", tc.wantErr)
			}
			if !contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestPlanTrip_OneWayStructuralInvariants asserts the assembled one-way plan: no
// return date echoed back, zero nights, and no return-flight leg fabricated. It is
// gated behind TRVL_TEST_LIVE_INTEGRATIONS because PlanTrip runs live searches past
// validation; offline those searches fail yet these structural invariants still hold.
// Mirrors the existing live-test pattern in this package.
func TestPlanTrip_OneWayStructuralInvariants(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") == "" {
		t.Skip("skipping live-API test; set TRVL_TEST_LIVE_INTEGRATIONS=1 to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	result, err := PlanTrip(ctx, PlanInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-08-01",
		Guests:      1,
	})
	if err != nil {
		t.Fatalf("PlanTrip returned a hard error for a valid one-way input: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.ReturnDate != "" {
		t.Errorf("ReturnDate = %q, want empty for one-way", result.ReturnDate)
	}
	if result.Nights != 0 {
		t.Errorf("Nights = %d, want 0 for one-way", result.Nights)
	}
	if len(result.ReturnFlights) != 0 {
		t.Errorf("ReturnFlights = %d, want 0 for one-way (no return fabricated)", len(result.ReturnFlights))
	}
}

// ============================================================
// Discover — additional validation paths
// ============================================================

func TestDiscover_BadFromDateFormat(t *testing.T) {
	_, err := Discover(context.Background(), DiscoverOptions{
		Origin: "HEL",
		From:   "31-07-2026",
		Until:  "2026-07-31",
		Budget: 1000,
	})
	if err == nil {
		t.Error("expected error for bad from date format")
	}
}

func TestDiscover_BadUntilDateFormat(t *testing.T) {
	_, err := Discover(context.Background(), DiscoverOptions{
		Origin: "HEL",
		From:   "2026-07-01",
		Until:  "31-07-2026",
		Budget: 1000,
	})
	if err == nil {
		t.Error("expected error for bad until date format")
	}
}

func TestDiscover_UntilSameAsFrom(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_, err := Discover(ctx, DiscoverOptions{
		Origin: "HEL",
		From:   "2026-07-15",
		Until:  "2026-07-15",
		Budget: 1000,
	})
	// Same date is not "before" — may succeed or fail depending on window generation.
	// Just verify it doesn't panic.
	_ = err
}

// ============================================================
// FindWeekendGetaways — additional validation
// ============================================================

func TestFindWeekendGetaways_DefaultNights(t *testing.T) {
	opts := WeekendOptions{Month: "july-2026"}
	opts.defaults()
	if opts.Nights != 2 {
		t.Errorf("default nights = %d, want 2", opts.Nights)
	}
}

func TestFindWeekendGetaways_CustomNights(t *testing.T) {
	opts := WeekendOptions{Month: "july-2026", Nights: 3}
	opts.defaults()
	if opts.Nights != 3 {
		t.Errorf("custom nights = %d, want 3", opts.Nights)
	}
}

// ============================================================
// parseMonth — edge cases
// ============================================================

func TestParseMonth_FirstFridayIsNotFirst(t *testing.T) {
	// 2026-03: March 1 is Sunday. First Friday = March 6.
	dep, ret, display, err := parseMonth("2026-03")
	if err != nil {
		t.Fatalf("parseMonth('2026-03') error: %v", err)
	}
	if dep != "2026-03-06" {
		t.Errorf("depart = %q, want 2026-03-06 (first Friday of March 2026)", dep)
	}
	if ret != "2026-03-08" {
		t.Errorf("return = %q, want 2026-03-08", ret)
	}
	if display != "March 2026" {
		t.Errorf("display = %q, want 'March 2026'", display)
	}
}

func TestParseMonth_JanuaryFormat(t *testing.T) {
	dep, _, _, err := parseMonth("January-2026")
	if err != nil {
		t.Fatalf("parseMonth error: %v", err)
	}
	if dep == "" {
		t.Error("expected non-empty depart date")
	}
	// Verify the date is a Friday.
	d, _ := time.Parse("2006-01-02", dep)
	if d.Weekday() != time.Friday {
		t.Errorf("depart date %s is %s, want Friday", dep, d.Weekday())
	}
}

// ============================================================
// determineVerdict — edge cases
// ============================================================

func TestDetermineVerdict_BlockerNoMatchingCheck(t *testing.T) {
	// hasBlocker=true but no check has status="blocker" — fallback path.
	checks := []ViabilityCheck{
		{Dimension: "flights", Status: "ok", Summary: "all good"},
	}
	verdict, reason := determineVerdict(checks, true, false)
	if verdict != "NO_GO" {
		t.Errorf("verdict = %q, want NO_GO", verdict)
	}
	if reason != "blocker detected" {
		t.Errorf("reason = %q, want 'blocker detected'", reason)
	}
}

func TestDetermineVerdict_MultipleWarnings(t *testing.T) {
	checks := []ViabilityCheck{
		{Dimension: "flights", Status: "warning", Summary: "no flights"},
		{Dimension: "hotels", Status: "warning", Summary: "no hotels"},
		{Dimension: "weather", Status: "ok", Summary: "sunny"},
	}
	verdict, reason := determineVerdict(checks, false, true)
	if verdict != "WAIT" {
		t.Errorf("verdict = %q, want WAIT", verdict)
	}
	if reason == "" {
		t.Error("expected non-empty reason for warnings")
	}
}

// ============================================================
// buildViabilityChecks — cost result edge cases
// ============================================================

func TestBuildViabilityChecks_CostNilResultNoError(t *testing.T) {
	checks, _, hasWarning := buildViabilityChecks(nil, nil, visa.Result{}, "", nil, nil)
	if !hasWarning {
		t.Error("expected warning for nil cost result")
	}
	found := false
	for _, c := range checks {
		if c.Dimension == "flights" && c.Status == "warning" {
			found = true
		}
	}
	if !found {
		t.Error("expected flights warning check")
	}
}

func TestBuildViabilityChecks_CostResultWithError(t *testing.T) {
	checks, _, hasWarning := buildViabilityChecks(nil, fmt.Errorf("network error"), visa.Result{}, "", nil, nil)
	if !hasWarning {
		t.Error("expected warning for cost error")
	}
	found := false
	for _, c := range checks {
		if c.Dimension == "flights" && c.Status == "warning" {
			found = true
			if c.Summary != "network error" {
				t.Errorf("summary = %q, want 'network error'", c.Summary)
			}
		}
	}
	if !found {
		t.Error("expected flights warning check")
	}
}

func TestBuildViabilityChecks_CostFailedWithMessage(t *testing.T) {
	result := &TripCostResult{Success: false, Error: "no flights available"}
	checks, _, hasWarning := buildViabilityChecks(result, nil, visa.Result{}, "", nil, nil)
	if !hasWarning {
		t.Error("expected warning for failed cost result")
	}
	found := false
	for _, c := range checks {
		if c.Dimension == "flights" && c.Summary == "no flights available" {
			found = true
		}
	}
	if !found {
		t.Error("expected flights warning with error message")
	}
}

func TestBuildViabilityChecks_FullSuccess(t *testing.T) {
	costResult := &TripCostResult{
		Success: true,
		Flights: FlightCost{
			Outbound: 100,
			Return:   120,
			Currency: "EUR",
		},
		Hotels: HotelCost{
			PerNight: 80,
			Total:    240,
			Name:     "Test Hotel",
			Currency: "EUR",
		},
		Total:    460,
		Currency: "EUR",
	}

	forecasts := []weather.Forecast{
		{TempMax: 25, TempMin: 15, Precipitation: 0},
		{TempMax: 27, TempMin: 17, Precipitation: 1},
	}
	weatherResult := &weather.WeatherResult{
		Success:   true,
		Forecasts: forecasts,
	}

	visaResult := visa.Result{
		Success: true,
		Requirement: visa.Requirement{
			Status:  "visa-free",
			MaxStay: "90 days",
		},
	}

	checks, hasBlocker, hasWarning := buildViabilityChecks(costResult, nil, visaResult, "FI", weatherResult, nil)
	if hasBlocker {
		t.Error("unexpected blocker")
	}
	if hasWarning {
		t.Error("unexpected warning")
	}
	if len(checks) < 4 {
		t.Errorf("expected >= 4 checks, got %d", len(checks))
	}

	// Check total_cost check exists.
	foundCost := false
	for _, c := range checks {
		if c.Dimension == "total_cost" {
			foundCost = true
			if c.Cost != 460 {
				t.Errorf("total cost = %f, want 460", c.Cost)
			}
		}
	}
	if !foundCost {
		t.Error("expected total_cost check")
	}
}

func TestBuildViabilityChecks_EVisa(t *testing.T) {
	visaResult := visa.Result{
		Success: true,
		Requirement: visa.Requirement{
			Status: "e-visa",
			Notes:  "Apply online 72h before",
		},
	}
	checks, _, hasWarning := buildViabilityChecks(nil, nil, visaResult, "FI", nil, nil)
	if !hasWarning {
		t.Error("expected warning for e-visa")
	}
	foundVisa := false
	for _, c := range checks {
		if c.Dimension == "visa" && c.Status == "warning" {
			foundVisa = true
		}
	}
	if !foundVisa {
		t.Error("expected visa warning check for e-visa")
	}
}

// ============================================================
// buildInsights — additional branches
// ============================================================

func TestBuildInsights_WeekendCheaperPattern(t *testing.T) {
	// Create dates where weekend is cheaper than weekday.
	dates := []models.DatePriceResult{
		{Date: "2026-07-04", Price: 80, Currency: "EUR"},  // Saturday
		{Date: "2026-07-05", Price: 85, Currency: "EUR"},  // Sunday
		{Date: "2026-07-06", Price: 150, Currency: "EUR"}, // Monday
		{Date: "2026-07-07", Price: 160, Currency: "EUR"}, // Tuesday
		{Date: "2026-07-08", Price: 140, Currency: "EUR"}, // Wednesday
	}
	target := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) // Wednesday
	avgPrice := (80 + 85 + 150 + 160 + 140) / 5.0
	insights := buildInsights(dates, target, avgPrice)

	foundPattern := false
	for _, ins := range insights {
		if ins.Type == "pattern" {
			foundPattern = true
			// Weekend should be cheaper.
			if ins.Savings <= 0 {
				t.Errorf("pattern insight should have positive savings, got %f", ins.Savings)
			}
		}
	}
	if !foundPattern {
		t.Error("expected a pattern insight")
	}
}

func TestBuildInsights_TargetDateNotInList(t *testing.T) {
	dates := []models.DatePriceResult{
		{Date: "2026-07-04", Price: 100, Currency: "EUR"},
	}
	target := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC) // not in list
	insights := buildInsights(dates, target, 100)

	// Should have "cheapest" and "average" but no "saving" insight.
	for _, ins := range insights {
		if ins.Type == "saving" {
			t.Error("did not expect saving insight when target date is not in the list")
		}
	}
}

func TestBuildInsights_AllSameDayOfWeek(t *testing.T) {
	// Only weekday dates — no weekend comparison possible.
	dates := []models.DatePriceResult{
		{Date: "2026-07-06", Price: 100, Currency: "EUR"}, // Monday
		{Date: "2026-07-07", Price: 110, Currency: "EUR"}, // Tuesday
		{Date: "2026-07-08", Price: 120, Currency: "EUR"}, // Wednesday
		{Date: "2026-07-09", Price: 105, Currency: "EUR"}, // Thursday
	}
	target := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	avgPrice := (100 + 110 + 120 + 105) / 4.0
	insights := buildInsights(dates, target, avgPrice)

	for _, ins := range insights {
		if ins.Type == "pattern" {
			t.Error("did not expect pattern insight with only weekday dates")
		}
	}
}

func TestBuildInsights_FairlyConsistentPrices(t *testing.T) {
	// All prices close to average — < 5% saving.
	dates := []models.DatePriceResult{
		{Date: "2026-07-04", Price: 100, Currency: "EUR"}, // Saturday
		{Date: "2026-07-06", Price: 101, Currency: "EUR"}, // Monday
		{Date: "2026-07-07", Price: 102, Currency: "EUR"}, // Tuesday
	}
	target := time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)
	avgPrice := (100 + 101 + 102) / 3.0
	insights := buildInsights(dates, target, avgPrice)

	foundAvg := false
	for _, ins := range insights {
		if ins.Type == "average" {
			foundAvg = true
			if ins.Description == "" {
				t.Error("average insight should have description")
			}
		}
	}
	if !foundAvg {
		t.Error("expected average insight")
	}
}

func TestBuildInsights_TargetDateFoundButCheapest(t *testing.T) {
	// Target date is already the cheapest — no saving.
	dates := []models.DatePriceResult{
		{Date: "2026-07-06", Price: 100, Currency: "EUR"}, // cheapest AND target
		{Date: "2026-07-07", Price: 200, Currency: "EUR"},
	}
	target := time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)
	insights := buildInsights(dates, target, 150)

	for _, ins := range insights {
		if ins.Type == "saving" {
			t.Error("did not expect saving insight when target IS the cheapest")
		}
	}
}

// ============================================================
// assembleDateResult — edge cases
// ============================================================

func TestAssembleDateResult_Empty(t *testing.T) {
	dr := &models.DateSearchResult{Success: true, Dates: nil}
	target := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	result := assembleDateResult("HEL", "BCN", target, dr)
	if result.Success {
		t.Error("expected failure for empty dates")
	}
}

func TestAssembleDateResult_AllZeroPricesOnly(t *testing.T) {
	dr := &models.DateSearchResult{
		Success: true,
		Dates:   []models.DatePriceResult{{Date: "2026-07-01", Price: 0}, {Date: "2026-07-02", Price: 0}},
	}
	target := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	result := assembleDateResult("HEL", "BCN", target, dr)
	if result.Success {
		t.Error("expected failure for all zero prices")
	}
}

func TestAssembleDateResult_Unsuccessful(t *testing.T) {
	dr := &models.DateSearchResult{Success: false}
	target := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	result := assembleDateResult("HEL", "BCN", target, dr)
	if result.Success {
		t.Error("expected failure for unsuccessful result")
	}
}

func TestAssembleDateResult_TwoDates(t *testing.T) {
	dr := &models.DateSearchResult{
		Success: true,
		Dates: []models.DatePriceResult{
			{Date: "2026-07-01", Price: 150, Currency: "EUR"},
			{Date: "2026-07-02", Price: 180, Currency: "EUR"},
		},
	}
	target := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	result := assembleDateResult("HEL", "BCN", target, dr)
	if !result.Success {
		t.Error("expected success")
	}
	if len(result.CheapestDates) != 2 {
		t.Errorf("expected 2 cheapest dates, got %d", len(result.CheapestDates))
	}
}

func TestAssembleDateResult_MultipleDates(t *testing.T) {
	dr := &models.DateSearchResult{
		Success: true,
		Dates: []models.DatePriceResult{
			{Date: "2026-07-01", Price: 200, Currency: "EUR"},
			{Date: "2026-07-02", Price: 100, Currency: "EUR"},
			{Date: "2026-07-03", Price: 150, Currency: "EUR"},
			{Date: "2026-07-04", Price: 120, Currency: "EUR"},
			{Date: "2026-07-05", Price: 300, Currency: "EUR"},
		},
	}
	target := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	result := assembleDateResult("HEL", "BCN", target, dr)
	if !result.Success {
		t.Error("expected success")
	}
	if len(result.CheapestDates) != 3 {
		t.Errorf("expected 3 cheapest dates, got %d", len(result.CheapestDates))
	}
	// First should be cheapest.
	if result.CheapestDates[0].Price != 100 {
		t.Errorf("cheapest date price = %f, want 100", result.CheapestDates[0].Price)
	}
	if result.Currency != "EUR" {
		t.Errorf("currency = %q, want EUR", result.Currency)
	}
}

// ============================================================
// SmartDateOptions defaults
// ============================================================

func TestSmartDateOptions_DefaultFlexDays(t *testing.T) {
	opts := SmartDateOptions{}
	opts.defaults()
	if opts.FlexDays != 7 {
		t.Errorf("flexDays = %d, want 7", opts.FlexDays)
	}
}

func TestSmartDateOptions_RoundTripDefaults(t *testing.T) {
	opts := SmartDateOptions{RoundTrip: true}
	opts.defaults()
	if opts.Duration != 7 {
		t.Errorf("duration = %d, want 7 (default for round-trip)", opts.Duration)
	}
}

func TestSmartDateOptions_CustomValues(t *testing.T) {
	opts := SmartDateOptions{FlexDays: 14, RoundTrip: true, Duration: 10}
	opts.defaults()
	if opts.FlexDays != 14 {
		t.Errorf("flexDays = %d, want 14", opts.FlexDays)
	}
	if opts.Duration != 10 {
		t.Errorf("duration = %d, want 10", opts.Duration)
	}
}

// ============================================================
// buildWeatherCheck — edge cases
// ============================================================

func TestBuildWeatherCheck_NoRain(t *testing.T) {
	forecasts := []weather.Forecast{
		{TempMax: 30, TempMin: 20, Precipitation: 0},
		{TempMax: 28, TempMin: 18, Precipitation: 2},
		{TempMax: 32, TempMin: 22, Precipitation: 0},
	}
	check := buildWeatherCheck(forecasts)
	if check.Status != "ok" {
		t.Errorf("status = %q, want ok for no rain", check.Status)
	}
}

func TestBuildWeatherCheck_SingleForecast(t *testing.T) {
	forecasts := []weather.Forecast{
		{TempMax: 35, TempMin: 25, Precipitation: 10},
	}
	check := buildWeatherCheck(forecasts)
	if check.Status != "warning" {
		t.Errorf("status = %q, want warning for single rainy day", check.Status)
	}
}

// ============================================================
// buildVisaCheck — edge cases
// ============================================================

func TestBuildVisaCheck_DefaultStatus(t *testing.T) {
	result := visa.Result{
		Success: true,
		Requirement: visa.Requirement{
			Status: "unknown-status",
		},
	}
	check := buildVisaCheck(result)
	if check.Status != "warning" {
		t.Errorf("status = %q, want warning for unknown visa status", check.Status)
	}
}

// ============================================================
// resolveDestinationCountry — edge cases
// ============================================================
