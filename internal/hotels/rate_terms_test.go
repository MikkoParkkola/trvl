package hotels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// rateTermsDonor is a same-name room from another fetcher carrying all five
// rate terms.
func rateTermsDonor(source, seller string) RoomType {
	yes := true
	return RoomType{
		Name: "Double Room", Price: 95, NightlyPrice: 95, Currency: "EUR",
		Provider: seller, Source: source, ProviderURL: "https://example.com/" + source,
		MatchConfidence:    models.RoomInventoryMatchExact,
		CancellationPolicy: "Free cancellation until 1 day before",
		Refundable:         &yes,
		FreeCancellation:   &yes,
		Board:              "breakfast_included",
		BreakfastIncluded:  &yes,
	}
}

func pricedGoogleRoom() RoomType {
	return RoomType{
		Name: "Double Room", Price: 100, Currency: "EUR",
		Provider: "Hotels.com", Source: "google_hotels", ProviderURL: "https://www.hotels.com/ho1",
		MatchConfidence: models.RoomInventoryMatchExact,
	}
}

func assertNoRateTerms(t *testing.T, label string, r RoomType) {
	t.Helper()
	if r.CancellationPolicy != "" || r.Refundable != nil || r.FreeCancellation != nil || r.Board != "" || r.BreakfastIncluded != nil {
		t.Errorf("%s: priced room took another seller's rate terms: policy %q refundable %v free %v board %q breakfast %v",
			label, r.CancellationPolicy, r.Refundable, r.FreeCancellation, r.Board, r.BreakfastIncluded)
	}
}

// TestMergeRoomTypes_KeepsOwnRateTerms is #823's fail-fast: Booking's refund
// flag on a priced Google room made it "ready" at Google's price.
func TestMergeRoomTypes_KeepsOwnRateTerms(t *testing.T) {
	m := mergeRoomTypes([]RoomType{pricedGoogleRoom()}, []RoomType{rateTermsDonor("booking.com", "Booking.com")})[0]
	assertNoRateTerms(t, "booking", m)
	if got := roomReadiness(m); got != ReadinessCaution {
		t.Errorf("readiness = %q, want caution: Hotels.com stated no refund terms", got)
	}
	if len(m.InventoryOptions) != 2 || m.InventoryOptions[1].Refundable == nil || !*m.InventoryOptions[1].Refundable {
		t.Errorf("Booking's quote lost its own refund flag: %+v", m.InventoryOptions)
	}
}

// Every merge caller (Booking, the Google detail relay, SerpAPI, Agoda) goes
// through the same rule.
func TestMergeRoomTypes_NoCallerSplicesRateTerms(t *testing.T) {
	for _, d := range []struct{ source, seller string }{
		{"booking.com", "Booking.com"}, {"google_hotels", "Google Hotels"}, {"serpapi", "Expedia"}, {"agoda", "agoda"},
	} {
		m := mergeRoomTypes([]RoomType{pricedGoogleRoom()}, []RoomType{rateTermsDonor(d.source, d.seller)})[0]
		assertNoRateTerms(t, d.source, m)
		if len(m.InventoryOptions) != 2 {
			t.Errorf("%s: merge shape changed: %+v", d.source, m)
		}
	}
}

// An unpriced room that adopts the other room's quote takes exactly that
// quote's rate terms, overwriting its own, including nil, empty and false.
func TestMergeRoomTypes_AdoptedQuoteBringsItsTerms(t *testing.T) {
	yes, no := true, false
	recipient := RoomType{
		Name: "Double Room", Currency: "EUR", Provider: "Hotels.com", Source: "google_hotels",
		CancellationPolicy: "Hotels.com policy", Refundable: &yes, FreeCancellation: &yes,
		Board: "half_board", BreakfastIncluded: &yes,
	}
	cases := map[string]RoomType{
		"donor states terms": rateTermsDonor("booking.com", "Booking.com"),
		"donor states false": {Name: "Double Room", Price: 95, Currency: "EUR", Provider: "Booking.com", Source: "booking.com",
			Refundable: &no, FreeCancellation: &no, BreakfastIncluded: &no, Board: "room_only", CancellationPolicy: "Non-refundable"},
		"donor states nothing": {Name: "Double Room", Price: 95, Currency: "EUR", Provider: "Booking.com", Source: "booking.com"},
	}
	for name, donor := range cases {
		m := mergeRoomTypes([]RoomType{recipient}, []RoomType{donor})[0]
		if m.Provider != donor.Provider || m.CancellationPolicy != donor.CancellationPolicy || m.Board != donor.Board ||
			!sameBoolPtr(m.Refundable, donor.Refundable) || !sameBoolPtr(m.FreeCancellation, donor.FreeCancellation) ||
			!sameBoolPtr(m.BreakfastIncluded, donor.BreakfastIncluded) {
			t.Errorf("%s: adopted room = %+v, want exactly the donor's terms", name, m)
		}
	}
}

