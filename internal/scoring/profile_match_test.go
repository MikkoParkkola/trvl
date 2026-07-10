package scoring_test

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/scoring"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

func defaultPrefs() *preferences.Preferences {
	return preferences.Default()
}

func baseInput() scoring.DiscoverInput {
	return scoring.DiscoverInput{
		AirportCode: "BCN",
		CityName:    "Barcelona",
		FlightPrice: 100,
		HotelPrice:  150,
		Total:       250,
		Budget:      500,
		HotelRating: 8.0,
		HotelName:   "Hotel Arts Barcelona",
	}
}

// ── ComputeProfileMatch — basic contract ─────────────────────────────────────

func TestComputeProfileMatch_NilPrefs(t *testing.T) {
	score, breakdown := scoring.ComputeProfileMatch(nil, baseInput())
	if score < 0 || score > 100 {
		t.Errorf("score %d out of [0,100]", score)
	}
	if len(breakdown) == 0 {
		t.Error("breakdown should not be empty")
	}
}

func TestComputeProfileMatch_DefaultPrefs(t *testing.T) {
	score, breakdown := scoring.ComputeProfileMatch(defaultPrefs(), baseInput())
	if score <= 0 || score > 100 {
		t.Errorf("score = %d, want (0, 100]", score)
	}
	// All expected factor keys should be present.
	for _, key := range []string{
		scoring.FactorBudgetFit,
		scoring.FactorLoyaltyEarn,
		scoring.FactorTimeWindowFit,
		scoring.FactorArrivalWindowFit,
		scoring.FactorDirectness,
		scoring.FactorDistrictMatch,
		scoring.FactorAirportAffinity,
		scoring.FactorEarlyConnectionCompliance,
		scoring.FactorStatusRetention,
		scoring.FactorLoungeAtTransit,
		scoring.FactorBucketListBoost,
		scoring.FactorWarsawFilter,
		scoring.FactorFamilyModeCompatibility,
	} {
		if _, ok := breakdown[key]; !ok {
			t.Errorf("breakdown missing factor %q", key)
		}
	}
}

func TestComputeProfileMatch_ScoreRange(t *testing.T) {
	inputs := []scoring.DiscoverInput{
		{AirportCode: "BCN", Total: 50, Budget: 500},
		{AirportCode: "NRT", Total: 490, Budget: 500, HotelRating: 9.0},
		{AirportCode: "HEL", Total: 250, Budget: 300, HotelRating: 7.5},
	}
	for _, in := range inputs {
		score, _ := scoring.ComputeProfileMatch(defaultPrefs(), in)
		if score < 0 || score > 100 {
			t.Errorf("score %d out of range for input %+v", score, in)
		}
	}
}

// ── Factor: budget_fit ────────────────────────────────────────────────────────

func TestFactor_BudgetFit_HighSlack(t *testing.T) {
	in := baseInput()
	in.Total = 50
	in.Budget = 500
	score, bd := scoring.ComputeProfileMatch(defaultPrefs(), in)
	if bd[scoring.FactorBudgetFit] < 0.8 {
		t.Errorf("budget_fit = %.2f, want ≥ 0.8 for large slack", bd[scoring.FactorBudgetFit])
	}
	_ = score
}

func TestFactor_BudgetFit_AtBudget(t *testing.T) {
	in := baseInput()
	in.Total = 500
	in.Budget = 500
	_, bd := scoring.ComputeProfileMatch(defaultPrefs(), in)
	if bd[scoring.FactorBudgetFit] != 0.0 {
		t.Errorf("budget_fit = %.2f, want 0.0 when total == budget", bd[scoring.FactorBudgetFit])
	}
}

func TestFactor_BudgetFit_HighSlackHigherScore(t *testing.T) {
	prefs := defaultPrefs()
	cheapInput := baseInput()
	cheapInput.Total = 100
	cheapInput.Budget = 500

	expensiveInput := baseInput()
	expensiveInput.Total = 450
	expensiveInput.Budget = 500

	s1, _ := scoring.ComputeProfileMatch(prefs, cheapInput)
	s2, _ := scoring.ComputeProfileMatch(prefs, expensiveInput)
	if s1 <= s2 {
		t.Errorf("cheap trip score %d should be > expensive trip score %d", s1, s2)
	}
}

// ── Factor: warsaw_filter (hard exclusion) ────────────────────────────────────

