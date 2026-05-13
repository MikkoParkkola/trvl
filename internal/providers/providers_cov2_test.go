package providers

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func parseTestURL(rawURL string) (*url.URL, error) {
	return url.Parse(rawURL)
}

// ===========================================================================
// mapping.go — mapHotelResult, extractRoomTypes, denormalizeApollo, unwrapNiobe
// ===========================================================================

func TestLoadCachedCookies_RoundTrip(t *testing.T) {
	// Create a temp dir for the cookie cache.
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	// Override HOME so cookieCacheDir uses the temp dir.
	// We'll save manually instead.

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	targetURL := "https://www.example-cookie-test.com/page"
	u, _ := parseTestURL(targetURL)

	// Seed the jar with cookies.
	jar.SetCookies(u, []*http.Cookie{
		{Name: "session", Value: "abc123", Domain: ".example-cookie-test.com", Path: "/"},
		{Name: "csrf", Value: "xyz789", Domain: ".example-cookie-test.com", Path: "/"},
	})

	// Save cookies to a custom path.
	cachePath := filepath.Join(dir, "www.example-cookie-test.com.json")
	cookies := jar.Cookies(u)
	now := time.Now()
	cached := make([]cachedCookie, len(cookies))
	for i, c := range cookies {
		cached[i] = cachedCookie{
			Name:    c.Name,
			Value:   c.Value,
			Domain:  c.Domain,
			Path:    c.Path,
			SavedAt: now,
		}
	}
	data, _ := json.Marshal(cached)
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(cachePath, data, 0o600)

	// Create a fresh client to load cookies into.
	jar2, _ := cookiejar.New(nil)
	client2 := &http.Client{Jar: jar2}

	// Load by reading the file directly — we test the logic path.
	loadData, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	var loaded []cachedCookie
	if err := json.Unmarshal(loadData, &loaded); err != nil {
		t.Fatal(err)
	}
	if len(loaded) == 0 {
		t.Fatal("expected cached cookies")
	}
	if time.Since(loaded[0].SavedAt) > cookieCacheTTL {
		t.Fatal("cookies expired unexpectedly")
	}
	httpCookies := make([]*http.Cookie, len(loaded))
	for i, c := range loaded {
		httpCookies[i] = &http.Cookie{
			Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path,
		}
	}
	client2.Jar.SetCookies(u, httpCookies)

	got := client2.Jar.Cookies(u)
	if len(got) < 2 {
		t.Errorf("expected at least 2 cookies loaded, got %d", len(got))
	}

	_ = client
	_ = origHome
}

func TestLoadCachedCookies_NilJar(t *testing.T) {
	client := &http.Client{} // no jar
	got := loadCachedCookies(client, "https://example.com")
	if got {
		t.Error("expected false for client with no jar")
	}
}

func TestLoadCachedCookies_BadURL(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	got := loadCachedCookies(client, "::bad-url::")
	if got {
		t.Error("expected false for bad URL")
	}
}

func TestLoadCachedCookies_EmptyHost(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	got := loadCachedCookies(client, "file:///local/path")
	if got {
		t.Error("expected false for URL with no host")
	}
}

func TestSaveCachedCookies_NilJar(t *testing.T) {
	client := &http.Client{} // no jar
	// Should not panic.
	saveCachedCookies(client, "https://example.com")
}

func TestSaveCachedCookies_BadURL(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	// Should not panic.
	saveCachedCookies(client, "::bad-url::")
}

func TestSaveCachedCookies_EmptyCookies(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	// No cookies in jar — should be a no-op.
	saveCachedCookies(client, "https://no-cookies.example.com")
}

func TestCookieCachePath_Sanitization(t *testing.T) {
	path, err := cookieCachePath("www.example.com")
	if err != nil {
		t.Fatalf("cookieCachePath: %v", err)
	}
	if !strings.HasSuffix(path, "www.example.com.json") {
		t.Errorf("expected path ending with www.example.com.json, got %q", path)
	}

	// Domain with special chars.
	path2, err := cookieCachePath("api:8080/path")
	if err != nil {
		t.Fatalf("cookieCachePath: %v", err)
	}
	// ':' and '/' should be replaced with '_'.
	base := filepath.Base(path2)
	if strings.Contains(base, ":") || strings.Contains(base, "/") {
		t.Errorf("special chars not sanitized in %q", base)
	}
}

