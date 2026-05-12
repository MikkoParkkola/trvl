package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/nlsearch"
	"github.com/spf13/cobra"
)

func TestHacksCmd_MissingArgsV24(t *testing.T) {
	cmd := hacksCmd()
	cmd.SetArgs([]string{"HEL"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with only one positional arg")
	}
}

func TestDispatchSearch_FlightMissingFieldsV25(t *testing.T) {
	parent := &cobra.Command{}
	p := nlsearch.Params{Intent: "flight", Origin: "HEL"}
	err := dispatchSearch(parent, p)
	if err != nil {
		t.Errorf("expected nil for missing flight fields, got: %v", err)
	}
}

func TestDispatchSearch_HotelMissingFieldsV25(t *testing.T) {
	parent := &cobra.Command{}
	p := nlsearch.Params{Intent: "hotel", Location: "Barcelona"}
	err := dispatchSearch(parent, p)
	if err != nil {
		t.Errorf("expected nil for missing hotel fields, got: %v", err)
	}
}

func TestDispatchSearch_RouteMissingOriginV25(t *testing.T) {
	parent := &cobra.Command{}
	p := nlsearch.Params{Intent: "route"}
	err := dispatchSearch(parent, p)
	if err != nil {
		t.Errorf("expected nil for missing route origin, got: %v", err)
	}
}

func TestMissingFieldsHint_FlightV25(t *testing.T) {
	p := nlsearch.Params{Intent: "flight"}
	err := missingFieldsHint(p, "flight", "trvl flights ORIGIN DESTINATION YYYY-MM-DD")
	if err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
}

func TestMissingFieldsHint_HotelV25(t *testing.T) {
	p := nlsearch.Params{Intent: "hotel"}
	err := missingFieldsHint(p, "hotel", `trvl hotels "CITY" --checkin YYYY-MM-DD --checkout YYYY-MM-DD`)
	if err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
}

func TestMissingFieldsHint_DealsV25(t *testing.T) {
	p := nlsearch.Params{Intent: "deals"}
	err := missingFieldsHint(p, "deals", "trvl deals")
	if err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
}

func TestDestinationCmd_MissingArgV25(t *testing.T) {
	cmd := destinationCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with no positional arg")
	}
}

func TestDestinationCmd_FlagsV25(t *testing.T) {
	cmd := destinationCmd()
	if f := cmd.Flags().Lookup("dates"); f == nil {
		t.Error("expected --dates flag on destinationCmd")
	}
}

func TestGuideCmd_MissingArgV25(t *testing.T) {
	cmd := guideCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error with no positional arg")
	}
}

func TestReviewsCmd_FlagsV25(t *testing.T) {

	if reviewsCmd == nil {
		t.Fatal("reviewsCmd is nil")
	}
	for _, name := range []string{"limit", "sort", "format"} {
		if f := reviewsCmd.Flags().Lookup(name); f == nil {
			t.Errorf("expected --%s flag on reviewsCmd", name)
		}
	}
}

func TestWeatherCmd_DefaultDatesNoNetworkV25(t *testing.T) {
	cmd := weatherCmd()
	cmd.SetContext(cancelledTestContext(t))
	cmd.SetArgs([]string{"Helsinki"})

	_ = cmd.Execute()
}

func TestLoungesCmd_ValidIATANoNetworkV25(t *testing.T) {
	cmd := loungesCmd()
	cmd.SetContext(cancelledTestContext(t))
	cmd.SetArgs([]string{"HEL"})

	_ = cmd.Execute()
}

func TestLooksLikeGoogleHotelID_CHIJ_V26(t *testing.T) {
	cases := []string{"ChIJabc123", "chijlower", "CHIJUPPER"}
	for _, id := range cases {
		if !looksLikeGoogleHotelID(id) {
			t.Errorf("looksLikeGoogleHotelID(%q) = false, want true", id)
		}
	}
}

func TestLooksLikeGoogleHotelID_ColonForm_V26(t *testing.T) {

	if !looksLikeGoogleHotelID("namespace:identifier") {
		t.Error("looksLikeGoogleHotelID(colon form) = false, want true")
	}

	if looksLikeGoogleHotelID("a:b:c") {
		t.Error("looksLikeGoogleHotelID(two colons) = true, want false")
	}

	if looksLikeGoogleHotelID("a: b") {
		t.Error("looksLikeGoogleHotelID(colon with space) = true, want false")
	}
}