func TestFactor_WarsawFilter_ExcludedAirport(t *testing.T) {
	prefs := defaultPrefs()
	prefs.ExcludedDestinations = []string{"WAW"}

	in := baseInput()
	in.AirportCode = "WAW"
	in.CityName = "Warsaw"

	score, bd := scoring.ComputeProfileMatch(prefs, in)
	if score != 0 {
		t.Errorf("score = %d, want 0 for excluded destination", score)
	}
	if bd[scoring.FactorWarsawFilter] != 0.0 {
		t.Errorf("warsaw_filter = %.2f, want 0.0 for excluded destination", bd[scoring.FactorWarsawFilter])
	}
}

func TestFactor_WarsawFilter_ExcludedCity(t *testing.T) {
	prefs := defaultPrefs()
	prefs.ExcludedDestinations = []string{"Warsaw"}

	in := baseInput()
	in.AirportCode = "WAW"
	in.CityName = "Warsaw"

	score, _ := scoring.ComputeProfileMatch(prefs, in)
	if score != 0 {
		t.Errorf("score = %d, want 0 for excluded city", score)
	}
}

func TestFactor_WarsawFilter_NonExcluded(t *testing.T) {
	prefs := defaultPrefs()
	prefs.ExcludedDestinations = []string{"WAW"}

	in := baseInput()
	in.AirportCode = "BCN"
	in.CityName = "Barcelona"

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorWarsawFilter] != 1.0 {
		t.Errorf("warsaw_filter = %.2f, want 1.0 for non-excluded destination", bd[scoring.FactorWarsawFilter])
	}
}

// ── Factor: bucket_list_boost ─────────────────────────────────────────────────

func TestFactor_BucketListBoost_Match(t *testing.T) {
	prefs := defaultPrefs()
	prefs.BucketList = []string{"Barcelona", "Kyoto"}

	in := baseInput()
	in.CityName = "Barcelona"

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorBucketListBoost] < 0.9 {
		t.Errorf("bucket_list_boost = %.2f, want ≥ 0.9 for bucket list city", bd[scoring.FactorBucketListBoost])
	}
}

func TestFactor_BucketListBoost_NoMatch(t *testing.T) {
	prefs := defaultPrefs()
	prefs.BucketList = []string{"Kyoto", "Patagonia"}

	in := baseInput()
	in.CityName = "Helsinki"

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorBucketListBoost] > 0.6 {
		t.Errorf("bucket_list_boost = %.2f, want ≤ 0.6 for non-bucket-list city", bd[scoring.FactorBucketListBoost])
	}
}

func TestFactor_BucketListBoost_AirportCodeMatch(t *testing.T) {
	prefs := defaultPrefs()
	prefs.BucketList = []string{"NRT"} // Tokyo Narita by airport code

	in := baseInput()
	in.AirportCode = "NRT"
	in.CityName = "Tokyo"

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorBucketListBoost] < 0.9 {
		t.Errorf("bucket_list_boost = %.2f, want ≥ 0.9 for airport code bucket list match", bd[scoring.FactorBucketListBoost])
	}
}

// ── Factor: airport_affinity ──────────────────────────────────────────────────

func TestFactor_AirportAffinity_High(t *testing.T) {
	prefs := defaultPrefs()
	prefs.AirportAffinity = map[string]float64{"BCN": 0.95}

	_, bd := scoring.ComputeProfileMatch(prefs, baseInput())
	if bd[scoring.FactorAirportAffinity] < 0.9 {
		t.Errorf("airport_affinity = %.2f, want ≥ 0.9 for high affinity", bd[scoring.FactorAirportAffinity])
	}
}

func TestFactor_AirportAffinity_Low(t *testing.T) {
	prefs := defaultPrefs()
	prefs.AirportAffinity = map[string]float64{"BCN": 0.1}

	_, bd := scoring.ComputeProfileMatch(prefs, baseInput())
	if bd[scoring.FactorAirportAffinity] > 0.2 {
		t.Errorf("airport_affinity = %.2f, want ≤ 0.2 for low affinity", bd[scoring.FactorAirportAffinity])
	}
}

func TestFactor_AirportAffinity_Unknown(t *testing.T) {
	prefs := defaultPrefs()
	in := baseInput()
	in.AirportCode = "PRG"

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorAirportAffinity] != 0.5 {
		t.Errorf("airport_affinity = %.2f, want 0.5 (neutral) for unknown airport", bd[scoring.FactorAirportAffinity])
	}
}