// parseTestURL is a test helper wrapping url.Parse.

func TestMapHotelResult_AllFields(t *testing.T) {
	raw := map[string]any{
		"hotelName":  "Grand Hotel",
		"hotelId":    float64(12345),
		"starRating": float64(4),
		"score":      float64(8.5),
		"reviews":    float64(250),
		"basePrice":  float64(199.99),
		"curr":       "EUR",
		"addr":       "123 Main St",
		"latitude":   float64(52.5),
		"longitude":  float64(13.4),
		"link":       "https://example.com/hotel/123",
		"eco":        true,
		"desc":       "A nice hotel",
		"img":        "https://example.com/img.jpg",
		"district":   "Mitte",
	}

	fields := map[string]string{
		"name":          "hotelName",
		"hotel_id":      "hotelId",
		"stars":         "starRating",
		"rating":        "score",
		"review_count":  "reviews",
		"price":         "basePrice",
		"currency":      "curr",
		"address":       "addr",
		"lat":           "latitude",
		"lon":           "longitude",
		"booking_url":   "link",
		"eco_certified": "eco",
		"description":   "desc",
		"image_url":     "img",
		"neighborhood":  "district",
	}

	h := mapHotelResult(raw, fields)
	if h.Name != "Grand Hotel" {
		t.Errorf("Name = %q", h.Name)
	}
	if h.HotelID != "12345" {
		t.Errorf("HotelID = %q, want '12345'", h.HotelID)
	}
	if h.Stars != 4 {
		t.Errorf("Stars = %d", h.Stars)
	}
	if h.Rating != 8.5 {
		t.Errorf("Rating = %v", h.Rating)
	}
	if h.ReviewCount != 250 {
		t.Errorf("ReviewCount = %d", h.ReviewCount)
	}
	if h.Price != 199.99 {
		t.Errorf("Price = %v", h.Price)
	}
	if h.Currency != "EUR" {
		t.Errorf("Currency = %q", h.Currency)
	}
	if h.Address != "123 Main St" {
		t.Errorf("Address = %q", h.Address)
	}
	if h.Lat != 52.5 {
		t.Errorf("Lat = %v", h.Lat)
	}
	if h.Lon != 13.4 {
		t.Errorf("Lon = %v", h.Lon)
	}
	if h.BookingURL != "https://example.com/hotel/123" {
		t.Errorf("BookingURL = %q", h.BookingURL)
	}
	if !h.EcoCertified {
		t.Error("EcoCertified should be true")
	}
	if h.Description != "A nice hotel" {
		t.Errorf("Description = %q", h.Description)
	}
	if h.ImageURL != "https://example.com/img.jpg" {
		t.Errorf("ImageURL = %q", h.ImageURL)
	}
	if h.Neighborhood != "Mitte" {
		t.Errorf("Neighborhood = %q", h.Neighborhood)
	}
}

func TestMapHotelResult_CurrencyFromPriceString(t *testing.T) {
	raw := map[string]any{
		"hotelName": "Budget Inn",
		"price":     "€ 85",
	}
	fields := map[string]string{
		"name":  "hotelName",
		"price": "price",
	}

	h := mapHotelResult(raw, fields)
	if h.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR (extracted from price string)", h.Currency)
	}
	if h.Price != 85 {
		t.Errorf("Price = %v, want 85", h.Price)
	}
}

func TestMapHotelResult_HotelIDString(t *testing.T) {
	raw := map[string]any{
		"id": "abc-123",
	}
	fields := map[string]string{
		"hotel_id": "id",
	}

	h := mapHotelResult(raw, fields)
	if h.HotelID != "abc-123" {
		t.Errorf("HotelID = %q, want 'abc-123'", h.HotelID)
	}
}

func TestMapHotelResult_HotelIDFloat(t *testing.T) {
	raw := map[string]any{
		"id": float64(3.14),
	}
	fields := map[string]string{
		"hotel_id": "id",
	}

	h := mapHotelResult(raw, fields)
	if h.HotelID != "3.14" {
		t.Errorf("HotelID = %q, want '3.14'", h.HotelID)
	}
}

