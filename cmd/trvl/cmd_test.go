package main

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// ---------------------------------------------------------------------------
// Root command
// ---------------------------------------------------------------------------

func TestRootCmd_HasExpectedSubcommands(t *testing.T) {
	expected := []string{
		"flights", "dates", "hotels", "prices", "reviews",
		"explore", "grid", "destination", "trip-cost", "weekend",
		"suggest", "multi-city", "guide", "nearby", "events",
		"restaurants", "ground", "watch", "mcp", "version", "points-value",
		"rooms",
	}
	for _, name := range expected {
		found := false
		for _, c := range rootCmd.Commands() {
			if c.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("root command missing subcommand %q", name)
		}
	}
}

func TestRootCmd_FormatFlag(t *testing.T) {
	f := rootCmd.PersistentFlags().Lookup("format")
	if f == nil {
		t.Fatal("root missing persistent --format flag")
	}
	if f.DefValue != "table" {
		t.Errorf("--format default = %q, want %q", f.DefValue, "table")
	}
}

func TestRootCmd_NoCacheFlag(t *testing.T) {
	f := rootCmd.PersistentFlags().Lookup("no-cache")
	if f == nil {
		t.Fatal("root missing persistent --no-cache flag")
	}
	if f.DefValue != "false" {
		t.Errorf("--no-cache default = %q, want %q", f.DefValue, "false")
	}
}

func TestRootCmd_HelpContainsExamples(t *testing.T) {
	rootCmd.SetArgs([]string{"--help"})
	// Execute returns nil for help; capture long desc.
	long := rootCmd.Long
	if !strings.Contains(long, "trvl flights") {
		t.Error("root long desc should contain example usage")
	}
}

// ---------------------------------------------------------------------------
// flights command
// ---------------------------------------------------------------------------

func TestFlightsCmd_RequiresTwoArgs(t *testing.T) {
	// Origin is now optional (resolved from prefs/geo), so the minimum is
	// DESTINATION + DATE. A single arg must still be rejected by cobra.
	cmd := flightsCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"HEL"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with only 1 arg")
	}
}

func TestFlightsCmd_AcceptsTwoArgs(t *testing.T) {
	// DEST + DATE (no origin) must pass cobra arg validation. We disable the
	// network geo path so the run is deterministic and offline; with no
	// resolvable origin it returns a clear "no origin" error rather than an
	// arg-count error. Either a nil error (origin resolved from prefs) or a
	// non-arg-count error is acceptable here — what we assert is that cobra's
	// RangeArgs(2,3) does NOT reject the 2-arg form.
	t.Setenv("TRVL_NO_GEO", "1")
	cmd := flightsCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"NRT", "2099-01-01"})
	err := cmd.Execute()
	if err != nil && strings.Contains(err.Error(), "accepts") {
		t.Errorf("2-arg form should pass cobra arg validation, got arg-count error: %v", err)
	}
}