// ── Factor: loyalty_earn ──────────────────────────────────────────────────────

func TestFactor_LoyaltyEarn_PreferredAirline(t *testing.T) {
	prefs := defaultPrefs()
	prefs.LoyaltyAirlines = []string{"KL", "AY"}

	in := baseInput()
	in.AirlineCodes = []string{"KL"}

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorLoyaltyEarn] < 0.9 {
		t.Errorf("loyalty_earn = %.2f, want ≥ 0.9 for preferred airline", bd[scoring.FactorLoyaltyEarn])
	}
}

func TestFactor_LoyaltyEarn_NoMatch(t *testing.T) {
	prefs := defaultPrefs()
	prefs.LoyaltyAirlines = []string{"KL", "AY"}

	in := baseInput()
	in.AirlineCodes = []string{"AA", "UA"}

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorLoyaltyEarn] > 0.3 {
		t.Errorf("loyalty_earn = %.2f, want ≤ 0.3 for non-preferred airline", bd[scoring.FactorLoyaltyEarn])
	}
}

func TestFactor_LoyaltyEarn_NoFlightData(t *testing.T) {
	prefs := defaultPrefs()
	prefs.LoyaltyAirlines = []string{"KL"}

	in := baseInput()
	in.AirlineCodes = nil

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorLoyaltyEarn] != 0.5 {
		t.Errorf("loyalty_earn = %.2f, want 0.5 (neutral) when no flight data", bd[scoring.FactorLoyaltyEarn])
	}
}

// ── Factor: time_window_fit ───────────────────────────────────────────────────

func TestFactor_TimeWindowFit_WithinWindow(t *testing.T) {
	prefs := defaultPrefs()
	prefs.FlightTimeEarliest = "07:00"
	prefs.FlightTimeLatest = "22:00"

	in := baseInput()
	in.DepartTime = "09:30"

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorTimeWindowFit] < 0.9 {
		t.Errorf("time_window_fit = %.2f, want ≥ 0.9 for in-window departure", bd[scoring.FactorTimeWindowFit])
	}
}

func TestFactor_TimeWindowFit_TooEarly(t *testing.T) {
	prefs := defaultPrefs()
	prefs.FlightTimeEarliest = "07:00"

	in := baseInput()
	in.DepartTime = "05:30"

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorTimeWindowFit] > 0.4 {
		t.Errorf("time_window_fit = %.2f, want ≤ 0.4 for too-early departure", bd[scoring.FactorTimeWindowFit])
	}
}

func TestFactor_TimeWindowFit_UnknownTime(t *testing.T) {
	prefs := defaultPrefs()
	prefs.FlightTimeEarliest = "07:00"

	in := baseInput()
	in.DepartTime = ""

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorTimeWindowFit] != 0.5 {
		t.Errorf("time_window_fit = %.2f, want 0.5 (neutral) when time unknown", bd[scoring.FactorTimeWindowFit])
	}
}

// ── Factor: directness ────────────────────────────────────────────────────────

func TestFactor_Directness_PreferDirectFlight(t *testing.T) {
	prefs := defaultPrefs()
	prefs.PreferDirect = true

	in := baseInput()
	in.AirlineCodes = []string{"AY"}
	in.Stops = 0

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorDirectness] < 0.9 {
		t.Errorf("directness = %.2f, want ≥ 0.9 for direct flight with prefer_direct", bd[scoring.FactorDirectness])
	}
}

func TestFactor_Directness_PreferDirectButHasStops(t *testing.T) {
	prefs := defaultPrefs()
	prefs.PreferDirect = true

	in := baseInput()
	in.AirlineCodes = []string{"AY"}
	in.Stops = 1

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorDirectness] > 0.6 {
		t.Errorf("directness = %.2f, want ≤ 0.6 for connection with prefer_direct", bd[scoring.FactorDirectness])
	}
}

func TestFactor_Directness_NoPreference(t *testing.T) {
	prefs := defaultPrefs()
	prefs.PreferDirect = false

	in := baseInput()
	in.AirlineCodes = []string{"AY"}
	in.Stops = 2

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorDirectness] != 0.5 {
		t.Errorf("directness = %.2f, want 0.5 (neutral) when no directness preference", bd[scoring.FactorDirectness])
	}
}

