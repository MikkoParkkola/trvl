package hacks

import (
	"context"
	"testing"
	"time"
)

// TestDetectorInput_currency verifies the currency fallback.
func TestDetectorInput_currency(t *testing.T) {
	tests := []struct {
		in   DetectorInput
		want string
	}{
		{DetectorInput{Currency: "USD"}, "USD"},
		{DetectorInput{Currency: ""}, "EUR"},
	}
	for _, tc := range tests {
		got := tc.in.currency()
		if got != tc.want {
			t.Errorf("currency() = %q, want %q", got, tc.want)
		}
	}
}

// TestDetectAll_emptyInput verifies DetectAll does not panic on empty input.
func TestDetectAll_emptyInput(t *testing.T) {
	// With empty input all detectors should return quickly without panicking.
	// We cannot assert specific counts because real API calls are involved,
	// but the function must not panic.
	_, _ = DetectAll(context.Background(), DetectorInput{})
}

// TestHackFields verifies the Hack struct serialises correctly.
func TestHackFields(t *testing.T) {
	h := Hack{
		Type:        "throwaway",
		Title:       "Throwaway ticketing",
		Description: "Some description",
		Savings:     88,
		Currency:    "EUR",
		Risks:       []string{"risk1"},
		Steps:       []string{"step1"},
		Citations:   []string{"https://example.com"},
	}
	if h.Type != "throwaway" {
		t.Errorf("unexpected Type: %q", h.Type)
	}
	if h.Savings != 88 {
		t.Errorf("unexpected Savings: %v", h.Savings)
	}
}

// TestStopoverPrograms verifies the static database is not empty and has
// required fields set.
func TestStopoverPrograms(t *testing.T) {
	if len(stopoverPrograms) == 0 {
		t.Fatal("stopoverPrograms is empty")
	}
	for code, prog := range stopoverPrograms {
		if prog.Airline == "" {
			t.Errorf("[%s] Airline is empty", code)
		}
		if prog.Hub == "" {
			t.Errorf("[%s] Hub is empty", code)
		}
		if prog.MaxNights <= 0 {
			t.Errorf("[%s] MaxNights must be > 0, got %d", code, prog.MaxNights)
		}
	}
}

// TestAddDays verifies date arithmetic.
func TestAddDays(t *testing.T) {
	tests := []struct {
		date  string
		delta int
		want  string
	}{
		{"2026-04-13", 7, "2026-04-20"},
		{"2026-04-13", -3, "2026-04-10"},
		{"2026-04-13", 0, "2026-04-13"},
		{"invalid", 1, ""},
	}
	for _, tc := range tests {
		got := addDays(tc.date, tc.delta)
		if got != tc.want {
			t.Errorf("addDays(%q, %d) = %q, want %q", tc.date, tc.delta, got, tc.want)
		}
	}
}

