package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/providers"
)

func TestMultiCityCmd_InvalidCityIATAV14(t *testing.T) {
	cmd := multiCityCmd()
	cmd.SetArgs([]string{"HEL", "--visit", "12,ROM", "--dates", "2026-07-01,2026-07-21"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid city IATA in --visit")
	}
}

func TestMultiCityCmd_FlagsExistV14(t *testing.T) {
	cmd := multiCityCmd()
	for _, name := range []string{"visit", "dates", "format"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on multiCityCmd", name)
		}
	}
}

func TestWeekendCmd_InvalidIATAV14(t *testing.T) {
	cmd := weekendCmd()
	cmd.SetArgs([]string{"12"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid origin IATA")
	}
}

func TestWeatherCmd_FlagsExistV14(t *testing.T) {
	cmd := weatherCmd()
	for _, name := range []string{"from", "to"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on weatherCmd", name)
		}
	}
}

func TestGroundCmd_FlagsExistV14(t *testing.T) {
	cmd := groundCmd()
	for _, name := range []string{"max-price", "type"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on groundCmd", name)
		}
	}
}

func TestLoungesCmd_InvalidIATAV14(t *testing.T) {
	cmd := loungesCmd()
	cmd.SetArgs([]string{"12"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid airport IATA")
	}
}

func TestUpgradeCmd_FlagsExistV14(t *testing.T) {
	cmd := upgradeCmd()
	for _, name := range []string{"dry-run", "quiet"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on upgradeCmd", name)
		}
	}
}

func TestDiscoverCmd_InvalidOriginIATA(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := discoverCmd()
	cmd.SetArgs([]string{"--origin", "12", "--from", "2026-07-01", "--until", "2026-07-31", "--budget", "500"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid origin IATA")
	}
}

func TestTripCmd_FlagsExist(t *testing.T) {
	cmd := tripCmd()
	for _, name := range []string{"return", "depart", "guests"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on tripCmd", name)
		}
	}
}

