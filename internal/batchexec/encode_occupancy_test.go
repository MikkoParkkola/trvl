package batchexec

import (
	"net/url"
	"strings"
	"testing"
)

// TestBuildHotelPricePayloadWithOccupancy_RequestShape pins the yY52ce request
// shape so the requested occupancy reaches Google server-side (MIK-277G.1).
// The occupancy block is [adults, [childAge...], 0].
func TestBuildHotelPricePayloadWithOccupancy_RequestShape(t *testing.T) {
	checkIn := [3]int{2026, 6, 15}
	checkOut := [3]int{2026, 6, 18}

	decode := func(t *testing.T, payload string) string {
		t.Helper()
		got, err := url.QueryUnescape(payload)
		if err != nil {
			t.Fatalf("unescape payload: %v", err)
		}
		return got
	}

	// The occupancy-aware payload carries adults + child ages into the RPC.
	withChildren := decode(t, BuildHotelPricePayloadWithOccupancy("/g/11test", checkIn, checkOut, "EUR", 3, []int{5, 9}))
	if !strings.Contains(withChildren, "[3,[5,9],0]") {
		t.Fatalf("occupancy payload missing block [3,[5,9],0]; got %s", withChildren)
	}

	// The default helper preserves the historical two-adult block, so existing
	// callers see a byte-identical request.
	def := decode(t, BuildHotelPricePayload("/g/11test", checkIn, checkOut, "EUR"))
	if !strings.Contains(def, "[2,[],0]") {
		t.Fatalf("default payload missing block [2,[],0]; got %s", def)
	}

	// Zero/empty occupancy degrades to the safe default rather than emitting a
	// nonsensical zero-adult query.
	zero := decode(t, BuildHotelPricePayloadWithOccupancy("/g/11test", checkIn, checkOut, "EUR", 0, nil))
	if !strings.Contains(zero, "[2,[],0]") {
		t.Fatalf("zero occupancy should default to [2,[],0]; got %s", zero)
	}
}
