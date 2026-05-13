package hotels

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchBookingPage_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "<html>mock booking page</html>")
	}))
	defer srv.Close()

	body, err := fetchBookingPage(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(body, "mock booking page") {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestFetchBookingPage_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := fetchBookingPage(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
	if !strings.Contains(err.Error(), "status 403") {
		t.Errorf("expected status 403 error, got: %v", err)
	}
}

func TestFetchBookingPage_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	_, err := fetchBookingPage(ctx, srv.URL)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ---------------------------------------------------------------------------
// FetchBookingRooms via httptest
// ---------------------------------------------------------------------------

func TestCov_FetchBookingRooms_EmptyURL(t *testing.T) {
	_, err := FetchBookingRooms(context.Background(), "", "2026-07-01", "2026-07-05", "EUR")
	if err == nil || !strings.Contains(err.Error(), "booking URL is required") {
		t.Errorf("expected 'booking URL is required', got: %v", err)
	}
}

func TestFetchBookingRooms_WithJSONLD(t *testing.T) {
	hotelData := map[string]any{
		"@type": "Hotel",
		"makesOffer": []any{
			map[string]any{
				"name":        "Superior Double Room with Sea View",
				"description": "Spacious 35 m\u00b2 room, sleeps 2 adults, with balcony and minibar",
				"priceSpecification": map[string]any{
					"price":         "189.50",
					"priceCurrency": "EUR",
				},
			},
			map[string]any{
				"name":        "Standard Single Room",
				"description": "Cozy 18 sqm room with free wifi",
				"priceSpecification": map[string]any{
					"price":         99.0,
					"priceCurrency": "EUR",
				},
			},
		},
	}
	jsonLD, _ := json.Marshal(hotelData)
	page := fmt.Sprintf("<html><head>\n"+
		`<script type="application/ld+json">%s</script>`+
		"\n</head><body>hotel page</body></html>", string(jsonLD))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, page)
	}))
	defer srv.Close()

	rooms, err := FetchBookingRooms(context.Background(), srv.URL, "2026-07-01", "2026-07-05", "EUR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rooms) != 2 {
		t.Fatalf("expected 2 rooms, got %d", len(rooms))
	}
	r := rooms[0]
	if r.Name != "Superior Double Room with Sea View" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Price != 189.50 {
		t.Errorf("Price = %v, want 189.50", r.Price)
	}
	if r.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", r.Currency)
	}
	if r.Provider != "Booking.com" {
		t.Errorf("Provider = %q, want Booking.com", r.Provider)
	}
	if r.SizeM2 != 35 {
		t.Errorf("SizeM2 = %v, want 35", r.SizeM2)
	}
	if r.MaxGuests != 2 {
		t.Errorf("MaxGuests = %d, want 2", r.MaxGuests)
	}
}

func TestFetchBookingRooms_FallsBackToSSR(t *testing.T) {
	page := "<html><body>\n" +
		`{"room_name":"Deluxe King Room with Balcony","price_breakdown":{"gross_amount":{"value":250.00}}}` + "\n" +
		`{"room_name":"Economy Twin Room","price_breakdown":{"gross_amount":{"value":120.00}}}` + "\n" +
		"</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, page)
	}))
	defer srv.Close()

	rooms, err := FetchBookingRooms(context.Background(), srv.URL, "2026-07-01", "2026-07-05", "USD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rooms) < 2 {
		t.Fatalf("expected at least 2 rooms from SSR, got %d", len(rooms))
	}
	if rooms[0].Name != "Deluxe King Room with Balcony" {
		t.Errorf("first room Name = %q", rooms[0].Name)
	}
}

func TestFetchBookingRooms_NoOffers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "<html><body>empty page</body></html>")
	}))
	defer srv.Close()
	_, err := FetchBookingRooms(context.Background(), srv.URL, "2026-07-01", "2026-07-05", "USD")
	if err == nil || !strings.Contains(err.Error(), "no room offers") {
		t.Errorf("expected 'no room offers' error, got: %v", err)
	}
}

