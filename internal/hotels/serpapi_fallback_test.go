package hotels

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/serpapi"
)

func TestSerpAPIPriceFallbackResolvesRawGoogleIDToVerifiedProviders(t *testing.T) {
	const rawID = "0x133b6ab82c204df7:0x437369f021e5e869"
	restore := stubSerpAPIFallback(t, &serpapi.MapsPlace{
		Title:   "Hotel Continental Mare",
		Address: "Via Baldassarre Cossa, 25, 80074 Ischia NA, Italy",
		DataID:  rawID,
		DataCID: "4860344902944680041",
	}, &serpapi.Response{
		Properties: []serpapi.Hotel{{
			Name:      "Hotel Continental Mare",
			TotalRate: serpapi.Rate{Extracted: 980},
			Prices: []serpapi.PriceOption{{
				Source:       "Hotel Continental Mare",
				RatePerNight: serpapi.Rate{Extracted: 196},
				TotalRate:    serpapi.Rate{Extracted: 980},
			}, {
				Source:       "Hotels.com",
				RatePerNight: serpapi.Rate{Extracted: 280},
				TotalRate:    serpapi.Rate{Extracted: 1402},
			}, {
				Source:       "Hotels.com",
				RatePerNight: serpapi.Rate{Extracted: 280},
				TotalRate:    serpapi.Rate{Extracted: 1402},
			}},
		}},
	})
	defer restore()

	result := trySerpAPIPriceFallback(context.Background(), HotelPriceOpts{
		HotelID:  rawID,
		CheckIn:  "2026-07-30",
		CheckOut: "2026-08-04",
		Currency: "EUR",
	})
	if result == nil {
		t.Fatal("expected SerpAPI fallback result")
	}
	if result.Name != "Hotel Continental Mare" {
		t.Fatalf("name = %q, want Hotel Continental Mare", result.Name)
	}
	if result.Notice != serpapiFallbackNotice {
		t.Fatalf("notice = %q, want SerpAPI fallback notice", result.Notice)
	}
	if len(result.Providers) != 2 {
		t.Fatalf("providers = %d, want 2 deduped providers", len(result.Providers))
	}
	provider := result.Providers[0]
	if provider.Provider != "Hotel Continental Mare" || provider.Price != 980 || provider.Currency != "EUR" {
		t.Fatalf("first provider = %#v, want cheapest verified total from Hotel Continental Mare in EUR", provider)
	}
	if provider.TotalPrice != 980 || provider.NightlyPrice != 196 || provider.PriceBasis == "" || provider.PriceConfidence == "" {
		t.Fatalf("provider detail fields = %#v, want nightly/total/trust metadata", provider)
	}
}

func TestSerpAPIPriceFallbackFetchesSelectedPropertyDetail(t *testing.T) {
	const rawID = "0x133b6ab82c204df7:0x437369f021e5e869"
	restore := stubSerpAPIFallbackWithDetail(t, &serpapi.MapsPlace{
		Title:   "Hotel Continental Mare",
		Address: "Via Baldassarre Cossa, 25, 80074 Ischia NA, Italy",
		DataID:  rawID,
	}, &serpapi.Response{
		Properties: []serpapi.Hotel{{
			Name:          "Hotel Continental Mare",
			PropertyToken: "continental-token",
			TotalRate:     serpapi.Rate{Extracted: 980},
		}},
	}, &serpapi.Hotel{
		Name:          "Hotel Continental Mare",
		PropertyToken: "continental-token",
		Prices: []serpapi.PriceOption{{
			Source:       "Hotel Continental Mare",
			Link:         "https://hotel.example/direct",
			RatePerNight: serpapi.Rate{Extracted: 196},
			TotalRate:    serpapi.Rate{Extracted: 980},
		}, {
			Source:       "Hotels.com",
			Link:         "https://hotels.example/continental",
			RatePerNight: serpapi.Rate{Extracted: 280},
			TotalRate:    serpapi.Rate{Extracted: 1400},
		}},
	}, 1)
	defer restore()

	result := trySerpAPIPriceFallback(context.Background(), HotelPriceOpts{
		HotelID:  rawID,
		CheckIn:  "2026-07-30",
		CheckOut: "2026-08-04",
		Currency: "EUR",
	})
	if result == nil {
		t.Fatal("expected SerpAPI fallback result")
	}
	if len(result.Providers) != 2 {
		t.Fatalf("providers = %#v, want selected-property OTA matrix", result.Providers)
	}
	if result.Providers[0].Provider != "Hotel Continental Mare" || result.Providers[0].ProviderURL == "" {
		t.Fatalf("first provider = %#v, want direct provider with URL", result.Providers[0])
	}
	if result.Providers[1].Provider != "Hotels.com" || result.Providers[1].TotalPrice != 1400 {
		t.Fatalf("second provider = %#v, want Hotels.com total", result.Providers[1])
	}
}