func sameBoolPtr(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// detailRelayRooms fetches rooms the way the room watch and hotel_rooms do,
// with the Google side served by a local detail relay answering detailJSON and
// Booking returning booking. Everything else Google is a local 404.
func detailRelayRooms(t *testing.T, detailJSON string, booking []RoomType) []RoomType {
	t.Helper()
	// TestMain switches the relay off for the suite; this test serves it locally.
	prevEnabled := googleRoomDetailEnabled
	googleRoomDetailEnabled = true
	t.Cleanup(func() { googleRoomDetailEnabled = prevEnabled })
	useTestDefaultClient(t, http.NotFoundHandler())
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/rooms/query") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(detailJSON))
	}))
	t.Cleanup(relay.Close)
	t.Setenv("GOOGLE_HOTELS_DETAIL_API_BASE", relay.URL)
	origKey := serpapiAPIKeyFunc
	serpapiAPIKeyFunc = func() string { return "" }
	t.Cleanup(func() { serpapiAPIKeyFunc = origKey })
	origFetch := FetchBookingRooms
	FetchBookingRooms = func(context.Context, string, string, string, string) ([]RoomType, error) { return booking, nil }
	t.Cleanup(func() { FetchBookingRooms = origFetch })

	av, err := GetRoomAvailabilityWithOpts(context.Background(), RoomSearchOptions{
		HotelID: "/g/11hotelx", CheckIn: "2026-11-18", CheckOut: "2026-11-20", Currency: "EUR",
		BookingURL: "https://www.booking.com/hotel/at/x.html",
	})
	if err != nil {
		t.Fatalf("GetRoomAvailabilityWithOpts: %v", err)
	}
	return av.Rooms
}

// roomNamed returns the Google room with that name, never an appended
// Booking-only room of the same name.
func roomNamed(t *testing.T, rooms []RoomType, name string) RoomType {
	t.Helper()
	for _, r := range rooms {
		if r.Name == name && r.Source == "google_hotels" {
			return r
		}
	}
	t.Fatalf("no Google room %q in %+v", name, rooms)
	return RoomType{}
}

// detailRoom is one exact Google detail room with a stable direct link; refund
// is the raw JSON fragment for its refund terms ("" for none stated).
func detailRoom(refund string) string {
	return `{"hotelName":"Hotel X","rooms":[{"roomName":"Deluxe Double Room","provider":"Expedia",` +
		`"bookingUrl":"https://www.expedia.com/h/deluxe","nightlyRate":{"amount":120,"currencyCode":"EUR"},` +
		`"totalRate":{"amount":240,"currencyCode":"EUR"}` + refund + `,"maxOccupancy":2}]}`
}

// TestGetRoomAvailability_ReadinessNeedsOwnRefundTerms: the room users see
// must not read "ready" on Booking's refund flag. The controls prove the link
// and match qualify, so unknown refundability alone yields "caution".
func TestGetRoomAvailability_ReadinessNeedsOwnRefundTerms(t *testing.T) {
	yes := true
	refundableBooking := []RoomType{{
		Name: "Deluxe Double Room", Price: 110, NightlyPrice: 110, Currency: "EUR",
		Provider: "Booking.com", Source: "booking.com", ProviderURL: "https://www.booking.com/hotel/at/x.html",
		MatchConfidence: models.RoomInventoryMatchExact, Refundable: &yes,
	}}

	t.Run("booking refund flag stays with booking", func(t *testing.T) {
		r := roomNamed(t, detailRelayRooms(t, detailRoom(""), refundableBooking), "Deluxe Double Room")
		if r.Refundable != nil || r.Readiness != ReadinessCaution {
			t.Errorf("Google room: refundable %v, readiness %q; want nil and caution", r.Refundable, r.Readiness)
		}
	})
	t.Run("control: no booking room", func(t *testing.T) {
		r := roomNamed(t, detailRelayRooms(t, detailRoom(""), nil), "Deluxe Double Room")
		if r.Readiness != ReadinessCaution {
			t.Errorf("readiness = %q, want caution", r.Readiness)
		}
	})
	t.Run("control: own refund terms", func(t *testing.T) {
		// The relay records refund terms only when refundable is true.
		r := roomNamed(t, detailRelayRooms(t, detailRoom(`,"refundable":true`), refundableBooking), "Deluxe Double Room")
		if r.Readiness != ReadinessReady || r.Refundable == nil || !*r.Refundable {
			t.Errorf("readiness %q refundable %v, want ready on the Google room's own refund term", r.Readiness, r.Refundable)
		}
	})
}

