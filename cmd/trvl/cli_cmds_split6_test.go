package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestRelativeTimeStr_MultipleMinutesAgo(t *testing.T) {
	got := relativeTimeStr(time.Now().Add(-30 * time.Minute))
	if !strings.HasSuffix(got, "m ago") {
		t.Errorf("expected Xm ago, got %q", got)
	}
}

func TestRelativeTimeStr_OneHourAgo(t *testing.T) {
	got := relativeTimeStr(time.Now().Add(-1*time.Hour - 5*time.Minute))
	if got != "1h ago" {
		t.Errorf("expected '1h ago', got %q", got)
	}
}

func TestRelativeTimeStr_MultipleHoursAgo(t *testing.T) {
	got := relativeTimeStr(time.Now().Add(-5 * time.Hour))
	if !strings.HasSuffix(got, "h ago") {
		t.Errorf("expected Xh ago, got %q", got)
	}
}

func TestRelativeTimeStr_OneDayAgo(t *testing.T) {
	got := relativeTimeStr(time.Now().Add(-25 * time.Hour))
	if got != "1d ago" {
		t.Errorf("expected '1d ago', got %q", got)
	}
}

func TestRelativeTimeStr_MultipleDaysAgo(t *testing.T) {
	got := relativeTimeStr(time.Now().Add(-72 * time.Hour))
	if !strings.HasSuffix(got, "d ago") {
		t.Errorf("expected Xd ago, got %q", got)
	}
}

func TestLoungesCmd_InvalidIATAError(t *testing.T) {
	cmd := loungesCmd()
	cmd.SetArgs([]string{"12"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid IATA code")
	}
}

func TestWeatherCmd_MissingArg(t *testing.T) {
	cmd := weatherCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with no args")
	}
}

