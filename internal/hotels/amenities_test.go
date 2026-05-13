package hotels

import (
	"sort"
	"testing"
)

// --- extractAmenityCodes ---

func TestExtractAmenityCodes_ValidPairs(t *testing.T) {
	// Simulate entry[10] structure: nested array with amenity pairs at the end.
	raw := []any{
		nil, nil, nil, nil, nil, nil,
		[]any{}, // some inner data
		nil,
		[]any{ // last non-nil sub-array with pairs
			[]any{float64(1), float64(54)}, // accessible
			[]any{float64(1), float64(29)}, // kitchen
			[]any{float64(1), float64(17)}, // room_service
			[]any{float64(1), float64(22)}, // restaurant
			[]any{float64(1), float64(2)},  // free_wifi
			[]any{float64(1), float64(18)}, // ev_charger
			[]any{float64(1), float64(8)},  // spa
			[]any{float64(1), float64(26)}, // bar
			[]any{float64(1), float64(4)},  // pool
			[]any{float64(1), float64(23)}, // free_parking
		},
	}

	amenities := extractAmenityCodes(raw)
	if len(amenities) != 10 {
		t.Fatalf("expected 10 amenities, got %d: %v", len(amenities), amenities)
	}

	// Verify all expected amenities are present.
	want := map[string]bool{
		"accessible":   true,
		"kitchen":      true,
		"room_service": true,
		"restaurant":   true,
		"free_wifi":    true,
		"ev_charger":   true,
		"spa":          true,
		"bar":          true,
		"pool":         true,
		"free_parking": true,
	}
	for _, a := range amenities {
		if !want[a] {
			t.Errorf("unexpected amenity: %q", a)
		}
		delete(want, a)
	}
	for missing := range want {
		t.Errorf("missing amenity: %q", missing)
	}
}

func TestExtractAmenityCodes_UnavailableSkipped(t *testing.T) {
	// Pairs with 0 (unavailable) should be skipped.
	raw := []any{
		[]any{
			[]any{float64(1), float64(2)},  // available: free_wifi
			[]any{float64(0), float64(4)},  // unavailable: pool
			[]any{float64(1), float64(26)}, // available: bar
		},
	}

	amenities := extractAmenityCodes(raw)
	if len(amenities) != 2 {
		t.Fatalf("expected 2 amenities, got %d: %v", len(amenities), amenities)
	}

	got := map[string]bool{}
	for _, a := range amenities {
		got[a] = true
	}
	if !got["free_wifi"] || !got["bar"] {
		t.Errorf("expected free_wifi and bar, got %v", amenities)
	}
	if got["pool"] {
		t.Error("pool should be excluded (unavailable)")
	}
}

func TestExtractAmenityCodes_UnknownCodesSkipped(t *testing.T) {
	raw := []any{
		[]any{
			[]any{float64(1), float64(999)}, // unknown code
			[]any{float64(1), float64(2)},   // free_wifi
		},
	}

	amenities := extractAmenityCodes(raw)
	if len(amenities) != 1 {
		t.Fatalf("expected 1 amenity, got %d: %v", len(amenities), amenities)
	}
	if amenities[0] != "free_wifi" {
		t.Errorf("expected free_wifi, got %q", amenities[0])
	}
}

func TestExtractAmenityCodes_DeduplicatesCodes(t *testing.T) {
	// Both code 11 and 54 map to "accessible".
	raw := []any{
		[]any{
			[]any{float64(1), float64(11)}, // accessible
			[]any{float64(1), float64(54)}, // accessible (duplicate)
		},
	}

	amenities := extractAmenityCodes(raw)
	if len(amenities) != 1 {
		t.Fatalf("expected 1 amenity (deduplicated), got %d: %v", len(amenities), amenities)
	}
}

func TestExtractAmenityCodes_NilInput(t *testing.T) {
	amenities := extractAmenityCodes(nil)
	if amenities != nil {
		t.Errorf("expected nil, got %v", amenities)
	}
}

func TestExtractAmenityCodes_NotArray(t *testing.T) {
	amenities := extractAmenityCodes("not an array")
	if amenities != nil {
		t.Errorf("expected nil, got %v", amenities)
	}
}

func TestExtractAmenityCodes_EmptyArray(t *testing.T) {
	amenities := extractAmenityCodes([]any{})
	if amenities != nil {
		t.Errorf("expected nil, got %v", amenities)
	}
}

