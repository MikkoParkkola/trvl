package hotels

import (
	"context"
	"testing"

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

func TestSerpAPIFallbackSkipsWithoutKey(t *testing.T) {
	origKey := serpapiAPIKeyFunc
	origResolve := serpapiResolveGoogleMapsPlaceFunc
	origSearch := serpapiSearchHotelsVerifiedFunc
	serpapiAPIKeyFunc = func() string { return "" }
	serpapiResolveGoogleMapsPlaceFunc = func(context.Context, string) (*serpapi.MapsPlace, error) {
		t.Fatal("resolver should not run without SERPAPI_KEY")
		return nil, nil
	}
	serpapiSearchHotelsVerifiedFunc = func(context.Context, serpapi.SearchOptions) (*serpapi.Response, error) {
		t.Fatal("search should not run without SERPAPI_KEY")
		return nil, nil
	}
	t.Cleanup(func() {
		serpapiAPIKeyFunc = origKey
		serpapiResolveGoogleMapsPlaceFunc = origResolve
		serpapiSearchHotelsVerifiedFunc = origSearch
	})

	if got := trySerpAPIPriceFallback(context.Background(), HotelPriceOpts{HotelID: "0x1:0x2", CheckIn: "2026-07-30", CheckOut: "2026-08-04"}); got != nil {
		t.Fatalf("price fallback = %#v, want nil without key", got)
	}
	rooms, name, notice := trySerpAPIRoomFallback(context.Background(), RoomSearchOptions{HotelID: "0x1:0x2", CheckIn: "2026-07-30", CheckOut: "2026-08-04"})
	if rooms != nil || name != "" || notice != "" {
		t.Fatalf("room fallback = rooms %#v name %q notice %q, want empty without key", rooms, name, notice)
	}
}

func stubSerpAPIFallback(t *testing.T, place *serpapi.MapsPlace, response *serpapi.Response) func() {
	t.Helper()
	origKey := serpapiAPIKeyFunc
	origResolve := serpapiResolveGoogleMapsPlaceFunc
	origSearch := serpapiSearchHotelsVerifiedFunc
	var resolveCalls, searchCalls int

	serpapiAPIKeyFunc = func() string { return "test-key" }
	serpapiResolveGoogleMapsPlaceFunc = func(_ context.Context, id string) (*serpapi.MapsPlace, error) {
		resolveCalls++
		if id == "" {
			t.Fatal("resolver called with empty hotel ID")
		}
		return place, nil
	}
	serpapiSearchHotelsVerifiedFunc = func(_ context.Context, opts serpapi.SearchOptions) (*serpapi.Response, error) {
		searchCalls++
		if opts.Query != "Ischia" {
			t.Fatalf("query = %q, want locality query Ischia", opts.Query)
		}
		if opts.CheckIn != "2026-07-30" || opts.CheckOut != "2026-08-04" || opts.Currency != "EUR" {
			t.Fatalf("search opts dates/currency = %#v", opts)
		}
		if opts.MaxDetails != 4 {
			t.Fatalf("max details = %d, want 4 for quota-bounded fallback", opts.MaxDetails)
		}
		return response, nil
	}

	return func() {
		if resolveCalls != 1 {
			t.Fatalf("resolve calls = %d, want 1", resolveCalls)
		}
		if searchCalls != 1 {
			t.Fatalf("search calls = %d, want 1", searchCalls)
		}
		serpapiAPIKeyFunc = origKey
		serpapiResolveGoogleMapsPlaceFunc = origResolve
		serpapiSearchHotelsVerifiedFunc = origSearch
	}
}