// TestRoundSavings verifies rounding behaviour.
func TestRoundSavings(t *testing.T) {
	tests := []struct {
		in   float64
		want float64
	}{
		{12.4, 12},
		{12.5, 13},
		{0, 0},
		{-5.1, -5},
	}
	for _, tc := range tests {
		got := roundSavings(tc.in)
		if got != tc.want {
			t.Errorf("roundSavings(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestIsOvernightRoute verifies overnight detection heuristic.
func TestIsOvernightRoute(t *testing.T) {
	tests := []struct {
		dep  string
		arr  string
		want bool
	}{
		{"2026-04-13T21:55", "2026-04-14T10:40", true},  // classic night bus
		{"2026-04-13T14:00", "2026-04-13T16:00", false}, // afternoon route
		{"2026-04-13T08:00", "2026-04-13T12:00", false}, // morning route
		{"2026-04-13T23:00", "2026-04-14T06:30", true},  // night train
	}
	for _, tc := range tests {
		got := isOvernightRoute(tc.dep, tc.arr)
		if got != tc.want {
			t.Errorf("isOvernightRoute(%q, %q) = %v, want %v", tc.dep, tc.arr, got, tc.want)
		}
	}
}

// TestMatchStopoverProgram verifies hub matching.
func TestMatchStopoverProgram(t *testing.T) {
	prog, ok := matchStopoverProgram("HEL", "AY")
	if !ok {
		t.Fatal("expected match for HEL/AY")
	}
	if prog.Airline != "Finnair" {
		t.Errorf("expected Finnair, got %q", prog.Airline)
	}

	_, ok = matchStopoverProgram("JFK", "AA")
	if ok {
		t.Error("expected no match for JFK/AA")
	}
}

// TestAdjustReturnDate verifies return date shifting.
func TestAdjustReturnDate(t *testing.T) {
	tests := []struct {
		ret   string
		delta int
		want  string
	}{
		{"2026-04-22", 3, "2026-04-25"},
		{"2026-04-22", -1, "2026-04-21"},
		{"", 3, ""},
	}
	for _, tc := range tests {
		got := adjustReturnDate(tc.ret, tc.delta)
		if got != tc.want {
			t.Errorf("adjustReturnDate(%q, %d) = %q, want %q", tc.ret, tc.delta, got, tc.want)
		}
	}
}

// TestCityFromCode verifies IATA code to city name mapping.
func TestCityFromCode(t *testing.T) {
	if got := cityFromCode("HEL"); got != "Helsinki" {
		t.Errorf("cityFromCode(HEL) = %q, want Helsinki", got)
	}
	// Unknown code returns the code itself.
	if got := cityFromCode("XYZ"); got != "XYZ" {
		t.Errorf("cityFromCode(XYZ) = %q, want XYZ", got)
	}
}

// TestHiddenCityExtensions verifies the static map is populated.
func TestHiddenCityExtensions(t *testing.T) {
	if len(hiddenCityExtensions) == 0 {
		t.Fatal("hiddenCityExtensions is empty")
	}
	beyonds, ok := hiddenCityExtensions["AMS"]
	if !ok {
		t.Fatal("AMS should have hidden-city extensions")
	}
	if len(beyonds) == 0 {
		t.Error("AMS beyonds should be non-empty")
	}
}

// TestNearbyAirports verifies the static positioning map.
func TestNearbyAirports(t *testing.T) {
	if len(nearbyAirports) == 0 {
		t.Fatal("nearbyAirports is empty")
	}
	entries, ok := nearbyAirports["HEL"]
	if !ok {
		t.Fatal("HEL should have nearby airports")
	}
	for _, e := range entries {
		if e.Code == "" {
			t.Error("entry with empty Code in HEL nearby airports")
		}
		if e.GroundCost < 0 {
			t.Errorf("negative GroundCost for %s", e.Code)
		}
	}
}

// --- New detector tests ---

// TestDetectOpenJaw_emptyInput verifies no panic on empty input.
func TestDetectOpenJaw_emptyInput(t *testing.T) {
	hacks := detectOpenJaw(context.Background(), DetectorInput{})
	if len(hacks) != 0 {
		t.Errorf("expected nil/empty for empty input, got %d hacks", len(hacks))
	}
}

// TestDetectOpenJaw_noReturnDate verifies early return without panic when
// ReturnDate is empty (one-way search — open-jaw not applicable).
func TestDetectOpenJaw_noReturnDate(t *testing.T) {
	hacks := detectOpenJaw(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "PRG",
		Date:        "2026-04-13",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for one-way query, got %d", len(hacks))
	}
}

// TestOpenJawAlternates verifies the static alternates map is populated
// and has no empty strings.
func TestOpenJawAlternates(t *testing.T) {
	if len(openJawAlternates) == 0 {
		t.Fatal("openJawAlternates is empty")
	}
	alts, ok := openJawAlternates["PRG"]
	if !ok {
		t.Fatal("PRG should have open-jaw alternates")
	}
	for _, a := range alts {
		if a == "" {
			t.Error("empty alternate code in PRG open-jaw alternates")
		}
	}
}

// TestIsHomeAirport verifies home airport detection.
func TestIsHomeAirport(t *testing.T) {
	if isHomeAirport("HEL", nil) {
		t.Error("nil prefs should not match any airport")
	}
}

// TestGroundCostBetween verifies known pairs return positive values and
// unknown pairs fall back to a positive default.
func TestGroundCostBetween(t *testing.T) {
	known := groundCostBetween("AMS", "BRU")
	if known <= 0 {
		t.Errorf("expected positive ground cost AMS↔BRU, got %v", known)
	}
	unknown := groundCostBetween("XYZ", "ABC")
	if unknown <= 0 {
		t.Errorf("expected positive default ground cost, got %v", unknown)
	}
}

// TestDetectFerryPositioning_emptyInput verifies no panic on empty input.
func TestDetectFerryPositioning_emptyInput(t *testing.T) {
	hacks := detectFerryPositioning(context.Background(), DetectorInput{})
	if len(hacks) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(hacks))
	}
}

// TestFerryPositioningRoutes verifies the static ferry routes map is populated.
func TestFerryPositioningRoutes(t *testing.T) {
	if len(ferryPositioningRoutes) == 0 {
		t.Fatal("ferryPositioningRoutes is empty")
	}
	routes, ok := ferryPositioningRoutes["HEL"]
	if !ok {
		t.Fatal("HEL should have ferry positioning routes")
	}
	for _, r := range routes {
		if r.FerryFrom == "" {
			t.Error("FerryFrom is empty in HEL routes")
		}
		if r.AirportTo == "" {
			t.Error("AirportTo is empty in HEL routes")
		}
		if r.FerryEUR <= 0 {
			t.Errorf("non-positive FerryEUR for %s→%s", r.FerryFrom, r.FerryTo)
		}
	}
}

// TestDetectMultiStop_emptyInput verifies no panic on empty input.
func TestDetectMultiStop_emptyInput(t *testing.T) {
	hacks := detectMultiStop(context.Background(), DetectorInput{})
	if len(hacks) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(hacks))
	}
}