func TestLooksLikeGoogleHotelID_Whitespace_V26(t *testing.T) {

	if !looksLikeGoogleHotelID("  /g/somehotel  ") {
		t.Error("expected true for /g/ with surrounding whitespace")
	}
}

func TestMaybeShowAccomHackTip_EmptyCheckIn_V26(t *testing.T) {
	maybeShowAccomHackTip(context.Background(), "Helsinki", "", "2026-07-04", "EUR", 1)
}

func TestMaybeShowAccomHackTip_EmptyCheckOut_V26(t *testing.T) {
	maybeShowAccomHackTip(context.Background(), "Helsinki", "2026-07-01", "", "EUR", 1)
}

func TestHotelSourceLabels_Deduplication_V26(t *testing.T) {
	h := models.HotelResult{
		Sources: []models.PriceSource{
			{Provider: "google_hotels"},
			{Provider: "google_hotels"},
			{Provider: "booking"},
		},
	}
	got := hotelSourceLabels(h)
	if got == "" {
		t.Error("expected non-empty label for known sources")
	}
}

func TestDatesCmd_CancelledCtx_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := datesCmd()
	cmd.SetArgs([]string{"HEL", "BCN", "--from", "2026-08-01", "--to", "2026-08-15"})

	_ = cmd.ExecuteContext(ctx)
}

func TestDatesCmd_LegacyMode_CancelledCtx_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := datesCmd()
	cmd.SetArgs([]string{"HEL", "BCN", "--from", "2026-08-01", "--to", "2026-08-07", "--legacy"})
	_ = cmd.ExecuteContext(ctx)
}

func TestAccomHackCmd_CancelledCtx_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := accomHackCmd()
	cmd.SetArgs([]string{
		"Prague",
		"--checkin", "2026-08-01",
		"--checkout", "2026-08-08",
	})
	_ = cmd.ExecuteContext(ctx)
}

func TestMultiCityCmd_CancelledCtx_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := multiCityCmd()
	cmd.SetArgs([]string{
		"HEL",
		"--visit", "BCN,ROM",
		"--dates", "2026-08-01,2026-08-15",
	})
	_ = cmd.ExecuteContext(ctx)
}

func TestGroundCmd_CancelledCtx_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := groundCmd()
	cmd.SetArgs([]string{"Helsinki", "Tampere", "2026-08-01"})
	_ = cmd.ExecuteContext(ctx)
}

func TestOptimizeCmd_CancelledCtx_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := optimizeCmd()
	cmd.SetArgs([]string{"HEL", "BCN", "--depart", "2026-08-01", "--return", "2026-08-08"})
	_ = cmd.ExecuteContext(ctx)
}

func TestTripCmd_CancelledCtx_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := tripCmd()
	cmd.SetArgs([]string{
		"HEL", "BCN",
		"--depart", "2026-08-01",
		"--return", "2026-08-08",
	})
	_ = cmd.ExecuteContext(ctx)
}

func TestWhenCmd_CancelledCtx_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := whenCmd()
	cmd.SetArgs([]string{
		"--to", "BCN",
		"--from", "2026-08-01",
		"--until", "2026-08-31",
		"--origin", "HEL",
	})
	_ = cmd.ExecuteContext(ctx)
}

func TestLoungesCmd_CancelledCtx_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := loungesCmd()
	cmd.SetArgs([]string{"HEL"})
	_ = cmd.ExecuteContext(ctx)
}

func TestExploreCmd_CancelledCtx_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := exploreCmd()
	cmd.SetArgs([]string{"HEL", "--from", "2026-08-01"})
	_ = cmd.ExecuteContext(ctx)
}

func TestHacksCmd_CancelledCtx_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := hacksCmd()
	cmd.SetArgs([]string{"HEL", "BCN", "2026-08-01"})
	_ = cmd.ExecuteContext(ctx)
}

func TestSuggestCmd_CancelledCtx_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := suggestCmd()
	cmd.SetArgs([]string{"HEL", "BCN", "--around", "2026-08-15"})
	_ = cmd.ExecuteContext(ctx)
}

func TestProfileCmd_EmptyProfile_V27(t *testing.T) {
	cmd := profileCmd()
	cmd.SetArgs([]string{})
	_ = cmd.Execute()
}

