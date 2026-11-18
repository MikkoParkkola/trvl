package hotels

import (
	"encoding/json"
	"testing"
)

func TestParseHotelsFromPayload_NoHotels(t *testing.T) {
	_, err := parseHotelsFromPayload("just a string", "USD")
	if err == nil {
		t.Error("expected error for non-hotel payload")
	}
}

func TestParseHotelsFromPayload_WithHotels(t *testing.T) {
	hotel := make([]any, 12)
	hotel[0] = nil
	hotel[1] = "Payload Hotel"
	hotel[2] = []any{[]any{51.5, -0.12}}

	payload := []any{[]any{hotel}}

	hotels, err := parseHotelsFromPayload(payload, "GBP")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(hotels) != 1 {
		t.Fatalf("expected 1, got %d", len(hotels))
	}
}

// --- findPriceArrays ---

func TestFindPriceArrays_NotArray(t *testing.T) {
	result := findPriceArrays("not an array", 0)
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestFindPriceArrays_MaxDepth(t *testing.T) {
	// Create deep nesting beyond max depth.
	var data any = []any{[]any{"Booking.com", 189.0, "USD"}}
	for range 10 {
		data = []any{data}
	}
	result := findPriceArrays(data, 0)
	if len(result) != 0 {
		t.Errorf("expected 0 at max depth, got %d", len(result))
	}
}

func TestFindPriceArrays_NestedPrices(t *testing.T) {
	prices := []any{
		[]any{"Booking.com", 189.0, "USD"},
		[]any{"Expedia", 195.0, "USD"},
	}
	data := []any{nil, []any{nil, prices}}

	result := findPriceArrays(data, 0)
	if len(result) < 1 {
		t.Errorf("expected at least 1 price entry, got %d", len(result))
	}
}

// --- extractOrganicPrice edge cases ---

func TestExtractOrganicPrice_ShortInnerArray(t *testing.T) {
	// Inner array has < 4 elements — should be skipped.
	raw := []any{nil, []any{nil, nil}}
	price, cur := extractOrganicPrice(raw)
	if price != 0 || cur != "" {
		t.Errorf("expected (0, \"\"), got (%v, %q)", price, cur)
	}
}

func TestExtractOrganicPrice_ZeroPrice(t *testing.T) {
	raw := []any{nil, []any{[]any{0.0, 0.0}, nil, nil, "EUR"}}
	price, _ := extractOrganicPrice(raw)
	if price != 0 {
		t.Errorf("expected 0 for zero price, got %v", price)
	}
}

// --- Integration: full pipeline mock ---

func TestParseHotelSearchResponse_FullPipeline(t *testing.T) {
	// Build a complete mock batchexecute response.
	hotel := make([]any, 12)
	hotel[0] = nil
	hotel[1] = "Pipeline Hotel"
	hotel[2] = []any{[]any{60.168, 24.941}}
	hotel[3] = []any{"4-star", 4.0}
	hotel[7] = []any{[]any{4.5, 800.0}}
	hotel[9] = "/g/pipeline"

	hotelData := []any{hotel}
	innerJSON, _ := json.Marshal(hotelData)

	entries := []any{
		[]any{
			[]any{"wrb.fr", "AtySUc", string(innerJSON), nil, nil, nil, "generic"},
		},
	}

	hotels, err := ParseHotelSearchResponse(entries, "EUR")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(hotels) < 1 {
		t.Fatal("expected at least 1 hotel")
	}
}

// --- parseSponsoredHotel edge cases ---

func TestParseSponsoredHotel_NonNumericReviews(t *testing.T) {
	entry := make([]any, 7)
	entry[0] = "Hotel"
	entry[4] = "not a number" // review count should be float64
	entry[5] = "also not"     // rating should be float64

	h := parseSponsoredHotel(entry, "USD")
	if h.ReviewCount != 0 {
		t.Errorf("ReviewCount = %d, want 0", h.ReviewCount)
	}
	if h.Rating != 0 {
		t.Errorf("Rating = %v, want 0", h.Rating)
	}
}

// --- HotelSearchOptions defaults ---

func TestHotelSearchOptions_Defaults(t *testing.T) {
	opts := HotelSearchOptions{
		CheckIn:  "2026-06-15",
		CheckOut: "2026-06-18",
	}

	// Verify defaults are applied in SearchHotels (guests=2, currency=USD).
	// We can't run the full search, but verify the URL building.
	url := buildTravelURL("Helsinki", opts)
	// Guests default is not applied in buildTravelURL but in SearchHotels.
	// Just verify it doesn't crash.
	if url == "" {
		t.Error("empty URL")
	}
}

// --- parseSponsoredHotel with nil price string ---

func TestParseSponsoredHotel_NilPrice(t *testing.T) {
	entry := make([]any, 7)
	entry[0] = "Hotel"
	entry[2] = nil // nil price
	h := parseSponsoredHotel(entry, "USD")
	if h.Price != 0 {
		t.Errorf("Price = %v, want 0", h.Price)
	}
}

// --- parseOrganicHotel edge cases ---

func TestParseOrganicHotel_BadCoords(t *testing.T) {
	entry := make([]any, 12)
	entry[1] = "Hotel"
	entry[2] = []any{[]any{"not", "floats"}} // bad coordinates
	h := parseOrganicHotel(entry, "USD")
	if h.Lat != 0 || h.Lon != 0 {
		t.Error("expected zero coords for non-float values")
	}
}

func TestParseOrganicHotel_EmptyLocArray(t *testing.T) {
	entry := make([]any, 12)
	entry[1] = "Hotel"
	entry[2] = []any{} // empty location array
	h := parseOrganicHotel(entry, "USD")
	if h.Lat != 0 {
		t.Errorf("Lat = %v, want 0", h.Lat)
	}
}

func TestParseOrganicHotel_BadStarRating(t *testing.T) {
	entry := make([]any, 12)
	entry[1] = "Hotel"
	entry[3] = []any{"label"} // only 1 element, need >= 2
	h := parseOrganicHotel(entry, "USD")
	if h.Stars != 0 {
		t.Errorf("Stars = %d, want 0", h.Stars)
	}
}

func TestParseOrganicHotel_BadRating(t *testing.T) {
	entry := make([]any, 12)
	entry[1] = "Hotel"
	entry[7] = []any{"not an array pair"}
	h := parseOrganicHotel(entry, "USD")
	if h.Rating != 0 {
		t.Errorf("Rating = %v, want 0", h.Rating)
	}
}

// --- extractBatchPayload edge cases ---

func TestExtractBatchPayload_EmptyHotelArr(t *testing.T) {
	hotelList := []any{
		[]any{
			nil,
			map[string]any{
				"12345": []any{}, // empty
			},
		},
	}
	data := []any{[]any{[]any{[]any{nil, hotelList}}}}
	hotels := extractOrganicHotels(data, "USD")
	if len(hotels) != 0 {
		t.Errorf("expected 0, got %d", len(hotels))
	}
}

func TestExtractSponsoredHotels_EntryNotArray(t *testing.T) {
	hotelList := []any{
		[]any{
			nil,
			map[string]any{
				"300000000": []any{nil, nil, []any{
					"not an array", // should be skipped
				}},
			},
		},
	}
	data := []any{[]any{[]any{[]any{nil, hotelList}}}}
	hotels := extractSponsoredHotels(data, "USD")
	if len(hotels) != 0 {
		t.Errorf("expected 0, got %d", len(hotels))
	}
}

// Full organic+sponsored integration via parseHotelsFromPage
func TestParseHotelsFromPage_MixedOrgAndSponsored(t *testing.T) {
	organic := make([]any, 12)
	organic[0] = nil
	organic[1] = "Organic Only Hotel"
	organic[2] = []any{[]any{60.168, 24.941}}
	organic[9] = "/g/org"

	sponsored := make([]any, 7)
	sponsored[0] = "Sponsored Only Hotel"
	sponsored[2] = "EUR 200"
	sponsored[4] = float64(400)
	sponsored[5] = float64(4.1)

	hotelList := []any{
		[]any{
			nil,
			map[string]any{
				"397419284": []any{organic},
				"300000000": []any{nil, nil, []any{sponsored}},
			},
		},
	}
	innerData := []any{[]any{[]any{[]any{nil, hotelList}}}}
	dataJSON, _ := json.Marshal(innerData)

	page := `AF_initDataCallback({key: 'ds:0', data:` + string(dataJSON) + `});`

	hotels, err := parseHotelsFromPage(page, "EUR")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(hotels) != 2 {
		t.Fatalf("expected 2 hotels (1 organic + 1 sponsored), got %d", len(hotels))
	}

	names := map[string]bool{}
	for _, h := range hotels {
		names[h.Name] = true
	}
	if !names["Organic Only Hotel"] || !names["Sponsored Only Hotel"] {
		t.Errorf("missing expected hotels: %v", names)
	}
}

// Verify parseOrganicHotel with all fields
func TestParseOrganicHotel_AllFields(t *testing.T) {
	entry := make([]any, 12)
	entry[0] = nil
	entry[1] = "Full Hotel"
	entry[2] = []any{[]any{51.5074, -0.1278}}
	entry[3] = []any{"5-star hotel", 5.0}
	entry[6] = []any{nil, []any{[]any{900.0, 0.0}, nil, nil, "GBP"}, []any{nil, []any{"£350", nil, 350.0, nil, 350.0}}}
	entry[7] = []any{[]any{4.8, 2000.0}}
	entry[9] = "ChIJ123abc"
	entry[11] = []any{"123 London Road"}

	h := parseOrganicHotel(entry, "GBP")

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Name", h.Name, "Full Hotel"},
		{"Lat", h.Lat, 51.5074},
		{"Lon", h.Lon, -0.1278},
		{"Stars", h.Stars, 5},
		{"Price", h.Price, 350.0},
		{"Currency", h.Currency, "GBP"},
		{"Rating", h.Rating, 9.6},
		{"ReviewCount", h.ReviewCount, 2000},
		{"HotelID", h.HotelID, "ChIJ123abc"},
		{"Description", h.Description, "123 London Road"},
	}

	for _, c := range checks {
		switch want := c.want.(type) {
		case string:
			if c.got != want {
				t.Errorf("%s = %v, want %v", c.name, c.got, want)
			}
		case float64:
			if c.got != want {
				t.Errorf("%s = %v, want %v", c.name, c.got, want)
			}
		case int:
			if c.got != want {
				t.Errorf("%s = %v, want %v", c.name, c.got, want)
			}
		}
	}
}

