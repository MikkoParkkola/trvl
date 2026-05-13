package flights

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestKiwiDate(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"2026-04-18", "18/04/2026", false},
		{"2025-01-01", "01/01/2025", false},
		{"2025-12-31", "31/12/2025", false},
		{"bad-date", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := kiwiDate(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("kiwiDate(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("kiwiDate(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestKiwiCabinClass(t *testing.T) {
	tests := []struct {
		cabin models.CabinClass
		want  string
	}{
		{models.Economy, "M"},
		{models.PremiumEconomy, "W"},
		{models.Business, "C"},
		{models.First, "F"},
		{0, "M"},  // zero value
		{99, "M"}, // unknown
	}
	for _, tt := range tests {
		got := kiwiCabinClass(tt.cabin)
		if got != tt.want {
			t.Errorf("kiwiCabinClass(%d) = %q, want %q", tt.cabin, got, tt.want)
		}
	}
}

func TestKiwiSort(t *testing.T) {
	tests := []struct {
		sortBy models.SortBy
		want   string
	}{
		{models.SortDuration, "duration"},
		{models.SortCheapest, "price"},
		{0, "price"}, // default
	}
	for _, tt := range tests {
		got := kiwiSort(tt.sortBy)
		if got != tt.want {
			t.Errorf("kiwiSort(%d) = %q, want %q", tt.sortBy, got, tt.want)
		}
	}
}

func TestFilterFlightsByAlliance(t *testing.T) {
	makeFlights := func() []models.FlightResult {
		return []models.FlightResult{
			{Legs: []models.FlightLeg{{AirlineCode: "AA"}}}, // oneworld
			{Legs: []models.FlightLeg{{AirlineCode: "LH"}}}, // star_alliance
			{Legs: []models.FlightLeg{{AirlineCode: "AF"}}}, // skyteam
			{Legs: []models.FlightLeg{{AirlineCode: "XX"}}}, // unknown
			{Legs: nil}, // no legs
		}
	}

	t.Run("oneworld", func(t *testing.T) {
		got := filterFlightsByAlliance(makeFlights(), []string{"oneworld"})
		if len(got) != 1 || got[0].Legs[0].AirlineCode != "AA" {
			t.Errorf("expected 1 oneworld flight, got %d", len(got))
		}
	})

	t.Run("star_alliance", func(t *testing.T) {
		got := filterFlightsByAlliance(makeFlights(), []string{"star_alliance"})
		if len(got) != 1 || got[0].Legs[0].AirlineCode != "LH" {
			t.Errorf("expected 1 star alliance flight, got %d", len(got))
		}
	})

	t.Run("multiple", func(t *testing.T) {
		got := filterFlightsByAlliance(makeFlights(), []string{"oneworld", "skyteam"})
		if len(got) != 2 {
			t.Errorf("expected 2 flights, got %d", len(got))
		}
	})

	t.Run("empty_alliances", func(t *testing.T) {
		got := filterFlightsByAlliance(makeFlights(), nil)
		if len(got) != 0 {
			t.Errorf("expected 0 flights with nil alliances, got %d", len(got))
		}
	})
}

func TestPriceLimit(t *testing.T) {
	if got := priceLimit(0); got != nil {
		t.Errorf("priceLimit(0) = %v, want nil", got)
	}
	if got := priceLimit(-1); got != nil {
		t.Errorf("priceLimit(-1) = %v, want nil", got)
	}
	if got := priceLimit(500); got != 500 {
		t.Errorf("priceLimit(500) = %v, want 500", got)
	}
}

func TestParseHour_EdgeCases(t *testing.T) {
	// Supplement depart_time_emissions_test.go with boundary cases.
	tests := []struct {
		input string
		want  int
	}{
		{"00:00", 0},
		{"09:30", 9},
		{"23:59", 23},
		{"24:00", -1}, // out of range hour
		{"0:00", -1},  // too short
		{"12:3", -1},  // too short
		{"1230", -1},  // missing colon
		{"ab:cd", -1}, // non-digit chars
		{"", -1},      // empty
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseHour(tt.input)
			if got != tt.want {
				t.Errorf("parseHour(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestEmissionsFilter_Values(t *testing.T) {
	// Supplement depart_time_emissions_test.go with value checks.
	got := emissionsFilter(true)
	arr, ok := got.([]any)
	if !ok || len(arr) != 1 || arr[0] != 1 {
		t.Errorf("emissionsFilter(true) = %v, want []any{1}", got)
	}
	if got := emissionsFilter(false); got != nil {
		t.Errorf("emissionsFilter(false) = %v, want nil", got)
	}
}

func TestParseKiwiTimestamp(t *testing.T) {
	tests := []struct {
		input string
		ok    bool
	}{
		{"2026-04-18T14:30:00Z", true},
		{"2026-04-18T14:30:00+02:00", true},
		{"2026-04-18T14:30:00.000", true},
		{"2026-04-18T14:30:00", true},
		{"2026-04-18T14:30", true},
		{"2026-04-18", false},
		{"", false},
		{"not-a-date", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, ok := parseKiwiTimestamp(tt.input)
			if ok != tt.ok {
				t.Errorf("parseKiwiTimestamp(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			}
		})
	}
}

func TestKiwiDurationMinutes(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		got := kiwiDurationMinutes("2026-04-18T10:00:00Z", "2026-04-18T12:30:00Z")
		if got != 150 {
			t.Errorf("got %d, want 150", got)
		}
	})

	t.Run("invalid_start", func(t *testing.T) {
		got := kiwiDurationMinutes("bad", "2026-04-18T12:30:00Z")
		if got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})

	t.Run("invalid_end", func(t *testing.T) {
		got := kiwiDurationMinutes("2026-04-18T10:00:00Z", "bad")
		if got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})

	t.Run("end_before_start", func(t *testing.T) {
		got := kiwiDurationMinutes("2026-04-18T12:00:00Z", "2026-04-18T10:00:00Z")
		if got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})
}

func TestKiwiDisplayTime(t *testing.T) {
	t.Run("local_time", func(t *testing.T) {
		dt := kiwiDateTime{Local: "2026-04-18T14:30:00", UTC: "2026-04-18T12:30:00Z"}
		got := kiwiDisplayTime(dt)
		if got != "2026-04-18T14:30" {
			t.Errorf("got %q, want 2026-04-18T14:30", got)
		}
	})

	t.Run("utc_fallback", func(t *testing.T) {
		dt := kiwiDateTime{Local: "", UTC: "2026-04-18T12:30:00Z"}
		got := kiwiDisplayTime(dt)
		if got != "2026-04-18T12:30" {
			t.Errorf("got %q, want 2026-04-18T12:30", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		dt := kiwiDateTime{}
		got := kiwiDisplayTime(dt)
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestFirstNonEmpty_Flights(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{"first_non_empty", []string{"hello", "world"}, "hello"},
		{"skip_empty", []string{"", "world"}, "world"},
		{"skip_whitespace", []string{"  ", "hello"}, "hello"},
		{"all_empty", []string{"", "  ", ""}, ""},
		{"none", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstNonEmpty(tt.values...)
			if got != tt.want {
				t.Errorf("firstNonEmpty(%v) = %q, want %q", tt.values, got, tt.want)
			}
		})
	}
}