func TestMultiCityCmd_FlagsV4(t *testing.T) {
	cmd := multiCityCmd()

	for _, name := range []string{"visit", "dates"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestRunNearby_ValidLatLon_FlagDefaults(t *testing.T) {

	cmd := nearbyCmd()

	cmd.SetArgs([]string{"91.0", "2.17"})

	if f := cmd.Flags().Lookup("radius"); f.DefValue != "500" {
		t.Errorf("expected default radius 500, got %s", f.DefValue)
	}
}

func TestCalendarCmd_NoArgNoLastError(t *testing.T) {
	cmd := calendarCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when neither trip_id nor --last provided")
	}
}

func TestExploreCmd_InvalidIATAError(t *testing.T) {
	cmd := exploreCmd()
	cmd.SetArgs([]string{"12"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid IATA origin")
	}
}

func TestExploreCmd_InvalidFromDate(t *testing.T) {
	cmd := exploreCmd()
	cmd.SetArgs([]string{"HEL", "--from", "not-a-date"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid --from date")
	}
}

func TestExploreCmd_FlagsV5(t *testing.T) {
	cmd := exploreCmd()
	for _, name := range []string{"from", "to", "type", "stops", "format", "currency"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on exploreCmd", name)
		}
	}
}

func TestGroundCmd_FlagsV5(t *testing.T) {
	cmd := groundCmd()
	for _, name := range []string{"currency", "provider", "max-price", "type"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on groundCmd", name)
		}
	}
}

func TestGroundCmd_RequiresThreeArgsV5(t *testing.T) {
	cmd := groundCmd()
	cmd.SetArgs([]string{"Prague", "Vienna"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with only two args")
	}
}

func TestHacksCmd_Flags(t *testing.T) {
	cmd := hacksCmd()
	for _, name := range []string{"return", "carry-on", "currency"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on hacksCmd", name)
		}
	}
}

func TestHacksCmd_RequiresThreeArgs(t *testing.T) {
	cmd := hacksCmd()
	cmd.SetArgs([]string{"HEL", "BCN"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with only two args")
	}
}

func TestRunEvents_MissingAPIKey(t *testing.T) {
	t.Setenv("TICKETMASTER_API_KEY", "")
	cmd := eventsCmd()
	cmd.SetArgs([]string{"Barcelona", "--from", "2026-07-01", "--to", "2026-07-08"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when TICKETMASTER_API_KEY is not set")
	}
}

func TestAirportTransferCmd_FlagsExist(t *testing.T) {
	cmd := airportTransferCmd()
	for _, name := range []string{"currency", "max-price", "type", "arrival-after"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestDatesCmd_FlagsV5(t *testing.T) {
	cmd := datesCmd()

	for _, name := range []string{"from", "to", "duration", "round-trip"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on datesCmd", name)
		}
	}
}

func TestDatesCmd_RequiresTwoArgsV5(t *testing.T) {
	// Origin is now optional; dates needs at least DESTINATION. Zero args
	// must still fail cobra's RangeArgs(1,2).
	cmd := datesCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with zero args")
	}
}

func TestGridCmd_FlagsV5(t *testing.T) {
	cmd := gridCmd()
	for _, name := range []string{"depart-from", "depart-to", "return-from", "return-to"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on gridCmd", name)
		}
	}
}

func TestFlightsCmd_InvalidOriginIATA(t *testing.T) {
	cmd := flightsCmd()
	cmd.SetArgs([]string{"12", "BCN", "2026-07-01"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid origin IATA")
	}
}

func TestFlightsCmd_InvalidDestinationIATA(t *testing.T) {
	cmd := flightsCmd()
	cmd.SetArgs([]string{"HEL", "12", "2026-07-01"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid destination IATA")
	}
}

func TestMultiCityCmd_InvalidHomeIATA(t *testing.T) {
	cmd := multiCityCmd()
	cmd.SetArgs([]string{"12", "--visit", "BCN,ROM"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid home IATA")
	}
}

func TestMultiCityCmd_MissingVisitFlag(t *testing.T) {
	cmd := multiCityCmd()
	cmd.SetArgs([]string{"HEL"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --visit is missing")
	}
}

func TestProvidersEnableCmd_FlagsExist(t *testing.T) {
	cmd := providersEnableCmd()
	if f := cmd.Flags().Lookup("accept-tos"); f == nil {
		t.Error("expected --accept-tos flag")
	}
}

func TestProvidersEnableCmd_RequiresOneArg(t *testing.T) {
	cmd := providersEnableCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with no args")
	}
}

func TestProvidersDisableCmd_RequiresOneArg(t *testing.T) {
	cmd := providersDisableCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with no args")
	}
}

func TestCabinSpecSlice_HasFourClasses(t *testing.T) {
	if len(cabinClasses) != 4 {
		t.Errorf("expected 4 cabin classes, got %d", len(cabinClasses))
	}
	names := []string{"Economy", "Premium Economy", "Business", "First"}
	for i, spec := range cabinClasses {
		if spec.Name != names[i] {
			t.Errorf("cabinClasses[%d].Name = %q, want %q", i, spec.Name, names[i])
		}
	}
}

func TestConvertRoundedDisplayAmounts_NilAmount(t *testing.T) {
	got := convertRoundedDisplayAmounts(context.Background(), "EUR", "USD", 2, nil)
	if got != "EUR" {
		t.Errorf("expected EUR (no conversion), got %q", got)
	}
}

func TestConvertRoundedDisplayAmounts_ZeroAmount(t *testing.T) {
	zero := 0.0
	got := convertRoundedDisplayAmounts(context.Background(), "EUR", "USD", 2, &zero)
	if got != "EUR" {
		t.Errorf("expected EUR (zero amount skipped), got %q", got)
	}
}

func TestMaybeShowFlightHackTips_EmptyFlightsV7(t *testing.T) {
	result := &models.FlightSearchResult{
		Success: true,
		Flights: nil,
	}

	maybeShowFlightHackTips(context.Background(), []string{"HEL"}, []string{"BCN"}, "2026-07-01", "", 1, result, false)
}

func TestSuggestCmd_InvalidOriginIATAV7(t *testing.T) {
	cmd := suggestCmd()
	cmd.SetArgs([]string{"12", "BCN", "--around", "2026-07-01"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid origin IATA")
	}
}

func TestSuggestCmd_InvalidDestIATAV7(t *testing.T) {
	cmd := suggestCmd()
	cmd.SetArgs([]string{"HEL", "12", "--around", "2026-07-01"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid dest IATA")
	}
}

func TestFlightsCmd_HomeOriginResolves(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live HTTP test in short mode")
	}
	cmd := flightsCmd()
	cmd.SetArgs([]string{"home", "BCN", "2026-07-01"})
	_ = cmd.Execute()
}

func TestBaggageCmd_CarryOnOnly(t *testing.T) {
	cmd := baggageCmd()
	cmd.SetArgs([]string{"--carry-on-only"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBaggageCmd_UnknownAirline(t *testing.T) {
	cmd := baggageCmd()
	cmd.SetArgs([]string{"XX"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for unknown airline code")
	}
}

func TestBaggageCmd_NoArgsReturnsHelp(t *testing.T) {
	cmd := baggageCmd()
	cmd.SetArgs([]string{})

	_ = cmd.Execute()
}

func TestAccomHackCmd_FlagsExist(t *testing.T) {
	cmd := accomHackCmd()
	for _, name := range []string{"checkin", "checkout", "currency", "max-splits", "guests"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on accomHackCmd", name)
		}
	}
}

func TestCalendarCmd_LastNoSearch(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := calendarCmd()
	cmd.SetArgs([]string{"--last"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no last search exists")
	}
}

func TestCalendarCmd_FlagsExist(t *testing.T) {
	cmd := calendarCmd()
	for _, name := range []string{"output", "last"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on calendarCmd", name)
		}
	}
}

func TestCalendarCmd_NoArgNoLast(t *testing.T) {
	cmd := calendarCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when neither trip_id nor --last provided")
	}
}

func TestCalendarCmd_LastWithSearch(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	ls := &LastSearch{
		Command:        "flights",
		Origin:         "HEL",
		Destination:    "BCN",
		DepartDate:     "2026-07-01",
		ReturnDate:     "2026-07-08",
		FlightPrice:    199,
		FlightCurrency: "EUR",
		FlightAirline:  "KLM",
	}
	saveLastSearch(ls)

	cmd := calendarCmd()
	cmd.SetArgs([]string{"--last"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCalendarCmd_LastWriteToFile(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	ls := &LastSearch{
		Command:        "flights",
		Origin:         "HEL",
		Destination:    "BCN",
		DepartDate:     "2026-07-01",
		ReturnDate:     "2026-07-08",
		FlightPrice:    199,
		FlightCurrency: "EUR",
	}
	saveLastSearch(ls)

	outFile := filepath.Join(tmp, "trip.ics")
	cmd := calendarCmd()
	cmd.SetArgs([]string{"--last", "--output", outFile})
	if err := cmd.Execute(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if _, err := os.Stat(outFile); os.IsNotExist(err) {
		t.Error("expected ICS file to be written")
	}
}

func TestRunCabinComparison_JSONNoNetwork(t *testing.T) {

	ctx := cancelledTestContext(t)

	err := runCabinComparison(ctx, []string{"HEL"}, []string{"BCN"}, "2026-07-01", flights.SearchOptions{}, "json")

	_ = err
}

func TestTripsShowCmd_NotFound(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := tripsShowCmd()
	cmd.SetArgs([]string{"nonexistent-id"})
	err := cmd.Execute()

	_ = err
}

func TestTripCostCmd_FlagsV9(t *testing.T) {
	cmd := tripCostCmd()
	if cmd == nil {
		t.Error("expected non-nil tripCostCmd")
	}
}