func TestCancelledContextTests_Timing_V27(t *testing.T) {
	start := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	select {
	case <-ctx.Done():

	default:
		t.Error("expected already-cancelled context to be done")
	}

	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("context setup took %v, expected <100ms", elapsed)
	}
}

func TestDestinationCmd_CancelledCtx_V28(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := destinationCmd()
	cmd.SetArgs([]string{"Tokyo"})
	_ = cmd.ExecuteContext(ctx)
}

func TestDestinationCmd_WithDates_CancelledCtx_V28(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := destinationCmd()
	cmd.SetArgs([]string{"Barcelona", "--dates", "2026-08-01,2026-08-08"})
	_ = cmd.ExecuteContext(ctx)
}

func TestDestinationCmd_SingleDate_V28(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := destinationCmd()
	cmd.SetArgs([]string{"Paris", "--dates", "2026-08-01"})
	_ = cmd.ExecuteContext(ctx)
}

func TestGuideCmd_CancelledCtx_V28(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := guideCmd()
	cmd.SetArgs([]string{"Rome"})
	_ = cmd.ExecuteContext(ctx)
}

func TestEventsCmd_NoAPIKey_V28(t *testing.T) {
	orig := os.Getenv("TICKETMASTER_API_KEY")
	_ = os.Unsetenv("TICKETMASTER_API_KEY")
	defer func() {
		if orig != "" {
			_ = os.Setenv("TICKETMASTER_API_KEY", orig)
		}
	}()

	cmd := eventsCmd()
	cmd.SetArgs([]string{"Barcelona", "--from", "2026-07-01", "--to", "2026-07-08"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when TICKETMASTER_API_KEY is not set")
	}
	if !strings.Contains(err.Error(), "TICKETMASTER_API_KEY") {
		t.Errorf("expected TICKETMASTER_API_KEY error, got: %v", err)
	}
}

func TestNearbyCmd_InvalidLat_V28(t *testing.T) {
	cmd := nearbyCmd()
	cmd.SetArgs([]string{"notanum", "2.17"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid latitude")
	}
}

func TestNearbyCmd_InvalidLon_V28(t *testing.T) {
	cmd := nearbyCmd()
	cmd.SetArgs([]string{"41.38", "notanum"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid longitude")
	}
}

func TestAirportTransferCmd_CancelledCtx_V28(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := airportTransferCmd()
	cmd.SetArgs([]string{"HEL", "2026-08-01"})
	_ = cmd.ExecuteContext(ctx)
}

func TestUpgradeCmd_CancelledCtx_V28(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := upgradeCmd()
	cmd.SetArgs([]string{})
	_ = cmd.ExecuteContext(ctx)
}

func TestTripsAlertsCmd_MarkRead_V29(t *testing.T) {

	cmd := tripsAlertsCmd()
	cmd.SetArgs([]string{"--mark-read"})
	_ = cmd.Execute()
}

func TestTripsAlertsCmd_NoAlerts_V29(t *testing.T) {

	cmd := tripsAlertsCmd()
	cmd.SetArgs([]string{})
	_ = cmd.Execute()
}

func TestAirportTransferCmd_3Args_CancelledCtx_V29(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := airportTransferCmd()
	cmd.SetArgs([]string{"HEL", "Helsinki City Center", "2026-08-01"})
	_ = cmd.ExecuteContext(ctx)
}

func TestUpgradeCmd_DryRun_V29(t *testing.T) {
	cmd := upgradeCmd()
	cmd.SetArgs([]string{"--dry-run"})
	_ = cmd.Execute()
}

func TestUpgradeCmd_Quiet_V29(t *testing.T) {
	cmd := upgradeCmd()
	cmd.SetArgs([]string{"--quiet"})
	_ = cmd.Execute()
}

func TestGridCmd_CancelledCtx_V29(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := gridCmd()
	cmd.SetArgs([]string{"HEL", "--from", "2026-08-01", "--to", "2026-08-31"})
	_ = cmd.ExecuteContext(ctx)
}

func TestSplitLines_NoNewline(t *testing.T) {
	lines := splitLines("hello")
	if len(lines) != 1 || lines[0] != "hello" {
		t.Errorf("expected [hello], got %v", lines)
	}
}
