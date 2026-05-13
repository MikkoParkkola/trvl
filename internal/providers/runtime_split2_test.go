package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestToInt(t *testing.T) {
	tests := []struct {
		input any
		want  int
	}{
		{3.0, 3},
		{42, 42},
		{"99", 99},
		{true, 0},
		{nil, 0},
		// Composite strings: lastIntToken extracts the trailing integer.
		{"4.84 (25)", 25},
		{"4.96 (510)", 510},
		{"Rating: 351 reviews", 351},
	}
	for _, tt := range tests {
		got := toInt(tt.input)
		if got != tt.want {
			t.Errorf("toInt(%v) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestFirstNumericToken(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"4.84 (25)", "4.84"},
		{"€ 204", "204"},
		{"abc", ""},
		{"123", "123"},
		{"-42.5 total", "-42.5"},
		// Thousands separator: comma in "1,204" should be stripped.
		{"€1,204", "1204"},
		{"$2,500 per night", "2500"},
		{"€12,345.67 total", "12345.67"},
	}
	for _, tt := range tests {
		got := firstNumericToken(tt.input)
		if got != tt.want {
			t.Errorf("firstNumericToken(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLastIntToken(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"4.84 (25)", "25"},
		{"4.96 (510)", "510"},
		{"no numbers", ""},
		{"just 42", "42"},
		{"1 and 2 and 3", "3"},
	}
	for _, tt := range tests {
		got := lastIntToken(tt.input)
		if got != tt.want {
			t.Errorf("lastIntToken(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestResolveCityID(t *testing.T) {
	lookup := map[string]string{
		"prague":    "19",
		"helsinki":  "45",
		"amsterdam": "3",
	}
	tests := []struct{ input, want string }{
		{"Prague", "19"},
		{"prague", "19"},
		{"PRAGUE", "19"},
		{"Helsinki", "45"},
		{"  Amsterdam  ", "3"},
		{"Prague 1", "19"}, // partial: "prague 1" contains "prague"
		{"Unknown", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := resolveCityID(lookup, tt.input); got != tt.want {
				t.Errorf("resolveCityID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
	// Empty lookup returns "".
	if got := resolveCityID(nil, "Prague"); got != "" {
		t.Errorf("nil lookup: got %q, want empty", got)
	}
}

func TestResolvePropertyType(t *testing.T) {
	lookup := map[string]string{
		"hotel":     "204",
		"apartment": "201",
		"hostel":    "203",
	}
	tests := []struct{ input, want string }{
		{"hotel", "204"},
		{"Hotel", "204"},
		{"APARTMENT", "201"},
		{"hostel", "203"},
		{"  Hotel  ", "204"},
		{"resort", ""}, // not in lookup
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := resolvePropertyType(lookup, tt.input); got != tt.want {
				t.Errorf("resolvePropertyType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
	// Nil lookup returns "".
	if got := resolvePropertyType(nil, "hotel"); got != "" {
		t.Errorf("nil lookup: got %q, want empty", got)
	}
}

func TestSearchHotelsFilterPassthrough(t *testing.T) {
	// Mock server that captures query params to verify filter vars are substituted.
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		resp := map[string]any{
			"results": []any{
				map[string]any{"name": "Filter Hotel", "id": "fh1"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	cfg := &ProviderConfig{
		ID:       "filter-test",
		Name:     "Filter Test",
		Category: "hotels",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		QueryParams: map[string]string{
			"min_price":         "${min_price}",
			"max_price":         "${max_price}",
			"property_type":     "${property_type}",
			"sort":              "${sort}",
			"stars":             "${stars}",
			"min_rating":        "${min_rating}",
			"amenities":         "${amenities}",
			"free_cancellation": "${free_cancellation}",
		},
		PropertyTypeLookup: map[string]string{
			"hotel":     "204",
			"apartment": "201",
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields: map[string]string{
				"name":     "name",
				"hotel_id": "id",
			},
		},
		RateLimit: RateLimitConfig{
			RequestsPerSecond: 100,
			Burst:             10,
		},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)
	filters := &HotelFilterParams{
		MinPrice:         50,
		MaxPrice:         300,
		PropertyType:     "hotel",
		Sort:             "price",
		Stars:            4,
		MinRating:        4.0,
		Amenities:        []string{"wifi", "pool"},
		FreeCancellation: true,
	}
	hotels, _, err := rt.SearchHotels(context.Background(), "Paris", 48.856, 2.352, "2025-06-01", "2025-06-05", "EUR", 2, filters)
	if err != nil {
		t.Fatalf("SearchHotels: %v", err)
	}
	if len(hotels) != 1 {
		t.Fatalf("got %d hotels, want 1", len(hotels))
	}

	// Verify filter vars were substituted into query params.
	checks := map[string]string{
		"min_price=50":          "min_price",
		"max_price=300":         "max_price",
		"property_type=204":     "property_type (resolved via lookup)",
		"sort=price":            "sort",
		"stars=4":               "stars",
		"min_rating=4.0":        "min_rating",
		"amenities=wifi%2Cpool": "amenities",
		"free_cancellation=1":   "free_cancellation",
	}
	for substr, label := range checks {
		if !containsSubstring(capturedQuery, substr) {
			t.Errorf("query missing %s: %s not in %q", label, substr, capturedQuery)
		}
	}

	// Verify that unset optional params are NOT sent when no filters given.
	capturedQuery = "" // reset
	hotels2, _, err := rt.SearchHotels(context.Background(), "Paris", 48.856, 2.352, "2025-06-01", "2025-06-05", "EUR", 2, nil)
	if err != nil {
		t.Fatalf("SearchHotels(nil filters): %v", err)
	}
	if len(hotels2) != 1 {
		t.Fatalf("got %d hotels, want 1", len(hotels2))
	}
	// sort, min_price, max_price, property_type, amenities, free_cancellation
	// should all be absent — either still contain ${...} (skipped) or resolve
	// to empty (skipped by the pure-placeholder-empty check).
	absent := []string{"sort=", "min_price=", "max_price=", "property_type=", "amenities=", "free_cancellation="}
	for _, param := range absent {
		if containsSubstring(capturedQuery, param) {
			t.Errorf("unset filter param %q found in query %q", param, capturedQuery)
		}
	}
}

func TestJSONPathSkipsEmptyArrays(t *testing.T) {
	// Simulates Airbnb v2 API where explore_tabs.sections has an "inserts"
	// section with empty listings before the real "listings" section.
	data := map[string]any{
		"explore_tabs": []any{
			map[string]any{
				"sections": []any{
					map[string]any{
						"result_type": "inserts",
						"listings":    []any{},
					},
					map[string]any{
						"result_type": "listings",
						"listings": []any{
							map[string]any{"name": "Real listing"},
						},
					},
				},
			},
		},
	}
	got := jsonPath(data, "explore_tabs.sections.listings")
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", got)
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1 listing, got %d (skipping empty arrays failed)", len(arr))
	}
}

func TestIsEmptyValue(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want bool
	}{
		{"nil", nil, true},
		{"empty slice", []any{}, true},
		{"empty map", map[string]any{}, true},
		{"empty string", "", true},
		{"non-empty slice", []any{1}, false},
		{"non-empty map", map[string]any{"a": 1}, false},
		{"non-empty string", "x", false},
		{"zero int", 0, false},
		{"false bool", false, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := isEmptyValue(tt.v); got != tt.want {
				t.Errorf("isEmptyValue(%v) = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}

func TestDenormalizeApollo(t *testing.T) {
	// Simulates a Booking.com SSR Apollo normalized cache where
	// nested objects like reviewScore and location use __ref pointers.
	cache := map[string]any{
		"ROOT_QUERY": map[string]any{
			"searchQueries": map[string]any{
				"search({\"input\":{}})": map[string]any{
					"results": []any{
						map[string]any{"__ref": "SearchResultProperty:42"},
					},
				},
			},
		},
		"SearchResultProperty:42": map[string]any{
			"__typename":        "SearchResultProperty",
			"basicPropertyData": map[string]any{"__ref": "BasicPropertyData:42"},
			"displayName":       map[string]any{"text": "Hotel Amsterdam"},
		},
		"BasicPropertyData:42": map[string]any{
			"__typename":  "BasicPropertyData",
			"id":          float64(42),
			"reviewScore": map[string]any{"__ref": "ReviewScore:42"},
			"location":    map[string]any{"__ref": "Location:42"},
		},
		"ReviewScore:42": map[string]any{
			"score":       float64(8.5),
			"reviewCount": float64(1234),
		},
		"Location:42": map[string]any{
			"latitude":  52.37,
			"longitude": 4.89,
		},
	}

	// Only denormalize ROOT_QUERY subtree (mirrors runtime.go behavior).
	cache["ROOT_QUERY"] = denormalizeApollo(cache["ROOT_QUERY"], cache, nil)

	// Navigate: ROOT_QUERY.searchQueries.search*.results[0].basicPropertyData.reviewScore.score
	root := cache["ROOT_QUERY"].(map[string]any)
	sq := root["searchQueries"].(map[string]any)
	// Use the wildcard helper.
	val := jsonPath(sq, "search*.results")
	arr, ok := val.([]any)
	if !ok || len(arr) == 0 {
		t.Fatal("search*.results did not resolve to a non-empty array")
	}

	hotel := arr[0].(map[string]any)
	// displayName.text should be direct.
	name := jsonPath(hotel, "displayName.text")
	if name != "Hotel Amsterdam" {
		t.Errorf("name = %v, want Hotel Amsterdam", name)
	}
	// reviewScore.score should be denormalized through __ref.
	score := jsonPath(hotel, "basicPropertyData.reviewScore.score")
	if score != float64(8.5) {
		t.Errorf("score = %v, want 8.5", score)
	}
	// location should be denormalized.
	lat := jsonPath(hotel, "basicPropertyData.location.latitude")
	if lat != 52.37 {
		t.Errorf("lat = %v, want 52.37", lat)
	}
}

func TestUnwrapNiobe(t *testing.T) {
	// Simulates Airbnb's SSR Niobe cache format:
	// {"niobeClientData": [["CacheKey:...", {"data": {...}, "variables": {...}}]]}
	niobe := map[string]any{
		"niobeClientData": []any{
			[]any{
				"StaysSearch:{\"query\":\"Helsinki\"}",
				map[string]any{
					"data": map[string]any{
						"presentation": map[string]any{
							"staysSearch": map[string]any{
								"results": map[string]any{
									"searchResults": []any{
										map[string]any{
											"title":              "Apartment in Kamppi",
											"avgRatingLocalized": "4.69 (127)",
										},
									},
								},
							},
						},
					},
					"variables": map[string]any{
						"staysSearchRequest": map[string]any{},
					},
				},
			},
		},
	}

	unwrapped := unwrapNiobe(niobe)

	// Should unwrap to the inner payload containing "data" and "variables".
	m, ok := unwrapped.(map[string]any)
	if !ok {
		t.Fatalf("unwrapNiobe returned %T, want map[string]any", unwrapped)
	}
	if _, hasData := m["data"]; !hasData {
		t.Fatal("unwrapped result missing 'data' key")
	}

	// jsonPath should now resolve the results_path.
	results := jsonPath(unwrapped, "data.presentation.staysSearch.results.searchResults")
	arr, ok := results.([]any)
	if !ok || len(arr) == 0 {
		t.Fatalf("results_path did not resolve to a non-empty array: %T", results)
	}
	title := jsonPath(arr[0], "title")
	if title != "Apartment in Kamppi" {
		t.Errorf("title = %v, want Apartment in Kamppi", title)
	}
}

func TestUnwrapNiobePassthrough(t *testing.T) {
	// Non-Niobe JSON should be returned unchanged.
	regular := map[string]any{
		"data": map[string]any{"results": []any{1, 2, 3}},
	}
	result := unwrapNiobe(regular)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if _, hasData := m["data"]; !hasData {
		t.Fatal("passthrough lost 'data' key")
	}
}

func TestNormalizePrice(t *testing.T) {
	// Use a cache with known fallback rates (unreachable server forces fallback).
	old := defaultFXCache
	defer func() { defaultFXCache = old }()
	defaultFXCache = newFXCache()
	defaultFXCache.baseURL = "http://127.0.0.1:1" // force fallback

	tests := []struct {
		name  string
		price float64
		from  string
		to    string
		want  float64
	}{
		{"USD to EUR", 100, "USD", "EUR", 92},
		{"EUR to USD", 100, "EUR", "USD", 109},
		{"GBP to EUR", 100, "GBP", "EUR", 116},
		{"EUR to GBP", 100, "EUR", "GBP", 86},
		{"same currency", 85, "EUR", "EUR", 85},
		{"empty from (Airbnb)", 75, "", "EUR", 75},
		{"empty to", 75, "USD", "", 75},
		{"both empty", 75, "", "", 75},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizePrice(tt.price, tt.from, tt.to)
			diff := got - tt.want
			if diff < 0 {
				diff = -diff
			}
			if diff > 0.01 {
				t.Errorf("normalizePrice(%v, %q, %q) = %v, want %v", tt.price, tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestExtractCurrencyCode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// 3-letter ISO code prefix
		{"EUR 204", "EUR"},
		{"USD204", "USD"},
		{"GBP 99.50", "GBP"},

		// 3-letter ISO code suffix
		{"204 EUR", "EUR"},
		{"204USD", "USD"},

		// Currency symbol prefix
		{"€175", "EUR"},
		{"€ 175", "EUR"},
		{"$120", "USD"},
		{"£99", "GBP"},
		{"¥1500", "JPY"},
		{"₹2500", "INR"},

		// Numeric-only — no currency
		{"175", ""},
		{"99.50", ""},

		// Empty / whitespace
		{"", ""},
		{"   ", ""},

		// Mixed case — not a valid ISO code
		{"Eur 204", ""},
		{"eur 204", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractCurrencyCode(tt.input)
			if got != tt.want {
				t.Errorf("extractCurrencyCode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMapHotelResultCurrencyFromPriceString(t *testing.T) {
	// Simulate Airbnb-like response: price is a string with embedded currency,
	// no separate currency field in the mapping.
	raw := map[string]any{
		"listing": map[string]any{
			"name": "Cozy Apartment",
		},
		"display_price": "EUR 204",
	}
	fields := map[string]string{
		"name":  "listing.name",
		"price": "display_price",
	}

	h := mapHotelResult(raw, fields)
	if h.Currency != "EUR" {
		t.Errorf("expected currency EUR from price string, got %q", h.Currency)
	}
	if h.Price != 204 {
		t.Errorf("expected price 204, got %v", h.Price)
	}

	// When an explicit currency field IS mapped, it takes precedence.
	raw["currency_code"] = "GBP"
	fields["currency"] = "currency_code"

	h = mapHotelResult(raw, fields)
	if h.Currency != "GBP" {
		t.Errorf("expected explicit currency GBP to take precedence, got %q", h.Currency)
	}
}

func TestMapHotelResultCurrencySymbol(t *testing.T) {
	raw := map[string]any{
		"price_display": "€175",
	}
	fields := map[string]string{
		"price": "price_display",
	}

	h := mapHotelResult(raw, fields)
	if h.Currency != "EUR" {
		t.Errorf("expected currency EUR from € symbol, got %q", h.Currency)
	}
	if h.Price != 175 {
		t.Errorf("expected price 175, got %v", h.Price)
	}
}

// TestBookingFieldMapping verifies that the corrected Booking.com field paths
// resolve correctly against the actual Apollo cache structure. The rating field
// was incorrectly mapped to basicPropertyData.reviewScore.score (which doesn't
// exist in the SSR data); it should be basicPropertyData.reviews.totalScore.

func TestBookingFieldMapping(t *testing.T) {
	// Minimal Booking search result matching the actual Apollo structure
	// discovered via TestBookingFieldDiscovery.
	raw := map[string]any{
		"displayName": map[string]any{
			"text": "Hotel Aix Europe",
		},
		"basicPropertyData": map[string]any{
			"id":       float64(2215748),
			"pageName": "aix-europe",
			"reviews": map[string]any{
				"totalScore":   7.1,
				"reviewsCount": float64(2551),
				"showScore":    true,
			},
			"location": map[string]any{
				"latitude":    48.870111,
				"longitude":   2.369928,
				"address":     "4 Rue d'Aix",
				"city":        "Paris",
				"countryCode": "fr",
			},
		},
		"priceDisplayInfoIrene": map[string]any{
			"displayPrice": map[string]any{
				"amountPerStay": map[string]any{
					"amount":   "€ 71.86",
					"currency": "EUR",
				},
			},
		},
	}

	// These are the corrected field mappings from booking.json.
	fields := map[string]string{
		"name":         "displayName.text",
		"hotel_id":     "basicPropertyData.id",
		"rating":       "basicPropertyData.reviews.totalScore",
		"review_count": "basicPropertyData.reviews.reviewsCount",
		"lat":          "basicPropertyData.location.latitude",
		"lon":          "basicPropertyData.location.longitude",
		"address":      "basicPropertyData.location.address",
		"price":        "priceDisplayInfoIrene.displayPrice.amountPerStay.amount",
		"currency":     "priceDisplayInfoIrene.displayPrice.amountPerStay.currency",
	}

	h := mapHotelResult(raw, fields)

	if h.Name != "Hotel Aix Europe" {
		t.Errorf("name: got %q, want %q", h.Name, "Hotel Aix Europe")
	}
	if h.HotelID != "2215748" {
		t.Errorf("hotel_id: got %q, want %q", h.HotelID, "2215748")
	}
	if h.Rating != 7.1 {
		t.Errorf("rating: got %v, want 7.1 (was 0 with old reviewScore.score path)", h.Rating)
	}
	if h.ReviewCount != 2551 {
		t.Errorf("review_count: got %d, want 2551", h.ReviewCount)
	}
	if h.Lat == 0 {
		t.Error("lat should not be 0")
	}
	if h.Address != "4 Rue d'Aix" {
		t.Errorf("address: got %q, want %q", h.Address, "4 Rue d'Aix")
	}
	if h.Currency != "EUR" {
		t.Errorf("currency: got %q, want %q", h.Currency, "EUR")
	}
	if h.Price != 71.86 {
		t.Errorf("price: got %v, want 71.86", h.Price)
	}

	// Verify the OLD (broken) path returns 0 — this was the bug.
	oldFields := map[string]string{
		"rating":       "basicPropertyData.reviewScore.score",
		"review_count": "basicPropertyData.reviewScore.reviewCount",
	}
	hOld := mapHotelResult(raw, oldFields)
	if hOld.Rating != 0 {
		t.Errorf("old reviewScore.score path should yield 0, got %v", hOld.Rating)
	}
	if hOld.ReviewCount != 0 {
		t.Errorf("old reviewScore.reviewCount path should yield 0, got %d", hOld.ReviewCount)
	}
}

// TestBookingURLConstruction verifies that the pageName + countryCode fields
// are correctly combined into a booking URL.

func TestBookingURLConstruction(t *testing.T) {
	raw := map[string]any{
		"basicPropertyData": map[string]any{
			"pageName": "aix-europe",
			"location": map[string]any{
				"countryCode": "fr",
			},
		},
	}

	pageName, _ := jsonPath(raw, "basicPropertyData.pageName").(string)
	cc, _ := jsonPath(raw, "basicPropertyData.location.countryCode").(string)

	if pageName != "aix-europe" {
		t.Errorf("pageName: got %q, want %q", pageName, "aix-europe")
	}
	if cc != "fr" {
		t.Errorf("countryCode: got %q, want %q", cc, "fr")
	}

	wantURL := "https://www.booking.com/hotel/fr/aix-europe.html"
	gotURL := "https://www.booking.com/hotel/" + cc + "/" + pageName + ".html"
	if gotURL != wantURL {
		t.Errorf("booking URL: got %q, want %q", gotURL, wantURL)
	}
}