// ── Factor: family_mode_compatibility ─────────────────────────────────────────

func TestFactor_FamilyMode_SoloTraveller(t *testing.T) {
	prefs := defaultPrefs()
	prefs.DefaultCompanions = 0

	_, bd := scoring.ComputeProfileMatch(prefs, baseInput())
	if bd[scoring.FactorFamilyModeCompatibility] != 0.5 {
		t.Errorf("family_mode = %.2f, want 0.5 (neutral) for solo traveller", bd[scoring.FactorFamilyModeCompatibility])
	}
}

func TestFactor_FamilyMode_GroupHighRating(t *testing.T) {
	prefs := defaultPrefs()
	prefs.DefaultCompanions = 2

	in := baseInput()
	in.HotelRating = 9.0

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorFamilyModeCompatibility] < 0.9 {
		t.Errorf("family_mode = %.2f, want ≥ 0.9 for group + high-rated hotel", bd[scoring.FactorFamilyModeCompatibility])
	}
}

func TestFactor_FamilyMode_GroupLowRating(t *testing.T) {
	prefs := defaultPrefs()
	prefs.DefaultCompanions = 2

	in := baseInput()
	in.HotelRating = 5.0

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorFamilyModeCompatibility] > 0.5 {
		t.Errorf("family_mode = %.2f, want ≤ 0.5 for group + low-rated hotel", bd[scoring.FactorFamilyModeCompatibility])
	}
}

// ── Custom weights ────────────────────────────────────────────────────────────

func TestCustomWeights_OverrideBudgetFit(t *testing.T) {
	prefs := defaultPrefs()
	// Give extreme weight to bucket list boost and zero to everything else.
	prefs.MatchWeights = map[string]float64{
		scoring.FactorBudgetFit:                 0.0,
		scoring.FactorLoyaltyEarn:               0.0,
		scoring.FactorTimeWindowFit:             0.0,
		scoring.FactorDirectness:                0.0,
		scoring.FactorDistrictMatch:             0.0,
		scoring.FactorAirportAffinity:           0.0,
		scoring.FactorEarlyConnectionCompliance: 0.0,
		scoring.FactorStatusRetention:           0.0,
		scoring.FactorLoungeAtTransit:           0.0,
		scoring.FactorBucketListBoost:           100.0,
		scoring.FactorFamilyModeCompatibility:   0.0,
	}
	prefs.BucketList = []string{"Barcelona"}

	// At-budget trip (would score 0 on budget_fit) but bucket-list destination.
	in := baseInput()
	in.Total = 500
	in.Budget = 500
	in.CityName = "Barcelona"

	score, _ := scoring.ComputeProfileMatch(prefs, in)
	// With 100% weight on bucket_list (score=1.0), final = 100.
	if score < 90 {
		t.Errorf("score = %d, want ≥ 90 when only bucket_list matters and destination matches", score)
	}
}

// ── Factor: budget_fit ────────────────────────────────────────────────────────

func TestFactor_BudgetFit_ZeroTotal(t *testing.T) {
	in := baseInput()
	in.Total = 0
	in.Budget = 500
	_, bd := scoring.ComputeProfileMatch(defaultPrefs(), in)
	if bd[scoring.FactorBudgetFit] != 1.0 {
		t.Errorf("budget_fit = %.2f, want 1.0 when total=0", bd[scoring.FactorBudgetFit])
	}
}

func TestFactor_BudgetFit_OverBudget(t *testing.T) {
	in := baseInput()
	in.Total = 600
	in.Budget = 500
	_, bd := scoring.ComputeProfileMatch(defaultPrefs(), in)
	if bd[scoring.FactorBudgetFit] != 0.0 {
		t.Errorf("budget_fit = %.2f, want 0.0 when over budget", bd[scoring.FactorBudgetFit])
	}
}

func TestFactor_BudgetFit_ZeroBudget(t *testing.T) {
	in := baseInput()
	in.Budget = 0
	_, bd := scoring.ComputeProfileMatch(defaultPrefs(), in)
	if bd[scoring.FactorBudgetFit] != 0.5 {
		t.Errorf("budget_fit = %.2f, want 0.5 (neutral) when budget=0", bd[scoring.FactorBudgetFit])
	}
}

// ── Factor: district_match ────────────────────────────────────────────────────

