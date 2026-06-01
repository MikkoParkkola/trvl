package mcp

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/travelctx"
)

// TestResolveDestOriginOptional_ExplicitWins verifies the explicit origin
// argument is honored and reported as the source, with no network needed.
func TestResolveDestOriginOptional_ExplicitWins(t *testing.T) {
	args := map[string]any{"origin": "AMS", "destination": "BCN"}
	origin, dest, src, err := resolveDestOriginOptional(context.Background(), args, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if origin != "AMS" {
		t.Errorf("origin = %q, want AMS", origin)
	}
	if dest != "BCN" {
		t.Errorf("dest = %q, want BCN", dest)
	}
	if src != travelctx.SourceExplicit {
		t.Errorf("source = %q, want explicit", src)
	}
}

// TestResolveDestOriginOptional_CityNameOrigin verifies a city-name origin is
// resolved to an IATA code.
func TestResolveDestOriginOptional_CityNameOrigin(t *testing.T) {
	args := map[string]any{"origin": "Amsterdam", "destination": "Barcelona"}
	origin, dest, _, err := resolveDestOriginOptional(context.Background(), args, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if origin != "AMS" {
		t.Errorf("origin = %q, want AMS", origin)
	}
	if dest != "BCN" {
		t.Errorf("dest = %q, want BCN", dest)
	}
}

// TestResolveDestOriginOptional_DestRequired verifies destination is still
// mandatory even though origin is now optional.
func TestResolveDestOriginOptional_DestRequired(t *testing.T) {
	args := map[string]any{"origin": "HEL"}
	if _, _, _, err := resolveDestOriginOptional(context.Background(), args, false); err == nil {
		t.Fatal("expected error when destination is missing")
	}
}

// TestResolveDestOriginOptional_NoOriginNoGeoErrors verifies that with no
// explicit origin, no resolvable home airport, and geo disabled, the helper
// returns a clear actionable error rather than proceeding with empty origin.
func TestResolveDestOriginOptional_NoOriginNoGeoErrors(t *testing.T) {
	// Point HOME at an empty dir so preferences.Load() yields no home airport,
	// and disable geo so no network is consulted.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("TRVL_NO_GEO", "1")
	args := map[string]any{"destination": "BCN"}
	_, _, src, err := resolveDestOriginOptional(context.Background(), args, false)
	if err == nil {
		t.Fatal("expected error when no origin can be resolved")
	}
	if src != travelctx.SourceUnknown {
		t.Errorf("source = %q, want unknown on failure", src)
	}
}

// TestBuildBookingContext_LeadTimeAndWindow verifies the time context attached
// to a result carries a sane window classification and is never nil for a
// valid date.
func TestBuildBookingContext_LeadTimeAndWindow(t *testing.T) {
	// A date ~2 years out is unambiguously in the very-early or early band,
	// regardless of when this test runs.
	bc := buildBookingContext("2099-01-01", "HEL", travelctx.SourcePrefs)
	if bc == nil {
		t.Fatal("booking context is nil for a valid date")
	}
	if bc.OriginSource != string(travelctx.SourcePrefs) {
		t.Errorf("origin_source = %q, want preferences", bc.OriginSource)
	}
	if bc.LeadTimeDays <= 0 {
		t.Errorf("lead_time_days = %d, want positive for a far-future date", bc.LeadTimeDays)
	}
	if bc.BookingWindow == "" {
		t.Error("booking_window is empty")
	}
	if bc.Timezone == "" {
		t.Error("timezone is empty")
	}
}

// TestBuildBookingContext_BadDateStillReturnsTime verifies a malformed date
// degrades gracefully: time context present, lead-time math skipped.
func TestBuildBookingContext_BadDateStillReturnsTime(t *testing.T) {
	bc := buildBookingContext("not-a-date", "HEL", travelctx.SourceExplicit)
	if bc == nil {
		t.Fatal("booking context should be non-nil even for a bad date")
	}
	if bc.SearchedAt == "" {
		t.Error("searched_at should be populated regardless of date parse")
	}
	if bc.BookingWindow != "" {
		t.Errorf("booking_window = %q, want empty for unparseable date", bc.BookingWindow)
	}
}
