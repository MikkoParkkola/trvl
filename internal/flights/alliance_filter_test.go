package flights

import "testing"

// Regression guards for the Google Flights alliance filter wire format.
//
// Context (MIK-6531): the alliance filter previously lived at outer[1][25]
// (the settings array) where Google rejected every payload with HTTP 400.
// Production moved it to segment[5] with an uppercased string array, which a
// live probe verified works (["STAR_ALLIANCE"] -> 45/115 flights, 61%
// reduction; see search_filters.go alliancesFilter + filters_probe_test.go).
//
// That fix was only protected by live, env-gated probes
// (filters_probe_test.go, alliance_codes_probe_test.go), which never run in
// the default CI suite. These deterministic tests lock the verified format so
// a regression back to a 400-producing shape (scalar instead of array, or the
// abandoned outer[1][25] position) fails offline in CI.

func TestAlliancesFilter_Set_UppercasesAndTrims(t *testing.T) {
	got := alliancesFilter([]string{" star_alliance ", "oneworld"})
	arr, ok := got.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("alliancesFilter = %v (%T), want []any of len 2", got, got)
	}
	if arr[0] != "STAR_ALLIANCE" || arr[1] != "ONEWORLD" {
		t.Errorf("alliancesFilter = %v, want [STAR_ALLIANCE ONEWORLD]", arr)
	}
}

func TestAlliancesFilter_Empty_IsNil(t *testing.T) {
	if got := alliancesFilter(nil); got != nil {
		t.Errorf("alliancesFilter(nil) = %v, want nil", got)
	}
}

func TestBuildSegment_Alliance_WiredToPosition5(t *testing.T) {
	opts := SearchOptions{Alliances: []string{"STAR_ALLIANCE"}}
	seg := buildSegment("HEL", "NRT", "2026-06-01", opts)
	arr := seg.([]any)

	// Wire format: []any{"STAR_ALLIANCE"} at segment[5]. A scalar string
	// returns 400, and the old outer[1][25] position returns 400 for any
	// value -- this guards the live-verified segment[5] string array.
	allArr, ok := arr[5].([]any)
	if !ok || len(allArr) != 1 || allArr[0] != "STAR_ALLIANCE" {
		t.Errorf("segment[5] expected []any{\"STAR_ALLIANCE\"} (alliance), got %v", arr[5])
	}
}

func TestBuildSegment_NoAlliance_Position5IsNil(t *testing.T) {
	seg := buildSegment("HEL", "NRT", "2026-06-01", SearchOptions{})
	arr := seg.([]any)

	if arr[5] != nil {
		t.Errorf("segment[5] expected nil when no alliance set, got %v", arr[5])
	}
}