func TestFactor_DistrictMatch_HotelInDistrict(t *testing.T) {
	prefs := defaultPrefs()
	prefs.PreferredDistricts = map[string][]string{"Barcelona": {"Eixample", "Gràcia"}}

	in := baseInput()
	in.HotelName = "Hotel Eixample Grand"

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorDistrictMatch] < 0.9 {
		t.Errorf("district_match = %.2f, want ≥ 0.9 when hotel name contains preferred district", bd[scoring.FactorDistrictMatch])
	}
}

func TestFactor_DistrictMatch_HotelNotInDistrict(t *testing.T) {
	prefs := defaultPrefs()
	prefs.PreferredDistricts = map[string][]string{"Barcelona": {"Eixample"}}

	in := baseInput()
	in.HotelName = "Hotel Barceloneta Beach"

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorDistrictMatch] > 0.4 {
		t.Errorf("district_match = %.2f, want ≤ 0.4 when hotel not in preferred district", bd[scoring.FactorDistrictMatch])
	}
}

func TestFactor_DistrictMatch_NoCityName(t *testing.T) {
	in := baseInput()
	in.CityName = ""

	_, bd := scoring.ComputeProfileMatch(defaultPrefs(), in)
	if bd[scoring.FactorDistrictMatch] != 0.5 {
		t.Errorf("district_match = %.2f, want 0.5 (neutral) when city name empty", bd[scoring.FactorDistrictMatch])
	}
}

// ── Factor: airport_affinity (edge cases) ────────────────────────────────────

func TestFactor_AirportAffinity_Negative(t *testing.T) {
	prefs := defaultPrefs()
	prefs.AirportAffinity = map[string]float64{"BCN": -0.5}

	_, bd := scoring.ComputeProfileMatch(prefs, baseInput())
	if bd[scoring.FactorAirportAffinity] != 0.0 {
		t.Errorf("airport_affinity = %.2f, want 0.0 for negative affinity", bd[scoring.FactorAirportAffinity])
	}
}

func TestFactor_AirportAffinity_OverOne(t *testing.T) {
	prefs := defaultPrefs()
	prefs.AirportAffinity = map[string]float64{"BCN": 1.5}

	_, bd := scoring.ComputeProfileMatch(prefs, baseInput())
	if bd[scoring.FactorAirportAffinity] != 1.0 {
		t.Errorf("airport_affinity = %.2f, want 1.0 for affinity > 1 (clamped)", bd[scoring.FactorAirportAffinity])
	}
}

// ── Factor: status_retention ──────────────────────────────────────────────────

func TestFactor_StatusRetention_MatchWithMiles(t *testing.T) {
	prefs := defaultPrefs()
	prefs.FrequentFlyerPrograms = []preferences.FrequentFlyerStatus{
		{AirlineCode: "AY", MilesBalance: 10000},
	}

	in := baseInput()
	in.AirlineCodes = []string{"AY"}

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorStatusRetention] < 0.9 {
		t.Errorf("status_retention = %.2f, want ≥ 0.9 for matching FFP airline with miles", bd[scoring.FactorStatusRetention])
	}
}

func TestFactor_StatusRetention_MatchNoMiles(t *testing.T) {
	prefs := defaultPrefs()
	prefs.FrequentFlyerPrograms = []preferences.FrequentFlyerStatus{
		{AirlineCode: "AY", MilesBalance: 0},
	}

	in := baseInput()
	in.AirlineCodes = []string{"AY"}

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorStatusRetention] < 0.7 || bd[scoring.FactorStatusRetention] > 0.9 {
		t.Errorf("status_retention = %.2f, want ≈0.8 for matching FFP airline without miles", bd[scoring.FactorStatusRetention])
	}
}

func TestFactor_StatusRetention_NoMatch(t *testing.T) {
	prefs := defaultPrefs()
	prefs.FrequentFlyerPrograms = []preferences.FrequentFlyerStatus{
		{AirlineCode: "KL", MilesBalance: 5000},
	}

	in := baseInput()
	in.AirlineCodes = []string{"AY", "AA"}

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorStatusRetention] > 0.3 {
		t.Errorf("status_retention = %.2f, want ≤ 0.3 for non-matching airline", bd[scoring.FactorStatusRetention])
	}
}

