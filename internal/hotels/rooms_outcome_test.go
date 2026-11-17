package hotels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// withRelay turns the opt-in Google room-detail relay on against a local
// server answering with h.
func withRelay(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Setenv("GOOGLE_HOTELS_DETAIL_API_BASE", srv.URL)
	prev := googleRoomDetailEnabled
	googleRoomDetailEnabled = true
	t.Cleanup(func() { googleRoomDetailEnabled = prev })
}

// TestGetRoomAvailability_MixedSourceRowDoesNotSettle: the entity page was
// blocked; the fallback then matched the hotel by its Google ID, but the price
// on that row is Booking's. Google supplied no price, so the lookup is not
// settled and the notice still names Google (#826 rev 6 [1]).
func TestGetRoomAvailability_MixedSourceRowDoesNotSettle(t *testing.T) {
	roomLookupWorld(t, googleRoutes{answer(http.StatusForbidden), answer(404), noSacherPage}, false)
	orig := fetchHotelPageFullFn
	t.Cleanup(func() { fetchHotelPageFullFn = orig })
	fetchHotelPageFullFn = func(context.Context, *batchexec.Client, string, HotelSearchOptions, int, string) (parseResult, error) {
		return parseResult{Hotels: []models.HotelResult{{Name: "Hotel Sacher Vienna", HotelID: sacherPlaceIDRooms, Lat: sacherLat, Lon: sacherLon}}, TotalAvailable: 1}, nil
	}
	withBookingResult(t, models.HotelResult{
		Name: "Hotel Sacher Vienna", HotelID: "4711", Price: 480, Currency: "USD", Lat: sacherLat, Lon: sacherLon,
		Sources: []models.PriceSource{{Provider: "booking", Price: 480, Currency: "USD"}},
	})

	res := lookupSacherRooms(t)
	if len(res.Rooms) == 0 {
		t.Fatal("precondition: want the fallback's Booking-priced room")
	}
	if !strings.Contains(res.Notice, googleClause) {
		t.Fatalf("notice = %q, want the Google Hotels clause: Google supplied no price", res.Notice)
	}
}

// TestGetRoomAvailability_EntityNameSurvivesAFallbackMiss: the entity page
// names the hotel; the fallback finds nothing and must not blank the name.
func TestGetRoomAvailability_EntityNameSurvivesAFallbackMiss(t *testing.T) {
	entity := page([]byte(`<html>` + strings.Repeat("<!-- padding -->", 40) +
		`AF_initDataCallback({key: 'ds:0', data:[["Hotel Sacher Vienna"]]});</html>`))
	roomLookupWorld(t, googleRoutes{entity, answer(404), noSacherPage}, false)

	if res := lookupSacherRooms(t); res.Name != "Hotel Sacher Vienna" {
		t.Fatalf("Name = %q, want the entity page's Hotel Sacher Vienna", res.Name)
	}
}

// TestGetRoomAvailability_RelayIsAGoogleCall: the relay's 503 is a Google
// refusal in the notice, and relay rates after an earlier refusal settle the
// lookup (#826 rev 6 [7]).
func TestGetRoomAvailability_RelayIsAGoogleCall(t *testing.T) {
	t.Run("relay 503 is recorded", func(t *testing.T) {
		roomLookupWorld(t, googleRoutes{shortEntity, answer(404), noSacherPage}, false)
		withRelay(t, answer(http.StatusServiceUnavailable))
		if res := lookupSacherRooms(t); !strings.Contains(res.Notice, googleClause) {
			t.Fatalf("notice = %q, want the Google Hotels clause", res.Notice)
		}
	})
	t.Run("relay rates settle after a refusal", func(t *testing.T) {
		fixture := readGoogleRoomDetailFixture(t)
		roomLookupWorld(t, googleRoutes{answer(http.StatusForbidden), answer(404), noSacherPage}, false)
		withRelay(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(fixture)
		})
		res := lookupSacherRooms(t)
		if len(res.Rooms) == 0 {
			t.Fatal("precondition: want the relay's rooms")
		}
		if strings.Contains(res.Notice, googleClause) {
			t.Fatalf("notice = %q, want no Google Hotels clause: the relay supplied Google's rates", res.Notice)
		}
	})
}
