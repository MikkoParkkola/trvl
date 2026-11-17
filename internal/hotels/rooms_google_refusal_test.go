package hotels

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/breaker"
	"github.com/MikkoParkkola/trvl/internal/models"
)

const sacherPlaceIDRooms = "ChIJI85wbpwHbUcRiJL_xm_zLE4"

// googleRoutes answers the three Google calls a room lookup makes: the entity
// page, the partner-price RPC and the hotel search page. "/prime" lets a test
// arm the cooldown before the lookup starts.
type googleRoutes struct {
	entity, prices, search http.HandlerFunc
}

func (g googleRoutes) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/prime":
		w.WriteHeader(http.StatusTooManyRequests)
	case strings.HasPrefix(r.URL.Path, "/travel/hotels/entity/"):
		g.entity(w, r)
	case strings.Contains(r.URL.Path, "batchexecute"):
		g.prices(w, r)
	default:
		g.search(w, r)
	}
}

func answer(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func page(b []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(b) }
}

// shortEntity is a deferred entity page: 200, too short to hold room data.
var shortEntity = page([]byte("<html></html>"))

// noSacherPage is a search page without the hotel.
var noSacherPage = page(fakeHotelPageWithPrices(hotelWithPrice{"Hotel Stefanie", 150}))

// sacherBookingUSD is Booking.com's row for the hotel, in the lookup currency.
var sacherBookingUSD = models.HotelResult{
	Name: "Hotel Sacher Wien", Address: "Philharmoniker-Straße 4, 1010 Wien, Austria", HotelID: "4711",
	Price: 480, Currency: "USD", Lat: sacherLat, Lon: sacherLon,
	Sources: []models.PriceSource{{Provider: "booking", Price: 480, Currency: "USD"}},
}

// roomLookupWorld serves routes as Google, keeps every other provider offline,
// answers Agoda with HTTP 429 and seeds the geocodes a Vienna lookup needs. It
// returns the number of requests Agoda has received so far.
func roomLookupWorld(t *testing.T, routes googleRoutes, prime bool) func() int32 {
	t.Helper()
	freshRateManager(t)
	stubNoFX(t)
	useTestDefaultClient(t, routes)
	if prime {
		if status, _, err := DefaultClient().Get(context.Background(), "https://www.google.com/prime"); err != nil || status != http.StatusTooManyRequests {
			t.Fatalf("prime the cooldown: status %d, err %v", status, err)
		}
	}
	origKey := serpapiAPIKeyFunc
	serpapiAPIKeyFunc = func() string { return "" }
	t.Cleanup(func() { serpapiAPIKeyFunc = origKey })

	var agodaHits atomic.Int32
	agoda := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		agodaHits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(agoda.Close)
	origURL, origEnabled, origBreaker := agodaBaseURL, agodaEnabled, hotelBreaker
	agodaBaseURL, agodaEnabled, hotelBreaker = agoda.URL, true, breaker.New()
	t.Setenv("AGODA_CITY_ID", "")
	t.Cleanup(func() { agodaBaseURL, agodaEnabled, hotelBreaker = origURL, origEnabled, origBreaker })

	for _, q := range []string{"Vienna", "Wien", "Hotel Sacher Vienna", "Hotel Sacher, Vienna"} {
		seedGeoAt(t, q, viennaLat, viennaLon)
	}
	return agodaHits.Load
}

// emptyPriceRPC answers the partner-price RPC with Google's real payload-less
// reply, which sends the lookup into the price fallback search.
func emptyPriceRPC(t *testing.T) http.HandlerFunc {
	t.Helper()
	body, err := os.ReadFile("testdata/price_empty_payload.txt")
	if err != nil {
		t.Fatal(err)
	}
	return page(body)
}

func lookupSacherRooms(t *testing.T) *RoomAvailability {
	t.Helper()
	res, err := GetRoomAvailabilityWithOpts(context.Background(), RoomSearchOptions{
		HotelID: sacherPlaceIDRooms, CheckIn: lookupCheckIn, CheckOut: lookupCheckOut,
		Currency: "USD", Location: "Hotel Sacher, Vienna",
	})
	if err != nil {
		t.Fatalf("room lookup: %v", err)
	}
	return res
}

var (
	googleClause = providerRateLimitNotice("Google Hotels", models.ErrRateLimited)
	agodaClause  = providerRateLimitNotice("Agoda", models.ErrRateLimited)
)