func TestSerpAPIRoomFallbackConvertsVerifiedRoomsAndRefundability(t *testing.T) {
	const rawID = "0x133b6ab82c204df7:0x437369f021e5e869"
	restore := stubSerpAPIFallback(t, &serpapi.MapsPlace{
		Title:   "Hotel Continental Mare",
		Address: "Via Baldassarre Cossa, 25, 80074 Ischia NA, Italy",
		DataID:  rawID,
	}, &serpapi.Response{
		Properties: []serpapi.Hotel{{
			Name: "Hotel Continental Mare",
			Prices: []serpapi.PriceOption{{
				Source:                    "Booking.com",
				Benefits:                  "Breakfast included",
				FreeCancellation:          true,
				FreeCancellationUntilDate: "2026-07-20",
				RatePerNight:              serpapi.Rate{Extracted: 196},
				TotalRate:                 serpapi.Rate{Extracted: 980},
				Rooms: []serpapi.RoomOption{{
					Name:         "Double Room",
					NumGuests:    2,
					RatePerNight: serpapi.Rate{Extracted: 196},
					TotalRate:    serpapi.Rate{Extracted: 980},
				}},
			}},
		}},
	})
	defer restore()

	rooms, name, notice := trySerpAPIRoomFallback(context.Background(), RoomSearchOptions{
		HotelID:  rawID,
		CheckIn:  "2026-07-30",
		CheckOut: "2026-08-04",
		Currency: "EUR",
		Guests:   2,
	})
	if name != "Hotel Continental Mare" {
		t.Fatalf("name = %q, want Hotel Continental Mare", name)
	}
	if notice != serpapiFallbackNotice {
		t.Fatalf("notice = %q, want SerpAPI fallback notice", notice)
	}
	if len(rooms) != 1 {
		t.Fatalf("rooms = %d, want 1", len(rooms))
	}
	room := rooms[0]
	if room.Name != "Double Room" || room.TotalPrice != 980 || room.NightlyPrice != 196 || room.Provider != "Booking.com" {
		t.Fatalf("room = %#v, want verified Booking.com room totals", room)
	}
	if room.FreeCancellation == nil || !*room.FreeCancellation {
		t.Fatalf("free cancellation = %#v, want true", room.FreeCancellation)
	}
	if room.Refundable == nil || !*room.Refundable {
		t.Fatalf("refundable = %#v, want true", room.Refundable)
	}
	if room.BreakfastIncluded == nil || !*room.BreakfastIncluded {
		t.Fatalf("breakfast included = %#v, want true", room.BreakfastIncluded)
	}
}

