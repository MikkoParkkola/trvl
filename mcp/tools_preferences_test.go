package mcp

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// setupTempPrefs creates a temp dir with a preferences file containing the
// given prefs and returns the file path. Caller should defer os.RemoveAll on
// the parent dir.
func setupTempPrefs(t *testing.T, p *preferences.Preferences) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences.json")
	if err := preferences.SaveTo(path, p); err != nil {
		t.Fatalf("setup prefs: %v", err)
	}
	return path
}

func TestUpdatePreferences_PartialUpdate_PreservesOtherFields(t *testing.T) {
	t.Parallel()
	initial := &preferences.Preferences{
		HomeAirports:    []string{"HEL"},
		HomeCities:      []string{"Helsinki"},
		CarryOnOnly:     true,
		DisplayCurrency: "EUR",
		Locale:          "en-FI",
		MinHotelRating:  7.0,
	}
	path := setupTempPrefs(t, initial)

	// Update only min_hotel_stars.
	args := map[string]any{
		"min_hotel_stars": float64(4),
	}
	content, structured, err := handleUpdatePreferencesWithPath(args, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}

	result, ok := structured.(*preferences.Preferences)
	if !ok {
		t.Fatalf("structured result is %T, want *preferences.Preferences", structured)
	}

	// Updated field.
	if result.MinHotelStars != 4 {
		t.Errorf("MinHotelStars = %d, want 4", result.MinHotelStars)
	}

	// Preserved fields.
	if len(result.HomeAirports) != 1 || result.HomeAirports[0] != "HEL" {
		t.Errorf("HomeAirports = %v, want [HEL]", result.HomeAirports)
	}
	if !result.CarryOnOnly {
		t.Error("CarryOnOnly should be preserved as true")
	}
	if result.DisplayCurrency != "EUR" {
		t.Errorf("DisplayCurrency = %q, want EUR", result.DisplayCurrency)
	}
	if result.MinHotelRating != 7.0 {
		t.Errorf("MinHotelRating = %f, want 7.0", result.MinHotelRating)
	}

	// Verify file was actually written.
	reloaded, err := preferences.LoadFrom(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.MinHotelStars != 4 {
		t.Errorf("reloaded MinHotelStars = %d, want 4", reloaded.MinHotelStars)
	}
	if !reloaded.CarryOnOnly {
		t.Error("reloaded CarryOnOnly should be true")
	}
}

func TestUpdatePreferences_AddFamilyMember(t *testing.T) {
	t.Parallel()
	initial := preferences.Default()
	path := setupTempPrefs(t, initial)

	args := map[string]any{
		"family_members": []any{
			map[string]any{
				"name":         "Liisa",
				"relationship": "spouse",
				"notes":        "vegetarian, window seat",
			},
			map[string]any{
				"name":         "Eero",
				"relationship": "son",
				"notes":        "age 8",
			},
		},
	}

	_, structured, err := handleUpdatePreferencesWithPath(args, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := structured.(*preferences.Preferences)
	if len(result.FamilyMembers) != 2 {
		t.Fatalf("FamilyMembers len = %d, want 2", len(result.FamilyMembers))
	}
	if result.FamilyMembers[0].Name != "Liisa" {
		t.Errorf("first member name = %q, want Liisa", result.FamilyMembers[0].Name)
	}
	if result.FamilyMembers[1].Relationship != "son" {
		t.Errorf("second member relationship = %q, want son", result.FamilyMembers[1].Relationship)
	}
}

func TestUpdatePreferences_FamilyMembersFromJSON(t *testing.T) {
	t.Parallel()
	initial := preferences.Default()
	path := setupTempPrefs(t, initial)

	args := map[string]any{
		"family_members": `[{"name":"Liisa","relationship":"spouse","notes":""}]`,
	}

	_, structured, err := handleUpdatePreferencesWithPath(args, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := structured.(*preferences.Preferences)
	if len(result.FamilyMembers) != 1 {
		t.Fatalf("FamilyMembers len = %d, want 1", len(result.FamilyMembers))
	}
	if result.FamilyMembers[0].Name != "Liisa" {
		t.Errorf("name = %q, want Liisa", result.FamilyMembers[0].Name)
	}
}

func TestUpdatePreferences_PreferredDistricts_Merge(t *testing.T) {
	t.Parallel()
	initial := &preferences.Preferences{
		DisplayCurrency: "EUR",
		Locale:          "en",
		PreferredDistricts: map[string][]string{
			"Helsinki": {"Kallio", "Punavuori"},
			"Prague":   {"Prague 1"},
		},
	}
	path := setupTempPrefs(t, initial)

	// Add Amsterdam, replace Prague.
	args := map[string]any{
		"preferred_districts": map[string]any{
			"Amsterdam": []any{"Jordaan", "De Pijp"},
			"Prague":    []any{"Prague 1", "Prague 2", "Vinohrady"},
		},
	}

	_, structured, err := handleUpdatePreferencesWithPath(args, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := structured.(*preferences.Preferences)

	// Helsinki preserved.
	if ds := result.PreferredDistricts["Helsinki"]; len(ds) != 2 {
		t.Errorf("Helsinki districts = %v, want [Kallio Punavuori]", ds)
	}
	// Amsterdam added.
	if ds := result.PreferredDistricts["Amsterdam"]; len(ds) != 2 {
		t.Errorf("Amsterdam districts = %v, want [Jordaan De Pijp]", ds)
	}
	// Prague replaced.
	if ds := result.PreferredDistricts["Prague"]; len(ds) != 3 {
		t.Errorf("Prague districts = %v, want 3 entries", ds)
	}
}

func TestUpdatePreferences_PreferredDistricts_FromJSON(t *testing.T) {
	t.Parallel()
	initial := preferences.Default()
	path := setupTempPrefs(t, initial)

	args := map[string]any{
		"preferred_districts": `{"Berlin":["Mitte","Kreuzberg"]}`,
	}

	_, structured, err := handleUpdatePreferencesWithPath(args, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := structured.(*preferences.Preferences)
	if ds := result.PreferredDistricts["Berlin"]; len(ds) != 2 {
		t.Errorf("Berlin districts = %v, want [Mitte Kreuzberg]", ds)
	}
}

func TestUpdatePreferences_InvalidFieldsIgnored(t *testing.T) {
	t.Parallel()
	initial := &preferences.Preferences{
		DisplayCurrency: "EUR",
		Locale:          "en",
		MinHotelStars:   3,
	}
	path := setupTempPrefs(t, initial)

	args := map[string]any{
		"nonexistent_field": "should be ignored",
		"another_bad_field": 42,
		"min_hotel_stars":   float64(4), // valid field mixed in
	}

	_, structured, err := handleUpdatePreferencesWithPath(args, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := structured.(*preferences.Preferences)
	if result.MinHotelStars != 4 {
		t.Errorf("MinHotelStars = %d, want 4", result.MinHotelStars)
	}
	// Confirm no panic or error from unknown fields.
}

func TestUpdatePreferences_BooleanFields(t *testing.T) {
	t.Parallel()
	initial := &preferences.Preferences{
		DisplayCurrency: "EUR",
		Locale:          "en",
		CarryOnOnly:     false,
		PreferDirect:    true,
		NoDormitories:   false,
	}
	path := setupTempPrefs(t, initial)

	args := map[string]any{
		"carry_on_only":  true,
		"no_dormitories": true,
		// prefer_direct NOT included — should stay true.
	}

	_, structured, err := handleUpdatePreferencesWithPath(args, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := structured.(*preferences.Preferences)
	if !result.CarryOnOnly {
		t.Error("CarryOnOnly should be true")
	}
	if !result.NoDormitories {
		t.Error("NoDormitories should be true")
	}
	if !result.PreferDirect {
		t.Error("PreferDirect should be preserved as true")
	}
}

func TestUpdatePreferences_StringArrayFromJSON(t *testing.T) {
	t.Parallel()
	initial := preferences.Default()
	path := setupTempPrefs(t, initial)

	args := map[string]any{
		"home_airports":    `["HEL","AMS"]`,
		"loyalty_airlines": `["AY","KL"]`,
	}

	_, structured, err := handleUpdatePreferencesWithPath(args, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := structured.(*preferences.Preferences)
	if len(result.HomeAirports) != 2 || result.HomeAirports[0] != "HEL" || result.HomeAirports[1] != "AMS" {
		t.Errorf("HomeAirports = %v, want [HEL AMS]", result.HomeAirports)
	}
	if len(result.LoyaltyAirlines) != 2 || result.LoyaltyAirlines[0] != "AY" {
		t.Errorf("LoyaltyAirlines = %v, want [AY KL]", result.LoyaltyAirlines)
	}
}

func TestUpdatePreferences_EmptyArgs_NoChange(t *testing.T) {
	t.Parallel()
	initial := &preferences.Preferences{
		HomeAirports:    []string{"HEL"},
		DisplayCurrency: "EUR",
		Locale:          "en-FI",
		MinHotelStars:   4,
	}
	path := setupTempPrefs(t, initial)

	// Empty args — nothing should change.
	args := map[string]any{}

	_, structured, err := handleUpdatePreferencesWithPath(args, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := structured.(*preferences.Preferences)
	if result.MinHotelStars != 4 {
		t.Errorf("MinHotelStars = %d, want 4", result.MinHotelStars)
	}
	if len(result.HomeAirports) != 1 {
		t.Errorf("HomeAirports = %v, want [HEL]", result.HomeAirports)
	}
}

func TestUpdatePreferences_NoExistingFile_CreatesNew(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "preferences.json")

	args := map[string]any{
		"home_airports":    []any{"AMS"},
		"display_currency": "USD",
		"min_hotel_stars":  float64(3),
	}

	_, structured, err := handleUpdatePreferencesWithPath(args, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := structured.(*preferences.Preferences)
	if len(result.HomeAirports) != 1 || result.HomeAirports[0] != "AMS" {
		t.Errorf("HomeAirports = %v, want [AMS]", result.HomeAirports)
	}
	if result.DisplayCurrency != "USD" {
		t.Errorf("DisplayCurrency = %q, want USD", result.DisplayCurrency)
	}

	// File should exist on disk.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("preferences file was not created")
	}
}

func TestUpdatePreferences_ConcurrentUpdatesPreserveDisjointFields(t *testing.T) {
	initial := &preferences.Preferences{
		DisplayCurrency: "EUR",
		Locale:          "en",
	}
	path := setupTempPrefs(t, initial)

	updateArgs := []map[string]any{
		{"display_currency": "USD"},
		{"locale": "fi-FI"},
	}

	for attempt := 0; attempt < 50; attempt++ {
		if err := preferences.SaveTo(path, initial); err != nil {
			t.Fatalf("reset preferences: %v", err)
		}

		start := make(chan struct{})
		errs := make(chan error, len(updateArgs))

		var wg sync.WaitGroup
		for _, args := range updateArgs {
			wg.Add(1)
			go func(args map[string]any) {
				defer wg.Done()
				<-start
				_, _, err := handleUpdatePreferencesWithPath(args, path, nil)
				errs <- err
			}(args)
		}

		close(start)
		wg.Wait()
		close(errs)

		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent update failed: %v", err)
			}
		}

		reloaded, err := preferences.LoadFrom(path)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if reloaded.DisplayCurrency != "USD" || reloaded.Locale != "fi-FI" {
			t.Fatalf("attempt %d: concurrent updates lost data: got currency=%q locale=%q", attempt, reloaded.DisplayCurrency, reloaded.Locale)
		}
	}
}

func TestUpdatePreferences_DefaultPathConcurrentUpdatesPreserveDisjointFields(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	initial := &preferences.Preferences{
		DisplayCurrency: "EUR",
		Locale:          "en",
	}

	updateArgs := []map[string]any{
		{"display_currency": "USD"},
		{"locale": "fi-FI"},
	}

	for attempt := 0; attempt < 50; attempt++ {
		if err := preferences.Save(initial); err != nil {
			t.Fatalf("reset preferences: %v", err)
		}

		start := make(chan struct{})
		errs := make(chan error, len(updateArgs))

		var wg sync.WaitGroup
		for _, args := range updateArgs {
			wg.Add(1)
			go func(args map[string]any) {
				defer wg.Done()
				<-start
				_, _, err := handleUpdatePreferencesWithPath(args, "", nil)
				errs <- err
			}(args)
		}

		close(start)
		wg.Wait()
		close(errs)

		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent update failed: %v", err)
			}
		}

		reloaded, err := preferences.Load()
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if reloaded.DisplayCurrency != "USD" || reloaded.Locale != "fi-FI" {
			t.Fatalf("attempt %d: concurrent default-path updates lost data: got currency=%q locale=%q", attempt, reloaded.DisplayCurrency, reloaded.Locale)
		}
	}
}

func TestResolvePreferenceUpdatePath_ResolvesSymlinkedFilesToSharedLockKey(t *testing.T) {
	targetPath := setupTempPrefs(t, preferences.Default())
	symlinkPath := filepath.Join(t.TempDir(), "preferences-link.json")
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	resolvedTarget, err := resolvePreferenceUpdatePath(targetPath)
	if err != nil {
		t.Fatalf("resolve target path: %v", err)
	}
	resolvedSymlink, err := resolvePreferenceUpdatePath(symlinkPath)
	if err != nil {
		t.Fatalf("resolve symlink path: %v", err)
	}

	if resolvedTarget != resolvedSymlink {
		t.Fatalf("resolved paths differ: target=%q symlink=%q", resolvedTarget, resolvedSymlink)
	}
	if preferenceUpdateLock(resolvedTarget) != preferenceUpdateLock(resolvedSymlink) {
		t.Fatal("resolved symlink path should share the same mutex as the target path")
	}
}

func TestUpdatePreferencesTool_Annotations(t *testing.T) {
	t.Parallel()
	tool := updatePreferencesTool()

	if tool.Annotations == nil {
		t.Fatal("Annotations is nil")
	}
	if tool.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint should be false for a write tool")
	}
	if tool.Annotations.DestructiveHint {
		t.Error("DestructiveHint should be false (merge, not replace)")
	}
	if !tool.Annotations.IdempotentHint {
		t.Error("IdempotentHint should be true")
	}
	if tool.Name != "update_preferences" {
		t.Errorf("Name = %q, want update_preferences", tool.Name)
	}
}

func TestUpdatePreferences_BudgetFields(t *testing.T) {
	t.Parallel()
	initial := &preferences.Preferences{
		DisplayCurrency: "EUR",
		Locale:          "en-FI",
		MinHotelStars:   3,
	}
	path := setupTempPrefs(t, initial)

	args := map[string]any{
		"budget_per_night_min": float64(60),
		"budget_per_night_max": float64(200),
		"budget_flight_max":    float64(350),
		"deal_tolerance":       "balanced",
	}

	_, structured, err := handleUpdatePreferencesWithPath(args, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := structured.(*preferences.Preferences)
	if result.BudgetPerNightMin != 60 {
		t.Errorf("BudgetPerNightMin = %f, want 60", result.BudgetPerNightMin)
	}
	if result.BudgetPerNightMax != 200 {
		t.Errorf("BudgetPerNightMax = %f, want 200", result.BudgetPerNightMax)
	}
	if result.BudgetFlightMax != 350 {
		t.Errorf("BudgetFlightMax = %f, want 350", result.BudgetFlightMax)
	}
	if result.DealTolerance != "balanced" {
		t.Errorf("DealTolerance = %q, want balanced", result.DealTolerance)
	}
	// Preserved fields.
	if result.MinHotelStars != 3 {
		t.Errorf("MinHotelStars = %d, want 3 (preserved)", result.MinHotelStars)
	}

	// Verify persistence.
	reloaded, err := preferences.LoadFrom(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.BudgetPerNightMax != 200 {
		t.Errorf("reloaded BudgetPerNightMax = %f, want 200", reloaded.BudgetPerNightMax)
	}
}

func TestUpdatePreferences_NationalityAndLanguages(t *testing.T) {
	t.Parallel()
	initial := preferences.Default()
	path := setupTempPrefs(t, initial)

	args := map[string]any{
		"nationality": "FI",
		"languages":   `["en","fi","sv"]`,
	}

	_, structured, err := handleUpdatePreferencesWithPath(args, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := structured.(*preferences.Preferences)
	if result.Nationality != "FI" {
		t.Errorf("Nationality = %q, want FI", result.Nationality)
	}
	if len(result.Languages) != 3 {
		t.Fatalf("Languages len = %d, want 3", len(result.Languages))
	}
	if result.Languages[0] != "en" || result.Languages[1] != "fi" || result.Languages[2] != "sv" {
		t.Errorf("Languages = %v, want [en fi sv]", result.Languages)
	}
}

func TestUpdatePreferences_TripTypesAndSeat(t *testing.T) {
	t.Parallel()
	initial := preferences.Default()
	path := setupTempPrefs(t, initial)

	args := map[string]any{
		"trip_types":         []any{"city_break", "adventure", "remote_work"},
		"seat_preference":    "window",
		"default_companions": float64(1),
	}

	_, structured, err := handleUpdatePreferencesWithPath(args, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := structured.(*preferences.Preferences)
	if len(result.TripTypes) != 3 {
		t.Fatalf("TripTypes len = %d, want 3", len(result.TripTypes))
	}
	if result.TripTypes[0] != "city_break" {
		t.Errorf("TripTypes[0] = %q, want city_break", result.TripTypes[0])
	}
	if result.SeatPreference != "window" {
		t.Errorf("SeatPreference = %q, want window", result.SeatPreference)
	}
	if result.DefaultCompanions != 1 {
		t.Errorf("DefaultCompanions = %d, want 1", result.DefaultCompanions)
	}
}

func TestUpdatePreferences_NotesAndContextFields(t *testing.T) {
	t.Parallel()
	initial := preferences.Default()
	path := setupTempPrefs(t, initial)

	args := map[string]any{
		"notes":                "I have a fear of flying but manage with medication",
		"previous_trips":       `["Japan","Spain","Germany"]`,
		"bucket_list":          `["New Zealand","Iceland"]`,
		"activity_preferences": `["museums","food","nature"]`,
		"dietary_needs":        `["vegetarian"]`,
	}

	_, structured, err := handleUpdatePreferencesWithPath(args, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := structured.(*preferences.Preferences)
	if result.Notes != "I have a fear of flying but manage with medication" {
		t.Errorf("Notes = %q, want fear-of-flying note", result.Notes)
	}
	if len(result.PreviousTrips) != 3 {
		t.Errorf("PreviousTrips = %v, want 3 entries", result.PreviousTrips)
	}
	if len(result.BucketList) != 2 {
		t.Errorf("BucketList = %v, want 2 entries", result.BucketList)
	}
	if len(result.ActivityPreferences) != 3 {
		t.Errorf("ActivityPreferences = %v, want 3 entries", result.ActivityPreferences)
	}
	if len(result.DietaryNeeds) != 1 || result.DietaryNeeds[0] != "vegetarian" {
		t.Errorf("DietaryNeeds = %v, want [vegetarian]", result.DietaryNeeds)
	}
}

func TestUpdatePreferences_FlightPreferences(t *testing.T) {
	t.Parallel()
	initial := preferences.Default()
	path := setupTempPrefs(t, initial)

	args := map[string]any{
		"flight_time_earliest": "07:00",
		"flight_time_latest":   "22:00",
		"red_eye_ok":           false,
	}

	_, structured, err := handleUpdatePreferencesWithPath(args, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := structured.(*preferences.Preferences)
	if result.FlightTimeEarliest != "07:00" {
		t.Errorf("FlightTimeEarliest = %q, want 07:00", result.FlightTimeEarliest)
	}
	if result.FlightTimeLatest != "22:00" {
		t.Errorf("FlightTimeLatest = %q, want 22:00", result.FlightTimeLatest)
	}
	if result.RedEyeOK {
		t.Error("RedEyeOK should be false")
	}
}

func TestUpdatePreferences_NewFields_PreserveExisting(t *testing.T) {
	t.Parallel()
	initial := &preferences.Preferences{
		HomeAirports:      []string{"HEL"},
		DisplayCurrency:   "EUR",
		Locale:            "en-FI",
		Nationality:       "FI",
		BudgetPerNightMax: 150,
	}
	path := setupTempPrefs(t, initial)

	// Update only notes — all other new fields should be preserved.
	args := map[string]any{
		"notes": "Prefer ground floor rooms",
	}

	_, structured, err := handleUpdatePreferencesWithPath(args, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := structured.(*preferences.Preferences)
	if result.Notes != "Prefer ground floor rooms" {
		t.Errorf("Notes = %q, want Prefer ground floor rooms", result.Notes)
	}
	// Preserved.
	if result.Nationality != "FI" {
		t.Errorf("Nationality = %q, want FI (preserved)", result.Nationality)
	}
	if result.BudgetPerNightMax != 150 {
		t.Errorf("BudgetPerNightMax = %f, want 150 (preserved)", result.BudgetPerNightMax)
	}
	if len(result.HomeAirports) != 1 || result.HomeAirports[0] != "HEL" {
		t.Errorf("HomeAirports = %v, want [HEL] (preserved)", result.HomeAirports)
	}
}
