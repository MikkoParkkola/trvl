package hotels

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// --- normalizeWords ---

func TestNormalizeWords(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"CORU House", []string{"coru", "house"}},
		{"Hotel Kamp", []string{"hotel", "kamp"}},
		{"Café de Flore", []string{"café", "flore"}}, // é is a letter (unicode), "de" too short
		{"", nil},
		{"AB CD", nil}, // all words < 3 chars
		{"foo-bar baz!", []string{"foo", "bar", "baz"}},
	}

	for _, tt := range tests {
		got := normalizeWords(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("normalizeWords(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("normalizeWords(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

// --- filterByNameMatch ---

func TestFilterByNameMatch(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "CORU House Prague - Design Hotel"},
		{Name: "Prague Hilton"},
		{Name: "Coru Boutique Prague"},
		{Name: "Hotel Josef Prague"},
	}

	t.Run("matches name words in hotel name", func(t *testing.T) {
		got := filterByNameMatch(hotels, "CORU House")
		if len(got) != 1 {
			t.Fatalf("expected 1 match, got %d: %v", len(got), names(got))
		}
		if got[0].Name != "CORU House Prague - Design Hotel" {
			t.Errorf("unexpected match: %q", got[0].Name)
		}
	})

	t.Run("single word matches multiple", func(t *testing.T) {
		got := filterByNameMatch(hotels, "Coru")
		// Both "CORU House Prague" and "Coru Boutique Prague" contain "coru".
		if len(got) != 2 {
			t.Fatalf("expected 2 matches, got %d: %v", len(got), names(got))
		}
	})

	t.Run("full name exact-ish match", func(t *testing.T) {
		got := filterByNameMatch(hotels, "Hotel Josef Prague")
		if len(got) != 1 {
			t.Fatalf("expected 1 match, got %d: %v", len(got), names(got))
		}
		if got[0].Name != "Hotel Josef Prague" {
			t.Errorf("unexpected match: %q", got[0].Name)
		}
	})

	t.Run("no match returns empty", func(t *testing.T) {
		got := filterByNameMatch(hotels, "Marriott")
		if len(got) != 0 {
			t.Errorf("expected no matches, got %v", names(got))
		}
	})

	t.Run("empty search name returns all", func(t *testing.T) {
		got := filterByNameMatch(hotels, "")
		if len(got) != len(hotels) {
			t.Errorf("expected all %d hotels, got %d", len(hotels), len(got))
		}
	})

	t.Run("short-word-only search name returns all (no filters applied)", func(t *testing.T) {
		// All words are < 3 chars so normalizeWords returns nil -> no filter.
		got := filterByNameMatch(hotels, "AB CD")
		if len(got) != len(hotels) {
			t.Errorf("expected all %d hotels, got %d", len(hotels), len(got))
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		got := filterByNameMatch(hotels, "coru HOUSE")
		if len(got) != 1 {
			t.Fatalf("expected 1 match, got %d: %v", len(got), names(got))
		}
	})
}

// --- buildNameQuery ---

func TestBuildNameQuery(t *testing.T) {
	tests := []struct {
		name     string
		location string
		want     string
	}{
		{"CORU House Prague", "Prague", "CORU House Prague"}, // location already in name
		{"CORU House", "Prague", "CORU House, Prague"},       // location appended
		{"Hotel Kamp", "Helsinki", "Hotel Kamp, Helsinki"},   // location appended
		{"Hotel Kamp", "", "Hotel Kamp"},                     // no location
		{"Grand Hotel", "grand", "Grand Hotel"},              // location substring of name
	}

	for _, tt := range tests {
		got := buildNameQuery(tt.name, tt.location)
		if got != tt.want {
			t.Errorf("buildNameQuery(%q, %q) = %q, want %q", tt.name, tt.location, got, tt.want)
		}
	}
}

// --- allWordsPresent ---

func TestAllWordsPresent(t *testing.T) {
	haystack := map[string]bool{"coru": true, "house": true, "prague": true, "design": true, "hotel": true}

	if !allWordsPresent([]string{"coru", "house"}, haystack) {
		t.Error("expected coru+house to be present")
	}
	if allWordsPresent([]string{"coru", "hilton"}, haystack) {
		t.Error("hilton should not be present")
	}
	if !allWordsPresent(nil, haystack) {
		t.Error("empty needles should always pass")
	}
}

// names is a test helper that extracts hotel names for readable failure messages.
func names(hotels []models.HotelResult) []string {
	out := make([]string, len(hotels))
	for i, h := range hotels {
		out[i] = h.Name
	}
	return out
}
