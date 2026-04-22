package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// Root command — exact subcommand count
// ---------------------------------------------------------------------------

func TestRootCmd_SubcommandCount(t *testing.T) {
	// rootCmd is the package-level var registered in root.go init().
	const want = 46
	got := len(rootCmd.Commands())
	if got != want {
		names := make([]string, 0, got)
		for _, c := range rootCmd.Commands() {
			names = append(names, c.Name())
		}
		t.Errorf("rootCmd has %d subcommands, want %d; got: %v", got, want, names)
	}
}

// ---------------------------------------------------------------------------
// route command
// ---------------------------------------------------------------------------

func TestRouteCmd_NonNil(t *testing.T) {
	cmd := routeCmd()
	if cmd == nil {
		t.Fatal("routeCmd() returned nil")
	}
}

func TestRouteCmd_Use(t *testing.T) {
	cmd := routeCmd()
	want := "route ORIGIN DESTINATION [DATE]"
	if cmd.Use != want {
		t.Errorf("routeCmd Use = %q, want %q", cmd.Use, want)
	}
}

func TestRouteCmd_Args_AcceptsTwoArgs(t *testing.T) {
	cmd := routeCmd()
	if cmd.Args == nil {
		t.Fatal("routeCmd Args validator is nil")
	}
	if err := cmd.Args(cmd, []string{"HEL", "BCN"}); err != nil {
		t.Errorf("unexpected error with 2 args: %v", err)
	}
}

func TestRouteCmd_Args_AcceptsThreeArgs(t *testing.T) {
	cmd := routeCmd()
	if err := cmd.Args(cmd, []string{"HEL", "BCN", "2026-07-01"}); err != nil {
		t.Errorf("unexpected error with 3 args: %v", err)
	}
}

func TestRouteCmd_Args_RejectsOneArg(t *testing.T) {
	cmd := routeCmd()
	if err := cmd.Args(cmd, []string{"HEL"}); err == nil {
		t.Error("expected error with 1 arg")
	}
}

func TestRouteCmd_Args_RejectsFourArgs(t *testing.T) {
	cmd := routeCmd()
	if err := cmd.Args(cmd, []string{"HEL", "BCN", "2026-07-01", "extra"}); err == nil {
		t.Error("expected error with 4 args")
	}
}

func TestRouteCmd_FlagDefaults(t *testing.T) {
	cmd := routeCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"sort", "price"},
		{"max-transfers", "3"},
		{"currency", ""},
		{"prefer", ""},
		{"avoid", ""},
		{"depart-after", ""},
		{"arrive-by", ""},
		{"max-price", "0"},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("routeCmd missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("routeCmd --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

// ---------------------------------------------------------------------------
// ground command — aliases (belt-and-suspenders alongside cmd_test.go)
// ---------------------------------------------------------------------------

func TestGroundCmd_HasBusAndTrainAliases(t *testing.T) {
	cmd := groundCmd()
	want := map[string]bool{"bus": true, "train": true}
	for _, a := range cmd.Aliases {
		delete(want, a)
	}
	for missing := range want {
		t.Errorf("groundCmd missing alias %q", missing)
	}
}

// ---------------------------------------------------------------------------
// trip command
// ---------------------------------------------------------------------------

func TestTripCmd_NonNil(t *testing.T) {
	cmd := tripCmd()
	if cmd == nil {
		t.Fatal("tripCmd() returned nil")
	}
}

func TestTripCmd_RequiresExactlyTwoArgs(t *testing.T) {
	cmd := tripCmd()
	if cmd.Args == nil {
		t.Fatal("tripCmd Args validator is nil")
	}
	if err := cmd.Args(cmd, []string{"HEL"}); err == nil {
		t.Error("expected error with 1 arg")
	}
	if err := cmd.Args(cmd, []string{"HEL", "BCN"}); err != nil {
		t.Errorf("unexpected error with 2 args: %v", err)
	}
	if err := cmd.Args(cmd, []string{"HEL", "BCN", "extra"}); err == nil {
		t.Error("expected error with 3 args")
	}
}

// ---------------------------------------------------------------------------
// shouldUseColor — env-variable edge cases not in the existing test table
// ---------------------------------------------------------------------------

func TestShouldUseColor_NoColorEnvDisables(t *testing.T) {
	got := shouldUseColor(fakeStdout{}, func(int) bool { return true }, func(key string) string {
		if key == "NO_COLOR" {
			return "1"
		}
		return ""
	})
	if got {
		t.Error("NO_COLOR set: expected false")
	}
}

func TestShouldUseColor_DumbTermDisables(t *testing.T) {
	got := shouldUseColor(fakeStdout{}, func(int) bool { return true }, func(key string) string {
		if key == "TERM" {
			return "dumb"
		}
		return ""
	})
	if got {
		t.Error("TERM=dumb: expected false")
	}
}

// ---------------------------------------------------------------------------
// airport completion
// ---------------------------------------------------------------------------

func TestAirportCompletion_TwoArgsMeansNoCompletion(t *testing.T) {
	suggestions, directive := airportCompletion(nil, []string{"HEL", "NRT"}, "BC")
	if len(suggestions) != 0 {
		t.Errorf("expected 0 suggestions with 2 args, got %d", len(suggestions))
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("unexpected directive: %v", directive)
	}
}

func TestAirportCompletion_HELPrefixReturnsHelsinki(t *testing.T) {
	suggestions, _ := airportCompletion(nil, nil, "HEL")
	found := false
	for _, s := range suggestions {
		if len(s) >= 3 && s[:3] == "HEL" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected HEL in airport completion suggestions")
	}
}
