package mcp

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
)

const budgetAccHotelID = "0x476d07b021f90d3f:0x557f8390f490f2f4"

var budgetAccArgs = mergeArgs(budgetHotelArgs, map[string]any{"currency": "EUR"})

func budgetReverifyArgs(extra map[string]any) map[string]any {
	return mergeArgs(budgetAccArgs, mergeArgs(map[string]any{"reverify_only": true, "hotel_id": budgetAccHotelID, "property_name": "Hotel 000"}, extra))
}

// stubBudgetAccRooms gives every hotel n rooms listed priciest first:
// even rooms sleep two (criteria-matched), odd rooms sleep one (rejected on
// occupancy). Hotel N's rooms cost 100*N more, so the best offer is hotel
// 000's cheapest EUR room, the last row a naive tail cut would reach.
func stubBudgetAccRooms(t *testing.T, n, pad int) {
	t.Helper()
	orig := getRoomAvailabilityWithOptsFunc
	t.Cleanup(func() { getRoomAvailabilityWithOptsFunc = orig })
	getRoomAvailabilityWithOptsFunc = func(_ context.Context, opts hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		offset := 0.0
		if opts.HotelID != budgetAccHotelID && len(opts.HotelID) == 4 {
			offset = float64(opts.HotelID[3]-'0') * 100
		}
		rooms := budgetRooms(n, 0)
		for i := range rooms {
			rooms[i].Price += offset
			// Offers carry the provider URL (twice), not the description.
			rooms[i].ProviderURL += "?pad=" + strings.Repeat("u", pad)
			if i%2 == 1 {
				rooms[i].MaxGuests = 1
			}
		}
		return &hotels.RoomAvailability{Success: true, HotelID: opts.HotelID, CheckIn: opts.CheckIn, CheckOut: opts.CheckOut, Rooms: rooms}, nil
	}
}

func offerIDs(body map[string]any, list string) []string { return rowNames(body, list, "offer_id") }

// assertEvidenceMatchesOffers checks that every offer-level evidence row
// belongs to an offer in this reply.
func assertEvidenceMatchesOffers(t *testing.T, body map[string]any) {
	t.Helper()
	shown := append(offerIDs(body, "offers"), offerIDs(body, "rejected_offers")...)
	for _, id := range rowNames(body, "evidence", "offer_id") {
		if id != "" && !slices.Contains(shown, id) {
			t.Errorf("evidence row for offer %q, which is not in the reply", id)
			return
		}
	}
}

func TestReplyBudgetSearchAccommodations(t *testing.T) {
	stubHotelSearch(t, budgetHotelFixture(5, 300))
	stubBudgetAccRooms(t, 60, 1000)
	got := callGroundTool(t, "search_accommodations", budgetAccArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	assertUnderPromise(t, got)
	offers, rejected := offerIDs(got.structured, "offers"), offerIDs(got.structured, "rejected_offers")
	if len(offers) == 0 {
		t.Fatal("no matched offers shown")
	}
	if names := rowNames(got.structured, "offers", "room_name"); names[0] != budgetRoomName(58) || rowNames(got.structured, "offers", "property_name")[0] != "Hotel 000" {
		t.Errorf("best offer = %s at %s, want %s at Hotel 000", names[0], rowNames(got.structured, "offers", "property_name")[0], budgetRoomName(58))
	}
	if o := bodyInt(got.structured, "budget_omitted"); o != 24-len(offers)-len(rejected) {
		t.Errorf("budget_omitted = %d, want %d (12 matched + 12 rejected before the cut)", o, 24-len(offers)-len(rejected))
	}
	if c := bodyInt(got.structured, "count"); c != len(offers) {
		t.Errorf("count = %d, want the %d offers in this response", c, len(offers))
	}
	assertEvidenceMatchesOffers(t, got.structured)
	if !strings.Contains(got.userText, "Showing") || !strings.Contains(got.userText, "50 KB") {
		t.Errorf("summary should say what the 50 KB budget left out:\n%s", got.userText)
	}
	assertTextCopyMatches(t, got)
}

func TestReplyBudgetAccommodationsFloor(t *testing.T) {
	stubHotelSearch(t, budgetHotelFixture(5, 300))
	stubBudgetAccRooms(t, 4, 6_000)
	got := callGroundTool(t, "search_accommodations", budgetAccArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	assertUnderPromise(t, got)
	if offers := rowNames(got.structured, "offers", "room_name"); len(offers) != 1 || offers[0] != budgetRoomName(2) {
		t.Errorf("floor offers = %v, want only the best %s", offers, budgetRoomName(2))
	}
	if r := offerIDs(got.structured, "rejected_offers"); len(r) != 0 {
		t.Errorf("floor kept %d rejected offers, want 0", len(r))
	}
	assertEvidenceMatchesOffers(t, got.structured)
}

func TestReplyBudgetAccommodationsFloorOverBudget(t *testing.T) {
	stubHotelSearch(t, budgetHotelFixture(5, 300))
	stubBudgetAccRooms(t, 2, 40_000)
	assertBoundedFloorError(t, callGroundTool(t, "search_accommodations", budgetAccArgs))
}

func TestReplyBudgetReverifyAccommodation(t *testing.T) {
	stubBudgetAccRooms(t, 60, 1000)
	got := callGroundTool(t, "search_accommodations", budgetReverifyArgs(nil))
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	assertUnderPromise(t, got)
	if names := rowNames(got.structured, "offers", "room_name"); len(names) == 0 || names[0] != budgetRoomName(58) {
		t.Errorf("reverify best offer = %v, want %s first", names, budgetRoomName(58))
	}
	assertEvidenceMatchesOffers(t, got.structured)
}

func TestReplyBudgetReverifySelectors(t *testing.T) {
	stubBudgetAccRooms(t, 60, 1000)

	// A selected rejected offer (sleeps one) stays in rejected_offers.
	got := callGroundTool(t, "search_accommodations", budgetReverifyArgs(map[string]any{"room_name": "room 59"}))
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	if r := rowNames(got.structured, "rejected_offers", "room_name"); !slices.Contains(r, budgetRoomName(59)) {
		t.Errorf("selected rejected offer missing: rejected = %v", r)
	}

	// A requested offer_id survives.
	got = callGroundTool(t, "search_accommodations", budgetReverifyArgs(map[string]any{"room_name": "room 10"}))
	ids := offerIDs(got.structured, "offers")
	if len(ids) != 1 {
		t.Fatalf("room_name selector returned %d offers, want 1", len(ids))
	}
	got = callGroundTool(t, "search_accommodations", budgetReverifyArgs(map[string]any{"offer_id": ids[0]}))
	if o := offerIDs(got.structured, "offers"); !slices.Contains(o, ids[0]) {
		t.Errorf("requested offer_id %s missing from offers %v", ids[0], o)
	}

	// A missing selected offer is reported, not silently dropped.
	got = callGroundTool(t, "search_accommodations", budgetReverifyArgs(map[string]any{"room_name": "penthouse"}))
	if !strings.Contains(got.assistant, "reverify_offer_not_found") {
		t.Errorf("missing selected offer should carry reverify_offer_not_found")
	}
}