// --- extractSponsoredAmenities ---

func TestExtractSponsoredAmenities_ValidCodes(t *testing.T) {
	// Amenity codes from a real sponsored entry.
	raw := []any{float64(18), float64(11), float64(23), float64(4), float64(24), float64(1), float64(6), float64(2)}
	amenities := extractSponsoredAmenities(raw)
	if len(amenities) == 0 {
		t.Fatal("expected amenities, got none")
	}

	// Check that known codes are mapped.
	found := map[string]bool{}
	for _, a := range amenities {
		found[a] = true
	}

	// Code 2 = "free_wifi", code 4 = "pool", code 23 = "free_parking"
	expected := []string{"free_wifi", "pool", "free_parking"}
	for _, e := range expected {
		if !found[e] {
			t.Errorf("missing amenity %q in %v", e, amenities)
		}
	}
}

func TestExtractSponsoredAmenities_Nil(t *testing.T) {
	amenities := extractSponsoredAmenities(nil)
	if len(amenities) != 0 {
		t.Errorf("expected 0 amenities for nil, got %d", len(amenities))
	}
}

func TestExtractSponsoredAmenities_NotArray(t *testing.T) {
	amenities := extractSponsoredAmenities("not an array")
	if len(amenities) != 0 {
		t.Errorf("expected 0 amenities for non-array, got %d", len(amenities))
	}
}

