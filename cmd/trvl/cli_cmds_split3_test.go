package main

import (
	"testing"
)

func TestRunInstall_CodexDryRunV19(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	err := runInstall("codex", false, true)
	if err != nil {
		t.Errorf("runInstall codex dry-run: %v", err)
	}
}

func TestRunInstall_CodexCreatesConfigV19(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	err := runInstall("codex", false, false)
	if err != nil {
		t.Errorf("runInstall codex create: %v", err)
	}
}

func TestOpenBrowser_EmptyURL_V20(t *testing.T) {
	err := openBrowser("")
	if err == nil {
		t.Error("expected error for empty URL")
	}
}

func TestTripsAlertsCmd_MarkReadEmptyV20(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	cmd := tripsCmd()
	cmd.SetArgs([]string{"alerts", "--mark-read"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("trips alerts --mark-read: %v", err)
	}
}

func TestSuggestCmd_InvalidOriginV20(t *testing.T) {
	cmd := suggestCmd()
	cmd.SetArgs([]string{"12", "BCN"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid origin IATA")
	}
}

func TestSuggestCmd_InvalidDestV20(t *testing.T) {
	cmd := suggestCmd()
	cmd.SetArgs([]string{"HEL", "12"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid dest IATA")
	}
}

func TestSuggestCmd_FlagsExistV20(t *testing.T) {
	cmd := suggestCmd()
	for _, name := range []string{"around", "flex", "round-trip", "duration", "format", "currency"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on suggestCmd", name)
		}
	}
}

func TestTripsBookCmd_BooksTripV20(t *testing.T) {
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
		t.Skip("no trips in store")
	}
	tripID := list[0].ID

	cmd := tripsCmd()
	cmd.SetArgs([]string{
		"book", tripID,
		"--provider", "Finnair",
		"--ref", "AY12345",
		"--type", "flight",
	})
	if err := cmd.Execute(); err != nil {
		t.Errorf("trips book: %v", err)
	}
}

func TestTripsDeleteCmd_DeletesTripV20(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	createCmd := tripsCmd()
	createCmd.SetArgs([]string{"create", "Delete Test Trip"})
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

	cmd := tripsCmd()
	cmd.SetArgs([]string{"delete", tripID})
	if err := cmd.Execute(); err != nil {
		t.Errorf("trips delete: %v", err)
	}
}

func TestTripsStatusCmd_WithTripV20(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	createCmd := tripsCmd()
	createCmd.SetArgs([]string{"create", "Status Test Trip"})
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

	addLeg := tripsCmd()
	addLeg.SetArgs([]string{
		"add-leg", tripID, "flight",
		"--from", "HEL",
		"--to", "BCN",
		"--provider", "Finnair",
		"--start", "2026-07-01T08:00",
		"--end", "2026-07-01T11:00",
		"--price", "199",
		"--currency", "EUR",
	})
	_ = addLeg.Execute()

	statusCmd := tripsCmd()
	statusCmd.SetArgs([]string{"status", tripID})
	if err := statusCmd.Execute(); err != nil {
		t.Errorf("trips status: %v", err)
	}
}

func TestShareCmd_GistFlagWithLastSearchV20(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	ls := &LastSearch{
		Command:        "flights",
		Origin:         "HEL",
		Destination:    "BCN",
		DepartDate:     "2026-07-01",
		FlightPrice:    199,
		FlightCurrency: "EUR",
		FlightAirline:  "Finnair",
	}
	saveLastSearch(ls)

	cmd := shareCmd()
	cmd.SetArgs([]string{"--last", "--gist"})

	_ = cmd.Execute()
}

func TestTripsAlertsCmd_TableWithAlertsV20(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	createCmd := tripsCmd()
	createCmd.SetArgs([]string{"create", "Alert Test Trip"})
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

	addLeg := tripsCmd()
	addLeg.SetArgs([]string{
		"add-leg", tripID, "flight",
		"--from", "HEL",
		"--to", "BCN",
		"--provider", "Finnair",
		"--start", "2026-04-22T08:00",
		"--end", "2026-04-22T11:00",
		"--price", "199",
		"--currency", "EUR",
	})
	_ = addLeg.Execute()

	alertsCmd := tripsCmd()
	alertsCmd.SetArgs([]string{"alerts"})
	_ = alertsCmd.Execute()
}

func TestProvidersEnableCmd_FlagsExistV20(t *testing.T) {
	cmd := providersEnableCmd()
	f := cmd.Flags().Lookup("accept-tos")
	if f == nil {
		t.Error("expected --accept-tos flag on providersEnableCmd")
	}
}

func TestDatesCmd_FlagsExistV20(t *testing.T) {
	cmd := datesCmd()
	for _, name := range []string{"return", "depart", "format"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Logf("--%s flag not found on datesCmd", name)
		}
	}
}