// TestGetRoomAvailability_GoogleRefusalInNotice is #813's fail-fast: whichever
// Google call takes the refusal, the notice names Google Hotels with the
// sentence Agoda gets, and ahead of Agoda's.
func TestGetRoomAvailability_GoogleRefusalInNotice(t *testing.T) {
	cases := []struct {
		name    string
		routes  googleRoutes
		prime   bool
		booking bool
	}{
		{"entity page 429", googleRoutes{answer(429), answer(404), noSacherPage}, false, false},
		{"partner prices 429", googleRoutes{shortEntity, answer(429), noSacherPage}, false, false},
		// Booking supplies the rooms; the refusal exists only in the search's
		// google_hotels status row.
		{"search page 429 with Booking rooms", googleRoutes{shortEntity, answer(404), answer(429)}, false, true},
		// The price RPC has no partner prices, so its fallback search runs;
		// Google refuses it and Booking prices the hotel. That price is not
		// Google's.
		{"empty prices, search 429, Booking match", googleRoutes{shortEntity, emptyPriceRPC(t), answer(429)}, false, true},
		// The city search is blocked; a later candidate fails for another
		// reason. That failure is not a Google answer and must not erase the
		// recorded block.
		{"city search 403, then a broken page", googleRoutes{shortEntity, answer(404), blockCityBreakHotel}, false, false},
		{"cooldown already armed", googleRoutes{shortEntity, answer(404), noSacherPage}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			roomLookupWorld(t, tc.routes, tc.prime)
			if tc.booking {
				withBookingResult(t, sacherBookingUSD)
			}
			res := lookupSacherRooms(t)
			if tc.booking && len(res.Rooms) == 0 {
				t.Fatal("precondition: want Booking's rooms from the search fallback")
			}
			g, a := strings.Index(res.Notice, googleClause), strings.Index(res.Notice, agodaClause)
			if g < 0 || a < 0 || g > a {
				t.Fatalf("notice = %q\nwant the Google Hotels clause, then Agoda's", res.Notice)
			}
		})
	}
}

// TestGetRoomAvailability_NoGoogleClauseWithoutRefusal (preservation): Agoda
// is limited, Google answered every call; the notice names Agoda only.
func TestGetRoomAvailability_NoGoogleClauseWithoutRefusal(t *testing.T) {
	roomLookupWorld(t, googleRoutes{shortEntity, answer(404), noSacherPage}, false)
	res := lookupSacherRooms(t)
	if res.Notice != agodaClause {
		t.Fatalf("notice = %q, want only %q", res.Notice, agodaClause)
	}
}

// TestSearchHotelByName_GoogleRefusalIsAnError: when Google refused, a match
// that lacks a Google ID is reported as the refusal, not as a hotel missing
// from Google's index.
func TestSearchHotelByName_GoogleRefusalIsAnError(t *testing.T) {
	// The city search answers; only a query naming the hotel is refused.
	refuseHotelQuery := func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("q"), "Sacher") {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write(fakeHotelPageWithPrices(hotelWithPrice{"Hotel Stefanie", 150}))
	}
	cases := []struct {
		name, query string
		search      http.HandlerFunc
		prime       bool
		wantClock   bool
		noBooking   bool
	}{
		{"candidate search 429", "Hotel Sacher Wien", answer(429), false, false, false},
		{"refusal only in the targeted search", "Hotel Sacher, Vienna", refuseHotelQuery, false, false, false},
		{"cooldown already armed", "Hotel Sacher Wien", noSacherPage, true, true, false},
		// Nothing else answered either, so the search itself fails.
		{"cooldown armed, no other provider", "Hotel Sacher Wien", noSacherPage, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			roomLookupWorld(t, googleRoutes{shortEntity, answer(404), tc.search}, tc.prime)
			if !tc.noBooking {
				withBookingResult(t, sacherBookingUSD)
			}
			seedGeoAt(t, "Hotel Sacher Wien", sacherLat, sacherLon)
			seedGeoAt(t, "Hotel Sacher Wien, Hotel Sacher Wien", sacherLat, sacherLon)

			got, err := SearchHotelByName(context.Background(), tc.query, lookupCheckIn, lookupCheckOut, "USD")
			if !errors.Is(err, models.ErrRateLimited) {
				name := ""
				if got != nil {
					name = got.Name + " / " + got.HotelID
				}
				t.Fatalf("err = %v (match %q), want an error wrapping ErrRateLimited", err, name)
			}
			if tc.wantClock && !strings.Contains(err.Error(), "retry after") {
				t.Fatalf("err = %v, want the cooldown's retry time", err)
			}
		})
	}
}

// TestGetRoomAvailability_CityHintKeepsTheFanOut (preservation): a bare city
// hint is a place, so both room fallbacks still search it everywhere.
func TestGetRoomAvailability_CityHintKeepsTheFanOut(t *testing.T) {
	roomLookupWorld(t, googleRoutes{shortEntity, emptyPriceRPC(t), noSacherPage}, false)
	paths := countBlueground(t)
	if _, err := GetRoomAvailabilityWithOpts(context.Background(), RoomSearchOptions{
		HotelID: sacherPlaceIDRooms, CheckIn: lookupCheckIn, CheckOut: lookupCheckOut,
		Currency: "USD", Location: "Vienna",
	}); err != nil {
		t.Fatalf("room lookup: %v", err)
	}
	if got := paths(); !slices.Contains(got, "/furnished-apartments-vienna") {
		t.Fatalf("Blueground paths = %q, want the Vienna city page", got)
	}
}