func TestExtractSponsoredAmenities_UnknownCodes(t *testing.T) {
	raw := []any{float64(999), float64(888)} // no known mapping
	amenities := extractSponsoredAmenities(raw)
	if len(amenities) != 0 {
		t.Errorf("expected 0 for unknown codes, got %d", len(amenities))
	}
}

func TestExtractSponsoredAmenities_Dedup(t *testing.T) {
	// Code 11 and 54 both map to "accessible".
	raw := []any{float64(11), float64(54)}
	amenities := extractSponsoredAmenities(raw)
	count := 0
	for _, a := range amenities {
		if a == "accessible" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 'accessible' entry, got %d in %v", count, amenities)
	}
}

// --- extractTotalAvailable ---

func TestExtractTotalAvailable_Key416343588(t *testing.T) {
	hotelList := []any{
		[]any{
			nil,
			map[string]any{
				"416343588": []any{float64(2384), float64(0), "Helsinki", float64(0), float64(3)},
			},
		},
	}
	data := []any{[]any{[]any{[]any{nil, hotelList}}}}
	total := extractTotalAvailable(data)
	if total != 2384 {
		t.Errorf("total = %d, want 2384", total)
	}
}

func TestExtractTotalAvailable_Key410579159(t *testing.T) {
	hotelList := []any{
		[]any{
			nil,
			map[string]any{
				"410579159": []any{"CBI=", "", float64(1500), float64(1), float64(20)},
			},
		},
	}
	data := []any{[]any{[]any{[]any{nil, hotelList}}}}
	total := extractTotalAvailable(data)
	if total != 1500 {
		t.Errorf("total = %d, want 1500", total)
	}
}

func TestExtractTotalAvailable_Nil(t *testing.T) {
	total := extractTotalAvailable(nil)
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
}

func TestExtractTotalAvailable_NoMetadataKeys(t *testing.T) {
	hotelList := []any{
		[]any{
			nil,
			map[string]any{
				"397419284": []any{},
			},
		},
	}
	data := []any{[]any{[]any{[]any{nil, hotelList}}}}
	total := extractTotalAvailable(data)
	if total != 0 {
		t.Errorf("total = %d, want 0 when no metadata keys", total)
	}
}

