package main

import "testing"

// ---------------------------------------------------------------------------
// These tests execute cobra commands with invalid arguments to cover the
// validation and early-return error paths in the RunE closures.
// ---------------------------------------------------------------------------

func TestDatesCmd_RequiresThreeArgs(t *testing.T) {
	cmd := datesCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"HEL"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 1 arg")
	}
}

func TestExploreCmd_RequiresOneArg_Extra(t *testing.T) {
	cmd := exploreCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestGridCmd_RequiresArgs(t *testing.T) {
	cmd := gridCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestDealsCmd_AcceptsZeroArgs(t *testing.T) {
	// deals command accepts 0 args (shows all deals).
	cmd := dealsCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetContext(cancelledTestContext(t))
	cmd.SetArgs([]string{})
	// Should not panic; may succeed or fail depending on network.
	_ = cmd.Execute()
}

func TestMultiCityCmd_RequiresArgs(t *testing.T) {
	cmd := multiCityCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestSuggestCmd_RequiresArgs(t *testing.T) {
	cmd := suggestCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestWeatherCmd_RequiresOneArg(t *testing.T) {
	cmd := weatherCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestWeekendCmd_RequiresArgs(t *testing.T) {
	cmd := weekendCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestHacksCmd_RequiresArgs(t *testing.T) {
	cmd := hacksCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestTripCostCmd_RequiresArgs(t *testing.T) {
	cmd := tripCostCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestAirportTransferCmd_RequiresArgs(t *testing.T) {
	cmd := airportTransferCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestDestinationCmd_RequiresArgs(t *testing.T) {
	cmd := destinationCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestGuideCmd_RequiresArgs(t *testing.T) {
	cmd := guideCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestEventsCmd_RequiresArgs(t *testing.T) {
	cmd := eventsCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestDiscoverCmd_RequiresFromFlag(t *testing.T) {
	cmd := discoverCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	// No --from flag, should error.
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error without --from flag")
	}
}

func TestWhenCmd_MissingRequiredFlags(t *testing.T) {
	cmd := whenCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error without --to flag")
	}
}

func TestPrefsSetCmd_RequiresTwoArgs(t *testing.T) {
	cmd := prefsSetCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"only_one"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 1 arg")
	}
}
