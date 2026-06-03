package models

import (
	"testing"
)

func TestMergeHotelResults_NoDuplicates(t *testing.T) {
	a := []HotelResult{{Name: "Hotel A", Price: 100, Currency: "EUR", Sources: []PriceSource{{Provider: "google_hotels", Price: 100, Currency: "EUR"}}}}
	b := []HotelResult{{Name: "Hotel B", Price: 200, Currency: "EUR", Sources: []PriceSource{{Provider: "trivago", Price: 200, Currency: "EUR"}}}}

	result := MergeHotelResults(a, b)
	if len(result) != 2 {
		t.Fatalf("expected 2 hotels, got %d", len(result))
	}
}

func TestMergeHotelResults_MergesSameHotel(t *testing.T) {
	a := []HotelResult{{Name: "Hilton Barcelona", Price: 150, Currency: "EUR", Sources: []PriceSource{{Provider: "google_hotels", Price: 150, Currency: "EUR"}}}}
	b := []HotelResult{{Name: "hilton barcelona", Price: 128, Currency: "EUR", Sources: []PriceSource{{Provider: "trivago", Price: 128, Currency: "EUR"}}}}

	result := MergeHotelResults(a, b)
	if len(result) != 1 {
		t.Fatalf("expected 1 merged hotel, got %d", len(result))
	}
	if result[0].Price != 128 {
		t.Errorf("expected lowest price 128, got %.0f", result[0].Price)
	}
	if len(result[0].Sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(result[0].Sources))
	}
}

func TestMergeHotelResults_KeepsLowestPrice(t *testing.T) {
	a := []HotelResult{{Name: "Test Hotel", Price: 200, Currency: "EUR", Address: "10 Rue Example", Sources: []PriceSource{{Provider: "google_hotels", Price: 200, Currency: "EUR"}}}}
	b := []HotelResult{{Name: "Test Hotel", Price: 180, Currency: "EUR", Address: "10 Rue Example", Sources: []PriceSource{{Provider: "trivago", Price: 180, Currency: "EUR"}}}}
	c := []HotelResult{{Name: "Test Hotel", Price: 195, Currency: "EUR", Address: "10 Rue Example", Sources: []PriceSource{{Provider: "airbnb", Price: 195, Currency: "EUR"}}}}

	result := MergeHotelResults(a, b, c)
	if len(result) != 1 {
		t.Fatalf("expected 1 hotel, got %d", len(result))
	}
	if result[0].Price != 180 {
		t.Errorf("expected lowest price 180, got %.0f", result[0].Price)
	}
	if len(result[0].Sources) != 3 {
		t.Errorf("expected 3 sources, got %d", len(result[0].Sources))
	}
}

func TestMergeHotelResults_GeoDisambiguation(t *testing.T) {
	// Same name but different cities (>200m apart).
	a := []HotelResult{{Name: "Hilton", Price: 150, Currency: "EUR", Lat: 41.3851, Lon: 2.1734, Address: "Barcelona", Sources: []PriceSource{{Provider: "google_hotels", Price: 150, Currency: "EUR"}}}}
	b := []HotelResult{{Name: "Hilton", Price: 200, Currency: "EUR", Lat: 48.8566, Lon: 2.3522, Address: "Paris", Sources: []PriceSource{{Provider: "trivago", Price: 200, Currency: "EUR"}}}}

	result := MergeHotelResults(a, b)
	if len(result) != 2 {
		t.Fatalf("expected 2 hotels (different cities), got %d", len(result))
	}
}

func TestMergeHotelResults_GeoProximityMerges(t *testing.T) {
	// Same hotel, slightly different coordinates (within 500m).
	a := []HotelResult{{Name: "Hilton Barcelona", Price: 150, Currency: "EUR", Lat: 41.3851, Lon: 2.1734, Sources: []PriceSource{{Provider: "google_hotels", Price: 150, Currency: "EUR"}}}}
	b := []HotelResult{{Name: "hilton barcelona", Price: 140, Currency: "EUR", Lat: 41.3852, Lon: 2.1735, Sources: []PriceSource{{Provider: "trivago", Price: 140, Currency: "EUR"}}}}

	result := MergeHotelResults(a, b)
	if len(result) != 1 {
		t.Fatalf("expected 1 merged hotel (same location), got %d", len(result))
	}
}