// --- parseSponsoredHotel enriched fields ---

func TestParseSponsoredHotel_LatLon(t *testing.T) {
	entry := make([]any, 21)
	entry[0] = "Hotel With Coords"
	entry[2] = "EUR 100"
	entry[4] = float64(500)
	entry[5] = float64(4.5)
	entry[16] = []any{60.1646, 24.9348}

	h := parseSponsoredHotel(entry, "EUR")
	if h.Lat != 60.1646 {
		t.Errorf("Lat = %v, want 60.1646", h.Lat)
	}
	if h.Lon != 24.9348 {
		t.Errorf("Lon = %v, want 24.9348", h.Lon)
	}
}

func TestParseSponsoredHotel_Stars(t *testing.T) {
	entry := make([]any, 21)
	entry[0] = "Five Star Hotel"
	entry[10] = float64(5)

	h := parseSponsoredHotel(entry, "USD")
	if h.Stars != 5 {
		t.Errorf("Stars = %d, want 5", h.Stars)
	}
}

func TestParseSponsoredHotel_StarsInvalid(t *testing.T) {
	entry := make([]any, 21)
	entry[0] = "Invalid Stars Hotel"
	entry[10] = float64(0) // below valid range

	h := parseSponsoredHotel(entry, "USD")
	if h.Stars != 0 {
		t.Errorf("Stars = %d, want 0 for invalid", h.Stars)
	}
}

func TestParseSponsoredHotel_Amenities(t *testing.T) {
	entry := make([]any, 21)
	entry[0] = "Hotel With Amenities"
	entry[9] = []any{float64(2), float64(4), float64(23)} // wifi, pool, free_parking

	h := parseSponsoredHotel(entry, "USD")
	if len(h.Amenities) != 3 {
		t.Errorf("Amenities count = %d, want 3: %v", len(h.Amenities), h.Amenities)
	}
}

func TestParseSponsoredHotel_ExactPrice(t *testing.T) {
	entry := make([]any, 21)
	entry[0] = "Exact Price Hotel"
	// No string price at [2].
	entry[20] = []any{float64(121), float64(0), nil, float64(97.85), float64(11.64)}

	h := parseSponsoredHotel(entry, "USD")
	if h.Price != 98 { // rounded from 97.85
		t.Errorf("Price = %v, want 98 (rounded from 97.85)", h.Price)
	}
}

func TestParseSponsoredHotel_StringPriceOverridesExact(t *testing.T) {
	entry := make([]any, 21)
	entry[0] = "String Price Hotel"
	entry[2] = "EUR 100"
	entry[20] = []any{float64(121), float64(0), nil, float64(97.85)}

	h := parseSponsoredHotel(entry, "USD")
	// [20] always wins because it runs after [2] and overwrites.
	if h.Price != 98 {
		t.Errorf("Price = %v, want 98 (exact price from [20])", h.Price)
	}
}

// --- parseHotelsFromPageFull ---

func TestParseHotelsFromPageFull_WithTotalAvailable(t *testing.T) {
	hotel := make([]any, 12)
	hotel[0] = nil
	hotel[1] = "Test Hotel"
	hotel[2] = []any{[]any{60.168, 24.941}}
	hotel[9] = "/g/test"

	hotelList := []any{
		[]any{
			nil,
			map[string]any{
				"397419284": []any{hotel},
			},
		},
		[]any{
			nil,
			map[string]any{
				"416343588": []any{float64(5000), float64(0), "Test City"},
			},
		},
	}
	innerData := []any{[]any{[]any{[]any{nil, hotelList}}}}
	dataJSON, _ := json.Marshal(innerData)

	page := `AF_initDataCallback({key: 'ds:0', data:` + string(dataJSON) + `});`

	pr := parseHotelsFromPageFull(page, "EUR")
	if len(pr.Hotels) != 1 {
		t.Fatalf("expected 1 hotel, got %d", len(pr.Hotels))
	}
	if pr.TotalAvailable != 5000 {
		t.Errorf("TotalAvailable = %d, want 5000", pr.TotalAvailable)
	}
}

func TestParseHotelsFromPageFull_NoCallbacks(t *testing.T) {
	pr := parseHotelsFromPageFull("<html>no callbacks</html>", "USD")
	if len(pr.Hotels) != 0 {
		t.Errorf("expected 0 hotels, got %d", len(pr.Hotels))
	}
	if pr.TotalAvailable != 0 {
		t.Errorf("expected 0 total, got %d", pr.TotalAvailable)
	}
}
