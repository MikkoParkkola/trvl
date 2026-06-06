package flights

import (
	"testing"
)

// TestBuildKiwiSearchArgs_AdvancedOptions verifies trvl passes Kiwi's advanced
// search options (round-trip + flexible date ranges) — previously ignored.
// MIK-4956.XSHOP.1.
func TestBuildKiwiSearchArgs_AdvancedOptions(t *testing.T) {
	// One-way, exact dates: no returnDate / flex keys present.
	base := buildKiwiSearchArgs("HEL", "CDG", "15/06/2026", "EUR", SearchOptions{Adults: 1})
	if _, ok := base["returnDate"]; ok {
		t.Error("one-way search should not set returnDate")
	}
	if _, ok := base["departureDateFlexRange"]; ok {
		t.Error("exact-date search should not set departureDateFlexRange")
	}
	if base["flyFrom"] != "HEL" || base["curr"] != "EUR" {
		t.Errorf("base args wrong: %v", base)
	}

	// Round-trip with flexible dates.
	rt := buildKiwiSearchArgs("HEL", "CDG", "15/06/2026", "EUR", SearchOptions{
		Adults:            1,
		ReturnDate:        "2026-06-22",
		DepartureFlexDays: 2,
		ReturnFlexDays:    5, // clamps to 3
	})
	if rt["returnDate"] != "22/06/2026" {
		t.Errorf("returnDate = %v, want 22/06/2026", rt["returnDate"])
	}
	if rt["departureDateFlexRange"] != 2 {
		t.Errorf("departureDateFlexRange = %v, want 2", rt["departureDateFlexRange"])
	}
	if rt["returnDateFlexRange"] != 3 {
		t.Errorf("returnDateFlexRange = %v, want 3 (clamped)", rt["returnDateFlexRange"])
	}

	// Return flex without a return date must be ignored (no one-way flex-return).
	noRet := buildKiwiSearchArgs("HEL", "CDG", "15/06/2026", "EUR", SearchOptions{Adults: 1, ReturnFlexDays: 3})
	if _, ok := noRet["returnDateFlexRange"]; ok {
		t.Error("returnDateFlexRange set without a returnDate")
	}
}

func TestClampFlexDays(t *testing.T) {
	for in, want := range map[int]int{-5: 0, 0: 0, 2: 2, 3: 3, 9: 3} {
		if got := clampFlexDays(in); got != want {
			t.Errorf("clampFlexDays(%d) = %d, want %d", in, got, want)
		}
	}
}
