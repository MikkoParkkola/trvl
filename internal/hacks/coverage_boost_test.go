package hacks

import (
	"testing"
)

// ============================================================
// dedupHacks — tests for deduplication logic
// ============================================================

func TestDedupHacks_Empty(t *testing.T) {
	result := dedupHacks(nil)
	if len(result) != 0 {
		t.Errorf("expected 0 hacks, got %d", len(result))
	}
}

func TestDedupHacks_Single(t *testing.T) {
	hacks := []Hack{{Type: "throwaway", Savings: 50}}
	result := dedupHacks(hacks)
	if len(result) != 1 {
		t.Errorf("expected 1 hack, got %d", len(result))
	}
}

func TestDedupHacks_NoDuplicates(t *testing.T) {
	hacks := []Hack{
		{Type: "throwaway", Savings: 50, Steps: []string{"Book HEL→BCN→PMI"}},
		{Type: "hidden_city", Savings: 100, Steps: []string{"Book HEL→AMS→CDG"}},
	}
	result := dedupHacks(hacks)
	if len(result) != 2 {
		t.Errorf("expected 2 unique hacks, got %d", len(result))
	}
}

func TestDedupHacks_DuplicateSameType(t *testing.T) {
	hacks := []Hack{
		{Type: "throwaway", Savings: 50, Steps: []string{"Book to BCN"}},
		{Type: "throwaway", Savings: 52, Steps: []string{"Book to BCN", "Skip last segment"}},
	}
	result := dedupHacks(hacks)
	if len(result) != 1 {
		t.Errorf("expected 1 deduped hack, got %d", len(result))
	}
	// The one with more steps (more detail) should be kept.
	if len(result[0].Steps) != 2 {
		t.Errorf("expected the more detailed hack to be kept (2 steps), got %d", len(result[0].Steps))
	}
}

func TestDedupHacks_SameTypeDifferentAirport(t *testing.T) {
	hacks := []Hack{
		{Type: "positioning", Savings: 50, Steps: []string{"Fly from AMS"}},
		{Type: "positioning", Savings: 50, Steps: []string{"Fly from BRU"}},
	}
	result := dedupHacks(hacks)
	// Different airport codes = different keys.
	if len(result) != 2 {
		t.Errorf("expected 2 hacks (different airports), got %d", len(result))
	}
}

func TestDedupHacks_SameTypeDifferentSavingsBucket(t *testing.T) {
	hacks := []Hack{
		{Type: "throwaway", Savings: 10, Steps: []string{"Book to BCN"}},
		{Type: "throwaway", Savings: 50, Steps: []string{"Book to BCN"}},
	}
	result := dedupHacks(hacks)
	// Savings 10 rounds to bucket 10, savings 50 rounds to bucket 50 — different keys.
	if len(result) != 2 {
		t.Errorf("expected 2 hacks (different savings buckets), got %d", len(result))
	}
}

// ============================================================
// DetectorInput.currency
// ============================================================

func TestDetectorInput_CurrencyFallback(t *testing.T) {
	tests := []struct {
		currency string
		want     string
	}{
		{"", "EUR"},
		{"USD", "USD"},
		{"GBP", "GBP"},
	}
	for _, tt := range tests {
		in := DetectorInput{Currency: tt.currency}
		got := in.currency()
		if got != tt.want {
			t.Errorf("currency(%q) = %q, want %q", tt.currency, got, tt.want)
		}
	}
}

// ============================================================
// stopoverPrograms completeness
// ============================================================

func TestStopoverPrograms_AllHaveURL(t *testing.T) {
	for code, prog := range stopoverPrograms {
		if prog.URL == "" {
			t.Errorf("[%s] stopover program has empty URL", code)
		}
		if prog.Restrictions == "" {
			t.Errorf("[%s] stopover program has empty Restrictions", code)
		}
	}
}

// ============================================================
// roundSavings edge cases
// ============================================================

func TestRoundSavings_NearHalf(t *testing.T) {
	tests := []struct {
		in   float64
		want float64
	}{
		{0.5, 1}, // rounds to nearest even? No, Go math.Round rounds to nearest, .5 away from zero.
		{1.5, 2},
		{-0.5, -1},
		{99.4, 99},
		{99.6, 100},
	}
	for _, tt := range tests {
		got := roundSavings(tt.in)
		if got != tt.want {
			t.Errorf("roundSavings(%.1f) = %.1f, want %.1f", tt.in, got, tt.want)
		}
	}
}

// ============================================================
// isOvernightRoute — full ISO datetime edge cases
// ============================================================

func TestIsOvernightRoute_FullISO(t *testing.T) {
	tests := []struct {
		name string
		dep  string
		arr  string
		want bool
	}{
		{
			name: "classic night bus 22:00→08:00",
			dep:  "2026-07-01T22:00",
			arr:  "2026-07-02T08:00",
			want: true,
		},
		{
			name: "morning flight 06:00→10:00",
			dep:  "2026-07-01T06:00",
			arr:  "2026-07-01T10:00",
			want: false,
		},
		{
			name: "late night dep 23:59, early morning arr",
			dep:  "2026-07-01T23:59",
			arr:  "2026-07-02T07:30",
			want: true,
		},
		{
			name: "short trip same evening",
			dep:  "2026-07-01T19:00",
			arr:  "2026-07-01T21:00",
			want: false, // arrival too close (not after 6h)
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isOvernightRoute(tt.dep, tt.arr)
			if got != tt.want {
				t.Errorf("isOvernightRoute(%q, %q) = %v, want %v", tt.dep, tt.arr, got, tt.want)
			}
		})
	}
}

// ============================================================
// addDays edge cases
// ============================================================

func TestAddDays_LeapYear(t *testing.T) {
	// 2028 is a leap year.
	got := addDays("2028-02-28", 1)
	if got != "2028-02-29" {
		t.Errorf("addDays leap year = %q, want 2028-02-29", got)
	}
}

func TestAddDays_CrossMonth(t *testing.T) {
	got := addDays("2026-01-31", 1)
	if got != "2026-02-01" {
		t.Errorf("addDays cross month = %q, want 2026-02-01", got)
	}
}

func TestAddDays_NegativeCrossYear(t *testing.T) {
	got := addDays("2026-01-01", -1)
	if got != "2025-12-31" {
		t.Errorf("addDays cross year = %q, want 2025-12-31", got)
	}
}
