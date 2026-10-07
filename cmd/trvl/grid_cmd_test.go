package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gridOffline gives a grid test an empty home and no geo-IP, so the result
// cannot depend on the machine's network location (MIK-8010).
func gridOffline(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	setTestHome(t, dir)
	t.Setenv("TRVL_NO_GEO", "1")
	return dir
}

func TestGridCmd_OneArgWithoutOriginFails(t *testing.T) {
	gridOffline(t)
	cmd := gridCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"HEL"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no origin given") {
		t.Errorf("one arg with no saved or detectable origin must fail on the origin, got %v", err)
	}
}

func TestGridCmd_OneArgInvalidDestinationWithoutOriginFailsOnOrigin(t *testing.T) {
	// Control for the preferences test below: with no saved airport the same
	// arguments must stop at the origin, so that test cannot pass by skipping it.
	gridOffline(t)
	cmd := gridCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"1X"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no origin given") {
		t.Errorf("without a saved airport the origin step must fail first, got %v", err)
	}
}

func TestGridCmd_OneArgUsesSavedHomeAirport(t *testing.T) {
	home := gridOffline(t)
	if err := os.MkdirAll(filepath.Join(home, ".trvl"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".trvl", "preferences.json"), []byte(`{"home_airports":["HEL"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := gridCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	// An invalid destination stops the command before any search, after the
	// origin step: reaching that error proves the saved home airport resolved.
	cmd.SetArgs([]string{"1X"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid destination") {
		t.Errorf("origin should resolve from preferences and the destination check fail, got %v", err)
	}
}
