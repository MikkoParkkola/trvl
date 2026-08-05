package mcp

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

func TestSearchHotelsTool_CurrencyProperty(t *testing.T) {
	t.Parallel()
	tool := searchHotelsTool()
	prop, ok := tool.InputSchema.Properties["currency"]
	if !ok {
		t.Fatal("missing currency property")
	}
	if prop.Type != "string" {
		t.Fatalf("currency type = %q, want string", prop.Type)
	}
}

func TestHandleSearchHotels_DefaultsCurrencyFromPreferences(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	prefs := preferences.Default()
	prefs.DisplayCurrency = "EUR"
	if err := preferences.SaveTo(filepath.Join(tmp, ".trvl", "preferences.json"), prefs); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	orig := searchHotelsFunc
	t.Cleanup(func() { searchHotelsFunc = orig })

	var got hotels.HotelSearchOptions
	searchHotelsFunc = func(_ context.Context, _ string, opts hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		got = opts
		return &models.HotelSearchResult{Success: true}, nil
	}

	_, _, err := handleSearchHotels(context.Background(), map[string]any{
		"location":  "Helsinki",
		"check_in":  "2026-06-15",
		"check_out": "2026-06-18",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleSearchHotels: %v", err)
	}
	if got.Currency != "EUR" {
		t.Fatalf("Currency = %q, want EUR", got.Currency)
	}
}

func TestHandleSearchHotels_ExplicitCurrencyOverridesPreferences(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	prefs := preferences.Default()
	prefs.DisplayCurrency = "EUR"
	if err := preferences.SaveTo(filepath.Join(tmp, ".trvl", "preferences.json"), prefs); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	orig := searchHotelsFunc
	t.Cleanup(func() { searchHotelsFunc = orig })

	var got hotels.HotelSearchOptions
	searchHotelsFunc = func(_ context.Context, _ string, opts hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		got = opts
		return &models.HotelSearchResult{Success: true}, nil
	}

	_, _, err := handleSearchHotels(context.Background(), map[string]any{
		"location":  "Helsinki",
		"check_in":  "2026-06-15",
		"check_out": "2026-06-18",
		"currency":  "gbp",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleSearchHotels: %v", err)
	}
	if got.Currency != "GBP" {
		t.Fatalf("Currency = %q, want GBP", got.Currency)
	}
}
