package hotels

import (
	"strings"
	"testing"
)

// --- propertyTypeCode ---

func TestPropertyTypeCode_KnownTypes(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hotel", "2"},
		{"Hotel", "2"}, // case-insensitive
		{"HOTEL", "2"},
		{"apartment", "3"},
		{"hostel", "4"},
		{"resort", "5"},
		{"bnb", "7"},
		{"bed_and_breakfast", "7"},
		{"bed and breakfast", "7"},
		{"villa", "8"},
		{"  hotel  ", "2"}, // trimmed
	}

	for _, tt := range tests {
		got := propertyTypeCode(tt.input)
		if got != tt.want {
			t.Errorf("propertyTypeCode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPropertyTypeCode_Unknown(t *testing.T) {
	unknown := []string{"", "motel", "chalet", "mansion", "campsite"}
	for _, input := range unknown {
		got := propertyTypeCode(input)
		if got != "" {
			t.Errorf("propertyTypeCode(%q) = %q, want empty string for unknown type", input, got)
		}
	}
}

// --- buildTravelURL with FreeCancellation ---

func TestBuildTravelURL_FreeCancellation(t *testing.T) {
	opts := HotelSearchOptions{
		CheckIn:          "2026-07-01",
		CheckOut:         "2026-07-05",
		Guests:           2,
		Currency:         "EUR",
		FreeCancellation: true,
	}

	url := buildTravelURL("Helsinki", opts)

	if !strings.Contains(url, "fc=1") {
		t.Errorf("expected fc=1 in URL, got: %s", url)
	}
}

func TestBuildTravelURL_FreeCancellationFalse(t *testing.T) {
	opts := HotelSearchOptions{
		CheckIn:          "2026-07-01",
		CheckOut:         "2026-07-05",
		Guests:           2,
		Currency:         "USD",
		FreeCancellation: false,
	}

	url := buildTravelURL("Helsinki", opts)

	if strings.Contains(url, "fc=") {
		t.Errorf("expected no fc= param when FreeCancellation=false, got: %s", url)
	}
}

// --- buildTravelURL with PropertyType ---

func TestBuildTravelURL_PropertyType(t *testing.T) {
	tests := []struct {
		propType string
		wantCode string
	}{
		{"hotel", "2"},
		{"apartment", "3"},
		{"hostel", "4"},
		{"resort", "5"},
		{"bnb", "7"},
		{"villa", "8"},
	}

	for _, tt := range tests {
		opts := HotelSearchOptions{
			CheckIn:      "2026-07-01",
			CheckOut:     "2026-07-05",
			Guests:       2,
			Currency:     "USD",
			PropertyType: tt.propType,
		}
		url := buildTravelURL("Barcelona", opts)
		want := "ptype=" + tt.wantCode
		if !strings.Contains(url, want) {
			t.Errorf("PropertyType %q: expected %s in URL, got: %s", tt.propType, want, url)
		}
	}
}

func TestBuildTravelURL_PropertyTypeEmpty(t *testing.T) {
	opts := HotelSearchOptions{
		CheckIn:      "2026-07-01",
		CheckOut:     "2026-07-05",
		Guests:       2,
		Currency:     "USD",
		PropertyType: "",
	}

	url := buildTravelURL("Rome", opts)

	if strings.Contains(url, "ptype=") {
		t.Errorf("expected no ptype= when PropertyType is empty, got: %s", url)
	}
}

func TestBuildTravelURL_PropertyTypeUnknown(t *testing.T) {
	opts := HotelSearchOptions{
		CheckIn:      "2026-07-01",
		CheckOut:     "2026-07-05",
		Guests:       2,
		Currency:     "USD",
		PropertyType: "spaceship", // unknown type
	}

	url := buildTravelURL("Tokyo", opts)

	if strings.Contains(url, "ptype=") {
		t.Errorf("expected no ptype= for unknown PropertyType, got: %s", url)
	}
}

// --- buildTravelURL combined filters ---

func TestBuildTravelURL_BothNewFilters(t *testing.T) {
	opts := HotelSearchOptions{
		CheckIn:          "2026-08-10",
		CheckOut:         "2026-08-15",
		Guests:           1,
		Currency:         "GBP",
		FreeCancellation: true,
		PropertyType:     "apartment",
	}

	url := buildTravelURL("London", opts)

	if !strings.Contains(url, "fc=1") {
		t.Errorf("expected fc=1 in URL, got: %s", url)
	}
	if !strings.Contains(url, "ptype=3") {
		t.Errorf("expected ptype=3 in URL, got: %s", url)
	}
}

// --- buildTravelURL baseline (no new params) ---

func TestBuildTravelURL_Baseline(t *testing.T) {
	opts := HotelSearchOptions{
		CheckIn:  "2026-07-01",
		CheckOut: "2026-07-05",
		Guests:   2,
		Currency: "USD",
	}

	url := buildTravelURL("Paris", opts)

	if !strings.Contains(url, "google.com/travel/hotels/") {
		t.Errorf("expected google.com/travel/hotels/ in URL, got: %s", url)
	}
	if !strings.Contains(url, "adults=2") {
		t.Errorf("expected adults=2 in URL, got: %s", url)
	}
	if !strings.Contains(url, "currency=USD") {
		t.Errorf("expected currency=USD in URL, got: %s", url)
	}
	// Baseline: no new filter params.
	if strings.Contains(url, "fc=") {
		t.Errorf("unexpected fc= in baseline URL: %s", url)
	}
	if strings.Contains(url, "ptype=") {
		t.Errorf("unexpected ptype= in baseline URL: %s", url)
	}
}