func TestExtractAmenityCodes_NoPairsFound(t *testing.T) {
	// Sub-arrays exist but none contain amenity pairs.
	raw := []any{
		nil,
		[]any{"not", "pairs"},
	}
	amenities := extractAmenityCodes(raw)
	if amenities != nil {
		t.Errorf("expected nil, got %v", amenities)
	}
}

func TestExtractAmenityCodes_MalformedPairs(t *testing.T) {
	raw := []any{
		[]any{
			[]any{float64(1)},                      // too short
			[]any{float64(1), float64(2), "extra"}, // too long (len != 2)
			[]any{"x", float64(2)},                 // non-numeric availability
			[]any{float64(1), "y"},                 // non-numeric code
		},
	}

	amenities := extractAmenityCodes(raw)
	if len(amenities) != 0 {
		t.Errorf("expected 0 amenities from malformed pairs, got %d: %v", len(amenities), amenities)
	}
}

// --- enrichAmenitiesFromDescription ---

func TestEnrichAmenitiesFromDescription_AddsNew(t *testing.T) {
	existing := []string{"free_wifi"}
	desc := "Hotel features a pool and spa, with a great restaurant and bar area."

	result := enrichAmenitiesFromDescription(existing, desc)

	got := map[string]bool{}
	for _, a := range result {
		got[a] = true
	}

	// Should keep existing and add new.
	if !got["free_wifi"] {
		t.Error("lost existing amenity free_wifi")
	}
	for _, want := range []string{"pool", "spa", "restaurant", "bar"} {
		if !got[want] {
			t.Errorf("missing amenity %q from description", want)
		}
	}
}

func TestEnrichAmenitiesFromDescription_NoDuplicates(t *testing.T) {
	existing := []string{"pool", "spa"}
	desc := "This hotel has a pool and spa."

	result := enrichAmenitiesFromDescription(existing, desc)
	if len(result) != 2 {
		t.Errorf("expected 2 (no new), got %d: %v", len(result), result)
	}
}

func TestEnrichAmenitiesFromDescription_CaseInsensitive(t *testing.T) {
	result := enrichAmenitiesFromDescription(nil, "Free Wi-Fi and GYM available")

	got := map[string]bool{}
	for _, a := range result {
		got[a] = true
	}
	if !got["free_wifi"] {
		t.Error("should match Wi-Fi case-insensitively")
	}
	if !got["fitness_center"] {
		t.Error("should match GYM case-insensitively")
	}
}

func TestEnrichAmenitiesFromDescription_EmptyDescription(t *testing.T) {
	existing := []string{"pool"}
	result := enrichAmenitiesFromDescription(existing, "")
	if len(result) != 1 || result[0] != "pool" {
		t.Errorf("expected unchanged [pool], got %v", result)
	}
}

func TestEnrichAmenitiesFromDescription_NilExisting(t *testing.T) {
	result := enrichAmenitiesFromDescription(nil, "breakfast included")
	if len(result) != 1 || result[0] != "breakfast" {
		t.Errorf("expected [breakfast], got %v", result)
	}
}

func TestEnrichAmenitiesFromDescription_MergesBothKeywords(t *testing.T) {
	// "fitness" and "gym" both map to "fitness_center" — should appear once.
	result := enrichAmenitiesFromDescription(nil, "fitness gym area")
	count := 0
	for _, a := range result {
		if a == "fitness_center" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected fitness_center once, got %d times in %v", count, result)
	}
}

// --- Integration: parseOrganicHotel with amenities ---

func TestParseOrganicHotel_WithAmenities(t *testing.T) {
	entry := make([]any, 12)
	entry[0] = nil
	entry[1] = "Amenity Hotel"
	entry[2] = []any{[]any{51.5, -0.12}}

	// entry[10] = amenity codes
	entry[10] = []any{
		nil, nil, nil, nil, nil, nil, nil, nil,
		[]any{
			[]any{float64(1), float64(2)},  // free_wifi
			[]any{float64(1), float64(4)},  // pool
			[]any{float64(1), float64(27)}, // breakfast
		},
	}

	// entry[11] = description with additional amenity keywords
	entry[11] = []any{"Central location with spa and restaurant"}

	h := parseOrganicHotel(entry, "EUR")

	got := map[string]bool{}
	for _, a := range h.Amenities {
		got[a] = true
	}

	// From codes.
	for _, want := range []string{"free_wifi", "pool", "breakfast"} {
		if !got[want] {
			t.Errorf("missing code-derived amenity %q", want)
		}
	}

	// From description enrichment.
	for _, want := range []string{"spa", "restaurant"} {
		if !got[want] {
			t.Errorf("missing description-derived amenity %q", want)
		}
	}
}