func TestSerpAPIRoomFallbackFetchesSelectedPropertyDetail(t *testing.T) {
	const rawID = "0x133b6ab82c204df7:0x437369f021e5e869"
	restore := stubSerpAPIFallbackWithDetail(t, &serpapi.MapsPlace{
		Title:   "Hotel Continental Mare",
		Address: "Via Baldassarre Cossa, 25, 80074 Ischia NA, Italy",
		DataID:  rawID,
	}, &serpapi.Response{
		Properties: []serpapi.Hotel{{
			Name:          "Hotel Continental Mare",
			PropertyToken: "continental-token",
			TotalRate:     serpapi.Rate{Extracted: 980},
		}},
	}, &serpapi.Hotel{
		Name:          "Hotel Continental Mare",
		PropertyToken: "continental-token",
		FeaturedPrices: []serpapi.PriceOption{{
			Source:       "Hotels.com",
			RatePerNight: serpapi.Rate{Extracted: 280},
			TotalRate:    serpapi.Rate{Extracted: 1400},
			Rooms: []serpapi.RoomOption{{
				Name:         "Comfort Double Room, Sea View",
				NumGuests:    2,
				RatePerNight: serpapi.Rate{Extracted: 280},
				TotalRate:    serpapi.Rate{Extracted: 1400},
			}},
		}},
	}, 1)
	defer restore()

	rooms, name, notice := trySerpAPIRoomFallback(context.Background(), RoomSearchOptions{
		HotelID:  rawID,
		CheckIn:  "2026-07-30",
		CheckOut: "2026-08-04",
		Currency: "EUR",
		Guests:   2,
	})
	if name != "Hotel Continental Mare" || notice != serpapiFallbackNotice {
		t.Fatalf("name/notice = %q/%q, want selected-property detail fallback", name, notice)
	}
	if len(rooms) != 1 || rooms[0].Name != "Comfort Double Room, Sea View" || rooms[0].Provider != "Hotels.com" || rooms[0].TotalPrice != 1400 {
		t.Fatalf("rooms = %#v, want room-level selected-property detail", rooms)
	}
}

func TestSerpAPIFallbackSkipsWithoutKey(t *testing.T) {
	origKey := serpapiAPIKeyFunc
	origResolve := serpapiResolveGoogleMapsPlaceFunc
	origSearch := serpapiSearchHotelsFunc
	origDetails := serpapiGetPropertyDetailsFunc
	serpapiAPIKeyFunc = func() string { return "" }
	serpapiResolveGoogleMapsPlaceFunc = func(context.Context, string) (*serpapi.MapsPlace, error) {
		t.Fatal("resolver should not run without SERPAPI_KEY")
		return nil, nil
	}
	serpapiSearchHotelsFunc = func(context.Context, serpapi.SearchOptions) (*serpapi.Response, error) {
		t.Fatal("search should not run without SERPAPI_KEY")
		return nil, nil
	}
	serpapiGetPropertyDetailsFunc = func(context.Context, serpapi.SearchOptions, string) (*serpapi.Hotel, error) {
		t.Fatal("details should not run without SERPAPI_KEY")
		return nil, nil
	}
	t.Cleanup(func() {
		serpapiAPIKeyFunc = origKey
		serpapiResolveGoogleMapsPlaceFunc = origResolve
		serpapiSearchHotelsFunc = origSearch
		serpapiGetPropertyDetailsFunc = origDetails
	})

	if got := trySerpAPIPriceFallback(context.Background(), HotelPriceOpts{HotelID: "0x1:0x2", CheckIn: "2026-07-30", CheckOut: "2026-08-04"}); got != nil {
		t.Fatalf("price fallback = %#v, want nil without key", got)
	}
	rooms, name, notice := trySerpAPIRoomFallback(context.Background(), RoomSearchOptions{HotelID: "0x1:0x2", CheckIn: "2026-07-30", CheckOut: "2026-08-04"})
	if rooms != nil || name != "" || notice != "" {
		t.Fatalf("room fallback = rooms %#v name %q notice %q, want empty without key", rooms, name, notice)
	}
}

