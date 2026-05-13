package models

import (
	"reflect"
	"sort"
	"testing"
)

func TestLookupAirportName_Known(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"HEL", "Helsinki"},
		{"JFK", "New York JFK"},
		{"NRT", "Tokyo Narita"},
		{"BCN", "Barcelona"},
		{"DBV", "Dubrovnik"},
		{"SIN", "Singapore"},
	}
	for _, tt := range tests {
		got := LookupAirportName(tt.code)
		if got != tt.want {
			t.Errorf("LookupAirportName(%q) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestLookupAirportName_Unknown(t *testing.T) {
	got := LookupAirportName("ZZZ")
	if got != "ZZZ" {
		t.Errorf("LookupAirportName(ZZZ) = %q, want ZZZ", got)
	}
}

func TestAirportNames_NotEmpty(t *testing.T) {
	if len(AirportNames) < 100 {
		t.Errorf("AirportNames has %d entries, want >= 100", len(AirportNames))
	}
}

func TestAirportNames_AllThreeLetterCodes(t *testing.T) {
	for code := range AirportNames {
		if len(code) != 3 {
			t.Errorf("airport code %q is not 3 characters", code)
		}
	}
}

func TestResolveLocationName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "known iata code", input: "PRG", want: "Prague"},
		{name: "trimmed iata code", input: "  HEL  ", want: "Helsinki"},
		{name: "unknown uppercase code", input: "ZZZ", want: "ZZZ"},
		{name: "city name passes through", input: "Prague", want: "Prague"},
		{name: "lowercase code passes through", input: "prg", want: "prg"},
		{name: "empty string", input: "   ", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveLocationName(tt.input); got != tt.want {
				t.Fatalf("ResolveLocationName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveAirportCity(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "airport override", input: "CDG", want: "Paris"},
		{name: "trimmed override", input: "  JFK  ", want: "New York"},
		{name: "plain airport name fallback", input: "AMS", want: "Amsterdam"},
		{name: "unknown code passes through", input: "ZZZ", want: "ZZZ"},
		{name: "empty string", input: "   ", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveAirportCity(tt.input); got != tt.want {
				t.Fatalf("ResolveAirportCity(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsIATACode(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"HEL", true},
		{"JFK", true},
		{"CDG", true},
		{"hel", false},   // lowercase
		{"PARIS", false}, // too long
		{"PA", false},    // too short
		{"Paris", false}, // mixed case
		{"123", false},   // digits
		{"", false},      // empty
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := IsIATACode(tt.input); got != tt.want {
				t.Errorf("IsIATACode(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveCityToAirports(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "multi-airport city",
			input: "Paris",
			want:  []string{"CDG", "ORY"},
		},
		{
			name:  "case insensitive",
			input: "paris",
			want:  []string{"CDG", "ORY"},
		},
		{
			name:  "multi-airport city Tokyo",
			input: "Tokyo",
			want:  []string{"HND", "NRT"},
		},
		{
			name:  "single-airport city Helsinki",
			input: "Helsinki",
			want:  []string{"HEL"},
		},
		{
			name:  "single-airport city Barcelona",
			input: "Barcelona",
			want:  []string{"BCN"},
		},
		{
			name:  "unknown city returns nil",
			input: "Narnia",
			want:  nil,
		},
		{
			name:  "empty string returns nil",
			input: "",
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveCityToAirports(tt.input)
			// Sort both for deterministic comparison
			sort.Strings(got)
			sort.Strings(tt.want)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ResolveCityToAirports(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
