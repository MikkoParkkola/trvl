package hotels

import (
	"context"
	"fmt"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestSearchPagePriceReachesRoomLevel locks the Google room-level fix: the
// headline search-page price (tagged "similar" by trySearchPageFallback) must
// resolve to a room-level / room-nightly quote so it can pass the booking-ready
// gate, while alternate provider prices (still property-level) stay lead-in.
func TestSearchPagePriceReachesRoomLevel(t *testing.T) {
	headline := roomInventoryQuote(RoomType{
		Name:            "Standard Room",
		Price:           140,
		NightlyPrice:    140,
		Currency:        "EUR",
		Provider:        "Google Hotels",
		MatchConfidence: models.RoomInventoryMatchSimilar,
	})
	if headline.PriceConfidence != models.PriceConfidenceRoomLevel {
		t.Fatalf("headline price confidence = %q, want room_level", headline.PriceConfidence)
	}
	if headline.PriceBasis != models.PriceBasisRoomNightly {
		t.Fatalf("headline price basis = %q, want room_nightly", headline.PriceBasis)
	}
	if headline.NightlyPrice != 140 {
		t.Fatalf("headline nightly price = %v, want 140", headline.NightlyPrice)
	}

	alt := roomInventoryQuote(RoomType{
		Name:            "Standard Room",
		Price:           150,
		Currency:        "EUR",
		Provider:        "Expedia",
		MatchConfidence: models.RoomInventoryMatchPropertyLevelOnly,
	})
	if alt.PriceConfidence != models.PriceConfidenceUnverified {
		t.Fatalf("alternate provider confidence = %q, want unverified", alt.PriceConfidence)
	}
	if alt.PriceBasis != models.PriceBasisLeadIn {
		t.Fatalf("alternate provider basis = %q, want lead_in", alt.PriceBasis)
	}
}

func TestHasVerifiedRoom(t *testing.T) {
	// Nightly-only "similar" match -> room-level but NOT verified, so SerpAPI
	// should still be consulted to try to upgrade it.
	weak := []RoomType{{
		Name:            "Standard Room",
		Price:           514,
		NightlyPrice:    514,
		Currency:        "EUR",
		Provider:        "Google Hotels",
		MatchConfidence: models.RoomInventoryMatchSimilar,
	}}
	if hasVerifiedRoom(weak) {
		t.Fatal("nightly-only similar match must not count as verified (SerpAPI upgrade should fire)")
	}

	taxIncluded := true
	// Tax-inclusive total on a real room match -> verified, so SerpAPI quota
	// should be conserved.
	strong := []RoomType{{
		Name:              "Deluxe Room",
		Price:             600,
		TotalPrice:        600,
		TaxesFeesIncluded: &taxIncluded,
		Currency:          "EUR",
		Provider:          "Booking.com",
		MatchConfidence:   models.RoomInventoryMatchExact,
	}}
	if !hasVerifiedRoom(strong) {
		t.Fatal("tax-inclusive total on an exact match must count as verified (skip SerpAPI)")
	}

	if hasVerifiedRoom(nil) {
		t.Fatal("empty room set is not verified")
	}
}

func TestGetRoomAvailability_ParallelBookingFetch(t *testing.T) {
	origFetch := FetchBookingRooms
	FetchBookingRooms = func(ctx context.Context, url, checkIn, checkOut, currency string) ([]RoomType, error) {
		return []RoomType{{
			Name:     "Booking Deluxe Room",
			Price:    120,
			Currency: "EUR",
			Provider: "Booking.com",
		}}, nil
	}
	defer func() { FetchBookingRooms = origFetch }()

	opts := RoomSearchOptions{
		HotelID:    "test-hotel-id",
		CheckIn:    "2026-08-10",
		CheckOut:   "2026-08-17",
		Currency:   "EUR",
		BookingURL: "https://www.booking.com/hotel/test",
	}

	result, err := GetRoomAvailabilityWithOpts(context.Background(), opts)
	if err != nil {
		t.Fatalf("GetRoomAvailabilityWithOpts failed: %v", err)
	}

	foundBooking := false
	for _, r := range result.Rooms {
		if r.Provider == "Booking.com" {
			foundBooking = true
			break
		}
	}
	if !foundBooking {
		t.Error("expected Booking.com room in results")
	}
}

func TestGetRoomAvailability_SkipsBookingWhenNoURL(t *testing.T) {
	origFetch := FetchBookingRooms
	callCount := 0
	FetchBookingRooms = func(ctx context.Context, url, checkIn, checkOut, currency string) ([]RoomType, error) {
		callCount++
		return nil, nil
	}
	defer func() { FetchBookingRooms = origFetch }()

	opts := RoomSearchOptions{
		HotelID:  "test-hotel-id",
		CheckIn:  "2026-08-10",
		CheckOut: "2026-08-17",
		Currency: "EUR",
	}

	_, err := GetRoomAvailabilityWithOpts(context.Background(), opts)
	if err != nil {
		t.Fatalf("GetRoomAvailabilityWithOpts failed: %v", err)
	}

	if callCount > 0 {
		t.Error("expected FetchBookingRooms NOT to be called when no BookingURL")
	}
}

func TestRoomFallbackSearchOptionsUsesRequestedGuests(t *testing.T) {
	opts := RoomSearchOptions{
		HotelID:      "test-hotel-id",
		CheckIn:      "2026-08-10",
		CheckOut:     "2026-08-17",
		Currency:     "EUR",
		Guests:       4,
		ChildrenAges: []int{7, 10},
		Rooms:        2,
	}

	got := roomFallbackSearchOptions(opts)
	if got.Guests != 4 {
		t.Fatalf("fallback Guests = %d, want requested guests 4", got.Guests)
	}
	if len(got.ChildrenAges) != 2 || got.ChildrenAges[0] != 7 || got.ChildrenAges[1] != 10 {
		t.Fatalf("fallback ChildrenAges = %v, want [7 10]", got.ChildrenAges)
	}
	if got.Rooms != 2 {
		t.Fatalf("fallback Rooms = %d, want requested rooms 2", got.Rooms)
	}
	if got.CheckIn != opts.CheckIn || got.CheckOut != opts.CheckOut || got.Currency != opts.Currency {
		t.Fatalf("fallback dates/currency = %s/%s/%s, want %s/%s/%s",
			got.CheckIn, got.CheckOut, got.Currency, opts.CheckIn, opts.CheckOut, opts.Currency)
	}
}

func TestRoomFallbackSearchOptionsDefaultsGuests(t *testing.T) {
	got := roomFallbackSearchOptions(RoomSearchOptions{
		HotelID:  "test-hotel-id",
		CheckIn:  "2026-08-10",
		CheckOut: "2026-08-17",
		Currency: "EUR",
	})
	if got.Guests != 2 {
		t.Fatalf("fallback Guests = %d, want default guests 2", got.Guests)
	}
}

// TestRoomMatchFromPriceBasis pins the honesty mapping used when converting
// Google's yY52ce partner-price matrix into room entries: only an explicit
// room-level basis earns an exact match; lead-in / unset bases stay
// property_level_only so a property lead-in is never promoted to booking-ready.
func TestRoomMatchFromPriceBasis(t *testing.T) {
	exact := map[string]bool{
		models.PriceBasisRoomTotal:         true,
		models.PriceBasisTaxInclusiveTotal: true,
		models.PriceBasisRoomNightly:       true,
	}
	for basis := range exact {
		if got := roomMatchFromPriceBasis(basis); got != models.RoomInventoryMatchExact {
			t.Fatalf("basis %q must map to exact_room_match, got %q", basis, got)
		}
	}
	for _, basis := range []string{models.PriceBasisLeadIn, "", "weird_unseen_basis"} {
		if got := roomMatchFromPriceBasis(basis); got != models.RoomInventoryMatchPropertyLevelOnly {
			t.Fatalf("basis %q must map to property_level_only, got %q", basis, got)
		}
	}
}

// TestHotelRoomsToRoomTypes locks the Agoda room-level converter: model rooms
// (as parseAgodaSearch emits them) map field-for-field into RoomType, the
// currency falls back to the request default when a room omits it, and the
// bool->*bool widening only sets the flag when the source said true (a source
// false stays nil = not-claimed, never a hard "no").
func TestHotelRoomsToRoomTypes(t *testing.T) {
	taxIncl := true
	rooms := []models.Room{
		{
			Name:              "Deluxe King",
			Price:             180,
			NightlyPrice:      90,
			TotalPrice:        180,
			TaxesAndFees:      20,
			TaxesFeesIncluded: &taxIncl,
			Currency:          "USD",
			Provider:          "Agoda",
			MatchConfidence:   models.RoomInventoryMatchExact,
			MaxGuests:         2,
			FreeCancellation:  true,
			BreakfastIncluded: false,
		},
		{
			Name:     "Standard Twin",
			Price:    120,
			Currency: "", // should fall back to default
			Provider: "Agoda",
		},
	}

	got := hotelRoomsToRoomTypes(rooms, "EUR")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}

	d := got[0]
	if d.Name != "Deluxe King" || d.Price != 180 || d.NightlyPrice != 90 || d.TotalPrice != 180 {
		t.Fatalf("deluxe prices wrong: %+v", d)
	}
	if d.Currency != "USD" {
		t.Fatalf("deluxe currency = %q, want USD (room had its own)", d.Currency)
	}
	if d.TaxesFeesIncluded == nil || !*d.TaxesFeesIncluded {
		t.Fatal("deluxe TaxesFeesIncluded should carry through as true")
	}
	if d.FreeCancellation == nil || !*d.FreeCancellation {
		t.Fatal("source FreeCancellation=true must widen to *bool true")
	}
	if d.BreakfastIncluded != nil {
		t.Fatal("source BreakfastIncluded=false must stay nil (not-claimed), not a hard false")
	}
	if d.MatchConfidence != models.RoomInventoryMatchExact {
		t.Fatalf("deluxe match = %q, want exact", d.MatchConfidence)
	}

	s := got[1]
	if s.Currency != "EUR" {
		t.Fatalf("twin currency = %q, want default EUR fallback", s.Currency)
	}
	if s.FreeCancellation != nil {
		t.Fatal("twin FreeCancellation must stay nil when source false/unset")
	}
}