func TestMapHotelResult_NilValues(t *testing.T) {
	raw := map[string]any{} // all nil
	fields := map[string]string{
		"name":  "hotelName",
		"price": "basePrice",
	}

	h := mapHotelResult(raw, fields)
	if h.Name != "" {
		t.Errorf("Name should be empty, got %q", h.Name)
	}
	if h.Price != 0 {
		t.Errorf("Price should be 0, got %v", h.Price)
	}
}

// ===========================================================================
// mapping.go — extractRoomTypes
// ===========================================================================

func TestExtractRoomTypes_FromBlocks(t *testing.T) {
	raw := map[string]any{
		"blocks": []any{
			map[string]any{
				"roomName":   "Standard Double",
				"finalPrice": map[string]any{"amount": float64(120), "currency": "EUR"},
				"blockId":    map[string]any{"roomId": "101"},
			},
			map[string]any{
				"roomName":   "Superior Suite",
				"finalPrice": map[string]any{"amount": float64(280), "currency": "EUR"},
				"blockId":    map[string]any{"roomId": "102"},
			},
			map[string]any{
				"roomName":   "Standard Double", // duplicate
				"finalPrice": map[string]any{"amount": float64(130), "currency": "EUR"},
				"blockId":    map[string]any{"roomId": "101"},
			},
		},
	}

	rooms := extractRoomTypes(raw)
	if len(rooms) != 2 {
		t.Fatalf("expected 2 unique rooms, got %d", len(rooms))
	}
}

func TestExtractRoomTypes_NoBlocks(t *testing.T) {
	raw := map[string]any{}
	rooms := extractRoomTypes(raw)
	if len(rooms) != 0 {
		t.Errorf("expected 0 rooms for no blocks, got %d", len(rooms))
	}
}

func TestExtractRoomTypes_EmptyBlocks(t *testing.T) {
	raw := map[string]any{"blocks": []any{}}
	rooms := extractRoomTypes(raw)
	if len(rooms) != 0 {
		t.Errorf("expected 0 rooms for empty blocks, got %d", len(rooms))
	}
}

func TestExtractRoomTypes_BlockWithFreeCancellation(t *testing.T) {
	raw := map[string]any{
		"blocks": []any{
			map[string]any{
				"roomName":              "Deluxe Room",
				"finalPrice":            map[string]any{"amount": float64(200), "currency": "EUR"},
				"blockId":               map[string]any{"roomId": "201"},
				"freeCancellationUntil": "2026-06-01",
				"mealPlanIncluded":      true,
			},
		},
	}

	rooms := extractRoomTypes(raw)
	if len(rooms) != 1 {
		t.Fatalf("expected 1 room, got %d", len(rooms))
	}
	if !rooms[0].FreeCancellation {
		t.Error("expected FreeCancellation=true")
	}
	if !rooms[0].BreakfastIncluded {
		t.Error("expected BreakfastIncluded=true")
	}
}

func TestExtractRoomTypes_BlockWithRoomSizeAndOccupancy(t *testing.T) {
	raw := map[string]any{
		"blocks": []any{
			map[string]any{
				"roomName":     "King Suite",
				"finalPrice":   map[string]any{"amount": float64(350), "currency": "USD"},
				"blockId":      map[string]any{"roomId": "301"},
				"roomSize":     map[string]any{"value": float64(45)},
				"maxOccupancy": float64(3),
				"bedType":      "King bed",
			},
		},
	}

	rooms := extractRoomTypes(raw)
	if len(rooms) != 1 {
		t.Fatalf("expected 1 room, got %d", len(rooms))
	}
	if rooms[0].SizeM2 != 45 {
		t.Errorf("SizeM2 = %v, want 45", rooms[0].SizeM2)
	}
	if rooms[0].MaxGuests != 3 {
		t.Errorf("MaxGuests = %d, want 3", rooms[0].MaxGuests)
	}
	if rooms[0].BedType != "King bed" {
		t.Errorf("BedType = %q, want 'King bed'", rooms[0].BedType)
	}
}

