package hotels

import (
	"encoding/json"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// --- rooms.go pure functions ---

func TestLooksLikeRoomName(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"Deluxe Double Room", true},
		{"Standard Twin", true},
		{"King Suite with Balcony", true},
		{"Single bed room", true},
		{"Studio Apartment", true},
		{"Ocean View Villa", true},
		{"Superior Room", true},
		{"Premium King Bed", true},
		{"ab", false},                     // too short
		{"", false},                       // empty
		{"https://example.com", false},    // URL
		{"<script>alert</script>", false}, // HTML
		{"Just A Regular String Here", false},
		{string(make([]byte, 101)), false}, // too long
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := looksLikeRoomName(tt.input)
			if got != tt.want {
				t.Errorf("looksLikeRoomName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestLooksLikeProvider(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"Booking.com", true},
		{"Expedia", true},
		{"Hotels.com", true},
		{"Agoda", true},
		{"Trip.com", true},
		{"Official Site", true},
		{"Marriott", true},
		{"Hilton Hotels", true},
		{"ab", false}, // too short
		{"", false},
		{"https://booking.com", false},    // starts with http
		{string(make([]byte, 61)), false}, // too long
		{"Some Provider Name", true},      // generic multi-word
		{"Provider{bad}", false},          // special chars
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := looksLikeProvider(tt.input)
			if got != tt.want {
				t.Errorf("looksLikeProvider(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestToFloat64_Rooms(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want float64
		ok   bool
	}{
		{"float64", 42.5, 42.5, true},
		{"int", 10, 10.0, true},
		{"json_number", json.Number("3.14"), 3.14, true},
		{"string", "hello", 0, false},
		{"nil", nil, 0, false},
		{"bool", true, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toFloat64(tt.val)
			if ok != tt.ok {
				t.Fatalf("toFloat64(%v) ok = %v, want %v", tt.val, ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("toFloat64(%v) = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}

func TestIsUpperAlpha(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"ABC", true},
		{"XYZ", true},
		{"A", true},
		{"", true}, // vacuously true
		{"abc", false},
		{"ABc", false},
		{"A1B", false},
		{"A B", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isUpperAlpha(tt.input)
			if got != tt.want {
				t.Errorf("isUpperAlpha(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestLooksLikeHotelName(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"Hotel Ritz Paris", true},
		{"The Grand Budapest Hotel", true},
		{"abc", false}, // too short (< 4)
		{"", false},
		{"https://hotel.com", false}, // URL
		{"<div>Hotel</div>", false},  // HTML
		{"{json: true}", false},      // JSON
		{"12345", false},             // no letters
		{"A Nice Place To Stay", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := looksLikeHotelName(tt.input)
			if got != tt.want {
				t.Errorf("looksLikeHotelName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- search.go pure functions ---

func TestHasAllAmenities(t *testing.T) {
	tests := []struct {
		name string
		have []string
		want []string
		ok   bool
	}{
		{"all_present", []string{"WiFi", "Pool", "Gym"}, []string{"wifi", "pool"}, true},
		{"missing_one", []string{"WiFi"}, []string{"wifi", "pool"}, false},
		{"empty_want", []string{"WiFi"}, nil, true},
		{"empty_have", nil, []string{"wifi"}, false},
		{"both_empty", nil, nil, true},
		{"whitespace_trimmed", []string{"WiFi", "Pool"}, []string{" WiFi "}, true}, // TrimSpace makes it match
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasAllAmenities(tt.have, tt.want)
			if got != tt.ok {
				t.Errorf("hasAllAmenities(%v, %v) = %v, want %v", tt.have, tt.want, got, tt.ok)
			}
		})
	}
}

func TestMedianHotelCoords(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		_, _, ok := medianHotelCoords(nil)
		if ok {
			t.Error("should return false for empty")
		}
	})

	t.Run("no_coords", func(t *testing.T) {
		hotels := []models.HotelResult{{Name: "A"}, {Name: "B"}}
		_, _, ok := medianHotelCoords(hotels)
		if ok {
			t.Error("should return false when no hotels have coords")
		}
	})

	t.Run("single", func(t *testing.T) {
		hotels := []models.HotelResult{{Lat: 60.17, Lon: 24.93}}
		lat, lon, ok := medianHotelCoords(hotels)
		if !ok {
			t.Fatal("expected ok")
		}
		if lat != 60.17 || lon != 24.93 {
			t.Errorf("got (%v, %v), want (60.17, 24.93)", lat, lon)
		}
	})

	t.Run("three", func(t *testing.T) {
		hotels := []models.HotelResult{
			{Lat: 10, Lon: 20},
			{Lat: 30, Lon: 40},
			{Lat: 20, Lon: 30},
		}
		lat, lon, ok := medianHotelCoords(hotels)
		if !ok {
			t.Fatal("expected ok")
		}
		if lat != 20 || lon != 30 {
			t.Errorf("got (%v, %v), want (20, 30)", lat, lon)
		}
	})
}

func TestCountBySource(t *testing.T) {
	hotels := []models.HotelResult{
		{Sources: []models.PriceSource{{Provider: "booking"}, {Provider: "google_hotels"}}},
		{Sources: []models.PriceSource{{Provider: "booking"}}},
		{Sources: []models.PriceSource{{Provider: "airbnb"}}},
		{Sources: nil},
	}
	if got := countBySource(hotels, "booking"); got != 2 {
		t.Errorf("countBySource(booking) = %d, want 2", got)
	}
	if got := countBySource(hotels, "airbnb"); got != 1 {
		t.Errorf("countBySource(airbnb) = %d, want 1", got)
	}
	if got := countBySource(hotels, "trivago"); got != 0 {
		t.Errorf("countBySource(trivago) = %d, want 0", got)
	}
}

func TestCountExternalSources(t *testing.T) {
	hotels := []models.HotelResult{
		{Sources: []models.PriceSource{{Provider: "booking"}}},
		{Sources: []models.PriceSource{{Provider: "google_hotels"}}},
		{Sources: nil},
	}
	got := countExternalSources(hotels)
	// "booking" is external, "google_hotels" is not.
	if got < 1 {
		t.Errorf("countExternalSources should count at least booking, got %d", got)
	}
}

func TestDisplayProvider(t *testing.T) {
	if got := displayProvider("google_hotels"); got != "Google Hotels" {
		t.Errorf("displayProvider(google_hotels) = %q, want Google Hotels", got)
	}
	if got := displayProvider("booking"); got != "booking" {
		t.Errorf("displayProvider(booking) = %q, want booking", got)
	}
}

func TestProviderFromSources(t *testing.T) {
	t.Run("with_source", func(t *testing.T) {
		h := &models.HotelResult{
			Sources: []models.PriceSource{{Provider: "booking"}},
		}
		got := providerFromSources(h)
		if got != "booking" {
			t.Errorf("providerFromSources = %q, want booking", got)
		}
	})

	t.Run("empty_sources", func(t *testing.T) {
		h := &models.HotelResult{}
		got := providerFromSources(h)
		if got != "Google Hotels" {
			t.Errorf("providerFromSources = %q, want Google Hotels", got)
		}
	})

	t.Run("google_hotels_source", func(t *testing.T) {
		h := &models.HotelResult{
			Sources: []models.PriceSource{{Provider: "google_hotels"}},
		}
		got := providerFromSources(h)
		if got != "Google Hotels" {
			t.Errorf("providerFromSources = %q, want Google Hotels", got)
		}
	})
}

// --- search_by_name.go pure functions ---

func TestBuildLocationCandidates(t *testing.T) {
	tests := []struct {
		query string
		min   int // minimum number of candidates
	}{
		{"Beverly Hills Heights, Tenerife", 3},
		{"Hotel Ritz", 1},
		{"", 1},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := buildLocationCandidates(tt.query)
			if len(got) < tt.min {
				t.Errorf("buildLocationCandidates(%q) returned %d candidates, want >= %d", tt.query, len(got), tt.min)
			}
		})
	}

	// Specific case: comma-separated should extract "Tenerife" as first candidate.
	got := buildLocationCandidates("Beverly Hills Heights, Tenerife")
	if got[0] != "Tenerife" {
		t.Errorf("first candidate = %q, want Tenerife", got[0])
	}
}

func TestFindBestNameMatch(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Grand Hotel Palace"},
		{Name: "Budget Inn"},
		{Name: "The Ritz-Carlton"},
	}

	t.Run("exact_contains", func(t *testing.T) {
		best := findBestNameMatch(hotels, "Grand Hotel Palace")
		if best == nil || best.Name != "Grand Hotel Palace" {
			t.Errorf("expected Grand Hotel Palace, got %v", best)
		}
	})

	t.Run("partial_match", func(t *testing.T) {
		best := findBestNameMatch(hotels, "Ritz Carlton")
		if best == nil || best.Name != "The Ritz-Carlton" {
			t.Errorf("expected The Ritz-Carlton, got %v", best)
		}
	})

	t.Run("no_match", func(t *testing.T) {
		best := findBestNameMatch(hotels, "ZZ Xylophone Quorum")
		if best != nil {
			t.Errorf("expected nil for no match, got %v", best.Name)
		}
	})

	t.Run("empty_hotels", func(t *testing.T) {
		best := findBestNameMatch(nil, "test")
		if best != nil {
			t.Error("expected nil for empty hotels")
		}
	})
}

// --- trivago.go ---

func TestExtractNSIDFromMap(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		m := map[string]json.RawMessage{
			"ns": json.RawMessage("1"),
			"id": json.RawMessage("42"),
		}
		ref, err := extractNSIDFromMap(m)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ref.NS != 1 || ref.ID != 42 {
			t.Errorf("got ns=%d id=%d, want ns=1 id=42", ref.NS, ref.ID)
		}
	})

	t.Run("uppercase_keys", func(t *testing.T) {
		m := map[string]json.RawMessage{
			"NS": json.RawMessage("2"),
			"ID": json.RawMessage("100"),
		}
		ref, err := extractNSIDFromMap(m)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ref.NS != 2 || ref.ID != 100 {
			t.Errorf("got ns=%d id=%d, want ns=2 id=100", ref.NS, ref.ID)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		m := map[string]json.RawMessage{
			"other": json.RawMessage("1"),
		}
		_, err := extractNSIDFromMap(m)
		if err == nil {
			t.Error("expected error for missing ns/id")
		}
	})
}

// --- rooms.go additional pure functions ---

func TestTryRoomEntry(t *testing.T) {
	t.Run("valid_room", func(t *testing.T) {
		arr := []any{"Deluxe Double Room", 199.0, "EUR", "Booking.com"}
		r := tryRoomEntry(arr, "USD")
		if r == nil {
			t.Fatal("expected room")
		}
		if r.Name != "Deluxe Double Room" {
			t.Errorf("Name = %q", r.Name)
		}
		if r.Price != 199.0 {
			t.Errorf("Price = %v", r.Price)
		}
		if r.Currency != "EUR" {
			t.Errorf("Currency = %q, want EUR", r.Currency)
		}
		if r.Provider != "Booking.com" {
			t.Errorf("Provider = %q, want Booking.com", r.Provider)
		}
	})

	t.Run("minimal_room", func(t *testing.T) {
		arr := []any{"Standard Twin", 89.0}
		r := tryRoomEntry(arr, "GBP")
		if r == nil {
			t.Fatal("expected room")
		}
		if r.Currency != "GBP" {
			t.Errorf("Currency = %q, want GBP (default)", r.Currency)
		}
	})

	t.Run("too_short", func(t *testing.T) {
		arr := []any{"Room"}
		r := tryRoomEntry(arr, "USD")
		if r != nil {
			t.Error("expected nil for too-short array")
		}
	})

	t.Run("non_room_name", func(t *testing.T) {
		arr := []any{"Something Random", 100.0}
		r := tryRoomEntry(arr, "USD")
		if r != nil {
			t.Error("expected nil for non-room name")
		}
	})

	t.Run("zero_price", func(t *testing.T) {
		arr := []any{"Deluxe Room", 0.0}
		r := tryRoomEntry(arr, "USD")
		if r != nil {
			t.Error("expected nil for zero price")
		}
	})

	t.Run("negative_price", func(t *testing.T) {
		arr := []any{"Deluxe Room", -50.0}
		r := tryRoomEntry(arr, "USD")
		if r != nil {
			t.Error("expected nil for negative price")
		}
	})
}

func TestTryRoomList(t *testing.T) {
	t.Run("valid_list", func(t *testing.T) {
		list := []any{
			[]any{"Deluxe Double Room", 199.0},
			[]any{"Standard Twin", 129.0},
			[]any{"King Suite", 349.0},
		}
		rooms := tryRoomList(list, "EUR")
		if len(rooms) != 3 {
			t.Fatalf("expected 3 rooms, got %d", len(rooms))
		}
	})

	t.Run("too_few_rooms", func(t *testing.T) {
		list := []any{
			[]any{"Deluxe Room", 199.0},
		}
		rooms := tryRoomList(list, "EUR")
		if rooms != nil {
			t.Error("expected nil for < 2 rooms")
		}
	})

	t.Run("mixed_entries", func(t *testing.T) {
		list := []any{
			[]any{"Deluxe Room", 199.0},
			"not an array",
			[]any{"Standard Room", 129.0},
		}
		rooms := tryRoomList(list, "EUR")
		if len(rooms) != 2 {
			t.Fatalf("expected 2 rooms, got %d", len(rooms))
		}
	})

	t.Run("empty", func(t *testing.T) {
		rooms := tryRoomList(nil, "EUR")
		if rooms != nil {
			t.Error("expected nil for nil input")
		}
	})
}

func TestExtractHotelNameFromCallback(t *testing.T) {
	t.Run("found_at_0_0", func(t *testing.T) {
		data := []any{
			[]any{
				"Hotel Ritz Paris",
			},
		}
		name := extractHotelNameFromCallback(data)
		if name != "Hotel Ritz Paris" {
			t.Errorf("got %q, want Hotel Ritz Paris", name)
		}
	})

	t.Run("not_array", func(t *testing.T) {
		name := extractHotelNameFromCallback("not an array")
		if name != "" {
			t.Errorf("expected empty for non-array, got %q", name)
		}
	})

	t.Run("nil_input", func(t *testing.T) {
		name := extractHotelNameFromCallback(nil)
		if name != "" {
			t.Errorf("expected empty for nil, got %q", name)
		}
	})
}

func TestExtractLocationFromSearchData(t *testing.T) {
	t.Run("found_triplet", func(t *testing.T) {
		data := []any{
			nil,
			"Helsinki",
			"0x46920b0af7b76d4f",
		}
		loc := extractLocationFromSearchData(data, 0)
		if loc != "Helsinki" {
			t.Errorf("got %q, want Helsinki", loc)
		}
	})

	t.Run("nested_triplet", func(t *testing.T) {
		data := []any{
			[]any{
				[]any{nil, "Paris", "0xabc123"},
			},
		}
		loc := extractLocationFromSearchData(data, 0)
		if loc != "Paris" {
			t.Errorf("got %q, want Paris", loc)
		}
	})

	t.Run("no_match", func(t *testing.T) {
		data := []any{"foo", "bar", "baz"}
		loc := extractLocationFromSearchData(data, 0)
		if loc != "" {
			t.Errorf("expected empty, got %q", loc)
		}
	})

	t.Run("depth_exceeded", func(t *testing.T) {
		data := []any{nil, "City", "0xabc"}
		loc := extractLocationFromSearchData(data, 11)
		if loc != "" {
			t.Error("should return empty when depth exceeded")
		}
	})

	t.Run("non_array", func(t *testing.T) {
		loc := extractLocationFromSearchData("string", 0)
		if loc != "" {
			t.Error("should return empty for non-array")
		}
	})
}

func TestFindRoomData(t *testing.T) {
	t.Run("finds_rooms", func(t *testing.T) {
		data := []any{
			[]any{
				[]any{"Deluxe Suite", 299.0},
				[]any{"Standard Room", 149.0},
			},
		}
		rooms := findRoomData(data, "EUR", 0)
		if len(rooms) < 2 {
			t.Errorf("expected at least 2 rooms, got %d", len(rooms))
		}
	})

	t.Run("depth_exceeded", func(t *testing.T) {
		data := []any{
			[]any{"Deluxe Suite", 299.0},
			[]any{"Standard Room", 149.0},
		}
		rooms := findRoomData(data, "EUR", 16)
		if rooms != nil {
			t.Error("should return nil when depth exceeded")
		}
	})

	t.Run("no_rooms", func(t *testing.T) {
		data := []any{"foo", 42.0, "bar"}
		rooms := findRoomData(data, "EUR", 0)
		if rooms != nil {
			t.Error("should return nil for non-room data")
		}
	})
}

func TestFindLocationTriplet(t *testing.T) {
	data := []any{nil, "London", "0x48761"}
	loc := findLocationTriplet(data, 0)
	if loc != "London" {
		t.Errorf("got %q, want London", loc)
	}
}

func TestExtractLocationFromCallback(t *testing.T) {
	data := []any{
		[]any{nil, "Tokyo", "0xfff"},
	}
	loc := extractLocationFromCallback(data)
	if loc != "Tokyo" {
		t.Errorf("got %q, want Tokyo", loc)
	}
}

func TestExtractLocationFromPage(t *testing.T) {
	// Empty page should return empty.
	loc := extractLocationFromPage("")
	if loc != "" {
		t.Errorf("expected empty for empty page, got %q", loc)
	}
}