func TestFactor_StatusRetention_NoAirlineCodes(t *testing.T) {
	prefs := defaultPrefs()
	prefs.FrequentFlyerPrograms = []preferences.FrequentFlyerStatus{
		{AirlineCode: "AY"},
	}

	in := baseInput()
	in.AirlineCodes = nil

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorStatusRetention] != 0.5 {
		t.Errorf("status_retention = %.2f, want 0.5 (neutral) when no airline data", bd[scoring.FactorStatusRetention])
	}
}

// ── Factor: lounge_at_transit ─────────────────────────────────────────────────

func TestFactor_LoungeAtTransit_HasCardWithStops(t *testing.T) {
	prefs := defaultPrefs()
	prefs.LoungeCards = []string{"Priority Pass"}

	in := baseInput()
	in.AirlineCodes = []string{"AY", "BA"}
	in.Stops = 1

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorLoungeAtTransit] < 0.7 {
		t.Errorf("lounge_at_transit = %.2f, want ≥ 0.7 for lounge card + connecting flight", bd[scoring.FactorLoungeAtTransit])
	}
}

func TestFactor_LoungeAtTransit_HasCardDirectFlight(t *testing.T) {
	prefs := defaultPrefs()
	prefs.LoungeCards = []string{"Priority Pass"}

	in := baseInput()
	in.AirlineCodes = []string{"AY"}
	in.Stops = 0

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorLoungeAtTransit] != 0.5 {
		t.Errorf("lounge_at_transit = %.2f, want 0.5 (neutral) for direct flight", bd[scoring.FactorLoungeAtTransit])
	}
}

func TestFactor_LoungeAtTransit_NoCard(t *testing.T) {
	prefs := defaultPrefs()
	prefs.LoungeCards = nil

	in := baseInput()
	in.Stops = 2

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorLoungeAtTransit] != 0.5 {
		t.Errorf("lounge_at_transit = %.2f, want 0.5 (neutral) when no lounge card", bd[scoring.FactorLoungeAtTransit])
	}
}

// ── Factor: early_connection_compliance (edge) ────────────────────────────────

func TestFactor_EarlyConnection_RedEyeOK(t *testing.T) {
	prefs := defaultPrefs()
	prefs.RedEyeOK = true

	in := baseInput()
	in.DepartTime = "04:30" // before 05:00 but user has opted in

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorEarlyConnectionCompliance] < 0.9 {
		t.Errorf("early_connection_compliance = %.2f, want ≥ 0.9 when red_eye_ok and pre-dawn departure", bd[scoring.FactorEarlyConnectionCompliance])
	}
}

func TestFactor_EarlyConnection_EarlyMorning_NotOK(t *testing.T) {
	prefs := defaultPrefs()
	prefs.RedEyeOK = false

	in := baseInput()
	in.DepartTime = "05:30" // 05:30 — between 05:00 and 06:00

	_, bd := scoring.ComputeProfileMatch(prefs, in)
	if bd[scoring.FactorEarlyConnectionCompliance] > 0.4 {
		t.Errorf("early_connection_compliance = %.2f, want ≤ 0.4 for 05:30 when red_eye_ok=false", bd[scoring.FactorEarlyConnectionCompliance])
	}
}

// ── DefaultWeights ────────────────────────────────────────────────────────────

func TestDefaultWeights_AllPositive(t *testing.T) {
	weights := scoring.DefaultWeights()
	if len(weights) == 0 {
		t.Fatal("default weights should not be empty")
	}
	for k, v := range weights {
		if v < 0 {
			t.Errorf("weight[%q] = %.2f, want ≥ 0", k, v)
		}
	}
}

func TestDefaultWeights_ContainsExpectedFactors(t *testing.T) {
	weights := scoring.DefaultWeights()
	for _, factor := range []string{
		scoring.FactorBudgetFit,
		scoring.FactorLoyaltyEarn,
		scoring.FactorArrivalWindowFit,
		scoring.FactorBucketListBoost,
		scoring.FactorFamilyModeCompatibility,
	} {
		if _, ok := weights[factor]; !ok {
			t.Errorf("default weights missing factor %q", factor)
		}
	}
	// WarsawFilter is a hard exclusion and should NOT have a weight.
	if _, ok := weights[scoring.FactorWarsawFilter]; ok {
		t.Errorf("default weights should not contain %q (hard exclusion, not weighted)", scoring.FactorWarsawFilter)
	}
}

// ── Factor: arrival_window_fit (GitHub #470) ─────────────────────────────────

