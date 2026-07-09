package mcp

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestHandleSearchHotels_ExcludesAdultsOnlyForChildren is the mcp-package
// regression test for the CLI/MCP parity fix: the MCP search_hotels surface
// must run the SAME adults-only exclusion the CLI does (via
// internal/hotels.ApplySharedHotelPolicy) when the party includes children.
// Before the fix, the exclusion lived only in cmd/trvl and the MCP surface
// returned adults-only properties to families.
//
// Fail-before / pass-after: revert the ApplySharedHotelPolicy call in
// runHotelSearch and this test fails (the adults-only property survives).
func TestHandleSearchHotels_ExcludesAdultsOnlyForChildren(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate from the operator's real prefs
	orig := searchHotelsFunc
	t.Cleanup(func() { searchHotelsFunc = orig })

	searchHotelsFunc = func(_ context.Context, _ string, opts hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		return &models.HotelSearchResult{
			Success: true,
			Count:   3,
			Hotels: []models.HotelResult{
				{Name: "Family Inn", HotelID: "a"},
				{Name: "TUI BLUE Adults Retreat", HotelID: "b", AdultsOnly: true},
				{Name: "City Hotel", HotelID: "c"},
			},
		}, nil
	}

	_, structured, err := handleSearchHotels(context.Background(), map[string]any{
		"location":     "Madeira",
		"check_in":     "2026-06-15",
		"check_out":    "2026-06-18",
		"children_ages": []any{7}, // party includes a child -> exclude adults-only
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleSearchHotels: %v", err)
	}

	res, ok := structured.(hotelSearchResponse)
	if !ok || res.HotelSearchResult == nil {
		t.Fatalf("expected hotelSearchResponse, got %T", structured)
	}
	if res.Count != 2 || len(res.Hotels) != 2 {
		t.Fatalf("adults-only property should be hidden for a party with children: got %d hotels (count=%d), want 2", len(res.Hotels), res.Count)
	}
	for _, h := range res.Hotels {
		if h.AdultsOnly {
			t.Errorf("adults-only property %q reached a family on the MCP surface", h.Name)
		}
	}
}

// TestHandleSearchHotels_KeepsAdultsOnlyWithoutChildren pins the parity
// counterpart: with no children in the party, adults-only properties are
// bookable and must NOT be hidden on the MCP surface (matching the CLI).
func TestHandleSearchHotels_KeepsAdultsOnlyWithoutChildren(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate from the operator's real prefs
	orig := searchHotelsFunc
	t.Cleanup(func() { searchHotelsFunc = orig })

	searchHotelsFunc = func(_ context.Context, _ string, opts hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		return &models.HotelSearchResult{
			Success: true,
			Count:   2,
			Hotels: []models.HotelResult{
				{Name: "Adults Retreat", HotelID: "b", AdultsOnly: true},
				{Name: "City Hotel", HotelID: "c"},
			},
		}, nil
	}

	_, structured, err := handleSearchHotels(context.Background(), map[string]any{
		"location":  "Madeira",
		"check_in":  "2026-06-15",
		"check_out": "2026-06-18",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleSearchHotels: %v", err)
	}
	res, ok := structured.(hotelSearchResponse)
	if !ok || res.HotelSearchResult == nil {
		t.Fatalf("expected hotelSearchResponse, got %T", structured)
	}
	if len(res.Hotels) != 2 {
		t.Fatalf("no children -> adults-only stays: got %d hotels, want 2", len(res.Hotels))
	}
}