func TestMergeHotelResults_AddressMatchOverridesGeoDrift(t *testing.T) {
	a := []HotelResult{{
		Name:     "Grand Hotel",
		Price:    150,
		Currency: "EUR",
		Lat:      60.1699,
		Lon:      24.9384,
		Address:  "Example Street 1",
		Sources:  []PriceSource{{Provider: "google_hotels", Price: 150, Currency: "EUR"}},
	}}
	b := []HotelResult{{
		Name:     "grand hotel",
		Price:    120,
		Currency: "EUR",
		Lat:      60.1760,
		Lon:      24.9384,
		Address:  "Example Street 1",
		Sources:  []PriceSource{{Provider: "booking", Price: 120, Currency: "EUR"}},
	}}

	result := MergeHotelResults(a, b)
	if len(result) != 1 {
		t.Fatalf("expected 1 merged hotel, got %d", len(result))
	}
	if result[0].Price != 120 {
		t.Fatalf("expected merged price 120, got %.0f", result[0].Price)
	}
}

func TestMergeHotelResults_PreservesHotelIDFromLaterSource(t *testing.T) {
	a := []HotelResult{{
		Name:       "Grand Hotel",
		Price:      120,
		Currency:   "EUR",
		Address:    "Mannerheimintie 10, Helsinki",
		BookingURL: "https://www.booking.com/hotel/fi/grand.html",
		Sources:    []PriceSource{{Provider: "booking", Price: 120, Currency: "EUR"}},
	}}
	b := []HotelResult{{
		Name:       "Grand Hotel",
		HotelID:    "/g/123",
		Price:      130,
		Currency:   "EUR",
		Address:    "Mannerheimintie 10, Helsinki",
		BookingURL: "https://www.google.com/travel/hotels/example",
		Sources:    []PriceSource{{Provider: "google_hotels", Price: 130, Currency: "EUR"}},
	}}

	result := MergeHotelResults(a, b)
	if len(result) != 1 {
		t.Fatalf("expected 1 merged hotel, got %d", len(result))
	}
	if result[0].HotelID != "/g/123" {
		t.Fatalf("hotel_id = %q, want /g/123", result[0].HotelID)
	}
}

func TestMergeHotelResults_DifferentAddressesDoNotMergeWithoutCoordinates(t *testing.T) {
	a := []HotelResult{{Name: "Hilton", Price: 150, Currency: "EUR", Address: "Barcelona", Sources: []PriceSource{{Provider: "google_hotels", Price: 150, Currency: "EUR"}}}}
	b := []HotelResult{{Name: "Hilton", Price: 200, Currency: "EUR", Address: "Paris", Sources: []PriceSource{{Provider: "booking", Price: 200, Currency: "EUR"}}}}

	result := MergeHotelResults(a, b)
	if len(result) != 2 {
		t.Fatalf("expected 2 hotels, got %d", len(result))
	}
}

func TestMergeHotelResults_EmptyInputs(t *testing.T) {
	result := MergeHotelResults(nil, nil)
	if len(result) != 0 {
		t.Fatalf("expected 0 hotels, got %d", len(result))
	}
}

func TestMergeHotelResults_SingleSource(t *testing.T) {
	a := []HotelResult{
		{Name: "Hotel A", Price: 100, Currency: "EUR"},
		{Name: "Hotel B", Price: 200, Currency: "EUR"},
	}
	result := MergeHotelResults(a)
	if len(result) != 2 {
		t.Fatalf("expected 2 hotels, got %d", len(result))
	}
}

