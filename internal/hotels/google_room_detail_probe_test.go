//go:build proof

package hotels

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
)

// TestProbeGoogleRoomLevelDetail documents the live reachability of Google's
// room-level price RPC from a static client. It is the proof artifact behind the
// Branch-B decision: the public yY52ce batchexecute RPC returns a NULL payload
// (HTTP 200, internal status [3], ~108 bytes) with no booking-partner / room
// data, so the per-room matrix is bot-walled for a static binary.
//
// Opt-in: build tag `proof` AND TRVL_TEST_LIVE_PROBES=1. It SKIPs otherwise, so
// the default `go test ./...` suite stays deterministic and offline.
//
//	Run: TRVL_TEST_LIVE_PROBES=1 go test -tags proof ./internal/hotels/ \
//	       -run TestProbeGoogleRoomLevelDetail -v -count=1
func TestProbeGoogleRoomLevelDetail(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("live probes disabled (set TRVL_TEST_LIVE_PROBES=1)")
	}

	checkIn := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	checkOut := time.Now().AddDate(0, 0, 31).Format("2006-01-02")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Resolve a real hotel ID via the live search page (still serves inline data).
	search, err := SearchHotels(ctx, "Paris", HotelSearchOptions{
		CheckIn: checkIn, CheckOut: checkOut, Guests: 2, Currency: "EUR",
	})
	if err != nil || len(search.Hotels) == 0 {
		t.Fatalf("search failed: %v (n=%d)", err, len(search.Hotels))
	}
	var hotelID string
	for _, h := range search.Hotels {
		if h.HotelID != "" {
			hotelID = h.HotelID
			t.Logf("resolved hotel %q id=%s", h.Name, h.HotelID)
			break
		}
	}
	if hotelID == "" {
		t.Fatal("no HotelID in search results")
	}

	// Probe the room-level price RPC and log the payload presence verbatim.
	client := DefaultClient()
	ciArr, _ := parseDateArray(checkIn)
	coArr, _ := parseDateArray(checkOut)
	status, body, perr := client.BatchExecute(ctx,
		batchexec.BuildHotelPricePayload(hotelID, ciArr, coArr, "EUR"))
	nonNull := payloadNonNull(body, "yY52ce")
	t.Logf("[probe yY52ce room-level] status=%d bodyLen=%d nonNullPayload=%v err=%v",
		status, len(body), nonNull, perr)

	if !nonNull {
		t.Logf("CONFIRMED Branch-B: Google room-level RPC returns a null payload to a " +
			"static client (bot-walled). Room-level Google pricing requires the opt-in " +
			"GOOGLE_HOTELS_DETAIL_API_BASE relay.")
	} else {
		t.Logf("Google room-level RPC returned a POPULATED payload — re-evaluate " +
			"Branch A (wire the static parser) and refresh testdata/price_empty_payload.txt.")
	}

	// Also exercise the opt-in relay path end-to-end if an operator configured it.
	if googleRoomDetailConfigured() {
		rooms, name, notice := tryGoogleRoomDetail(ctx, RoomSearchOptions{
			HotelID: hotelID, CheckIn: checkIn, CheckOut: checkOut, Guests: 2, Currency: "EUR",
		})
		t.Logf("[probe relay] rooms=%d name=%q notice=%q", len(rooms), name, notice)
	}
}