// TestPartnersToRoomTypes locks the Google partner-matrix expansion (issue
// #290 AC.3/AC.4): the cheapest priced partner is promoted to room-level
// "similar" so it reaches the booking-ready gate, an explicit room-basis
// partner keeps its "exact" confidence, a non-cheapest lead-in stays
// property-level (never a fabricated match), and a zero-price partner is
// dropped entirely rather than emitted as a free room.
func TestPartnersToRoomTypes(t *testing.T) {
	providers := []models.ProviderPrice{
		{Provider: "Expedia", Price: 150, Currency: "EUR", PriceBasis: models.PriceBasisLeadIn},
		{Provider: "Booking.com", Price: 120, Currency: "", PriceBasis: models.PriceBasisLeadIn}, // cheapest -> promote
		{Provider: "Hotels.com", Price: 200, Currency: "EUR", PriceBasis: models.PriceBasisRoomTotal},
		{Provider: "Dead", Price: 0, NightlyPrice: 0, Currency: "EUR"}, // no price -> dropped
	}

	rooms := partnersToRoomTypes(providers, "USD")
	if len(rooms) != 3 {
		t.Fatalf("len = %d, want 3 (zero-price partner dropped)", len(rooms))
	}

	byProvider := map[string]RoomType{}
	for _, r := range rooms {
		byProvider[r.Provider] = r
	}

	cheap := byProvider["Booking.com"]
	if cheap.MatchConfidence != models.RoomInventoryMatchSimilar {
		t.Fatalf("cheapest lead-in match = %q, want similar (promoted to booking-ready)", cheap.MatchConfidence)
	}
	if cheap.Currency != "USD" {
		t.Fatalf("cheapest currency = %q, want USD default fallback", cheap.Currency)
	}
	if cheap.NightlyPrice != 120 {
		t.Fatalf("cheapest nightly = %v, want 120 (filled from price when no nightly/total)", cheap.NightlyPrice)
	}

	if got := byProvider["Hotels.com"].MatchConfidence; got != models.RoomInventoryMatchExact {
		t.Fatalf("room-total partner match = %q, want exact", got)
	}
	if got := byProvider["Expedia"].MatchConfidence; got != models.RoomInventoryMatchPropertyLevelOnly {
		t.Fatalf("non-cheapest lead-in match = %q, want property_level_only (no fabricated promotion)", got)
	}
	if _, dead := byProvider["Dead"]; dead {
		t.Fatal("zero-price partner must be dropped, not emitted as a free room")
	}
}