// TestMergeHotelResults_GeoProximityDedup verifies that hotels with different
// name variants but the same physical location are merged across providers.
// This is the core cross-provider dedup scenario: Google Hotels calls it
// "Holiday Inn Express Amsterdam - Arena Towers" and Booking calls it
// "Holiday Inn Express Amsterdam Arena Towers by IHG" — different names
// but within 100m of each other.
func TestMergeHotelResults_GeoProximityDedup(t *testing.T) {
	google := []HotelResult{
		{
			Name: "Holiday Inn Express Amsterdam - Arena Towers", Price: 120, Currency: "EUR",
			Rating: 8.6, ReviewCount: 5000, Lat: 52.3096, Lon: 4.9418,
			Sources: []PriceSource{{Provider: "google_hotels", Price: 120, Currency: "EUR"}},
		},
	}
	booking := []HotelResult{
		{
			Name: "Holiday Inn Express Amsterdam Arena Towers by IHG", Price: 110, Currency: "EUR",
			Rating: 0, ReviewCount: 0, Lat: 52.3096, Lon: 4.9419, // ~10m away
			Sources: []PriceSource{{Provider: "booking", Price: 110, Currency: "EUR"}},
		},
	}
	result := MergeHotelResults(google, booking)
	if len(result) != 1 {
		t.Fatalf("expected 1 merged hotel, got %d", len(result))
	}
	h := result[0]
	// Should have Google's rating + Booking's lower price.
	if h.Rating != 8.6 {
		t.Errorf("rating = %v, want 8.6 (from Google)", h.Rating)
	}
	if h.Price != 110 {
		t.Errorf("price = %v, want 110 (cheapest)", h.Price)
	}
	if len(h.Sources) != 2 {
		t.Errorf("sources = %d, want 2 (both providers)", len(h.Sources))
	}
}

func TestMergeHotelResults_MergesMissingFields(t *testing.T) {
	a := []HotelResult{{Name: "Hotel X", Price: 100, Currency: "EUR", Rating: 9.0, Stars: 4, Lat: 48.8566, Lon: 2.3522}}
	b := []HotelResult{{Name: "hotel x", Price: 90, Currency: "EUR", Address: "123 Main St", ReviewCount: 500, Lat: 48.8566, Lon: 2.3522}}

	result := MergeHotelResults(a, b)
	if len(result) != 1 {
		t.Fatalf("expected 1 hotel, got %d", len(result))
	}
	h := result[0]
	if h.Rating != 9.0 {
		t.Errorf("expected rating 9.0, got %f", h.Rating)
	}
	if h.Stars != 4 {
		t.Errorf("expected stars 4, got %d", h.Stars)
	}
	if h.Address != "123 Main St" {
		t.Errorf("expected address from second source, got %q", h.Address)
	}
	if h.ReviewCount != 500 {
		t.Errorf("expected review count 500, got %d", h.ReviewCount)
	}
}