// TestLayoverMinutes verifies layover computation.
func TestLayoverMinutes(t *testing.T) {
	tests := []struct {
		arr  string
		dep  string
		want int
	}{
		{"2026-04-13T10:00", "2026-04-13T14:30", 270},
		{"2026-04-13T22:00", "2026-04-14T06:00", 480},
		{"2026-04-13T10:00", "2026-04-13T09:00", 0}, // negative diff → 0
		{"invalid", "2026-04-13T10:00", 0},
	}
	for _, tc := range tests {
		got := layoverMinutes(tc.arr, tc.dep)
		if got != tc.want {
			t.Errorf("layoverMinutes(%q, %q) = %d, want %d", tc.arr, tc.dep, got, tc.want)
		}
	}
}

// TestSliceContains verifies slice membership helper.
func TestSliceContains(t *testing.T) {
	if !sliceContains([]string{"AMS", "FRA"}, "AMS") {
		t.Error("expected AMS to be found")
	}
	if sliceContains([]string{"AMS", "FRA"}, "CDG") {
		t.Error("expected CDG not to be found")
	}
}

// TestDetectCurrencyArbitrage_emptyInput verifies no panic on empty input.
func TestDetectCurrencyArbitrage_emptyInput(t *testing.T) {
	hacks := detectCurrencyArbitrage(context.Background(), DetectorInput{})
	if len(hacks) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(hacks))
	}
}

// TestKnownArbitrageAirlines verifies the static arbitrage airline list.
func TestKnownArbitrageAirlines(t *testing.T) {
	if len(knownArbitrageAirlines) == 0 {
		t.Fatal("knownArbitrageAirlines is empty")
	}
	for _, a := range knownArbitrageAirlines {
		if a.AirlineCode == "" {
			t.Error("empty AirlineCode in knownArbitrageAirlines")
		}
		if a.HomeCurrency == "" {
			t.Error("empty HomeCurrency in knownArbitrageAirlines")
		}
	}
}

// TestToEUR verifies currency conversion helper.
func TestToEUR(t *testing.T) {
	tests := []struct {
		price    float64
		currency string
		wantMin  float64
		wantMax  float64
	}{
		{100, "EUR", 99.9, 100.1},
		{100, "USD", 80, 100},     // USD < EUR typically
		{1000, "SEK", 70, 100},    // SEK is small
		{100, "UNKNOWN", 99, 101}, // unknown falls back to price as-is
	}
	for _, tc := range tests {
		got := toEUR(tc.price, tc.currency)
		if got < tc.wantMin || got > tc.wantMax {
			t.Errorf("toEUR(%v, %q) = %v, want [%v, %v]", tc.price, tc.currency, got, tc.wantMin, tc.wantMax)
		}
	}
}

// TestDetectCalendarConflict_emptyInput verifies no panic on empty input.
func TestDetectCalendarConflict_emptyInput(t *testing.T) {
	hacks := detectCalendarConflict(context.Background(), DetectorInput{})
	if len(hacks) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(hacks))
	}
}

// TestComputeEaster verifies Easter dates for known years.
func TestComputeEaster(t *testing.T) {
	tests := []struct {
		year  int
		month int // expected month
		day   int // expected day
	}{
		{2024, 3, 31}, // Easter 2024: March 31
		{2025, 4, 20}, // Easter 2025: April 20
		{2026, 4, 5},  // Easter 2026: April 5
		{2027, 3, 28}, // Easter 2027: March 28
	}
	for _, tc := range tests {
		got := computeEaster(tc.year)
		if got.Month() != time.Month(tc.month) || got.Day() != tc.day {
			t.Errorf("computeEaster(%d) = %s, want %04d-%02d-%02d", tc.year, got.Format("2006-01-02"), tc.year, tc.month, tc.day)
		}
	}
}