// real, bookable price lead (exact > similar > property-level lead-in), then
// cheapest-first within a tier, and rooms with no usable price sink to the
// bottom of their tier. This delivers "lead with the ones that have proper
// price available" at the room level.
func TestSortRoomsByBookability(t *testing.T) {
	rooms := []RoomType{
		{Name: "lead-in cheap", Price: 50, MatchConfidence: models.RoomInventoryMatchPropertyLevelOnly},
		{Name: "exact pricey", Price: 200, MatchConfidence: models.RoomInventoryMatchExact},
		{Name: "no price", MatchConfidence: models.RoomInventoryMatchSimilar},
		{Name: "similar mid", Price: 120, MatchConfidence: models.RoomInventoryMatchSimilar},
		{Name: "exact cheap", Price: 100, MatchConfidence: models.RoomInventoryMatchExact},
		{Name: "similar nightly", NightlyPrice: 90, MatchConfidence: models.RoomInventoryMatchSimilar},
	}

	sortRoomsByBookability(rooms)

	want := []string{
		"exact cheap",     // exact tier, cheapest
		"exact pricey",    // exact tier
		"similar nightly", // similar tier, 90 (nightly counts)
		"similar mid",     // similar tier, 120
		"no price",        // similar tier, no usable price -> bottom of tier
		"lead-in cheap",   // property-level lead-in last regardless of price
	}
	if len(rooms) != len(want) {
		t.Fatalf("len = %d, want %d", len(rooms), len(want))
	}
	for i, name := range want {
		if rooms[i].Name != name {
			got := make([]string, len(rooms))
			for k, r := range rooms {
				got[k] = r.Name
			}
			t.Fatalf("order[%d] = %q, want %q (full: %v)", i, rooms[i].Name, name, got)
		}
	}
}