func TestNormalizeName(t *testing.T) {
	tests := []struct{ input, want string }{
		{"  Hilton  Barcelona  ", "hilton barcelona"},
		{"HOTEL ABC", "hotel abc"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeName(tt.input)
		if got != tt.want {
			t.Errorf("normalizeName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeAddress(t *testing.T) {
	tests := []struct{ input, want string }{
		{" Example Street 1 ", "example street 1"},
		{"Rue-de-Paris, 5", "rue de paris 5"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeAddress(tt.input)
		if got != tt.want {
			t.Errorf("normalizeAddress(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestHasExternalProviderSource(t *testing.T) {
	tests := []struct {
		name  string
		hotel HotelResult
		want  bool
	}{
		{
			name:  "google only",
			hotel: HotelResult{Sources: []PriceSource{{Provider: "google_hotels"}}},
			want:  false,
		},
		{
			name:  "trivago only",
			hotel: HotelResult{Sources: []PriceSource{{Provider: "trivago"}}},
			want:  false,
		},
		{
			name:  "hostelworld",
			hotel: HotelResult{Sources: []PriceSource{{Provider: "hostelworld"}}},
			want:  true,
		},
		{
			name:  "airbnb",
			hotel: HotelResult{Sources: []PriceSource{{Provider: "airbnb"}}},
			want:  true,
		},
		{
			name: "mixed google and booking",
			hotel: HotelResult{Sources: []PriceSource{
				{Provider: "google_hotels"},
				{Provider: "booking"},
			}},
			want: true,
		},
		{
			name:  "no sources",
			hotel: HotelResult{},
			want:  false,
		},
		{
			name:  "empty provider string",
			hotel: HotelResult{Sources: []PriceSource{{Provider: ""}}},
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasExternalProviderSource(tt.hotel); got != tt.want {
				t.Errorf("HasExternalProviderSource() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMergeHotelResults_DeduplicatesSources(t *testing.T) {
	// Simulate Google Hotels returning the same hotel from multiple sort
	// orders / pagination pages — all with identical provider+price+currency.
	pages := make([][]HotelResult, 9)
	for i := range pages {
		pages[i] = []HotelResult{{
			Name:     "Clarion Hotel Helsinki",
			Price:    105,
			Currency: "EUR",
			Sources:  []PriceSource{{Provider: "google_hotels", Price: 105, Currency: "EUR"}},
		}}
	}

	result := MergeHotelResults(pages...)
	if len(result) != 1 {
		t.Fatalf("expected 1 hotel, got %d", len(result))
	}
	if len(result[0].Sources) != 1 {
		t.Errorf("expected 1 deduplicated source, got %d", len(result[0].Sources))
	}
}

func TestMergeHotelResults_KeepsDifferentPricesSameProvider(t *testing.T) {
	// Same provider but different prices should be kept (e.g. price changed
	// between pages, or different room types).
	a := []HotelResult{{
		Name:     "Clarion Hotel Helsinki",
		Price:    105,
		Currency: "EUR",
		Sources:  []PriceSource{{Provider: "google_hotels", Price: 105, Currency: "EUR"}},
	}}
	b := []HotelResult{{
		Name:     "Clarion Hotel Helsinki",
		Price:    115,
		Currency: "EUR",
		Sources:  []PriceSource{{Provider: "google_hotels", Price: 115, Currency: "EUR"}},
	}}

	result := MergeHotelResults(a, b)
	if len(result) != 1 {
		t.Fatalf("expected 1 hotel, got %d", len(result))
	}
	if len(result[0].Sources) != 2 {
		t.Errorf("expected 2 sources (different prices), got %d", len(result[0].Sources))
	}
	if result[0].Price != 105 {
		t.Errorf("expected lowest price 105, got %.0f", result[0].Price)
	}
}

func TestDeduplicateSources(t *testing.T) {
	sources := []PriceSource{
		{Provider: "google_hotels", Price: 105, Currency: "EUR"},
		{Provider: "google_hotels", Price: 105, Currency: "EUR"},
		{Provider: "google_hotels", Price: 105, Currency: "EUR"},
		{Provider: "booking", Price: 110, Currency: "EUR"},
		{Provider: "booking", Price: 110, Currency: "EUR"},
		{Provider: "google_hotels", Price: 120, Currency: "EUR"},
	}
	got := deduplicateSources(sources)
	if len(got) != 3 {
		t.Errorf("expected 3 unique sources, got %d", len(got))
	}
}

func TestComputeSavings_MultipleSourcesDifferentPrices(t *testing.T) {
	hotels := []HotelResult{{
		Name: "Test Hotel",
		Sources: []PriceSource{
			{Provider: "google_hotels", Price: 120, Currency: "EUR"},
			{Provider: "booking", Price: 95, Currency: "EUR"},
			{Provider: "airbnb", Price: 150, Currency: "EUR"},
		},
	}}
	ComputeSavings(hotels)
	if hotels[0].Savings != 55 {
		t.Errorf("Savings = %v, want 55", hotels[0].Savings)
	}
	if hotels[0].CheapestSource != "booking" {
		t.Errorf("CheapestSource = %q, want booking", hotels[0].CheapestSource)
	}
}

func TestComputeSavings_SingleSource(t *testing.T) {
	hotels := []HotelResult{{
		Name: "Test Hotel",
		Sources: []PriceSource{
			{Provider: "google_hotels", Price: 120, Currency: "EUR"},
		},
	}}
	ComputeSavings(hotels)
	if hotels[0].Savings != 0 {
		t.Errorf("Savings = %v, want 0 for single source", hotels[0].Savings)
	}
}

func TestComputeSavings_SamePrice(t *testing.T) {
	hotels := []HotelResult{{
		Name: "Test Hotel",
		Sources: []PriceSource{
			{Provider: "google_hotels", Price: 100, Currency: "EUR"},
			{Provider: "booking", Price: 100, Currency: "EUR"},
		},
	}}
	ComputeSavings(hotels)
	if hotels[0].Savings != 0 {
		t.Errorf("Savings = %v, want 0 for same price", hotels[0].Savings)
	}
}

func TestComputeSavings_ZeroPriceIgnored(t *testing.T) {
	hotels := []HotelResult{{
		Name: "Test Hotel",
		Sources: []PriceSource{
			{Provider: "google_hotels", Price: 0, Currency: "EUR"},
			{Provider: "booking", Price: 95, Currency: "EUR"},
			{Provider: "airbnb", Price: 120, Currency: "EUR"},
		},
	}}
	ComputeSavings(hotels)
	if hotels[0].Savings != 25 {
		t.Errorf("Savings = %v, want 25", hotels[0].Savings)
	}
	if hotels[0].CheapestSource != "booking" {
		t.Errorf("CheapestSource = %q, want booking", hotels[0].CheapestSource)
	}
}

func TestComputeSavings_EmptySources(t *testing.T) {
	hotels := []HotelResult{{Name: "Test Hotel"}}
	ComputeSavings(hotels)
	if hotels[0].Savings != 0 {
		t.Errorf("Savings = %v, want 0 for no sources", hotels[0].Savings)
	}
}

func TestComputeSavings_CalledByMerge(t *testing.T) {
	// Verify that MergeHotelResults populates savings.
	batch1 := []HotelResult{{
		Name: "Hotel Foo", Price: 120, Currency: "EUR", Address: "42 Rue de Rivoli",
		Sources: []PriceSource{{Provider: "google_hotels", Price: 120, Currency: "EUR"}},
	}}
	batch2 := []HotelResult{{
		Name: "Hotel Foo", Price: 95, Currency: "EUR", Address: "42 Rue de Rivoli",
		Sources: []PriceSource{{Provider: "booking", Price: 95, Currency: "EUR"}},
	}}
	merged := MergeHotelResults(batch1, batch2)
	if len(merged) != 1 {
		t.Fatalf("expected 1 merged hotel, got %d", len(merged))
	}
	if merged[0].Savings != 25 {
		t.Errorf("Savings = %v, want 25 after merge", merged[0].Savings)
	}
	if merged[0].CheapestSource != "booking" {
		t.Errorf("CheapestSource = %q, want booking", merged[0].CheapestSource)
	}
}

func TestMergeHotelResults_NearbyDifferentHotelsDoNotMerge(t *testing.T) {
	// Two different hotels 80m apart in central Paris. The old logic would
	// merge these via geo-proximity (150m threshold, no name check).
	a := []HotelResult{{
		Name: "Hotel de Ville", Price: 150, Currency: "EUR",
		Lat: 48.8566, Lon: 2.3522, Address: "1 Place de l'Hotel de Ville",
		Sources: []PriceSource{{Provider: "google_hotels", Price: 150, Currency: "EUR"}},
	}}
	b := []HotelResult{{
		Name: "Le Marais Boutique", Price: 180, Currency: "EUR",
		Lat: 48.8572, Lon: 2.3530, Address: "15 Rue des Archives",
		Sources: []PriceSource{{Provider: "booking", Price: 180, Currency: "EUR"}},
	}}

	result := MergeHotelResults(a, b)
	if len(result) != 2 {
		t.Fatalf("expected 2 distinct hotels, got %d — nearby different hotels should not merge", len(result))
	}
}

func TestMergeHotelResults_SimilarNamesNearbyDoMerge(t *testing.T) {
	// Same hotel, slightly different name across providers, 30m apart.
	a := []HotelResult{{
		Name: "Holiday Inn Express Paris Canal de la Villette", Price: 120, Currency: "EUR",
		Lat: 48.8800, Lon: 2.3700,
		Sources: []PriceSource{{Provider: "google_hotels", Price: 120, Currency: "EUR"}},
	}}
	b := []HotelResult{{
		Name: "Holiday Inn Express Paris Canal de la Villette by IHG", Price: 110, Currency: "EUR",
		Lat: 48.8802, Lon: 2.3702, // ~30m away
		Sources: []PriceSource{{Provider: "booking", Price: 110, Currency: "EUR"}},
	}}

	result := MergeHotelResults(a, b)
	if len(result) != 1 {
		t.Fatalf("expected 1 merged hotel, got %d — same hotel with brand suffix should merge", len(result))
	}
}

func TestMergeHotelResults_ShortNameNoLocationDoesNotMerge(t *testing.T) {
	// Short generic names without location data should NOT auto-merge.
	a := []HotelResult{{
		Name: "Hilton", Price: 200, Currency: "EUR",
		Sources: []PriceSource{{Provider: "google_hotels", Price: 200, Currency: "EUR"}},
	}}
	b := []HotelResult{{
		Name: "Hilton", Price: 250, Currency: "EUR",
		Sources: []PriceSource{{Provider: "booking", Price: 250, Currency: "EUR"}},
	}}

	result := MergeHotelResults(a, b)
	if len(result) != 2 {
		t.Fatalf("expected 2 hotels (ambiguous short name, no location), got %d", len(result))
	}
}

func TestMergeHotelResults_LongNameNoLocationDoesMerge(t *testing.T) {
	// A sufficiently specific name (>=15 chars) should merge even without
	// address/coordinates, since the name itself disambiguates.
	a := []HotelResult{{
		Name: "Hilton Paris Opera", Price: 200, Currency: "EUR",
		Sources: []PriceSource{{Provider: "google_hotels", Price: 200, Currency: "EUR"}},
	}}
	b := []HotelResult{{
		Name: "Hilton Paris Opera", Price: 180, Currency: "EUR",
		Sources: []PriceSource{{Provider: "booking", Price: 180, Currency: "EUR"}},
	}}

	result := MergeHotelResults(a, b)
	if len(result) != 1 {
		t.Fatalf("expected 1 merged hotel (specific long name), got %d", len(result))
	}
}

func TestMergeHotelResults_DifferentAddressesNeverMerge(t *testing.T) {
	// Same normalized name, different addresses, close coordinates.
	// The old code would merge via geo fallback; now different addresses = different hotel.
	a := []HotelResult{{
		Name: "Hotel Paris", Price: 120, Currency: "EUR",
		Lat: 48.8566, Lon: 2.3522, Address: "5 Rue de Rivoli",
		Sources: []PriceSource{{Provider: "google_hotels", Price: 120, Currency: "EUR"}},
	}}
	b := []HotelResult{{
		Name: "Hotel Paris", Price: 140, Currency: "EUR",
		Lat: 48.8570, Lon: 2.3525, Address: "12 Rue Saint-Antoine",
		Sources: []PriceSource{{Provider: "booking", Price: 140, Currency: "EUR"}},
	}}

	result := MergeHotelResults(a, b)
	if len(result) != 2 {
		t.Fatalf("expected 2 hotels (same name, different addresses), got %d", len(result))
	}
}

func TestNameSimilar(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// Same hotel, brand suffix stripped by normalizeName
		{"holiday inn express amsterdam arena towers", "holiday inn express amsterdam arena towers", true},
		// High overlap
		{"hotel & residence paris bastille", "hotel and residence paris bastille", true},
		// Completely different hotels
		{"hotel de ville", "le marais boutique", false},
		// Short names — must match exactly
		{"hilton", "hilton", true},
		{"hilton", "marriott", false},
		// One-word names
		{"hilton", "hyatt", false},
		// Partial overlap below threshold
		{"hotel paris opera", "hotel lyon bellecour", false},
	}
	for _, tt := range tests {
		a := normalizeName(tt.a)
		b := normalizeName(tt.b)
		got := NameSimilar(a, b)
		if got != tt.want {
			t.Errorf("NameSimilar(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestHaversineMeters(t *testing.T) {
	// Helsinki to Tallinn ≈ 80km.
	dist := haversineMeters(60.1699, 24.9384, 59.4370, 24.7536)
	if dist < 70000 || dist > 90000 {
		t.Errorf("Helsinki-Tallinn expected ~80km, got %.0fm", dist)
	}

	// Same point = 0.
	dist = haversineMeters(60.17, 24.94, 60.17, 24.94)
	if dist != 0 {
		t.Errorf("same point expected 0m, got %.0fm", dist)
	}
}
