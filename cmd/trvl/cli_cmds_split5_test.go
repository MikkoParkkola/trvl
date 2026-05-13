package main

import (
	"bufio"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/providers"
)

func TestSplitLines_MultiLine(t *testing.T) {
	lines := splitLines("a\nb\nc")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "a" || lines[1] != "b" || lines[2] != "c" {
		t.Errorf("wrong lines: %v", lines)
	}
}

func TestSplitAndTrim_Basic(t *testing.T) {
	out := splitAndTrim("HEL, AMS ,NRT")
	if len(out) != 3 || out[0] != "HEL" || out[1] != "AMS" || out[2] != "NRT" {
		t.Errorf("unexpected: %v", out)
	}
}

func TestSplitAndTrim_Empty(t *testing.T) {
	out := splitAndTrim("")
	if len(out) != 0 {
		t.Errorf("expected empty, got %v", out)
	}
}

func TestSplitAndTrim_SkipsBlank(t *testing.T) {
	out := splitAndTrim("HEL,,AMS")
	if len(out) != 2 {
		t.Errorf("expected 2, got %v", out)
	}
}

func TestParseBool_TrueVariants(t *testing.T) {
	for _, v := range []string{"true", "yes", "1", "on", "TRUE", "YES"} {
		b, err := parseBool(v)
		if err != nil || !b {
			t.Errorf("parseBool(%q) should be true, got %v err %v", v, b, err)
		}
	}
}

func TestParseBool_FalseVariants(t *testing.T) {
	for _, v := range []string{"false", "no", "0", "off", "FALSE", "NO"} {
		b, err := parseBool(v)
		if err != nil || b {
			t.Errorf("parseBool(%q) should be false, got %v err %v", v, b, err)
		}
	}
}

func TestParseBool_Invalid(t *testing.T) {
	_, err := parseBool("maybe")
	if err == nil {
		t.Error("expected error for invalid boolean")
	}
}

func TestPromptString_UsesInput(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("Amsterdam\n"))
	result := promptString(scanner, "City", "Helsinki")
	if result != "Amsterdam" {
		t.Errorf("expected Amsterdam, got %q", result)
	}
}

func TestPromptString_EmptyUsesDefault(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("\n"))
	result := promptString(scanner, "City", "Helsinki")
	if result != "Helsinki" {
		t.Errorf("expected Helsinki (default), got %q", result)
	}
}

func TestPromptString_EmptyCurrentNoDefault(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("\n"))
	result := promptString(scanner, "City", "")
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}
}

func TestPromptBool_TrueInput(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("yes\n"))
	if !promptBool(scanner, "Enable?", false) {
		t.Error("expected true")
	}
}

func TestPromptBool_FalseInput(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("no\n"))
	if promptBool(scanner, "Enable?", true) {
		t.Error("expected false")
	}
}

func TestPromptBool_InvalidUsesDefault(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("maybe\n"))
	if promptBool(scanner, "Enable?", true) != true {
		t.Error("expected default (true) on invalid input")
	}
}

func TestPromptStringSlice_Basic(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("HEL, AMS\n"))
	result := promptStringSlice(scanner, "Airports", []string{"NRT"})
	if len(result) != 2 || result[0] != "HEL" || result[1] != "AMS" {
		t.Errorf("unexpected: %v", result)
	}
}

func TestDestinationCmd_Flags(t *testing.T) {
	cmd := destinationCmd()
	if cmd.Use != "destination <location>" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}
	if f := cmd.Flags().Lookup("dates"); f == nil {
		t.Error("expected --dates flag")
	}
}

func TestDestinationCmd_ExactArgs(t *testing.T) {
	cmd := destinationCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with no args")
	}
}