// TestSearchHotelByName_RefusalSurvivesANameMiss (#813): Google refused and
// another provider returned only other hotels. The miss may be Google's
// silence, so the refusal travels with it.
func TestSearchHotelByName_RefusalSurvivesANameMiss(t *testing.T) {
	roomLookupWorld(t, googleRoutes{shortEntity, answer(404), answer(429)}, false)
	withBookingResult(t, models.HotelResult{
		Name: "Hotel Stefanie", HotelID: "4712", Price: 150, Currency: "USD", Lat: sacherLat, Lon: sacherLon,
		Sources: []models.PriceSource{{Provider: "booking", Price: 150, Currency: "USD"}},
	})
	seedGeoAt(t, "Hotel Sacher Wien", sacherLat, sacherLon)

	_, err := SearchHotelByName(context.Background(), "Hotel Sacher Wien", lookupCheckIn, lookupCheckOut, "USD")
	if !errors.Is(err, ErrNoNameMatch) || !errors.Is(err, models.ErrRateLimited) {
		t.Fatalf("err = %v, want the name miss joined with Google's rate limit", err)
	}
}

// TestSearchHotelByName_LaterGoogleAnswerClearsAnEarlierBlock: Google blocked
// the city search (403) but then answered the hotel query without the hotel.
// That is an index miss, not a refusal: the match comes back without a
// Google ID and the caller's index guidance applies.
func TestSearchHotelByName_LaterGoogleAnswerClearsAnEarlierBlock(t *testing.T) {
	blockCityAnswerHotel := func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("q"), "Sacher") {
			_, _ = w.Write(fakeHotelPageWithPrices(hotelWithPrice{"Hotel Stefanie", 150}))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}
	roomLookupWorld(t, googleRoutes{shortEntity, answer(404), blockCityAnswerHotel}, false)
	withBookingResult(t, sacherBookingUSD)

	got, err := SearchHotelByName(context.Background(), "Hotel Sacher, Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if err != nil || got == nil || got.HotelID != "4711" {
		t.Fatalf("got %+v, err = %v; want Booking's match and no error", got, err)
	}
}

// TestGetRoomAvailability_NoGoogleClauseWhenFallbackPricedFromGoogle: the
// entity page was blocked, but the search fallback then priced the hotel from
// Google itself. Google withheld nothing, so the notice must not say so.
func TestGetRoomAvailability_NoGoogleClauseWhenFallbackPricedFromGoogle(t *testing.T) {
	sacherPage := page(fakeHotelPageWithPrices(hotelWithPrice{"Hotel Sacher", 300}))
	roomLookupWorld(t, googleRoutes{answer(http.StatusForbidden), answer(404), sacherPage}, false)
	// The fake Google row sits at seedGeo's point; put Vienna there too.
	seedGeo(t, "Vienna")
	seedGeo(t, "Wien")
	res := lookupSacherRooms(t)
	if len(res.Rooms) == 0 {
		t.Fatal("precondition: want the search fallback's Google rooms")
	}
	if strings.Contains(res.Notice, googleClause) {
		t.Fatalf("notice = %q, want no Google Hotels clause: Google priced the hotel", res.Notice)
	}
}

// blockCityBreakHotel blocks the city search (403) and answers a query
// naming the hotel with a page too short to parse.
func blockCityBreakHotel(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Query().Get("q"), "Sacher") {
		_, _ = w.Write([]byte("<html></html>"))
		return
	}
	w.WriteHeader(http.StatusForbidden)
}

// TestSearchHotelByName_TargetedFailureKeepsTheRefusal: Google blocked the
// city search, where Booking found the hotel; the targeted search then failed
// for another reason (a broken page, nothing from Booking). That failure is
// not a Google answer, so the block still decides the outcome.
func TestSearchHotelByName_TargetedFailureKeepsTheRefusal(t *testing.T) {
	roomLookupWorld(t, googleRoutes{shortEntity, answer(404), blockCityBreakHotel}, false)
	origBooking := SearchBooking
	t.Cleanup(func() { SearchBooking = origBooking })
	SearchBooking = func(_ context.Context, loc string, _ HotelSearchOptions) ([]models.HotelResult, error) {
		if strings.Contains(loc, "Sacher") {
			return nil, nil
		}
		return []models.HotelResult{sacherBookingUSD}, nil
	}

	_, err := SearchHotelByName(context.Background(), "Hotel Sacher, Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if !errors.Is(err, models.ErrRateLimited) {
		t.Fatalf("err = %v, want the city search's block reported as a rate limit", err)
	}
}

// TestSearchHotelByName_RefusalSurvivesLaterFailures: no candidate returned
// hotels. The city search was blocked; the later ones failed for another
// reason. The lookup's error still reports the block as a rate limit.
func TestSearchHotelByName_RefusalSurvivesLaterFailures(t *testing.T) {
	roomLookupWorld(t, googleRoutes{shortEntity, answer(404), blockCityBreakHotel}, false)

	_, err := SearchHotelByName(context.Background(), "Hotel Sacher, Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if !errors.Is(err, models.ErrRateLimited) {
		t.Fatalf("err = %v, want the city search's block reported as a rate limit", err)
	}
}