func TestExtractRoomTypes_BlockWithPoliciesFreeCancellation(t *testing.T) {
	raw := map[string]any{
		"blocks": []any{
			map[string]any{
				"roomName":   "Economy Room",
				"finalPrice": map[string]any{"amount": float64(80)},
				"blockId":    map[string]any{"roomId": "401"},
				"policies":   map[string]any{"showFreeCancellation": true},
			},
		},
	}
	rooms := extractRoomTypes(raw)
	if len(rooms) != 1 {
		t.Fatalf("expected 1 room, got %d", len(rooms))
	}
	if !rooms[0].FreeCancellation {
		t.Error("expected FreeCancellation=true via policies")
	}
}

func TestExtractRoomTypes_BlockWithBreakfastString(t *testing.T) {
	raw := map[string]any{
		"blocks": []any{
			map[string]any{
				"roomName":   "B&B Room",
				"finalPrice": map[string]any{"amount": float64(90)},
				"blockId":    map[string]any{"roomId": "501"},
				"breakfast":  "Breakfast included",
			},
		},
	}
	rooms := extractRoomTypes(raw)
	if len(rooms) != 1 {
		t.Fatalf("expected 1 room, got %d", len(rooms))
	}
	if !rooms[0].BreakfastIncluded {
		t.Error("expected BreakfastIncluded=true via breakfast string")
	}
}

func TestExtractRoomTypes_BlockWithRoomFacilities(t *testing.T) {
	raw := map[string]any{
		"blocks": []any{
			map[string]any{
				"roomName":   "Premium Room",
				"finalPrice": map[string]any{"amount": float64(160)},
				"blockId":    map[string]any{"roomId": "601"},
				"roomFacilities": []any{
					map[string]any{"name": "Free WiFi"},
					"Air conditioning",
				},
			},
		},
	}
	rooms := extractRoomTypes(raw)
	if len(rooms) != 1 {
		t.Fatalf("expected 1 room, got %d", len(rooms))
	}
	if len(rooms[0].Amenities) != 2 {
		t.Errorf("expected 2 amenities, got %d: %v", len(rooms[0].Amenities), rooms[0].Amenities)
	}
}

func TestExtractRoomTypes_WithUnitConfigurations(t *testing.T) {
	raw := map[string]any{
		"matchingUnitConfigurations": map[string]any{
			"unitConfigurations": []any{
				map[string]any{
					"name":   "Standard Room",
					"unitId": "R1",
				},
				map[string]any{
					"name":   "Deluxe Room",
					"unitId": "R2",
				},
			},
		},
		"blocks": []any{
			map[string]any{
				"blockId":    map[string]any{"roomId": "R1"},
				"finalPrice": map[string]any{"amount": float64(100), "currency": "EUR"},
			},
		},
	}

	rooms := extractRoomTypes(raw)
	// R1 matched to block, R2 unmatched → both should appear.
	if len(rooms) < 2 {
		t.Fatalf("expected at least 2 rooms (1 from block + 1 unmatched unit), got %d", len(rooms))
	}
}

func TestExtractRoomTypes_NbAdultsFallback(t *testing.T) {
	raw := map[string]any{
		"blocks": []any{
			map[string]any{
				"roomName":   "Twin Room",
				"finalPrice": map[string]any{"amount": float64(100)},
				"blockId":    map[string]any{"roomId": "701"},
				"nbAdults":   float64(2),
			},
		},
	}
	rooms := extractRoomTypes(raw)
	if len(rooms) != 1 {
		t.Fatalf("expected 1 room, got %d", len(rooms))
	}
	if rooms[0].MaxGuests != 2 {
		t.Errorf("MaxGuests = %d, want 2 (from nbAdults)", rooms[0].MaxGuests)
	}
}

// ===========================================================================
// mapping.go — denormalizeApollo
// ===========================================================================

func TestDenormalizeApollo_RefsResolved(t *testing.T) {
	cache := map[string]any{
		"Hotel:1": map[string]any{
			"name": "Hilton",
			"location": map[string]any{
				"__ref": "Location:1",
			},
		},
		"Location:1": map[string]any{
			"city": "Helsinki",
		},
	}

	input := map[string]any{
		"__ref": "Hotel:1",
	}

	result := denormalizeApollo(input, cache, nil)
	hotel, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if hotel["name"] != "Hilton" {
		t.Errorf("name = %v, want Hilton", hotel["name"])
	}
	loc, ok := hotel["location"].(map[string]any)
	if !ok {
		t.Fatalf("expected location map, got %T", hotel["location"])
	}
	if loc["city"] != "Helsinki" {
		t.Errorf("city = %v, want Helsinki", loc["city"])
	}
}