func TestNearbyCmd_InvalidLatitude(t *testing.T) {
	cmd := nearbyCmd()
	cmd.SetArgs([]string{"not-a-float", "2.17"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid latitude")
	}
}

func TestNearbyCmd_InvalidLongitude(t *testing.T) {
	cmd := nearbyCmd()
	cmd.SetArgs([]string{"41.38", "not-a-float"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid longitude")
	}
}

func TestGuideCmd_ExactArgs(t *testing.T) {
	cmd := guideCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with no args")
	}
}

func TestGuideCmd_Use(t *testing.T) {
	cmd := guideCmd()
	if cmd.Use != "guide <location>" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}
}

func TestOptimizeCmd_Flags(t *testing.T) {
	cmd := optimizeCmd()
	for _, name := range []string{"depart", "return", "flex", "guests", "currency", "results"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestOptimizeCmd_RequiresTwoArgs(t *testing.T) {
	cmd := optimizeCmd()
	cmd.SetArgs([]string{"HEL"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with single arg")
	}
}

func TestWeatherCmd_Flags(t *testing.T) {
	cmd := weatherCmd()
	for _, name := range []string{"from", "to"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestWeatherCmd_Use(t *testing.T) {
	cmd := weatherCmd()
	if cmd.Use != "weather CITY" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}
}

func TestHotelSourceLabel_KnownProviders(t *testing.T) {
	cases := map[string]string{
		"google_hotels": "Google",
		"trivago":       "Trivago",
		"airbnb":        "Airbnb",
		"booking":       "Booking",
	}
	for input, want := range cases {
		got := hotelSourceLabel(input)
		if got != want {
			t.Errorf("hotelSourceLabel(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestHotelSourceLabel_WithSpaces(t *testing.T) {
	got := hotelSourceLabel("  airbnb  ")
	if got != "Airbnb" {
		t.Errorf("expected 'Airbnb' with spaces trimmed, got %q", got)
	}
}

func TestMaybeShowAccomHackTip_ShortStayNoOp(t *testing.T) {

	maybeShowAccomHackTip(context.TODO(), "Prague", "2026-06-15", "2026-06-17", "EUR", 2)
}

func TestProfileCmd_SubcommandsExist(t *testing.T) {
	cmd := profileCmd()
	names := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		names[sub.Use] = true
	}
	for _, want := range []string{"add", "summary", "import-email"} {
		if !names[want] {
			t.Errorf("expected subcommand %q", want)
		}
	}
}

// Ensure the bytes import is actually used to avoid "imported and not used" error.

func TestLooksLikeGoogleHotelID_SlashG(t *testing.T) {
	if !looksLikeGoogleHotelID("/g/11b6d4_v_4") {
		t.Error("expected true for /g/ prefix")
	}
}

func TestLooksLikeGoogleHotelID_ChIJ(t *testing.T) {
	if !looksLikeGoogleHotelID("ChIJy7MSZP0LkkYRZw2dDekQP78") {
		t.Error("expected true for ChIJ prefix")
	}
}

func TestLooksLikeGoogleHotelID_ColonSeparated(t *testing.T) {
	if !looksLikeGoogleHotelID("0x123456:0xabcdef") {
		t.Error("expected true for colon-separated ID without spaces")
	}
}

func TestLooksLikeGoogleHotelID_HotelName(t *testing.T) {
	if looksLikeGoogleHotelID("Hotel Lutetia Paris") {
		t.Error("expected false for hotel name with spaces")
	}
}

func TestLooksLikeGoogleHotelID_Empty(t *testing.T) {
	if looksLikeGoogleHotelID("") {
		t.Error("expected false for empty string")
	}
}

func TestLooksLikeGoogleHotelID_WithSpaces(t *testing.T) {
	if !looksLikeGoogleHotelID("   /g/abc   ") {
		t.Error("expected true after trimming spaces for /g/ prefix")
	}
}

func TestRunRestaurants_InvalidLat(t *testing.T) {

	cmd := restaurantsCmd

	err := runRestaurants(cmd, []string{"not-lat", "2.17"})
	if err == nil {
		t.Error("expected error for invalid latitude")
	}
}

func TestRunRestaurants_InvalidLon(t *testing.T) {
	cmd := restaurantsCmd
	err := runRestaurants(cmd, []string{"41.38", "not-lon"})
	if err == nil {
		t.Error("expected error for invalid longitude")
	}
}

func TestRunRestaurants_LatOutOfRange(t *testing.T) {
	cmd := restaurantsCmd
	err := runRestaurants(cmd, []string{"91.0", "2.17"})
	if err == nil {
		t.Error("expected error for lat > 90")
	}
}

func TestRunRestaurants_LonOutOfRange(t *testing.T) {
	cmd := restaurantsCmd
	err := runRestaurants(cmd, []string{"41.38", "181.0"})
	if err == nil {
		t.Error("expected error for lon > 180")
	}
}

func TestRoomsCmd_FlagsV3(t *testing.T) {
	cmd := roomsCmd()
	for _, name := range []string{"checkin", "checkout", "currency", "location"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestReviewsCmd_FlagsV3(t *testing.T) {
	for _, name := range []string{"limit", "sort", "format"} {
		if f := reviewsCmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on reviewsCmd", name)
		}
	}
}

func TestDiscoverCmd_MissingOriginError(t *testing.T) {
	// Isolate home so real preferences (which may have home_airports set) do not
	// cause discoverCmd to skip the missing-origin check and hit live APIs.
	setTestHome(t, t.TempDir())

	cmd := discoverCmd()
	cmd.SetArgs([]string{"--from", "2026-07-01", "--until", "2026-07-31", "--budget", "500"})

	if err := cmd.Execute(); err == nil {
		t.Error("expected error when --origin is absent and no home_airports in prefs")
	}
}

func TestClassifyProviderStatus_Healthy(t *testing.T) {
	cfg := &providers.ProviderConfig{
		LastSuccess: time.Now().Add(-1 * time.Hour),
	}
	got := classifyProviderStatus(cfg)
	if got != "healthy" {
		t.Errorf("expected healthy, got %q", got)
	}
}

func TestClassifyProviderStatus_Stale(t *testing.T) {
	cfg := &providers.ProviderConfig{
		LastSuccess: time.Now().Add(-25 * time.Hour),
	}
	got := classifyProviderStatus(cfg)
	if got != "stale" {
		t.Errorf("expected stale, got %q", got)
	}
}

func TestClassifyProviderStatus_Error(t *testing.T) {
	cfg := &providers.ProviderConfig{
		ErrorCount:  3,
		LastError:   "connection refused",
		LastSuccess: time.Now().Add(-1 * time.Hour),
	}
	got := classifyProviderStatus(cfg)
	if got != "error" {
		t.Errorf("expected error, got %q", got)
	}
}

func TestClassifyProviderStatus_Unconfigured(t *testing.T) {
	cfg := &providers.ProviderConfig{}
	got := classifyProviderStatus(cfg)
	if got != "unconfigured" {
		t.Errorf("expected unconfigured, got %q", got)
	}
}

func TestColorProviderStatus_AllBranches(t *testing.T) {
	for _, status := range []string{"healthy", "stale", "error", "unconfigured", "unknown"} {
		got := colorProviderStatus(status)
		if got == "" {
			t.Errorf("colorProviderStatus(%q) returned empty", status)
		}
	}
}

func TestRelativeTimeStr_Zero(t *testing.T) {
	got := relativeTimeStr(time.Time{})
	if got != "-" {
		t.Errorf("expected -, got %q", got)
	}
}

func TestRelativeTimeStr_JustNow(t *testing.T) {
	got := relativeTimeStr(time.Now().Add(-10 * time.Second))
	if got != "just now" {
		t.Errorf("expected 'just now', got %q", got)
	}
}

func TestRelativeTimeStr_OneMinuteAgo(t *testing.T) {
	got := relativeTimeStr(time.Now().Add(-1*time.Minute - 5*time.Second))
	if got != "1m ago" {
		t.Errorf("expected '1m ago', got %q", got)
	}
}