func TestFetchBookingRooms_FetchError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	_, err := FetchBookingRooms(ctx, srv.URL, "2026-07-01", "2026-07-05", "USD")
	if err == nil || !strings.Contains(err.Error(), "fetch booking detail page") {
		t.Errorf("expected fetch error, got: %v", err)
	}
}

func TestFetchBookingRooms_CurrencyFallback(t *testing.T) {
	hotelData := map[string]any{
		"@type":      "Hotel",
		"makesOffer": []any{map[string]any{"name": "Basic Room", "price": 50.0}},
	}
	jsonLD, _ := json.Marshal(hotelData)
	page := fmt.Sprintf("<html><head>\n"+
		`<script type="application/ld+json">%s</script>`+
		"\n</head></html>", string(jsonLD))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, page)
	}))
	defer srv.Close()

	rooms, err := FetchBookingRooms(context.Background(), srv.URL, "2026-07-01", "2026-07-05", "GBP")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rooms) != 1 || rooms[0].Currency != "GBP" {
		t.Errorf("expected GBP fallback, got %+v", rooms)
	}
}

// ---------------------------------------------------------------------------
// parseBookingApolloRooms / extractRoomNamesFromSSR
// ---------------------------------------------------------------------------

func TestParseBookingApolloRooms_WithRooms(t *testing.T) {
	page := `blah "room_name":"Deluxe Suite" blah "room_name":"Standard Room with Garden View" blah
"price_breakdown":{"gross_amount":{"value":300.00}} more
"price_breakdown":{"gross_amount":{"value":150.00}}`
	offers := parseBookingApolloRooms(page)
	if len(offers) < 2 {
		t.Fatalf("expected at least 2 offers, got %d", len(offers))
	}
	if offers[0].Price != 300.00 {
		t.Errorf("first offer Price = %v, want 300", offers[0].Price)
	}
}

func TestParseBookingApolloRooms_Empty(t *testing.T) {
	if offers := parseBookingApolloRooms("<html>no room data</html>"); offers != nil {
		t.Errorf("expected nil, got %d offers", len(offers))
	}
}

func TestExtractRoomNamesFromSSR_Dedup(t *testing.T) {
	offers := extractRoomNamesFromSSR(`"room_name":"Deluxe Room" and "room_name":"Deluxe Room" again`)
	if len(offers) != 1 {
		t.Errorf("expected 1 after dedup, got %d", len(offers))
	}
}

func TestExtractRoomNamesFromSSR_NoPrices(t *testing.T) {
	offers := extractRoomNamesFromSSR(`"room_name":"King Suite with View" and "room_name":"Twin Room"`)
	if len(offers) != 2 {
		t.Fatalf("expected 2, got %d", len(offers))
	}
	for _, o := range offers {
		if o.Price != 0 {
			t.Errorf("expected 0 price for %q, got %v", o.Name, o.Price)
		}
	}
}

// ---------------------------------------------------------------------------
// ldFloat type branches
// ---------------------------------------------------------------------------