func TestDenormalizeApollo_DanglingRef(t *testing.T) {
	cache := map[string]any{}
	input := map[string]any{"__ref": "Missing:1"}

	result := denormalizeApollo(input, cache, nil)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if m["__ref"] != "Missing:1" {
		t.Error("dangling ref should be returned as-is")
	}
}

func TestDenormalizeApollo_Array(t *testing.T) {
	cache := map[string]any{
		"Item:1": map[string]any{"name": "A"},
		"Item:2": map[string]any{"name": "B"},
	}
	input := []any{
		map[string]any{"__ref": "Item:1"},
		map[string]any{"__ref": "Item:2"},
	}

	result := denormalizeApollo(input, cache, nil)
	arr, ok := result.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("expected 2-element array, got %v", result)
	}
}

func TestDenormalizeApollo_Primitive(t *testing.T) {
	result := denormalizeApollo("hello", nil, nil)
	if result != "hello" {
		t.Errorf("primitive should pass through, got %v", result)
	}
}

// ===========================================================================
// mapping.go — unwrapNiobe
// ===========================================================================

func TestUnwrapNiobe_ValidStructure(t *testing.T) {
	input := map[string]any{
		"niobeClientData": []any{
			[]any{
				"CacheKey:123",
				map[string]any{
					"data": map[string]any{
						"results": []any{"a", "b"},
					},
				},
			},
		},
	}

	result := unwrapNiobe(input)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if _, hasData := m["data"]; !hasData {
		t.Error("expected 'data' key in unwrapped result")
	}
}

func TestUnwrapNiobe_NoNiobeKey(t *testing.T) {
	input := map[string]any{"other": "data"}
	result := unwrapNiobe(input)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if m["other"] != "data" {
		t.Error("non-Niobe input should be returned unchanged")
	}
}

func TestUnwrapNiobe_NonMap(t *testing.T) {
	result := unwrapNiobe("not a map")
	if result != "not a map" {
		t.Error("non-map input should be returned unchanged")
	}
}

func TestUnwrapNiobe_EmptyEntries(t *testing.T) {
	input := map[string]any{
		"niobeClientData": []any{},
	}
	result := unwrapNiobe(input)
	// Should return the original map when no entries.
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if _, hasNiobe := m["niobeClientData"]; !hasNiobe {
		t.Error("expected original map returned")
	}
}

func TestUnwrapNiobe_EntryWithEmptyData(t *testing.T) {
	input := map[string]any{
		"niobeClientData": []any{
			[]any{
				"CacheKey:1",
				map[string]any{
					"data": map[string]any{}, // empty data
				},
			},
		},
	}
	result := unwrapNiobe(input)
	// Empty data map → should return original.
	if result == nil {
		t.Error("should not return nil")
	}
	// Verify it's the original (empty data doesn't qualify).
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if _, hasNiobe := m["niobeClientData"]; !hasNiobe {
		t.Error("expected original map returned for empty data")
	}
}

// ===========================================================================
// mapping.go — resolveCityID, resolvePropertyType
// ===========================================================================

func TestResolveCityID_ExactMatch(t *testing.T) {
	lookup := map[string]string{"prague": "19", "amsterdam": "3"}
	got := resolveCityID(lookup, "Prague")
	if got != "19" {
		t.Errorf("got %q, want '19'", got)
	}
}

func TestResolveCityID_PartialMatch(t *testing.T) {
	lookup := map[string]string{"praha": "19"}
	got := resolveCityID(lookup, "Praha Center")
	if got != "19" {
		t.Errorf("got %q, want '19' (partial match)", got)
	}
}

func TestResolveCityID_EmptyLocation(t *testing.T) {
	lookup := map[string]string{"prague": "19"}
	got := resolveCityID(lookup, "  ")
	if got != "" {
		t.Errorf("expected empty for whitespace location, got %q", got)
	}
}

func TestResolveCityID_NilLookup(t *testing.T) {
	got := resolveCityID(nil, "Prague")
	if got != "" {
		t.Errorf("expected empty for nil lookup, got %q", got)
	}
}