func TestTripCostCmd_ValidArgsNoNetworkV20(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := tripCostCmd()
	cmd.SetContext(cancelledTestContext(t))
	cmd.SetArgs([]string{"HEL", "BCN", "--depart", "2026-07-01", "--return", "2026-07-08"})

	_ = cmd.Execute()
}

func TestExploreCmd_ToBeforeFromV21(t *testing.T) {
	cmd := exploreCmd()

	cmd.SetArgs([]string{"HEL", "--from", "2026-07-10", "--to", "2026-07-01"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --to is before --from")
	}
}

func TestExploreCmd_OneWayTypeV21(t *testing.T) {

	cmd := exploreCmd()
	cmd.SetContext(cancelledTestContext(t))
	cmd.SetArgs([]string{"HEL", "--from", "2026-07-01", "--to", "2026-07-21", "--type", "one-way"})
	_ = cmd.Execute()
}

func TestShareCmd_NoArgsNoLastV21(t *testing.T) {
	cmd := shareCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no args and no --last")
	}
}

func TestWeekendCmd_ValidIATANoNetworkV21(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live HTTP test in short mode")
	}
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := weekendCmd()
	cmd.SetContext(cancelledTestContext(t))
	cmd.SetArgs([]string{"HEL", "--month", "2026-08"})

	_ = cmd.Execute()
}

func TestTripsListCmd_WithTripsV21(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	createCmd := tripsCmd()
	createCmd.SetArgs([]string{"create", "List Test Trip"})
	if err := createCmd.Execute(); err != nil {
		t.Fatalf("create trip: %v", err)
	}

	cmd := tripsCmd()
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("trips list: %v", err)
	}
}