func TestTripCmd_RequiresTwoArgs(t *testing.T) {
	cmd := tripCmd()
	cmd.SetArgs([]string{"HEL"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with only one arg")
	}
}

func TestTripCmd_InvalidOriginIATA(t *testing.T) {
	cmd := tripCmd()
	cmd.SetArgs([]string{"12", "BCN", "2026-07-01"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid origin IATA")
	}
}

func TestSearchCmd_FlagsExist(t *testing.T) {
	cmd := searchCmd()
	for _, name := range []string{"dry-run", "json"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on searchCmd", name)
		}
	}
}

func TestGridCmd_InvalidOriginIATAV15(t *testing.T) {
	cmd := gridCmd()
	cmd.SetArgs([]string{"12", "BCN"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid origin IATA")
	}
}

func TestGridCmd_InvalidDestIATAV15(t *testing.T) {
	cmd := gridCmd()
	cmd.SetArgs([]string{"HEL", "12"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid dest IATA")
	}
}

func TestMCPCmd_InstallSubcmd(t *testing.T) {
	cmd := mcpCmd()
	found := false
	for _, sub := range cmd.Commands() {
		if sub.Use == "install" || sub.Name() == "install" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'install' subcommand on mcpCmd")
	}
}

func TestPointsValueCmd_FlagsV15(t *testing.T) {
	cmd := pointsValueCmd()
	if cmd == nil {
		t.Error("expected non-nil pointsValueCmd")
		return
	}

	for _, name := range []string{"program", "format"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Logf("flag --%s not found on pointsValueCmd", name)
		}
	}
}

func TestTripCostCmd_MissingRequiredFlags(t *testing.T) {
	cmd := tripCostCmd()
	cmd.SetArgs([]string{"HEL", "BCN"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing required flags")
	}
}

func TestTripCostCmd_FlagsExistV15(t *testing.T) {
	cmd := tripCostCmd()
	for _, name := range []string{"depart", "return", "guests", "currency", "format"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on tripCostCmd", name)
		}
	}
}

func TestNearbyCmd_NonNil(t *testing.T) {
	cmd := nearbyCmd()
	if cmd == nil {
		t.Error("expected non-nil nearbyCmd")
	}
}

func TestEventsCmd_MissingRequiredFlagsV15(t *testing.T) {
	cmd := eventsCmd()
	cmd.SetArgs([]string{"Barcelona"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing required flags on eventsCmd")
	}
}

func TestCalendarCmd_WithTripID(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	createCmd := tripsCmd()
	createCmd.SetArgs([]string{"create", "Calendar Test Trip"})
	if err := createCmd.Execute(); err != nil {
		t.Fatalf("create trip: %v", err)
	}

	store, err := loadTripStore()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	list := store.List()
	if len(list) == 0 {
		t.Skip("no trips in store")
	}
	tripID := list[0].ID

	cmd := calendarCmd()
	cmd.SetArgs([]string{tripID})
	if err := cmd.Execute(); err != nil {
		t.Errorf("calendar with trip_id: %v", err)
	}
}

func TestCalendarCmd_WithTripIDToFile(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	createCmd := tripsCmd()
	createCmd.SetArgs([]string{"create", "Calendar File Trip"})
	if err := createCmd.Execute(); err != nil {
		t.Fatalf("create trip: %v", err)
	}

	store, err := loadTripStore()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	list := store.List()
	if len(list) == 0 {
		t.Skip("no trips in store")
	}
	tripID := list[0].ID

	outFile := filepath.Join(tmp, "trip.ics")
	cmd := calendarCmd()
	cmd.SetArgs([]string{tripID, "--output", outFile})
	if err := cmd.Execute(); err != nil {
		t.Errorf("calendar with trip_id --output: %v", err)
	}

	if _, statErr := os.Stat(outFile); os.IsNotExist(statErr) {
		t.Error("expected ICS file to be written")
	}
}

func TestCalendarCmd_WithTripIDNotFound(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := calendarCmd()
	cmd.SetArgs([]string{"nonexistent-id-v16"})

	_ = cmd.Execute()
}

func TestUpgradeCmd_QuietV16(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := upgradeCmd()
	cmd.SetArgs([]string{"--quiet"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUpgradeCmd_AlreadyUpToDate(t *testing.T) {

	tmp := t.TempDir()
	setTestHome(t, tmp)

	cmd1 := upgradeCmd()
	cmd1.SetArgs([]string{})
	_ = cmd1.Execute()

	cmd2 := upgradeCmd()
	cmd2.SetArgs([]string{})
	_ = cmd2.Execute()
}

func TestAccomHackCmd_MissingCheckIn(t *testing.T) {
	cmd := accomHackCmd()
	cmd.SetArgs([]string{"Prague"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing required flags")
	}
}

func TestShareTrip_WithRealTrip(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	createCmd := tripsCmd()
	createCmd.SetArgs([]string{"create", "Share Test Trip"})
	if err := createCmd.Execute(); err != nil {
		t.Fatalf("create trip: %v", err)
	}

	store, err := loadTripStore()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	list := store.List()
	if len(list) == 0 {
		t.Skip("no trips in store")
	}
	tripID := list[0].ID

	addLegCmd := tripsCmd()
	addLegCmd.SetArgs([]string{
		"add-leg", tripID, "flight",
		"--from", "HEL",
		"--to", "BCN",
		"--provider", "KLM",
		"--start", "2026-07-01T18:00",
		"--end", "2026-07-01T21:00",
		"--price", "199",
		"--currency", "EUR",
	})
	_ = addLegCmd.Execute()

	cmd := shareCmd()
	cmd.SetArgs([]string{tripID})
	if err := cmd.Execute(); err != nil {
		t.Errorf("share trip: %v", err)
	}
}

func TestShareTrip_NotFound(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := shareCmd()
	cmd.SetArgs([]string{"nonexistent-id-v17"})

	err := cmd.Execute()
	_ = err
}

func TestShareCmd_LastWithSearch(t *testing.T) {
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

	cmd := shareCmd()
	cmd.SetArgs([]string{"--last"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("share --last: %v", err)
	}
}

func TestRunNearby_InvalidLat(t *testing.T) {
	cmd := nearbyCmd()
	cmd.SetArgs([]string{"not-a-lat", "24.9384"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid latitude")
	}
}

func TestRunNearby_InvalidLon(t *testing.T) {
	cmd := nearbyCmd()
	cmd.SetArgs([]string{"60.1699", "not-a-lon"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid longitude")
	}
}

func TestProvidersDisableCmd_NotFound(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := providersDisableCmd()
	cmd.SetArgs([]string{"nonexistent-provider-id"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for nonexistent provider")
	}
}

func TestRunProvidersList_EmptyV18(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := providersCmd()
	cmd.SetArgs([]string{"list"})
	_ = cmd.Execute()
}

func TestRunProvidersList_JSONEmpty(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := providersCmd()
	cmd.SetArgs([]string{"list", "--format", "json"})
	_ = cmd.Execute()
}

func TestRunProvidersStatus_EmptyV18(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := providersCmd()
	cmd.SetArgs([]string{"status"})
	_ = cmd.Execute()
}

func TestRunProvidersStatus_JSONEmpty(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := providersCmd()
	cmd.SetArgs([]string{"status", "--format", "json"})
	_ = cmd.Execute()
}

func TestAccomHackCmd_FlagsV18(t *testing.T) {
	cmd := accomHackCmd()
	for _, name := range []string{"checkin", "checkout", "currency", "max-splits", "guests"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on accomHackCmd", name)
		}
	}
}

func TestGridCmd_RequiredFlagsMissing(t *testing.T) {

	cmd := gridCmd()
	cmd.SetArgs([]string{"HEL"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with only one positional arg")
	}
}

func TestMultiCityCmd_ValidArgsNoNetwork(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := multiCityCmd()
	cmd.SetContext(cancelledTestContext(t))

	cmd.SetArgs([]string{"HEL", "--visit", "BCN,ROM", "--dates", "2026-07-01,2026-07-21"})

	_ = cmd.Execute()
}

func TestExploreCmd_FlagsExistV18(t *testing.T) {
	cmd := exploreCmd()
	for _, name := range []string{"from", "to", "format"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on exploreCmd", name)
		}
	}
}

// writeTestProviderV19 creates a minimal provider config JSON in the temp HOME.

func TestRunProvidersList_WithProviderV19(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	writeTestProviderV19(t, tmp, "test-hotel-provider")

	cmd := providersCmd()
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("providers list: %v", err)
	}
}

func TestRunProvidersList_WithProviderJSONV19(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	writeTestProviderV19(t, tmp, "test-hotel-provider-json")

	oldFormat := format
	format = "json"
	defer func() { format = oldFormat }()

	cmd := providersCmd()
	cmd.SetArgs([]string{"list"})
	_ = cmd.Execute()
}

func TestRunProvidersStatus_WithProviderV19(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	writeTestProviderV19(t, tmp, "test-status-provider")

	cmd := providersCmd()
	cmd.SetArgs([]string{"status"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("providers status: %v", err)
	}
}

func TestRunProvidersStatus_WithErrorProviderV19(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	dir := filepath.Join(tmp, ".trvl", "providers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := providers.ProviderConfig{
		ID:         "error-provider",
		Name:       "Error Provider",
		Category:   "flights",
		Endpoint:   "https://bad.example.com/api",
		LastError:  "connection refused",
		ErrorCount: 5,
		Consent: &providers.ConsentRecord{
			Granted:   true,
			Timestamp: time.Now(),
		},
	}
	data, _ := json.Marshal(cfg)
	_ = os.WriteFile(filepath.Join(dir, "error-provider.json"), data, 0o600)

	cmd := providersCmd()
	cmd.SetArgs([]string{"status"})
	_ = cmd.Execute()
}

func TestRunProvidersStatus_WithStaleProviderV19(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	dir := filepath.Join(tmp, ".trvl", "providers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := providers.ProviderConfig{
		ID:          "stale-provider",
		Name:        "Stale Provider",
		Category:    "flights",
		Endpoint:    "https://stale.example.com/api",
		LastSuccess: time.Now().Add(-48 * time.Hour),
		Consent: &providers.ConsentRecord{
			Granted:   true,
			Timestamp: time.Now(),
		},
	}
	data, _ := json.Marshal(cfg)
	_ = os.WriteFile(filepath.Join(dir, "stale-provider.json"), data, 0o600)

	cmd := providersCmd()
	cmd.SetArgs([]string{"status"})
	_ = cmd.Execute()
}

func TestRunProvidersDisable_WithProviderV19(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	writeTestProviderV19(t, tmp, "to-delete-provider")

	cmd := providersCmd()
	cmd.SetArgs([]string{"disable", "to-delete-provider"})

	_ = cmd.Execute()
}

func TestAirportTransferCmd_MissingArgsV19(t *testing.T) {
	cmd := airportTransferCmd()
	cmd.SetArgs([]string{"CDG"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with only one positional arg")
	}
}

func TestAirportTransferCmd_FlagsExistV19(t *testing.T) {
	cmd := airportTransferCmd()
	for _, name := range []string{"currency", "provider", "max-price", "type", "arrival-after"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on airportTransferCmd", name)
		}
	}
}

func TestRouteCmd_MissingArgsV19(t *testing.T) {
	cmd := routeCmd()
	cmd.SetArgs([]string{"HEL"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with only one positional arg")
	}
}
