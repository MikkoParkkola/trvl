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