func TestSearchPageFallbackRejectsBroadCityNameMatch(t *testing.T) {
	result := &models.HotelSearchResult{Hotels: []models.HotelResult{{
		Name:    "Cocos Park Ischia",
		HotelID: "other",
		Price:   56,
	}}}
	got := searchPageFallbackHotel(result, HotelPriceOpts{
		HotelID:  "requested",
		Location: "Ischia",
	})
	if got != nil {
		t.Fatalf("fallback hotel = %#v, want nil for broad city hint without ID match", got)
	}
}

func TestSearchPageFallbackAllowsPropertyNameMatch(t *testing.T) {
	result := &models.HotelSearchResult{Hotels: []models.HotelResult{{
		Name:    "Hotel Continental Mare",
		HotelID: "other",
		Price:   180,
	}}}
	got := searchPageFallbackHotel(result, HotelPriceOpts{
		HotelID:  "requested",
		Location: "Hotel Continental Mare",
	})
	if got == nil || got.Name != "Hotel Continental Mare" {
		t.Fatalf("fallback hotel = %#v, want name-matched property", got)
	}
}

func stubSerpAPIFallbackNoPlace(t *testing.T, response *serpapi.Response, forbidDetail bool) {
	t.Helper()
	origKey := serpapiAPIKeyFunc
	origResolve := serpapiResolveGoogleMapsPlaceFunc
	origSearch := serpapiSearchHotelsFunc
	origDetails := serpapiGetPropertyDetailsFunc
	t.Cleanup(func() {
		serpapiAPIKeyFunc = origKey
		serpapiResolveGoogleMapsPlaceFunc = origResolve
		serpapiSearchHotelsFunc = origSearch
		serpapiGetPropertyDetailsFunc = origDetails
	})
	serpapiAPIKeyFunc = func() string { return "test-key" }
	// A hotel name (not a Google place ID) does not resolve to a Maps place.
	serpapiResolveGoogleMapsPlaceFunc = func(_ context.Context, _ string) (*serpapi.MapsPlace, error) {
		return nil, nil
	}
	serpapiSearchHotelsFunc = func(_ context.Context, _ serpapi.SearchOptions) (*serpapi.Response, error) {
		return response, nil
	}
	serpapiGetPropertyDetailsFunc = func(_ context.Context, _ serpapi.SearchOptions, _ string) (*serpapi.Hotel, error) {
		if forbidDetail {
			t.Fatal("detail lookup must not run when no requested property matches")
		}
		return nil, nil
	}
}

// Regression for RobertoReale's report: with SerpAPI active and a hotel *name*
// (no resolvable Google place ID), the name-based fallback used to return the
// first priced property in the area and label it `verified` — a different
// hotel. Querying "Hotel Villa Maria" returned "Miramare Sea Resort & Spa" at
// €2,336. The safe outcome is no match (providers: null upstream), never a
// confident price for the wrong property.
func TestSerpAPIPriceFallbackRejectsWrongHotelOnNameMismatch(t *testing.T) {
	stubSerpAPIFallbackNoPlace(t, &serpapi.Response{Properties: []serpapi.Hotel{{
		Name:      "Miramare Sea Resort & Spa",
		TotalRate: serpapi.Rate{Extracted: 2336},
		Prices: []serpapi.PriceOption{{
			Source:    "Booking.com",
			TotalRate: serpapi.Rate{Extracted: 2336},
		}},
	}}}, true)

	result := trySerpAPIPriceFallback(context.Background(), HotelPriceOpts{
		HotelID:  "Hotel Villa Maria",
		Location: "Sant Angelo Ischia Italy",
		CheckIn:  "2026-07-30",
		CheckOut: "2026-08-04",
		Currency: "EUR",
	})
	if result != nil {
		t.Fatalf("price fallback = %#v, want nil: a name mismatch must not return a different hotel as verified", result)
	}
}

