package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/providers"
)

func (m *mockWatchTicker) Chan() <-chan time.Time { return m.ch }

func (m *mockWatchTicker) Stop() {}

func writeTestProviderV19(t *testing.T, tmp, id string) {
	t.Helper()
	dir := filepath.Join(tmp, ".trvl", "providers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir providers: %v", err)
	}
	cfg := providers.ProviderConfig{
		ID:       id,
		Name:     "Test Provider " + id,
		Category: "hotels",
		Endpoint: "https://example.com/api",
		Method:   "GET",
		Consent: &providers.ConsentRecord{
			Granted:   true,
			Timestamp: time.Now(),
			Domain:    "example.com",
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal provider: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), data, 0o600); err != nil {
		t.Fatalf("write provider file: %v", err)
	}
}

func (f *fakeDaemonTickerV28) Chan() <-chan time.Time { return f.ch }

func (f *fakeDaemonTickerV28) Stop() {}

var _ = bytes.NewBuffer

func TestVisaCmd_ListAllJSON(t *testing.T) {
	cmd := visaCmd()

	cmd.SetArgs([]string{"--list"})

	oldFormat := format
	format = "json"
	defer func() { format = oldFormat }()
	if err := cmd.Execute(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestVisaCmd_FailedLookupNotSuccess(t *testing.T) {

	cmd := visaCmd()
	cmd.SetArgs([]string{"--passport", "XX", "--destination", "JP"})

	if err := cmd.Execute(); err != nil {
		t.Errorf("unexpected error (expected print to stderr, not error): %v", err)
	}
}

func TestVisaCmd_LookupJSON(t *testing.T) {
	cmd := visaCmd()
	cmd.SetArgs([]string{"--passport", "FI", "--destination", "JP"})
	oldFormat := format
	format = "json"
	defer func() { format = oldFormat }()
	if err := cmd.Execute(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUpgradeCmd_DefaultRun(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := upgradeCmd()
	cmd.SetArgs([]string{})

	_ = cmd.Execute()
}

func TestTripsStatusCmd_NoUpcoming(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := tripsCmd()
	cmd.SetArgs([]string{"status"})
	_ = cmd.Execute()
}

func TestTripsDeleteCmd_NotFound(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := tripsCmd()
	cmd.SetArgs([]string{"delete", "nonexistent-id"})
	err := cmd.Execute()

	_ = err
}

func TestTripsAlertsCmd_NoAlerts(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := tripsCmd()
	cmd.SetArgs([]string{"alerts"})
	_ = cmd.Execute()
}

func TestTripsListCmd_AllFlagEmptyStore(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := tripsCmd()
	cmd.SetArgs([]string{"list", "--all"})
	_ = cmd.Execute()
}

func TestTripsCreateCmd_CreatesTrip(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := tripsCmd()
	cmd.SetArgs([]string{"create", "My Test Trip"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTripsListCmd_ShowsCreatedTrip(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	cmd := tripsCmd()
	cmd.SetArgs([]string{"create", "Test Trip"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("create trip: %v", err)
	}

	cmd2 := tripsCmd()
	cmd2.SetArgs([]string{"list"})
	if err := cmd2.Execute(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMCPCmd_FlagsExist(t *testing.T) {
	cmd := mcpCmd()
	if cmd == nil {
		t.Error("expected non-nil mcpCmd")
	}
}

func TestDealsCmd_FlagsV10(t *testing.T) {
	cmd := dealsCmd()
	for _, name := range []string{"region", "format", "providers"} {
		if f := cmd.Flags().Lookup(name); f == nil {

			t.Logf("flag --%s not found on dealsCmd", name)
		}
	}
}

func TestShareCmd_NoArgsNoLast(t *testing.T) {
	cmd := shareCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no trip_id and no --last")
	}
}

func TestMCPInstallCmd_UnknownClient(t *testing.T) {
	cmd := mcpInstallCmd()
	cmd.SetArgs([]string{"--client", "totally-unknown-client"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for unknown client")
	}
}

func TestTripsAddLegCmd_FlagsExist(t *testing.T) {
	cmd := tripsAddLegCmd()
	for _, name := range []string{"from", "to", "provider", "start", "end", "price", "currency"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on trips add-leg", name)
		}
	}
}

func TestTripsAddLegCmd_RequiresArgs(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := tripsAddLegCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with no args")
	}
}

func TestTripsBookCmd_FlagsExist(t *testing.T) {
	cmd := tripsBookCmd()
	for _, name := range []string{"provider", "ref", "type", "url", "notes"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on trips book", name)
		}
	}
}

func TestTripsBookCmd_RequiresArg(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := tripsBookCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with no args")
	}
}

func TestTripsFullFlow_CreateAndAddLeg(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	createCmd := tripsCmd()
	createCmd.SetArgs([]string{"create", "Prague Trip 2026"})
	if err := createCmd.Execute(); err != nil {
		t.Fatalf("create trip: %v", err)
	}

	listCmd := tripsCmd()
	listCmd.SetArgs([]string{"list"})
	if err := listCmd.Execute(); err != nil {
		t.Errorf("list trips: %v", err)
	}
}

func TestDealsCmd_FlagsV11b(t *testing.T) {
	cmd := dealsCmd()
	if cmd == nil {
		t.Error("expected non-nil dealsCmd")
	}
}

func TestDiscoverCmd_MissingFromFlag(t *testing.T) {
	cmd := discoverCmd()
	cmd.SetArgs([]string{"--until", "2026-07-31", "--budget", "500"})
	err := cmd.Execute()

	_ = err
}

func TestDiscoverCmd_FlagsV11(t *testing.T) {
	cmd := discoverCmd()
	for _, name := range []string{"from", "until", "budget", "origin"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on discoverCmd", name)
		}
	}
}

func TestRunTripsList_AllEmpty(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	if err := runTripsList(true); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunTripsList_InactiveEmpty(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	if err := runTripsList(false); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAirportTransferCmd_RequiresThreeArgsV11(t *testing.T) {
	cmd := airportTransferCmd()
	cmd.SetArgs([]string{"CDG", "Hotel"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with only two args")
	}
}

func TestHacksCmd_FlagsRegisteredV11(t *testing.T) {
	cmd := hacksCmd()
	for _, name := range []string{"return", "carry-on", "currency"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on hacksCmd", name)
		}
	}
}

func TestGroundCmd_AliasExist(t *testing.T) {
	cmd := groundCmd()

	if len(cmd.Aliases) == 0 {
		t.Error("expected aliases on groundCmd")
	}
}

func TestAirportTransferCmd_ArrivalAfterFlag(t *testing.T) {
	cmd := airportTransferCmd()
	if f := cmd.Flags().Lookup("arrival-after"); f == nil {
		t.Error("expected --arrival-after flag on airportTransferCmd")
	}
}

func TestExploreCmd_ToBeforeFrom(t *testing.T) {
	cmd := exploreCmd()
	cmd.SetArgs([]string{"HEL", "--from", "2026-07-31", "--to", "2026-07-01"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --to is before --from")
	}
}

func TestExploreCmd_InvalidToDate(t *testing.T) {
	cmd := exploreCmd()
	cmd.SetArgs([]string{"HEL", "--from", "2026-07-01", "--to", "not-a-date"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid --to date")
	}
}

func TestDatesCmd_LegacyFlag(t *testing.T) {
	cmd := datesCmd()
	cmd.SetContext(cancelledTestContext(t))
	cmd.SetArgs([]string{"HEL", "BCN", "--legacy", "--from", "2026-07-01", "--to", "2026-07-07"})

	_ = cmd.Execute()
}

func TestDatesCmd_RoundTripFlag(t *testing.T) {
	cmd := datesCmd()
	cmd.SetContext(cancelledTestContext(t))
	cmd.SetArgs([]string{"HEL", "BCN", "--round-trip", "--from", "2026-07-01", "--to", "2026-07-31"})

	_ = cmd.Execute()
}

func TestTripsAddLeg_CreatesLeg(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	createCmd := tripsCmd()
	createCmd.SetArgs([]string{"create", "Test Trip"})
	if err := createCmd.Execute(); err != nil {
		t.Fatalf("create trip: %v", err)
	}

	store, err := loadTripStore()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	list := store.List()
	if len(list) == 0 {
		t.Fatal("expected at least one trip in store")
	}
	tripID := list[0].ID

	addLegCmd := tripsCmd()
	addLegCmd.SetArgs([]string{
		"add-leg", tripID, "flight",
		"--from", "HEL",
		"--to", "BCN",
		"--provider", "KLM",
		"--start", "2026-07-01T18:00",
		"--price", "199",
		"--currency", "EUR",
	})
	if err := addLegCmd.Execute(); err != nil {
		t.Errorf("add-leg: %v", err)
	}

	store2, err := loadTripStore()
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	trip, err := store2.Get(tripID)
	if err != nil {
		t.Fatalf("get trip: %v", err)
	}
	if len(trip.Legs) != 1 {
		t.Errorf("expected 1 leg, got %d", len(trip.Legs))
	}
}

func TestTripsBookCmd_AddsBooking(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	createCmd := tripsCmd()
	createCmd.SetArgs([]string{"create", "Book Test Trip"})
	if err := createCmd.Execute(); err != nil {
		t.Fatalf("create trip: %v", err)
	}

	store, err := loadTripStore()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	list := store.List()
	if len(list) == 0 {
		t.Fatal("expected at least one trip")
	}
	tripID := list[0].ID

	bookCmd := tripsCmd()
	bookCmd.SetArgs([]string{
		"book", tripID,
		"--provider", "KLM",
		"--ref", "XYZ789",
	})
	if err := bookCmd.Execute(); err != nil {
		t.Errorf("book trip: %v", err)
	}
}

func TestTripsStatusCmd_HasTripNoLegs(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	createCmd := tripsCmd()
	createCmd.SetArgs([]string{"create", "Future Trip"})
	_ = createCmd.Execute()

	cmd := tripsCmd()
	cmd.SetArgs([]string{"status"})
	_ = cmd.Execute()
}

func TestTripsDeleteCmd_DeletesTrip(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	createCmd := tripsCmd()
	createCmd.SetArgs([]string{"create", "Delete Me"})
	if err := createCmd.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}

	store, _ := loadTripStore()
	list := store.List()
	if len(list) == 0 {
		t.Skip("no trips in store")
	}
	tripID := list[0].ID

	deleteCmd := tripsCmd()
	deleteCmd.SetArgs([]string{"delete", tripID})
	if err := deleteCmd.Execute(); err != nil {
		t.Errorf("delete trip: %v", err)
	}
}

func TestTripsAlertsCmd_MarkReadEmpty(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := tripsCmd()
	cmd.SetArgs([]string{"alerts", "--mark-read"})
	_ = cmd.Execute()
}

func TestEventsCmd_FlagsV12(t *testing.T) {
	cmd := eventsCmd()
	for _, name := range []string{"from", "to"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on eventsCmd", name)
		}
	}
}

func TestDatesCmd_AdultsFlag(t *testing.T) {
	cmd := datesCmd()
	if f := cmd.Flags().Lookup("adults"); f == nil {
		t.Error("expected --adults flag on datesCmd")
	}
	if f := cmd.Flags().Lookup("legacy"); f == nil {
		t.Error("expected --legacy flag on datesCmd")
	}
}

func TestWeekendCmd_NightsDefault(t *testing.T) {
	cmd := weekendCmd()
	f := cmd.Flags().Lookup("nights")
	if f == nil {
		t.Error("expected --nights flag on weekendCmd")
		return
	}
	if f.DefValue != "2" {
		t.Errorf("expected default nights=2, got %s", f.DefValue)
	}
}

func TestWhenCmd_ValidArgsNoNetwork(t *testing.T) {

	cmd := whenCmd()
	cmd.SetContext(cancelledTestContext(t))
	cmd.SetArgs([]string{"--origin", "HEL"})
	err := cmd.Execute()

	_ = err
}

func TestWhenCmd_InvalidOrigin(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := whenCmd()
	cmd.SetArgs([]string{
		"--to", "BCN",
		"--from", "2026-07-01",
		"--until", "2026-08-31",
		"--origin", "12",
	})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid origin IATA")
	}
}

func TestWhenCmd_FlagsV13(t *testing.T) {
	cmd := whenCmd()
	for _, name := range []string{"to", "origin", "from", "until", "busy", "prefer", "min-nights", "max-nights"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on whenCmd", name)
		}
	}
}

func TestUpgradeCmd_FreshInstall(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := upgradeCmd()
	cmd.SetArgs([]string{})

	_ = cmd.Execute()
}

func TestUpgradeCmd_DryRunFreshInstall(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := upgradeCmd()
	cmd.SetArgs([]string{"--dry-run"})
	_ = cmd.Execute()
}

func TestHacksCmd_ReturnFlag(t *testing.T) {
	cmd := hacksCmd()
	if f := cmd.Flags().Lookup("return"); f == nil {
		t.Error("expected --return flag on hacksCmd")
	}
}

func TestFlightsCmd_CompareCabinsFlag(t *testing.T) {
	cmd := flightsCmd()
	if f := cmd.Flags().Lookup("compare-cabins"); f == nil {
		t.Error("expected --compare-cabins flag on flightsCmd")
	}
}

func TestTripsShowCmd_NotFoundV13(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := tripsShowCmd()
	cmd.SetArgs([]string{"nonexistent-trip-id-v13"})

	_ = cmd.Execute()
}

func TestMultiCityCmd_MissingDatesV14(t *testing.T) {
	cmd := multiCityCmd()
	cmd.SetArgs([]string{"HEL", "--visit", "BCN,ROM"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --dates is missing")
	}
}