// No room or quote leaves the package without a seller, so a caller can
// compare two concrete seller names.
func TestGetRoomAvailability_EverySellerNamed(t *testing.T) {
	useTestDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body>` + strings.Repeat("<!-- pad -->", 50) +
			`<script>AF_initDataCallback({key: 'ds:1', data:[[["Deluxe Suite",120,"EUR"],["Standard Room",90,"EUR"]]]});</script></body></html>`))
	}))
	t.Setenv("GOOGLE_HOTELS_DETAIL_API_BASE", "")
	origKey := serpapiAPIKeyFunc
	serpapiAPIKeyFunc = func() string { return "" }
	t.Cleanup(func() { serpapiAPIKeyFunc = origKey })
	origFetch := FetchBookingRooms
	FetchBookingRooms = func(context.Context, string, string, string, string) ([]RoomType, error) {
		return []RoomType{
			{Name: "Deluxe Suite", Price: 95, NightlyPrice: 95, Currency: "EUR", Provider: "Booking.com", Source: "booking.com"},
			// Unmatched, so appended as is, with a quote that names no seller.
			{Name: "Penthouse", Price: 300, NightlyPrice: 300, Currency: "EUR", Provider: "Booking.com", Source: "booking.com",
				InventoryOptions: []models.RoomInventoryQuote{{NightlyPrice: 300, Currency: "EUR"}}},
		}, nil
	}
	t.Cleanup(func() { FetchBookingRooms = origFetch })

	av, err := GetRoomAvailabilityWithOpts(context.Background(), RoomSearchOptions{
		HotelID: "/g/11hotelx", CheckIn: "2026-11-18", CheckOut: "2026-11-20", Currency: "EUR",
		BookingURL: "https://www.booking.com/hotel/at/x.html",
	})
	if err != nil {
		t.Fatalf("GetRoomAvailabilityWithOpts: %v", err)
	}
	if len(av.Rooms) < 2 {
		t.Fatalf("rooms = %+v, want the two entity-page rooms", av.Rooms)
	}
	for _, r := range av.Rooms {
		if strings.TrimSpace(r.Provider) == "" {
			t.Errorf("room %q has no seller", r.Name)
		}
		for _, q := range r.InventoryOptions {
			if strings.TrimSpace(q.Provider) == "" {
				t.Errorf("room %q has a quote with no seller", r.Name)
			}
		}
	}
}

// An adopted quote is the donor's whole quote: an unpriced room's own
// currency, rate id and rate plan never mix in beside the donor's price
// (#832 review).
func TestMergeRoomTypes_AdoptionTakesTheWholeQuote(t *testing.T) {
	recipient := RoomType{
		Name: "Double Room", Currency: "USD", RateID: "g-1", RatePlanName: "Google plan",
		Provider: "Hotels.com", Source: "google_hotels", Description: "Courtyard view",
	}
	donor := RoomType{
		Name: "Double Room", Price: 95, NightlyPrice: 95, Currency: "EUR", RateID: "b-1", RatePlanName: "Flexible",
		Provider: "Booking.com", Source: "booking.com", ProviderURL: "https://www.booking.com/hotel/at/x.html",
		MatchConfidence: models.RoomInventoryMatchExact,
	}
	m := mergeRoomTypes([]RoomType{recipient}, []RoomType{donor})[0]
	if m.Currency != "EUR" || m.RateID != "b-1" || m.RatePlanName != "Flexible" || m.MatchConfidence != models.RoomInventoryMatchExact {
		t.Errorf("adopted quote = currency %q rate %q plan %q match %q, want Booking's EUR b-1 Flexible exact",
			m.Currency, m.RateID, m.RatePlanName, m.MatchConfidence)
	}
	if m.Name != "Double Room" || m.Description != "Courtyard view" || len(m.InventoryOptions) != 2 {
		t.Errorf("room identity or enrichment lost: %+v", m)
	}
}