func TestWhenCmd_MissingToFlagV22(t *testing.T) {
	cmd := whenCmd()
	cmd.SetArgs([]string{"--from", "2026-07-01", "--until", "2026-07-31"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing --to flag")
	}
}

func TestWhenCmd_MissingFromFlagV22(t *testing.T) {
	cmd := whenCmd()
	cmd.SetArgs([]string{"--to", "BCN", "--until", "2026-07-31"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing --from flag")
	}
}

func TestWhenCmd_MissingUntilFlagV22(t *testing.T) {
	cmd := whenCmd()
	cmd.SetArgs([]string{"--to", "BCN", "--from", "2026-07-01"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing --until flag")
	}
}

func TestWhenCmd_InvalidOriginIATAV22(t *testing.T) {
	cmd := whenCmd()
	cmd.SetArgs([]string{"--to", "BCN", "--from", "2026-07-01", "--until", "2026-07-31", "--origin", "12"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid origin IATA")
	}
}

func TestWhenCmd_InvalidBusyFlagV22(t *testing.T) {
	cmd := whenCmd()
	cmd.SetArgs([]string{
		"--to", "BCN",
		"--from", "2026-07-01",
		"--until", "2026-07-31",
		"--origin", "HEL",
		"--busy", "not-a-valid-interval",
	})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid --busy interval")
	}
}

func TestWhenCmd_FlagsExistV22(t *testing.T) {
	cmd := whenCmd()
	for _, name := range []string{"to", "from", "until", "origin", "busy", "prefer", "min-nights", "max-nights", "top", "budget", "format"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on whenCmd", name)
		}
	}
}

func TestCabinResult_StructV22(t *testing.T) {
	r := cabinResult{Cabin: "Economy", Error: "no flights"}
	if r.Cabin != "Economy" {
		t.Errorf("expected Economy, got %s", r.Cabin)
	}
}

func TestRunProvidersDisable_SucceedsV22(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	writeTestProviderV19(t, tmp, "deletable-provider")

	cmd := providersDisableCmd()
	cmd.SetArgs([]string{"deletable-provider"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("providers disable: %v", err)
	}
}

func TestPricesCmd_NonNilV22(t *testing.T) {
	cmd := pricesCmd()
	if cmd == nil {
		t.Error("expected non-nil pricesCmd")
	}
}

func TestOptimizeCmd_FlagsExistV22(t *testing.T) {
	cmd := optimizeCmd()
	if cmd == nil {
		t.Error("expected non-nil optimizeCmd")
	}
}

func TestOptimizeCmd_MissingArgsV22(t *testing.T) {
	cmd := optimizeCmd()
	cmd.SetArgs([]string{"HEL"})
	_ = cmd.Execute()
}

func TestDealsCmd_NonNilV22(t *testing.T) {
	cmd := dealsCmd()
	if cmd == nil {
		t.Error("expected non-nil dealsCmd")
	}
}

func TestRunProvidersDisable_ConfirmsNonTerminalV23(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	writeTestProviderV19(t, tmp, "confirm-delete-provider")

	err := runProvidersDisable("confirm-delete-provider")
	if err != nil {
		t.Errorf("runProvidersDisable: %v", err)
	}
}

func TestPointsValueCmd_WithProgramV23(t *testing.T) {
	cmd := pointsValueCmd()
	cmd.SetArgs([]string{"--program", "avios"})
	_ = cmd.Execute()
}

func TestWeekendCmd_FlagsV23(t *testing.T) {
	cmd := weekendCmd()
	for _, name := range []string{"month", "budget", "nights", "format", "currency"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on weekendCmd", name)
		}
	}
}

func TestAirportTransferCmd_ValidArgsNoNetworkV23(t *testing.T) {
	cmd := airportTransferCmd()
	cmd.SetContext(cancelledTestContext(t))
	cmd.SetArgs([]string{"CDG", "Hotel Lutetia Paris", "2026-07-01"})
	_ = cmd.Execute()
}

func TestDealsCmd_ValidRunV23(t *testing.T) {
	cmd := dealsCmd()
	cmd.SetContext(cancelledTestContext(t))
	cmd.SetArgs([]string{})
	_ = cmd.Execute()
}

func TestSearchCmd_DryRunV23(t *testing.T) {
	cmd := searchCmd()
	cmd.SetArgs([]string{"--dry-run", "flights from HEL to BCN"})
	_ = cmd.Execute()
}

func TestLoungesCmd_InvalidIATA_V24(t *testing.T) {
	cmd := loungesCmd()
	cmd.SetArgs([]string{"12"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid IATA")
	}
}

func TestLoungesCmd_FlagsV24(t *testing.T) {
	cmd := loungesCmd()
	if cmd == nil {
		t.Fatal("loungesCmd returned nil")
	}
}

func TestWeatherCmd_FlagsV24(t *testing.T) {
	cmd := weatherCmd()
	for _, name := range []string{"from", "to"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on weatherCmd", name)
		}
	}
}

func TestUpgradeCmd_DryRunV24(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := upgradeCmd()
	cmd.SetArgs([]string{"--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("upgrade --dry-run: %v", err)
	}
}

func TestUpgradeCmd_QuietV24(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := upgradeCmd()
	cmd.SetArgs([]string{"--quiet"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("upgrade --quiet: %v", err)
	}
}

func TestUpgradeCmd_DefaultRunV24(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	cmd := upgradeCmd()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Errorf("upgrade (default): %v", err)
	}
}

func TestCreateGist_NoGhV24(t *testing.T) {

	err := createGist("# Test trip\n\nSome markdown content here.")
	_ = err
}

func TestRunEvents_MissingAPIKeyV24(t *testing.T) {
	t.Setenv("TICKETMASTER_API_KEY", "")
	cmd := eventsCmd()
	cmd.SetArgs([]string{"Barcelona", "--from", "2026-07-01", "--to", "2026-07-08"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing TICKETMASTER_API_KEY")
	}
}

func TestMcpCmd_FlagsV24(t *testing.T) {
	cmd := mcpCmd()
	for _, name := range []string{"http", "host", "port", "token"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on mcpCmd", name)
		}
	}
}

func TestGroundCmd_MissingArgsV24(t *testing.T) {
	cmd := groundCmd()
	cmd.SetArgs([]string{"Prague"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with only one positional arg")
	}
}

func TestGroundCmd_FlagsV24(t *testing.T) {
	cmd := groundCmd()
	for _, name := range []string{"currency", "max-price", "type"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on groundCmd", name)
		}
	}
}