func TestParseOrganicHotel_NoAmenityData(t *testing.T) {
	entry := make([]any, 12)
	entry[0] = nil
	entry[1] = "Basic Hotel"
	entry[2] = []any{[]any{51.5, -0.12}}
	// entry[10] and entry[11] left as nil.

	h := parseOrganicHotel(entry, "EUR")
	if len(h.Amenities) != 0 {
		t.Errorf("expected 0 amenities, got %v", h.Amenities)
	}
}

func TestParseOrganicHotel_DescriptionOnlyAmenities(t *testing.T) {
	entry := make([]any, 12)
	entry[0] = nil
	entry[1] = "Description Hotel"
	entry[2] = []any{[]any{51.5, -0.12}}
	entry[10] = nil // no structured codes
	entry[11] = []any{"Hotel with pool and free parking, laundry available"}

	h := parseOrganicHotel(entry, "EUR")

	got := map[string]bool{}
	for _, a := range h.Amenities {
		got[a] = true
	}
	for _, want := range []string{"pool", "free_parking", "laundry"} {
		if !got[want] {
			t.Errorf("missing description amenity %q in %v", want, h.Amenities)
		}
	}
}

// --- All amenity codes covered ---

func TestAmenityCodeMap_AllKnownCodes(t *testing.T) {
	// Verify every code in the map produces the expected name.
	expected := map[int]string{
		1: "air_conditioning", 2: "free_wifi", 4: "pool",
		7: "fitness_center", 8: "spa", 11: "accessible",
		14: "pet_friendly", 15: "airport_shuttle", 17: "room_service",
		18: "ev_charger", 22: "restaurant", 23: "free_parking",
		24: "paid_parking", 26: "bar", 27: "breakfast",
		29: "kitchen", 31: "laundry", 54: "accessible",
	}

	for code, want := range expected {
		raw := []any{
			[]any{
				[]any{float64(1), float64(code)},
			},
		}
		amenities := extractAmenityCodes(raw)
		if len(amenities) != 1 {
			t.Errorf("code %d: expected 1 amenity, got %d", code, len(amenities))
			continue
		}
		if amenities[0] != want {
			t.Errorf("code %d: got %q, want %q", code, amenities[0], want)
		}
	}
}

func TestDescriptionKeywords_AllMapped(t *testing.T) {
	// Verify every keyword produces an amenity.
	for keyword, want := range descriptionAmenityKeywords {
		result := enrichAmenitiesFromDescription(nil, keyword)
		found := false
		for _, a := range result {
			if a == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("keyword %q: expected amenity %q, got %v", keyword, want, result)
		}
	}
}

func TestExtractAmenityCodes_FullRealisticStructure(t *testing.T) {
	// Realistic entry[10] mimicking actual Google Hotels response shape.
	raw := []any{
		nil, nil, nil, nil, nil, nil,
		[]any{"some", "inner", "data"},
		nil,
		[]any{
			[]any{float64(1), float64(54)},
			[]any{float64(1), float64(29)},
			[]any{float64(1), float64(17)},
			[]any{float64(1), float64(22)},
			[]any{float64(1), float64(2)},
			[]any{float64(1), float64(18)},
			[]any{float64(1), float64(8)},
			[]any{float64(1), float64(26)},
			[]any{float64(1), float64(4)},
			[]any{float64(1), float64(23)},
			[]any{float64(1), float64(24)},
			[]any{float64(1), float64(1)},
			[]any{float64(1), float64(31)},
			[]any{float64(1), float64(27)},
			[]any{float64(1), float64(7)},
			[]any{float64(1), float64(11)},
		},
	}

	amenities := extractAmenityCodes(raw)

	// 16 pairs but code 11 and 54 both map to "accessible" so 15 unique.
	if len(amenities) != 15 {
		sorted := make([]string, len(amenities))
		copy(sorted, amenities)
		sort.Strings(sorted)
		t.Fatalf("expected 15 unique amenities, got %d: %v", len(amenities), sorted)
	}
}