func TestLdFloat_Branches(t *testing.T) {
	cases := []struct {
		name string
		obj  map[string]any
		key  string
		want float64
	}{
		{"int", map[string]any{"v": 42}, "v", 42},
		{"json_number", map[string]any{"v": json.Number("3.14")}, "v", 3.14},
		{"string", map[string]any{"v": "  99.99 "}, "v", 99.99},
		{"invalid_string", map[string]any{"v": "nope"}, "v", 0},
		{"missing_key", map[string]any{"other": 1.0}, "v", 0},
		{"bool_type", map[string]any{"v": true}, "v", 0},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := ldFloat(tt.obj, tt.key); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseBookingJSONLD edge cases
// ---------------------------------------------------------------------------

func TestParseBookingJSONLD_ArrayWrapper(t *testing.T) {
	hotelData := []map[string]any{{
		"@type":      "Hotel",
		"makesOffer": []any{map[string]any{"name": "Junior Suite", "price": 220.0}},
	}}
	jsonLD, _ := json.Marshal(hotelData)
	page := fmt.Sprintf(`<script type="application/ld+json">%s</script>`, string(jsonLD))
	offers, err := parseBookingJSONLD(page)
	if err != nil || len(offers) != 1 || offers[0].Name != "Junior Suite" {
		t.Errorf("err=%v offers=%+v", err, offers)
	}
}

func TestCov_ParseBookingJSONLD_GraphArray(t *testing.T) {
	data := map[string]any{
		"@graph": []any{map[string]any{
			"@type":      "Hotel",
			"makesOffer": []any{map[string]any{"name": "Penthouse Suite", "price": 500.0}},
		}},
	}
	jsonLD, _ := json.Marshal(data)
	page := fmt.Sprintf(`<script type="application/ld+json">%s</script>`, string(jsonLD))
	offers, err := parseBookingJSONLD(page)
	if err != nil || len(offers) != 1 {
		t.Errorf("err=%v len=%d", err, len(offers))
	}
}

func TestParseBookingJSONLD_NoBlocks(t *testing.T) {
	if _, err := parseBookingJSONLD("<html>no json-ld</html>"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCov_ParseBookingJSONLD_NoOffers(t *testing.T) {
	data := map[string]any{"@type": "Organization", "name": "SomeOrg"}
	jsonLD, _ := json.Marshal(data)
	if _, err := parseBookingJSONLD(fmt.Sprintf(`<script type="application/ld+json">%s</script>`, string(jsonLD))); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseBookingJSONLD_InvalidJSON(t *testing.T) {
	if _, err := parseBookingJSONLD(`<script type="application/ld+json">{bad json</script>`); err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// parseOfferObject branches
// ---------------------------------------------------------------------------

func TestParseOfferObject_PriceCurrencyFallback(t *testing.T) {
	room := parseOfferObject(map[string]any{"name": "Budget Room", "price": 75.0, "priceCurrency": "CHF"})
	if room.Price != 75.0 || room.Currency != "CHF" {
		t.Errorf("Price=%v Currency=%q", room.Price, room.Currency)
	}
}

func TestParseOfferObject_BedTypeFromName(t *testing.T) {
	if room := parseOfferObject(map[string]any{"name": "King Suite"}); room.BedType != "1 king bed" {
		t.Errorf("BedType = %q", room.BedType)
	}
}

func TestParseOfferObject_PriceSpecCurrencyKey(t *testing.T) {
	offer := map[string]any{
		"name":               "Test Room",
		"priceSpecification": map[string]any{"price": 100.0, "currency": "SEK"},
	}
	if room := parseOfferObject(offer); room.Currency != "SEK" {
		t.Errorf("Currency = %q", room.Currency)
	}
}

// ---------------------------------------------------------------------------
// extractMakesOffer edge cases
// ---------------------------------------------------------------------------

func TestExtractMakesOffer_SingleObject(t *testing.T) {
	hotel := map[string]any{"@type": "Hotel", "makesOffer": map[string]any{"name": "Solo Room", "price": 80.0}}
	if offers := extractMakesOffer(hotel); len(offers) != 1 {
		t.Fatalf("expected 1, got %d", len(offers))
	}
}

func TestExtractMakesOffer_NoMakesOffer(t *testing.T) {
	if offers := extractMakesOffer(map[string]any{"@type": "Hotel"}); offers != nil {
		t.Errorf("expected nil, got %d", len(offers))
	}
}

func TestExtractMakesOffer_InvalidType(t *testing.T) {
	if offers := extractMakesOffer(map[string]any{"@type": "Hotel", "makesOffer": 42}); offers != nil {
		t.Errorf("expected nil, got %d", len(offers))
	}
}

func TestExtractMakesOffer_EmptyNameSkipped(t *testing.T) {
	hotel := map[string]any{
		"@type": "Hotel",
		"makesOffer": []any{
			map[string]any{"name": "", "price": 50.0},
			map[string]any{"name": "Valid Room", "price": 100.0},
		},
	}
	if offers := extractMakesOffer(hotel); len(offers) != 1 {
		t.Fatalf("expected 1, got %d", len(offers))
	}
}

// ---------------------------------------------------------------------------
// deduplicateOffers edge cases
// ---------------------------------------------------------------------------

func TestDeduplicateOffers_ReplacementChain(t *testing.T) {
	// First: price=0, desc="". Second: price=200, replaces (price>0, existing==0).
	// Third: desc set, replaces (desc!="" && existing.Description=="").
	offers := []bookingRoomOffer{
		{Name: "Deluxe Room", Price: 0},
		{Name: "Deluxe Room", Price: 200},
		{Name: "deluxe room", Description: "A nice room"},
	}
	result := deduplicateOffers(offers)
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
	if result[0].Description != "A nice room" {
		t.Errorf("desc = %q", result[0].Description)
	}
}

func TestDeduplicateOffers_PreferWithPriceOnly(t *testing.T) {
	offers := []bookingRoomOffer{
		{Name: "Standard Room", Price: 0},
		{Name: "standard room", Price: 150},
	}
	result := deduplicateOffers(offers)
	if len(result) != 1 || result[0].Price != 150 {
		t.Errorf("got %+v", result)
	}
}

func TestDeduplicateOffers_EmptyNameSkipped(t *testing.T) {
	offers := []bookingRoomOffer{
		{Name: "", Price: 100},
		{Name: "  ", Price: 200},
		{Name: "Real Room", Price: 300},
	}
	result := deduplicateOffers(offers)
	if len(result) != 1 || result[0].Name != "Real Room" {
		t.Errorf("got %+v", result)
	}
}

// ---------------------------------------------------------------------------
// mergeStringSlices edge cases
// ---------------------------------------------------------------------------

func TestMergeStringSlices_BothEmpty(t *testing.T) {
	if result := mergeStringSlices(nil, nil); result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestMergeStringSlices_AEmpty(t *testing.T) {
	result := mergeStringSlices(nil, []string{"a"})
	if len(result) != 1 || result[0] != "a" {
		t.Errorf("got %v", result)
	}
}

func TestMergeStringSlices_BEmpty(t *testing.T) {
	result := mergeStringSlices([]string{"a"}, nil)
	if len(result) != 1 || result[0] != "a" {
		t.Errorf("got %v", result)
	}
}

func TestMergeStringSlices_CaseInsensitiveDedup(t *testing.T) {
	result := mergeStringSlices([]string{"WiFi"}, []string{"wifi", "Pool"})
	if len(result) != 2 {
		t.Errorf("expected 2, got %d: %v", len(result), result)
	}
}

// ---------------------------------------------------------------------------
// mergeRoomTypes branches
// ---------------------------------------------------------------------------

func TestMergeRoomTypes_EnrichExisting(t *testing.T) {
	google := []RoomType{{Name: "Deluxe Room", Price: 200, Currency: "EUR"}}
	booking := []RoomType{{
		Name: "Deluxe Room", Price: 190, Currency: "EUR", Provider: "Booking.com",
		Description: "A spacious room", BedType: "1 king bed", SizeM2: 35,
		MaxGuests: 2, Amenities: []string{"WiFi", "Minibar"},
	}}
	merged := mergeRoomTypes(google, booking)
	if len(merged) != 1 {
		t.Fatalf("expected 1, got %d", len(merged))
	}
	m := merged[0]
	if m.Price != 200 {
		t.Errorf("Price = %v, want 200", m.Price)
	}
	if m.Description != "A spacious room" || m.BedType != "1 king bed" || m.SizeM2 != 35 {
		t.Errorf("enrichment failed: desc=%q bed=%q size=%v", m.Description, m.BedType, m.SizeM2)
	}
	if len(m.Amenities) == 0 {
		t.Error("Amenities not enriched")
	}
}

func TestMergeRoomTypes_BookingOnlyRooms(t *testing.T) {
	google := []RoomType{{Name: "Standard Room", Price: 100}}
	booking := []RoomType{{Name: "Penthouse Suite", Price: 500, Provider: "Booking.com"}}
	if merged := mergeRoomTypes(google, booking); len(merged) != 2 {
		t.Fatalf("expected 2, got %d", len(merged))
	}
}

func TestMergeRoomTypes_GoogleZeroPriceEnriched(t *testing.T) {
	google := []RoomType{{Name: "Test Room", Price: 0, Currency: "EUR"}}
	booking := []RoomType{{Name: "Test Room", Price: 150, Currency: "EUR", Provider: "Booking.com"}}
	merged := mergeRoomTypes(google, booking)
	if len(merged) != 1 || merged[0].Price != 150 {
		t.Errorf("got %+v", merged)
	}
}

// ---------------------------------------------------------------------------
// GetRoomAvailabilityWithOpts validation
// ---------------------------------------------------------------------------

func TestGetRoomAvailabilityWithOpts_EmptyHotelID(t *testing.T) {
	_, err := GetRoomAvailabilityWithOpts(context.Background(), RoomSearchOptions{
		CheckIn: "2026-07-01", CheckOut: "2026-07-05",
	})
	if err == nil || !strings.Contains(err.Error(), "hotel ID is required") {
		t.Errorf("got: %v", err)
	}
}

func TestGetRoomAvailabilityWithOpts_NoDates(t *testing.T) {
	_, err := GetRoomAvailabilityWithOpts(context.Background(), RoomSearchOptions{HotelID: "test-id"})
	if err == nil || !strings.Contains(err.Error(), "dates are required") {
		t.Errorf("got: %v", err)
	}
}

func TestGetRoomAvailability_Delegates(t *testing.T) {
	if _, err := GetRoomAvailability(context.Background(), "", "2026-07-01", "2026-07-05", "USD"); err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// parseRoomsFromPage
// ---------------------------------------------------------------------------

func TestParseRoomsFromPage_EmptyPage(t *testing.T) {
	rooms, name := parseRoomsFromPage("", "USD")
	if rooms != nil || name != "" {
		t.Errorf("expected nil/empty for empty page")
	}
}

func TestParseRoomsFromPage_WithCallbacks(t *testing.T) {
	roomData := []any{[]any{
		[]any{"Deluxe Suite", 299.0, "EUR", "Booking.com"},
		[]any{"Standard Room", 149.0, "EUR", "Expedia"},
	}}
	jsonBytes, _ := json.Marshal(roomData)
	page := fmt.Sprintf("AF_initDataCallback({key: 'ds:1', data:%s});", string(jsonBytes))
	rooms, _ := parseRoomsFromPage(page, "EUR")
	if len(rooms) < 2 {
		t.Errorf("expected at least 2 rooms, got %d", len(rooms))
	}
}

func TestParseRoomsFromPage_WithHotelName(t *testing.T) {
	data := []any{[]any{"The Grand Hotel Budapest"}}
	jsonBytes, _ := json.Marshal(data)
	page := fmt.Sprintf("AF_initDataCallback({key: 'ds:0', data:%s});", string(jsonBytes))
	if _, name := parseRoomsFromPage(page, "USD"); name != "The Grand Hotel Budapest" {
		t.Errorf("name = %q", name)
	}
}

// ---------------------------------------------------------------------------
// extractLocationFromPage
// ---------------------------------------------------------------------------

func TestExtractLocationFromPage_WithLocation(t *testing.T) {
	data := []any{[]any{nil, "Helsinki", "0x46920b0af7b76d4f"}}
	jsonBytes, _ := json.Marshal(data)
	page := fmt.Sprintf("AF_initDataCallback({key: 'ds:0', data:%s});", string(jsonBytes))
	if loc := extractLocationFromPage(page); loc != "Helsinki" {
		t.Errorf("location = %q", loc)
	}
}

func TestExtractLocationFromPage_NoCallbacks(t *testing.T) {
	if loc := extractLocationFromPage("<html>no callbacks</html>"); loc != "" {
		t.Errorf("expected empty, got %q", loc)
	}
}

func TestExtractLocationFromPage_NoTriplet(t *testing.T) {
	data := []any{"foo", "bar", "baz"}
	jsonBytes, _ := json.Marshal(data)
	page := fmt.Sprintf("AF_initDataCallback({key: 'ds:0', data:%s});", string(jsonBytes))
	if loc := extractLocationFromPage(page); loc != "" {
		t.Errorf("expected empty, got %q", loc)
	}
}

// ---------------------------------------------------------------------------
// extractReviewsFromText
// ---------------------------------------------------------------------------
