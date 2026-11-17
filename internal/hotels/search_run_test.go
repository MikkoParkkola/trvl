package hotels

import (
	"context"
	"net/http"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestSearchHotelsCall_RecordsEveryPage: page 1 answers, page 2 is refused.
// Both requests land in the run's Google events, in order.
func TestSearchHotelsCall_RecordsEveryPage(t *testing.T) {
	roomLookupWorld(t, googleRoutes{shortEntity, answer(404), func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("start") != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write(fakeHotelPageWithPrices(hotelWithPrice{"Hotel Stefanie", 150}))
	}}, false)

	_, call, err := searchHotelsCall(context.Background(), DefaultClient(), "Vienna", HotelSearchOptions{
		CheckIn: lookupCheckIn, CheckOut: lookupCheckOut, Currency: "USD", MaxPages: 2,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(call.events) < 2 || call.events[0].kind != googleAnswered || call.refusal() == nil {
		t.Fatalf("events = %+v, want page 1 answered, then a later refusal", call.events)
	}
}

// TestSharedHotelRun_CallersGetTheirOwnCopies: every caller of a shared run,
// the leader included, receives it through sharedHotelRun, so each gets a
// result and an event slice it may change without touching the others'.
func TestSharedHotelRun_CallersGetTheirOwnCopies(t *testing.T) {
	var call googleCall
	call.note(http.StatusOK, nil)
	shared := hotelSearchRun{
		result: &models.HotelSearchResult{Hotels: []models.HotelResult{{Name: "Hotel Sacher"}}},
		call:   call,
	}
	first, _ := sharedHotelRun(shared, nil)
	second, _ := sharedHotelRun(shared, nil)

	first.result.Hotels[0].Name = "mutated"
	first.call.events[0].kind = googleRefused
	if second.result.Hotels[0].Name != "Hotel Sacher" || second.call.events[0].kind != googleAnswered {
		t.Fatalf("a caller saw another caller's changes: %+v / %+v", second.result.Hotels[0], second.call.events)
	}
	if shared.result.Hotels[0].Name != "Hotel Sacher" || shared.call.events[0].kind != googleAnswered {
		t.Fatal("a caller changed the shared run itself")
	}
}