// TestFindPeakPeriod verifies peak period detection.
func TestFindPeakPeriod(t *testing.T) {
	tests := []struct {
		date string
		want string
	}{
		{"2026-07-15", "Summer holidays"},
		{"2026-12-25", "Christmas/New Year"},
		{"2026-01-03", "Christmas/New Year"},
		{"2026-10-15", "October half-term"},
		{"2026-02-17", "February ski week"},
		{"2026-04-05", "Easter"}, // Easter 2026 = April 5
		{"2026-03-01", ""},       // quiet period
		{"2026-11-15", ""},       // quiet period
	}
	for _, tc := range tests {
		parsed, err := parseDate(tc.date)
		if err != nil {
			t.Fatalf("cannot parse %q: %v", tc.date, err)
		}
		got := findPeakPeriod(parsed)
		if got != tc.want {
			t.Errorf("findPeakPeriod(%s) = %q, want %q", tc.date, got, tc.want)
		}
	}
}

// TestDetectTuesdayBooking_emptyInput verifies no panic on empty input.
func TestDetectTuesdayBooking_emptyInput(t *testing.T) {
	hacks := detectTuesdayBooking(context.Background(), DetectorInput{})
	if len(hacks) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(hacks))
	}
}

// TestDetectTuesdayBooking_nonExpensive verifies no hack on a non-expensive day.
func TestDetectTuesdayBooking_nonExpensive(t *testing.T) {
	// Wednesday is a cheap day — should return nil without API calls.
	hacks := detectTuesdayBooking(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "PRG",
		Date:        "2026-04-15", // Wednesday
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for Wednesday departure, got %d", len(hacks))
	}
}

// TestIsCheapDay verifies cheap/expensive day classification.
func TestIsCheapDay(t *testing.T) {
	tests := []struct {
		day  time.Weekday
		want bool
	}{
		{time.Tuesday, true},
		{time.Wednesday, true},
		{time.Saturday, true},
		{time.Friday, false},
		{time.Sunday, false},
		{time.Monday, false},
		{time.Thursday, false},
	}
	for _, tc := range tests {
		got := isCheapDay(tc.day)
		if got != tc.want {
			t.Errorf("isCheapDay(%v) = %v, want %v", tc.day, got, tc.want)
		}
	}
}

// TestDateDelta verifies date difference computation.
func TestDateDelta(t *testing.T) {
	tests := []struct {
		base string
		alt  string
		want int
	}{
		{"2026-04-13", "2026-04-15", 2},
		{"2026-04-15", "2026-04-13", -2},
		{"2026-04-13", "2026-04-13", 0},
		{"invalid", "2026-04-13", 0},
	}
	for _, tc := range tests {
		got := dateDelta(tc.base, tc.alt)
		if got != tc.want {
			t.Errorf("dateDelta(%q, %q) = %d, want %d", tc.base, tc.alt, got, tc.want)
		}
	}
}

// TestDetectLowCostCarrier_emptyInput verifies no panic on empty input.
func TestDetectLowCostCarrier_emptyInput(t *testing.T) {
	hacks := detectLowCostCarrier(context.Background(), DetectorInput{})
	if len(hacks) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(hacks))
	}
}

// TestLowCostCarriers verifies the static LCC map is populated.
func TestLowCostCarriers(t *testing.T) {
	if len(lowCostCarriers) == 0 {
		t.Fatal("lowCostCarriers is empty")
	}
	if _, ok := lowCostCarriers["FR"]; !ok {
		t.Error("Ryanair (FR) should be in lowCostCarriers")
	}
	if _, ok := lowCostCarriers["W6"]; !ok {
		t.Error("Wizz Air (W6) should be in lowCostCarriers")
	}
	if _, ok := lowCostCarriers["U2"]; !ok {
		t.Error("easyJet (U2) should be in lowCostCarriers")
	}
}

// TestHubStopoverAllowance verifies the multi-stop hub allowance map.
func TestHubStopoverAllowance(t *testing.T) {
	if len(hubStopoverAllowance) == 0 {
		t.Fatal("hubStopoverAllowance is empty")
	}
	ams, ok := hubStopoverAllowance["AMS"]
	if !ok {
		t.Fatal("AMS should be in hubStopoverAllowance")
	}
	if ams.Airline == "" {
		t.Error("AMS hub has empty Airline")
	}
	if ams.MaxNight <= 0 {
		t.Errorf("AMS hub MaxNight should be > 0, got %d", ams.MaxNight)
	}
}

// TestMultistopHubs verifies the multi-stop routing map.
func TestMultistopHubs(t *testing.T) {
	if len(multistopHubs) == 0 {
		t.Fatal("multistopHubs is empty")
	}
	hubs, ok := multistopHubs["PRG"]
	if !ok {
		t.Fatal("PRG should have multi-stop hub options")
	}
	if len(hubs) == 0 {
		t.Error("PRG multi-stop hubs should be non-empty")
	}
}
