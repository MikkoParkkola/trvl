package main

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestHotelsOutputCountsFartherHotels: the CLI says how many hotels the
// distance cap left out and how to bring them back, with or without results.
func TestHotelsOutputCountsFartherHotels(t *testing.T) {
	shown := &models.HotelSearchResult{Success: true, Count: 1, FartherOmitted: 2, MaxDistanceKm: 25,
		Hotels: []models.HotelResult{{Name: "A", Price: 80, Currency: "EUR"}}}
	empty := &models.HotelSearchResult{Success: true, FartherOmitted: 2, MaxDistanceKm: 25}
	for name, out := range map[string]string{
		"table": captureStdout(t, func() { _ = formatHotelsTable(context.Background(), "", "Vienna", shown, false) }),
		"empty": hotelsEmptyMessage(empty),
	} {
		for _, want := range []string{"2 hotels", "25 km", "--max-distance"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s output lacks %q:\n%s", name, want, out)
			}
		}
	}
}

// TestHotelsMaxDistanceDefaultsTo25Km: `trvl hotels` caps distance at 25 km
// unless --max-distance is given.
func TestHotelsMaxDistanceDefaultsTo25Km(t *testing.T) {
	f := hotelsCmd().Flags().Lookup("max-distance")
	if f == nil || f.DefValue != "25" {
		t.Fatalf("--max-distance default = %v, want 25", f)
	}
}

// TestHotelsFartherNoteSaysWhenGoogleLimitedResults mirrors the MCP note.
func TestHotelsFartherNoteSaysWhenGoogleLimitedResults(t *testing.T) {
	tight := hotelsFartherNote(&models.HotelSearchResult{FartherOmitted: 3, MaxDistanceKm: 2,
		ProviderStatuses: []models.ProviderStatus{{ID: "google_hotels", Status: models.StatusCheckedHit}}})
	if !strings.Contains(tight, "Google limited its results to 2 km") || !strings.Contains(tight, "only") {
		t.Errorf("2 km note %q does not say Google limited its results", tight)
	}
	if def := hotelsFartherNote(&models.HotelSearchResult{FartherOmitted: 3, MaxDistanceKm: 25}); strings.Contains(def, "Google") {
		t.Errorf("25 km note %q mentions Google", def)
	}
}

// TestHotelsFartherNoteGoogleClauseOnlyWhenGoogleAnswered mirrors the MCP rule.
func TestHotelsFartherNoteGoogleClauseOnlyWhenGoogleAnswered(t *testing.T) {
	failed := []models.ProviderStatus{{ID: "google_hotels", Status: models.StatusFailed}}
	answered := []models.ProviderStatus{{ID: "google_hotels", Status: models.StatusCheckedHit}}
	const clause = "Google limited its results to 2 km"
	if got := hotelsFartherNote(&models.HotelSearchResult{FartherOmitted: 3, MaxDistanceKm: 2, ProviderStatuses: failed}); strings.Contains(got, "Google") {
		t.Errorf("Google failed, 2 km: note %q claims Google limited its results", got)
	}
	if got := hotelsFartherNote(&models.HotelSearchResult{MaxDistanceKm: 2, ProviderStatuses: answered}); !strings.Contains(got, clause) {
		t.Errorf("Google answered, 2 km, 0 removed: note %q lacks %q", got, clause)
	}
	if got := hotelsFartherNote(&models.HotelSearchResult{FartherOmitted: 3, MaxDistanceKm: 25, ProviderStatuses: answered}); strings.Contains(got, "Google") {
		t.Errorf("default cap: note %q mentions Google", got)
	}
}

// TestHotelsOutputCountsMonthlyRentals: `trvl hotels` says how many monthly
// rentals it left out of the nightly list and why, and points nowhere:
// search_accommodations shows only its top few candidates, so it may leave
// them out.
func TestHotelsOutputCountsMonthlyRentals(t *testing.T) {
	shown := &models.HotelSearchResult{Success: true, Count: 1, MonthlyOmitted: 2,
		Hotels: []models.HotelResult{{Name: "A", Price: 80, Currency: "EUR"}}}
	empty := &models.HotelSearchResult{Success: true, MonthlyOmitted: 2}
	for name, out := range map[string]string{
		"table": captureStdout(t, func() { _ = formatHotelsTable(context.Background(), "", "Vienna", shown, false) }),
		"empty": hotelsEmptyMessage(empty),
	} {
		for _, want := range []string{"2 monthly rentals", "monthly rents, not nightly rates"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s output lacks %q:\n%s", name, want, out)
			}
		}
		if strings.Contains(out, "search_accommodations") {
			t.Errorf("%s output promises search_accommodations lists the rentals:\n%s", name, out)
		}
	}
}