func TestFactor_ArrivalWindowFit_HigherForInWindowArrival(t *testing.T) {
	prefs := defaultPrefs()
	prefs.ArrivalWindows = map[string]preferences.ArrivalWindow{
		"HEL": {After: "00:00", Before: "03:00"},
	}

	// all else equal; only arrival time differs
	inEarly := baseInput()
	inEarly.AirportCode = "HEL"
	inEarly.CityName = "Helsinki"
	inEarly.ArrivalTime = "00:25"

	inLate := baseInput()
	inLate.AirportCode = "HEL"
	inLate.CityName = "Helsinki"
	inLate.ArrivalTime = "22:45"

	sEarly, bdEarly := scoring.ComputeProfileMatch(prefs, inEarly)
	sLate, bdLate := scoring.ComputeProfileMatch(prefs, inLate)

	if bdEarly[scoring.FactorArrivalWindowFit] != 1.0 {
		t.Errorf("00:25 arrival must be in-window (1.0), got %.2f", bdEarly[scoring.FactorArrivalWindowFit])
	}
	if bdLate[scoring.FactorArrivalWindowFit] != 0.25 {
		t.Errorf("22:45 arrival must be out-of-window (0.25), got %.2f", bdLate[scoring.FactorArrivalWindowFit])
	}
	if bdEarly[scoring.FactorArrivalWindowFit] <= bdLate[scoring.FactorArrivalWindowFit] {
		t.Errorf("arrival_window_fit for 00:25 (%.2f) must be > 22:45 (%.2f)",
			bdEarly[scoring.FactorArrivalWindowFit], bdLate[scoring.FactorArrivalWindowFit])
	}
	// since inputs equal except the factor, overall score must be strictly higher
	if sEarly <= sLate {
		t.Errorf("overall score for 00:25 arrival (%d) must be > 22:45 (%d) when window set", sEarly, sLate)
	}
}

func TestFactor_ArrivalWindowFit_NeutralWhenUnset_NoRankingChange(t *testing.T) {
	prefs := defaultPrefs()
	// deliberately no ArrivalWindows

	inEarly := baseInput()
	inEarly.AirportCode = "HEL"
	inEarly.ArrivalTime = "00:25"

	inLate := baseInput()
	inLate.AirportCode = "HEL"
	inLate.ArrivalTime = "22:45"

	sEarly, bdEarly := scoring.ComputeProfileMatch(prefs, inEarly)
	sLate, bdLate := scoring.ComputeProfileMatch(prefs, inLate)

	if bdEarly[scoring.FactorArrivalWindowFit] != 0.5 || bdLate[scoring.FactorArrivalWindowFit] != 0.5 {
		t.Errorf("no window configured: factor must stay neutral 0.5, got %.2f/%.2f",
			bdEarly[scoring.FactorArrivalWindowFit], bdLate[scoring.FactorArrivalWindowFit])
	}
	if sEarly != sLate {
		t.Errorf("no window: arrival times must not affect ranking (scores %d vs %d)", sEarly, sLate)
	}
}

// TestFactor_ArrivalWindowFit_WrapAround tests the required midnight-crossing case:
// After > Before means (arr >= After || arr <= Before) is in-window.
func TestFactor_ArrivalWindowFit_WrapAround(t *testing.T) {
	prefs := defaultPrefs()
	prefs.ArrivalWindows = map[string]preferences.ArrivalWindow{
		"HEL": {After: "22:00", Before: "02:00"},
	}

	inIn := baseInput()
	inIn.AirportCode = "HEL"
	inIn.CityName = "Helsinki"
	inIn.ArrivalTime = "23:30"

	inOut := baseInput()
	inOut.AirportCode = "HEL"
	inOut.CityName = "Helsinki"
	inOut.ArrivalTime = "12:00"

	_, bdIn := scoring.ComputeProfileMatch(prefs, inIn)
	_, bdOut := scoring.ComputeProfileMatch(prefs, inOut)

	if bdIn[scoring.FactorArrivalWindowFit] != 1.0 {
		t.Errorf("23:30 must be in wrap-around window (1.0), got %.2f", bdIn[scoring.FactorArrivalWindowFit])
	}
	if bdOut[scoring.FactorArrivalWindowFit] != 0.25 {
		t.Errorf("12:00 must be out of wrap-around window (0.25), got %.2f", bdOut[scoring.FactorArrivalWindowFit])
	}
}