func TestFlightsCmd_Flags(t *testing.T) {
	cmd := flightsCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"return", ""},
		{"cabin", "economy"},
		{"stops", "any"},
		{"sort", ""},
		{"airline", "[]"},
		{"adults", "1"},
		{"format", "table"},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("flights missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("flights --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

func TestFlightsCmd_UseLine(t *testing.T) {
	cmd := flightsCmd()
	if cmd.Use != "flights [ORIGIN] DESTINATION DATE" {
		t.Errorf("flights Use = %q", cmd.Use)
	}
}

// ---------------------------------------------------------------------------
// hotels command
// ---------------------------------------------------------------------------

func TestHotelsCmd_RequiresOneArg(t *testing.T) {
	cmd := hotelsCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestHotelsCmd_RequiresCheckinCheckout(t *testing.T) {
	cmd := hotelsCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	// Provide the positional arg but omit required flags.
	cmd.SetArgs([]string{"Helsinki"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --checkin/--checkout missing")
	}
}

func TestHotelsCmd_Flags(t *testing.T) {
	cmd := hotelsCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"checkin", ""},
		{"checkout", ""},
		{"guests", "2"},
		{"stars", "0"},
		{"sort", "cheapest"},
		{"currency", ""},
		{"min-price", "0"},
		{"max-price", "0"},
		{"min-rating", "0"},
		{"max-distance", "0"},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("hotels missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("hotels --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

type fakeStdout struct{}

func (fakeStdout) Fd() uintptr { return 1 }

func TestShouldUseColor(t *testing.T) {
	tests := []struct {
		name       string
		isTerminal bool
		env        map[string]string
		want       bool
	}{
		{name: "terminal by default", isTerminal: true, want: true},
		{name: "piped by default", isTerminal: false, want: false},
		{name: "no color disables", isTerminal: true, env: map[string]string{"NO_COLOR": "1"}, want: false},
		{name: "clicolor zero disables", isTerminal: true, env: map[string]string{"CLICOLOR": "0"}, want: false},
		{name: "dumb terminal disables", isTerminal: true, env: map[string]string{"TERM": "dumb"}, want: false},
		{name: "force color overrides pipe", isTerminal: false, env: map[string]string{"FORCE_COLOR": "1"}, want: true},
		{name: "clicolor force overrides pipe", isTerminal: false, env: map[string]string{"CLICOLOR_FORCE": "1"}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldUseColor(fakeStdout{}, func(int) bool { return tt.isTerminal }, func(key string) string {
				return tt.env[key]
			})
			if got != tt.want {
				t.Fatalf("shouldUseColor() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPriceScaleApply(t *testing.T) {
	origColor := models.UseColor
	models.UseColor = true
	defer func() { models.UseColor = origColor }()

	scale := priceScale{}.With(120).With(260).With(480)

	if got := scale.Apply(120, "EUR 120"); got != "\033[32mEUR 120\033[0m" {
		t.Fatalf("expected cheapest price in green, got %q", got)
	}
	if got := scale.Apply(260, "EUR 260"); got != "EUR 260" {
		t.Fatalf("expected midpoint price uncolored, got %q", got)
	}
	if got := scale.Apply(480, "EUR 480"); got != "\033[31mEUR 480\033[0m" {
		t.Fatalf("expected highest price in red, got %q", got)
	}
	if got := scale.Apply(0, "-"); got != "-" {
		t.Fatalf("expected zero price to stay plain, got %q", got)
	}
}

func TestColorizeStops(t *testing.T) {
	origColor := models.UseColor
	models.UseColor = true
	defer func() { models.UseColor = origColor }()

	if got := colorizeStops(0); got != "\033[32mDirect\033[0m" {
		t.Fatalf("expected direct route in green, got %q", got)
	}
	if got := colorizeStops(1); got != "\033[33m1 stop\033[0m" {
		t.Fatalf("expected one stop in yellow, got %q", got)
	}
	if got := colorizeStops(2); got != "\033[31m2 stops\033[0m" {
		t.Fatalf("expected multi-stop route in red, got %q", got)
	}
}

func TestColorizeRating(t *testing.T) {
	origColor := models.UseColor
	models.UseColor = true
	defer func() { models.UseColor = origColor }()

	if got := colorizeRating(9.6, "9.6"); got != "\033[32m9.6\033[0m" {
		t.Fatalf("expected strong rating in green, got %q", got)
	}
	if got := colorizeRating(8.0, "8.0"); got != "\033[33m8.0\033[0m" {
		t.Fatalf("expected middling rating in yellow, got %q", got)
	}
	if got := colorizeRating(6.4, "6.4"); got != "\033[31m6.4\033[0m" {
		t.Fatalf("expected weak rating in red, got %q", got)
	}
	if got := colorizeRating(0, "-"); got != "-" {
		t.Fatalf("expected missing rating to stay plain, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// airport-transfer command
// ---------------------------------------------------------------------------

func TestAirportTransferCmd_RequiresThreeArgs(t *testing.T) {
	cmd := airportTransferCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"CDG"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with only 1 arg")
	}
}

func TestAirportTransferCmd_Flags(t *testing.T) {
	cmd := airportTransferCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"currency", ""},
		{"provider", ""},
		{"max-price", "0"},
		{"type", ""},
		{"arrival-after", ""},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("airport-transfer missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("airport-transfer --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

// ---------------------------------------------------------------------------
// ground command
// ---------------------------------------------------------------------------

func TestGroundCmd_RequiresThreeArgs(t *testing.T) {
	cmd := groundCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"Prague"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with only 1 arg")
	}
}

func TestGroundCmd_Aliases(t *testing.T) {
	cmd := groundCmd()
	aliases := cmd.Aliases
	want := map[string]bool{"bus": true, "train": true}
	for _, a := range aliases {
		delete(want, a)
	}
	for missing := range want {
		t.Errorf("ground missing alias %q", missing)
	}
}

func TestGroundCmd_Flags(t *testing.T) {
	cmd := groundCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"currency", ""},
		{"provider", ""},
		{"max-price", "0"},
		{"type", ""},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("ground missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("ground --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

// ---------------------------------------------------------------------------
// deals command
// ---------------------------------------------------------------------------

func TestDealsCmd_NoRequiredArgs(t *testing.T) {
	// deals takes no positional args; it should not fail on arg count.
	cmd := dealsCmd()
	if cmd.Args != nil {
		// Cobra nil means ArbitraryArgs (0+). ExactArgs would be a function.
		// We just verify the Use line has no ARG placeholder in uppercase.
		if strings.Contains(cmd.Use, "ORIGIN") || strings.Contains(cmd.Use, "DESTINATION") {
			t.Error("deals should not require positional args")
		}
	}
}

func TestDealsCmd_Flags(t *testing.T) {
	cmd := dealsCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"from", ""},
		{"max-price", "0"},
		{"type", ""},
		{"hours", "48"},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("deals missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("deals --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

// ---------------------------------------------------------------------------
// watch command + subcommands
// ---------------------------------------------------------------------------

func TestWatchCmd_HasSubcommands(t *testing.T) {
	cmd := watchCmd()
	expected := []string{"add", "list", "remove", "check", "daemon", "history"}
	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("watch missing subcommand %q", name)
		}
	}
}

func TestWatchAddCmd_RejectsZeroArgs(t *testing.T) {
	cmd := watchAddCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestWatchAddCmd_AcceptsRouteWatch(t *testing.T) {
	// Route watch: 2 args, no --depart required.
	cmd := watchAddCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"HEL", "BCN", "--below", "200"})
	err := cmd.Execute()
	// May fail at store.Load but not at arg validation.
	if err != nil && strings.Contains(err.Error(), "arg") {
		t.Errorf("should accept 2 args without --depart: %v", err)
	}
}

func TestWatchAddCmd_Flags(t *testing.T) {
	cmd := watchAddCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"depart", ""},
		{"return", ""},
		{"from", ""},
		{"to", ""},
		{"below", "0"},
		{"currency", ""},
		{"type", "flight"},
		{"last-minute", "false"},
		{"last-minute-drop", "25"},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("watch add missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("watch add --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

func TestWatchRemoveCmd_RequiresOneArg(t *testing.T) {
	cmd := watchRemoveCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestWatchHistoryCmd_RequiresOneArg(t *testing.T) {
	cmd := watchHistoryCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestWatchListCmd_NoArgs(t *testing.T) {
	cmd := watchListCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"extra"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with unexpected args")
	}
}

func TestWatchCheckCmd_NoArgs(t *testing.T) {
	cmd := watchCheckCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"extra"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with unexpected args")
	}
}

func TestWatchDaemonCmd_NoArgs(t *testing.T) {
	cmd := watchDaemonCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"extra"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with unexpected args")
	}
}

func TestWatchDaemonCmd_Flags(t *testing.T) {
	cmd := watchDaemonCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"every", "6h0m0s"},
		{"run-now", "true"},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("watch daemon missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("watch daemon --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

// ---------------------------------------------------------------------------
// dates command
// ---------------------------------------------------------------------------

func TestDatesCmd_RequiresTwoArgs(t *testing.T) {
	// Origin is now optional; the minimum is DESTINATION. Zero args must
	// still be rejected by cobra's RangeArgs(1,2).
	cmd := datesCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestDatesCmd_Flags(t *testing.T) {
	cmd := datesCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"from", ""},
		{"to", ""},
		{"duration", "7"},
		{"round-trip", "false"},
		{"adults", "1"},
		{"format", "table"},
		{"legacy", "false"},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("dates missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("dates --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

// ---------------------------------------------------------------------------
// explore command
// ---------------------------------------------------------------------------

func TestExploreCmd_RequiresOneArg(t *testing.T) {
	// Origin is now optional (resolved from prefs/geo). With geo disabled and
	// no home airport, an explicit origin is still required — assert that
	// path rather than a cobra arg-count error.
	tmp := t.TempDir()
	setTestHome(t, tmp)
	t.Setenv("TRVL_NO_GEO", "1")
	cmd := exploreCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error when no origin and none resolvable")
	}
}

func TestExploreCmd_Flags(t *testing.T) {
	cmd := exploreCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"from", ""},
		{"to", ""},
		{"type", "round-trip"},
		{"stops", "any"},
		{"format", "table"},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("explore missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("explore --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

// ---------------------------------------------------------------------------
// grid command
// ---------------------------------------------------------------------------

func TestGridCmd_RequiresTwoArgs(t *testing.T) {
	cmd := gridCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"HEL"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with only 1 arg")
	}
}

func TestGridCmd_Flags(t *testing.T) {
	cmd := gridCmd()
	flags := []string{"depart-from", "depart-to", "return-from", "return-to", "format"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("grid missing --%s flag", name)
		}
	}
}

// ---------------------------------------------------------------------------
// destination command
// ---------------------------------------------------------------------------

func TestDestinationCmd_RequiresOneArg(t *testing.T) {
	cmd := destinationCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestDestinationCmd_HasDatesFlag(t *testing.T) {
	cmd := destinationCmd()
	if cmd.Flags().Lookup("dates") == nil {
		t.Error("destination missing --dates flag")
	}
}

// ---------------------------------------------------------------------------
// prices command
// ---------------------------------------------------------------------------

func TestPricesCmd_RequiresOneArg(t *testing.T) {
	cmd := pricesCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestPricesCmd_RequiresCheckinCheckout(t *testing.T) {
	cmd := pricesCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"/g/11test"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --checkin/--checkout missing")
	}
}

func TestPricesCmd_Flags(t *testing.T) {
	cmd := pricesCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"checkin", ""},
		{"checkout", ""},
		{"currency", ""},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("prices missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("prices --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

func TestPricesCmd_HasHoldAndRebookSubcommands(t *testing.T) {
	cmd := pricesCmd()
	for _, name := range []string{"hold", "rebook"} {
		found := false
		for _, sub := range cmd.Commands() {
			if sub.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("prices missing %q subcommand", name)
		}
	}
}

// ---------------------------------------------------------------------------
// reviews command
// ---------------------------------------------------------------------------