// Guards against over-rejection: when the requested property *is* among the
// results, the name-based fallback must still resolve it even without a Google
// place ID.
func TestSerpAPIPriceFallbackMatchesRequestedPropertyByName(t *testing.T) {
	stubSerpAPIFallbackNoPlace(t, &serpapi.Response{Properties: []serpapi.Hotel{{
		Name:      "Other Resort",
		TotalRate: serpapi.Rate{Extracted: 2336},
		Prices:    []serpapi.PriceOption{{Source: "Booking.com", TotalRate: serpapi.Rate{Extracted: 2336}}},
	}, {
		Name:      "Hotel Villa Maria",
		TotalRate: serpapi.Rate{Extracted: 1106},
		Prices:    []serpapi.PriceOption{{Source: "Booking.com", TotalRate: serpapi.Rate{Extracted: 1106}}},
	}}}, false)

	result := trySerpAPIPriceFallback(context.Background(), HotelPriceOpts{
		HotelID:  "Hotel Villa Maria",
		Location: "Sant Angelo Ischia Italy",
		CheckIn:  "2026-07-30",
		CheckOut: "2026-08-04",
		Currency: "EUR",
	})
	if result == nil {
		t.Fatal("price fallback = nil, want the name-matched property")
	}
	if result.Name != "Hotel Villa Maria" {
		t.Fatalf("name = %q, want Hotel Villa Maria (must match the requested property, not the first priced one)", result.Name)
	}
}

func stubSerpAPIFallback(t *testing.T, place *serpapi.MapsPlace, response *serpapi.Response) func() {
	return stubSerpAPIFallbackWithDetail(t, place, response, nil, 0)
}

func stubSerpAPIFallbackWithDetail(t *testing.T, place *serpapi.MapsPlace, response *serpapi.Response, detail *serpapi.Hotel, wantDetailCalls int) func() {
	t.Helper()
	origKey := serpapiAPIKeyFunc
	origResolve := serpapiResolveGoogleMapsPlaceFunc
	origSearch := serpapiSearchHotelsFunc
	origDetails := serpapiGetPropertyDetailsFunc
	var resolveCalls, searchCalls, detailCalls int

	serpapiAPIKeyFunc = func() string { return "test-key" }
	serpapiResolveGoogleMapsPlaceFunc = func(_ context.Context, id string) (*serpapi.MapsPlace, error) {
		resolveCalls++
		if id == "" {
			t.Fatal("resolver called with empty hotel ID")
		}
		return place, nil
	}
	serpapiSearchHotelsFunc = func(_ context.Context, opts serpapi.SearchOptions) (*serpapi.Response, error) {
		searchCalls++
		if opts.Query != "Ischia" {
			t.Fatalf("query = %q, want locality query Ischia", opts.Query)
		}
		if opts.CheckIn != "2026-07-30" || opts.CheckOut != "2026-08-04" || opts.Currency != "EUR" {
			t.Fatalf("search opts dates/currency = %#v", opts)
		}
		if opts.MaxDetails != 0 {
			t.Fatalf("max details = %d, want 0 for list-only selected-property search", opts.MaxDetails)
		}
		return response, nil
	}
	serpapiGetPropertyDetailsFunc = func(_ context.Context, opts serpapi.SearchOptions, propertyToken string) (*serpapi.Hotel, error) {
		detailCalls++
		if opts.Query != "Ischia" {
			t.Fatalf("details query = %q, want Ischia", opts.Query)
		}
		if detail != nil && propertyToken != detail.PropertyToken {
			t.Fatalf("property token = %q, want %q", propertyToken, detail.PropertyToken)
		}
		return detail, nil
	}

	return func() {
		if resolveCalls != 1 {
			t.Fatalf("resolve calls = %d, want 1", resolveCalls)
		}
		if searchCalls != 1 {
			t.Fatalf("search calls = %d, want 1", searchCalls)
		}
		if detailCalls != wantDetailCalls {
			t.Fatalf("detail calls = %d, want %d", detailCalls, wantDetailCalls)
		}
		serpapiAPIKeyFunc = origKey
		serpapiResolveGoogleMapsPlaceFunc = origResolve
		serpapiSearchHotelsFunc = origSearch
		serpapiGetPropertyDetailsFunc = origDetails
	}
}
