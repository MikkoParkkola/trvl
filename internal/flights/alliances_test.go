package flights

import (
	"encoding/json"
	"testing"
)

// --- alliancesFilter ---

func TestAlliancesFilter_Nil(t *testing.T) {
	got := alliancesFilter(nil)
	if got != nil {
		t.Errorf("alliancesFilter(nil) = %v, want nil", got)
	}
}

func TestAlliancesFilter_Empty(t *testing.T) {
	got := alliancesFilter([]string{})
	if got != nil {
		t.Errorf("alliancesFilter([]) = %v, want nil", got)
	}
}

func TestAlliancesFilter_SingleAlliance(t *testing.T) {
	got := alliancesFilter([]string{"star_alliance"})
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", got)
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1 element, got %d", len(arr))
	}
	if arr[0] != "STAR_ALLIANCE" {
		t.Errorf("got %q, want STAR_ALLIANCE (normalized to upper)", arr[0])
	}
}

func TestAlliancesFilter_MultipleAlliances(t *testing.T) {
	got := alliancesFilter([]string{"STAR_ALLIANCE", "ONEWORLD", "SKYTEAM"})
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", got)
	}
	if len(arr) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(arr))
	}
	if arr[0] != "STAR_ALLIANCE" || arr[1] != "ONEWORLD" || arr[2] != "SKYTEAM" {
		t.Errorf("alliances = %v, want [STAR_ALLIANCE ONEWORLD SKYTEAM]", arr)
	}
}

func TestAlliancesFilter_Normalization(t *testing.T) {
	// Values are uppercased and trimmed.
	got := alliancesFilter([]string{"  oneworld  ", "skyteam"})
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", got)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(arr))
	}
	if arr[0] != "ONEWORLD" {
		t.Errorf("arr[0] = %q, want ONEWORLD", arr[0])
	}
	if arr[1] != "SKYTEAM" {
		t.Errorf("arr[1] = %q, want SKYTEAM", arr[1])
	}
}

// --- buildFilters with Alliances ---

func TestBuildFilters_WithAlliances(t *testing.T) {
	opts := SearchOptions{
		Adults:    1,
		Alliances: []string{"STAR_ALLIANCE", "ONEWORLD"},
	}
	filters := buildFilters("HEL", "NRT", "2026-06-15", opts)

	data, _ := json.Marshal(filters)
	var arr []any
	if err := json.Unmarshal(data, &arr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	settings, ok := arr[1].([]any)
	if !ok {
		t.Fatalf("arr[1] not []any")
	}
	// Alliance filter is at segment[5] (verified via live probe).
	segments := settings[13].([]any)
	seg := segments[0].([]any)
	alliancesRaw := seg[5]
	if alliancesRaw == nil {
		t.Fatal("expected alliances at segment[5], got nil")
	}
	alliances, ok := alliancesRaw.([]any)
	if !ok {
		t.Fatalf("segment[5] = %T, want []any", alliancesRaw)
	}
	if len(alliances) != 2 {
		t.Fatalf("expected 2 alliances, got %d: %v", len(alliances), alliances)
	}
	if alliances[0] != "STAR_ALLIANCE" {
		t.Errorf("alliances[0] = %q, want STAR_ALLIANCE", alliances[0])
	}
	if alliances[1] != "ONEWORLD" {
		t.Errorf("alliances[1] = %q, want ONEWORLD", alliances[1])
	}
}

func TestBuildFilters_NoAlliances(t *testing.T) {
	opts := SearchOptions{Adults: 1}
	filters := buildFilters("HEL", "NRT", "2026-06-15", opts)

	data, _ := json.Marshal(filters)
	var arr []any
	if err := json.Unmarshal(data, &arr); err != nil {
		t.Fatalf("Unmarshal filters: %v", err)
	}

	settings := arr[1].([]any)
	// With no alliances, segment[5] should be nil.
	segments := settings[13].([]any)
	seg := segments[0].([]any)
	if seg[5] != nil {
		t.Errorf("expected nil at segment[5] when no alliances, got %v", seg[5])
	}
}

func TestBuildFilters_AlliancesDoNotAffectSegment(t *testing.T) {
	// Alliances are at the top-level settings; airlines remain at segment[4].
	opts := SearchOptions{
		Adults:    1,
		Airlines:  []string{"AY"},
		Alliances: []string{"ONEWORLD"},
	}
	filters := buildFilters("HEL", "NRT", "2026-06-15", opts)

	data, _ := json.Marshal(filters)
	var arr []any
	if err := json.Unmarshal(data, &arr); err != nil {
		t.Fatalf("Unmarshal filters: %v", err)
	}

	settings := arr[1].([]any)
	segments := settings[13].([]any)
	seg := segments[0].([]any)

	// Airlines at seg[4] should still be ["AY"].
	airlinesRaw := seg[4].([]any)
	if len(airlinesRaw) != 1 || airlinesRaw[0] != "AY" {
		t.Errorf("segment airlines = %v, want [AY]", airlinesRaw)
	}

	// Alliances at segment[5] should be ["ONEWORLD"].
	alliancesRaw := seg[5].([]any)
	if len(alliancesRaw) != 1 || alliancesRaw[0] != "ONEWORLD" {
		t.Errorf("segment alliances = %v, want [ONEWORLD]", alliancesRaw)
	}
}
