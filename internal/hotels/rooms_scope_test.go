package hotels

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestLocationCandidates_TypesNameAndPlace (#812): with a hotel query only the
// text after a comma is a place; every candidate carrying the name is a
// property search. A plain city hint stays an area search.
func TestLocationCandidates_TypesNameAndPlace(t *testing.T) {
	cases := []struct {
		hint string
		kind LocationKind
		want []locationCandidate
	}{
		{"Hotel Sacher Wien", LocationHotelQuery, []locationCandidate{{"Hotel Sacher Wien", SearchScopeProperty}}},
		{"Hotel Lutetia, Paris", LocationHotelQuery, []locationCandidate{
			{"Paris", SearchScopeArea},
			{"Hotel Lutetia Paris", SearchScopeProperty},
			{"Hotel Lutetia, Paris", SearchScopeProperty},
		}},
		{"Vienna", LocationArea, []locationCandidate{{"Vienna", SearchScopeArea}}},
		// The text after a comma is the place even when the name repeats it
		// or is missing.
		{"Paris, Paris", LocationHotelQuery, []locationCandidate{
			{"Paris", SearchScopeArea},
			{"Paris Paris", SearchScopeProperty},
			{"Paris, Paris", SearchScopeProperty},
		}},
		{",Vienna", LocationHotelQuery, []locationCandidate{
			{"Vienna", SearchScopeArea},
			{",Vienna", SearchScopeProperty},
		}},
	}
	for _, tc := range cases {
		if got := locationCandidates(tc.hint, tc.kind); !slices.Equal(got, tc.want) {
			t.Errorf("locationCandidates(%q, %v) = %+v, want %+v", tc.hint, tc.kind, got, tc.want)
		}
	}
}

// TestSearchHotels_PropertyScopeAsksGoogleAndBookingOnly (#812): a property
// search runs only the providers that search by property name, and does not
// geocode the name as a city centre (no distance is computed from it).
func TestSearchHotels_PropertyScopeAsksGoogleAndBookingOnly(t *testing.T) {
	serveGoogleRecording(t, fakeHotelPageWithPrices(hotelWithPrice{"Hotel Sacher Wien", 300}))
	seedGeoAt(t, "Hotel Sacher Wien", viennaLat, viennaLon) // would put the Helsinki-pinned row ~1400 km out

	res, err := SearchHotels(context.Background(), "Hotel Sacher Wien", HotelSearchOptions{
		CheckIn: lookupCheckIn, CheckOut: lookupCheckOut, Currency: "USD", MaxPages: 1, Scope: SearchScopeProperty,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var ids []string
	for _, ps := range res.ProviderStatuses {
		ids = append(ids, ps.ID)
	}
	slices.Sort(ids)
	if want := []string{"booking", "google_hotels"}; !slices.Equal(ids, want) {
		t.Errorf("providers asked = %q, want %q", ids, want)
	}
	if !slices.ContainsFunc(res.Hotels, func(h models.HotelResult) bool { return h.Name == "Hotel Sacher Wien" }) {
		t.Fatalf("hotels = %v, want Hotel Sacher Wien before checking distances", hotelNames(res.Hotels))
	}
	for _, h := range res.Hotels {
		if h.DistanceKm != 0 {
			t.Errorf("%s: DistanceKm = %v, want 0: a hotel name is not a city centre", h.Name, h.DistanceKm)
		}
	}
}

// TestHotelSearchKey_ScopeSeparatesSharedSearches (#812): singleflight shares
// results by this key, so an area search and a property search for the same
// text must never share one.
func TestHotelSearchKey_ScopeSeparatesSharedSearches(t *testing.T) {
	opts := HotelSearchOptions{CheckIn: lookupCheckIn, CheckOut: lookupCheckOut, Currency: "USD"}
	area := hotelSearchKey("Hotel Sacher Wien", opts)
	opts.Scope = SearchScopeProperty
	if property := hotelSearchKey("Hotel Sacher Wien", opts); property == area {
		t.Fatalf("area and property searches share the key %q", area)
	}
}

// TestGetRoomAvailability_HotelQueryHintStaysOffCitySites (#812): a hotel
// name passed as the hint reaches neither the rental sites nor Agoda, in the
// price fallback and the search-page fallback alike.
func TestGetRoomAvailability_HotelQueryHintStaysOffCitySites(t *testing.T) {
	agodaHits := roomLookupWorld(t, googleRoutes{shortEntity, emptyPriceRPC(t), noSacherPage}, false)
	paths := countBlueground(t)
	seedGeoAt(t, "Hotel Sacher Wien", sacherLat, sacherLon)

	if _, err := GetRoomAvailabilityWithOpts(context.Background(), RoomSearchOptions{
		HotelID: sacherPlaceIDRooms, CheckIn: lookupCheckIn, CheckOut: lookupCheckOut,
		Currency: "USD", Location: "Hotel Sacher Wien", LocationKind: LocationHotelQuery,
	}); err != nil {
		t.Fatalf("room lookup: %v", err)
	}
	if got := paths(); len(got) != 0 {
		t.Errorf("Blueground was asked for %q, want no request", got)
	}
	if n := agodaHits(); n != 0 {
		t.Errorf("Agoda got %d requests, want none: the hint names a hotel, not a city", n)
	}
}

// TestGetRoomAvailability_EntityCityIsAnArea (review round 1): when the hint
// is empty and the entity page supplies the city, that city is a place even if
// the caller marked its (empty) hint as a hotel query.
func TestGetRoomAvailability_EntityCityIsAnArea(t *testing.T) {
	entity := []byte(`<html>` + strings.Repeat("<!-- padding -->", 40) +
		`AF_initDataCallback({key: 'ds:0', data:[[null,"Vienna","0x476d079b259d2a7f:0x0"]]});</html>`)
	roomLookupWorld(t, googleRoutes{page(entity), emptyPriceRPC(t), noSacherPage}, false)
	paths := countBlueground(t)

	if _, err := GetRoomAvailabilityWithOpts(context.Background(), RoomSearchOptions{
		HotelID: sacherPlaceIDRooms, CheckIn: lookupCheckIn, CheckOut: lookupCheckOut,
		Currency: "USD", LocationKind: LocationHotelQuery,
	}); err != nil {
		t.Fatalf("room lookup: %v", err)
	}
	if got := paths(); !slices.Contains(got, "/furnished-apartments-vienna") {
		t.Fatalf("Blueground paths = %q, want the entity page's city searched as a place", got)
	}
}

// TestGoogleRoomCalls_ReportRefusal (#813): each default Google room call
// returns a rate-limit error both when Google answers 429 and when the
// cooldown is already armed. A fresh client per row: a 429 arms it.
func TestGoogleRoomCalls_ReportRefusal(t *testing.T) {
	opts := RoomSearchOptions{
		HotelID: sacherPlaceIDRooms, CheckIn: lookupCheckIn, CheckOut: lookupCheckOut,
		Currency: "USD", Location: "Hotel Sacher, Vienna", LocationKind: LocationHotelQuery,
	}
	calls := map[string]func() error{
		"entity page": func() error { _, _, _, err := tryEntityPage(context.Background(), opts); return err },
		"partner prices": func() error {
			_, _, err := tryBatchExecutePrices(context.Background(), opts)
			return err
		},
		"search page": func() error { _, _, err := trySearchPageFallback(context.Background(), opts, nil); return err },
	}
	for name, call := range calls {
		for _, prime := range []bool{false, true} {
			label := name + " 429"
			if prime {
				label = name + " in cooldown"
			}
			t.Run(label, func(t *testing.T) {
				refuse := answer(http.StatusTooManyRequests)
				roomLookupWorld(t, googleRoutes{refuse, refuse, refuse}, prime)
				if err := call(); models.ClassifyProviderError(err) != models.StatusRateLimited {
					t.Fatalf("err = %v, want a rate-limit error", err)
				}
			})
		}
	}
}
