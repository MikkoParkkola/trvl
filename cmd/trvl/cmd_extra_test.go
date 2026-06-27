package main

import (
	"strings"
	"testing"
)

func TestReviewsCmd_UseLine(t *testing.T) {
	if reviewsCmd.Use != "reviews <hotel_id>" {
		t.Errorf("reviews Use = %q, want %q", reviewsCmd.Use, "reviews <hotel_id>")
	}
}

func TestReviewsCmd_ArgsIsExactOne(t *testing.T) {
	// reviewsCmd uses cobra.ExactArgs(1); verify by testing the Args validator.
	if reviewsCmd.Args == nil {
		t.Fatal("reviews Args validator is nil")
	}
	if err := reviewsCmd.Args(reviewsCmd, []string{}); err == nil {
		t.Error("expected error with 0 args")
	}
	if err := reviewsCmd.Args(reviewsCmd, []string{"id1"}); err != nil {
		t.Errorf("unexpected error with 1 arg: %v", err)
	}
	if err := reviewsCmd.Args(reviewsCmd, []string{"id1", "id2"}); err == nil {
		t.Error("expected error with 2 args")
	}
}

func TestReviewsCmd_Flags(t *testing.T) {
	flags := []struct {
		name     string
		defValue string
	}{
		{"limit", "10"},
		{"sort", "newest"},
		{"format", "table"},
	}
	for _, tt := range flags {
		f := reviewsCmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("reviews missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("reviews --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

func TestRoomsCmd_UseLine(t *testing.T) {
	cmd := roomsCmd()
	if cmd.Use != "rooms <hotel_name_or_id>" {
		t.Errorf("rooms Use = %q, want %q", cmd.Use, "rooms <hotel_name_or_id>")
	}
}

func TestRoomsCmd_ArgsIsExactOne(t *testing.T) {
	cmd := roomsCmd()
	if cmd.Args == nil {
		t.Fatal("rooms Args validator is nil")
	}
	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Error("expected error with 0 args")
	}
	if err := cmd.Args(cmd, []string{"Hotel Lutetia Paris"}); err != nil {
		t.Errorf("unexpected error with 1 arg: %v", err)
	}
	if err := cmd.Args(cmd, []string{"id1", "id2"}); err == nil {
		t.Error("expected error with 2 args")
	}
}

func TestRoomsCmd_Flags(t *testing.T) {
	cmd := roomsCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"checkin", ""},
		{"checkout", ""},
		{"currency", "USD"},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("rooms missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("rooms --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

func TestLooksLikeGoogleHotelID(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "/g/11b6d4_v_4", want: true},
		{value: "ChIJy7MSZP0LkkYRZw2dDekQP78", want: true},
		{value: "0x123:0x456", want: true},
		{value: "Hotel Lutetia Paris", want: false},
	}

	for _, tt := range tests {
		if got := looksLikeGoogleHotelID(tt.value); got != tt.want {
			t.Errorf("looksLikeGoogleHotelID(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// suggest command
// ---------------------------------------------------------------------------

func TestSuggestCmd_RequiresTwoArgs(t *testing.T) {
	cmd := suggestCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"HEL"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with only 1 arg")
	}
}

func TestSuggestCmd_Flags(t *testing.T) {
	cmd := suggestCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"around", ""},
		{"flex", "7"},
		{"round-trip", "false"},
		{"duration", "7"},
		{"format", "table"},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("suggest missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("suggest --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

// ---------------------------------------------------------------------------
// multi-city command
// ---------------------------------------------------------------------------

func TestMultiCityCmd_RequiresOneArg(t *testing.T) {
	cmd := multiCityCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestMultiCityCmd_RequiresVisitAndDates(t *testing.T) {
	cmd := multiCityCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"HEL"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --visit/--dates missing")
	}
}

func TestMultiCityCmd_Flags(t *testing.T) {
	cmd := multiCityCmd()
	flags := []string{"visit", "dates", "format"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("multi-city missing --%s flag", name)
		}
	}
}

// ---------------------------------------------------------------------------
// guide command
// ---------------------------------------------------------------------------

func TestGuideCmd_RequiresOneArg(t *testing.T) {
	cmd := guideCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

// ---------------------------------------------------------------------------
// nearby command
// ---------------------------------------------------------------------------

func TestNearbyCmd_RequiresTwoArgs(t *testing.T) {
	cmd := nearbyCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"41.38"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with only 1 arg")
	}
}

func TestNearbyCmd_Flags(t *testing.T) {
	cmd := nearbyCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"category", "all"},
		{"radius", "500"},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("nearby missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("nearby --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

// ---------------------------------------------------------------------------
// events command
// ---------------------------------------------------------------------------

func TestEventsCmd_RequiresOneArg(t *testing.T) {
	cmd := eventsCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestEventsCmd_RequiresFromTo(t *testing.T) {
	cmd := eventsCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"Barcelona"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --from/--to missing")
	}
}

func TestEventsCmd_MissingAPIKeyReturnsError(t *testing.T) {
	t.Setenv("TICKETMASTER_API_KEY", "")

	cmd := eventsCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"Barcelona", "--from", "2026-07-01", "--to", "2026-07-08"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing API key to return an error")
	}
	if !strings.Contains(err.Error(), "TICKETMASTER_API_KEY") {
		t.Fatalf("expected missing API key error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// restaurants command
// ---------------------------------------------------------------------------

func TestRestaurantsCmd_ArgsIsExactTwo(t *testing.T) {
	if restaurantsCmd.Args == nil {
		t.Fatal("restaurants Args validator is nil")
	}
	// Now accepts 1 arg ("lat,lon") or 2 args ("lat" "lon").
	if err := restaurantsCmd.Args(restaurantsCmd, []string{"41.38"}); err != nil {
		t.Errorf("unexpected error with 1 arg (lat,lon): %v", err)
	}
	if err := restaurantsCmd.Args(restaurantsCmd, []string{"41.38", "2.17"}); err != nil {
		t.Errorf("unexpected error with 2 args: %v", err)
	}
}

func TestRestaurantsCmd_Flags(t *testing.T) {
	flags := []struct {
		name     string
		defValue string
	}{
		{"query", "restaurants"},
		{"limit", "10"},
	}
	for _, tt := range flags {
		f := restaurantsCmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("restaurants missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("restaurants --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

// ---------------------------------------------------------------------------
// trip-cost command
// ---------------------------------------------------------------------------

func TestTripCostCmd_RequiresTwoArgs(t *testing.T) {
	cmd := tripCostCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"HEL"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with only 1 arg")
	}
}

func TestTripCostCmd_RequiresDepartReturn(t *testing.T) {
	cmd := tripCostCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"HEL", "BCN"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --depart/--return missing")
	}
}

func TestTripCostCmd_Flags(t *testing.T) {
	cmd := tripCostCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"depart", ""},
		{"return", ""},
		{"guests", "1"},
		{"currency", ""},
		{"format", "table"},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("trip-cost missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("trip-cost --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

// ---------------------------------------------------------------------------
// weekend command
// ---------------------------------------------------------------------------

func TestWeekendCmd_RequiresOneArg(t *testing.T) {
	cmd := weekendCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with 0 args")
	}
}

func TestWeekendCmd_Flags(t *testing.T) {
	cmd := weekendCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"month", ""},
		{"budget", "0"},
		{"nights", "2"},
		{"format", "table"},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("weekend missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("weekend --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}

// ---------------------------------------------------------------------------
// mcp command
// ---------------------------------------------------------------------------

func TestMcpCmd_Flags(t *testing.T) {
	cmd := mcpCmd()
	flags := []struct {
		name     string
		defValue string
	}{
		{"http", "false"},
		{"host", "127.0.0.1"},
		{"port", "8080"},
		{"token", ""},
	}
	for _, tt := range flags {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("mcp missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("mcp --%s default = %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}