// TestBookingRateLimitNotice locks the rate-limited-vs-failed distinction for
// the Booking room drill-down: a retryable bot-wall / 429 must surface a
// caution (so a withheld price is not mistaken for "no rooms"), while a hard
// parse failure or no error stays silent (a retry would not help).
func TestBookingRateLimitNotice(t *testing.T) {
	// Retryable conditions -> caution surfaced.
	for _, brErr := range []error{
		models.ErrRateLimited,
		fmt.Errorf("request blocked by DataDome challenge"),
		fmt.Errorf("HTTP 429: too many requests"),
		fmt.Errorf("503 service unavailable"),
	} {
		if got := bookingRateLimitNotice(brErr); got == "" {
			t.Errorf("retryable %v -> empty notice, want caution", brErr)
		}
	}
	// Hard failure / nil -> silent.
	for _, brErr := range []error{
		nil,
		fmt.Errorf("no room offers found on booking detail page"),
		fmt.Errorf("unexpected JSON-LD shape"),
	} {
		if got := bookingRateLimitNotice(brErr); got != "" {
			t.Errorf("non-retryable %v -> %q, want empty", brErr, got)
		}
	}
}

// TestAppendNotice covers the notice-join helper: clauses accumulate
// space-separated and an empty operand never adds a stray separator.
func TestAppendNotice(t *testing.T) {
	if got := appendNotice("", "a"); got != "a" {
		t.Errorf("empty+a = %q, want a", got)
	}
	if got := appendNotice("a", ""); got != "a" {
		t.Errorf("a+empty = %q, want a", got)
	}
	if got := appendNotice("a", "b"); got != "a b" {
		t.Errorf("a+b = %q, want 'a b'", got)
	}
}
