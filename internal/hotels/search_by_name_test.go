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

// --- findBestNameMatch: deterministic ID-preferring selection (BUG 2) ---

// TestFindBestNameMatchPrefersGoogleID asserts that among equally name-matching
// results, selection deterministically prefers one carrying a usable Google
// place ID over an empty-ID OTA entry — and that the choice is independent of
// input ordering. This is the root-cause fix for the intermittent
// "found but has no Google ID" abort in the rooms flow.
func TestFindBestNameMatchPrefersGoogleID(t *testing.T) {
	const query = "Hotel Best Front Maritim Barcelona"

	noID := models.HotelResult{Name: "Hotel Best Front Maritim Barcelona"}
	googleID := models.HotelResult{Name: "Hotel Best Front Maritim Barcelona", HotelID: "/g/11abcd1234"}
	otaID := models.HotelResult{Name: "Hotel Best Front Maritim Barcelona", HotelID: "flatio:98765"}

	tests := []struct {
		name    string
		hotels  []models.HotelResult
		wantID  string
		wantNil bool
	}{
		{
			name:   "google id wins over no id (google first)",
			hotels: []models.HotelResult{googleID, noID},
			wantID: "/g/11abcd1234",
		},
		{
			name:   "google id wins over no id (no id first)",
			hotels: []models.HotelResult{noID, googleID},
			wantID: "/g/11abcd1234",
		},
		{
			name:   "google id wins over ota id regardless of order",
			hotels: []models.HotelResult{otaID, googleID, noID},
			wantID: "/g/11abcd1234",
		},
		{
			name:   "non-empty ota id preferred over empty id",
			hotels: []models.HotelResult{noID, otaID},
			wantID: "flatio:98765",
		},
		{
			name:   "all empty ids still returns a match",
			hotels: []models.HotelResult{noID},
			wantID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findBestNameMatch(tt.hotels, query)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected a match, got nil")
			}
			if got.HotelID != tt.wantID {
				t.Errorf("selected HotelID = %q, want %q", got.HotelID, tt.wantID)
			}
		})
	}
}

// TestFindBestNameMatchStableOrdering asserts the selection is identical across
// all permutations of an input set that mixes ID usability and name score, so
// the result never depends on arbitrary provider-merge ordering.
func TestFindBestNameMatchStableOrdering(t *testing.T) {
	const query = "Hotel Best Front Maritim Barcelona"
	base := []models.HotelResult{
		{Name: "Hotel Best Front Maritim Barcelona", HotelID: "/g/11zzz"},
		{Name: "Hotel Best Front Maritim Barcelona", HotelID: "/g/11aaa"},
		{Name: "Hotel Best Front Maritim Barcelona", HotelID: ""},
		{Name: "Best Front Maritim", HotelID: "/g/11partial"},
	}

	// Lexically-smallest Google place ID among the full-score matches wins
	// ("/g/11aaa" < "/g/11zzz"); the partial-name match scores lower and loses.
	const wantID = "/g/11aaa"

	perms := [][]int{
		{0, 1, 2, 3}, {3, 2, 1, 0}, {2, 0, 3, 1}, {1, 3, 0, 2},
	}
	for _, p := range perms {
		shuffled := make([]models.HotelResult, len(p))
		for i, idx := range p {
			shuffled[i] = base[idx]
		}
		got := findBestNameMatch(shuffled, query)
		if got == nil {
			t.Fatalf("perm %v: expected a match, got nil", p)
		}
		if got.HotelID != wantID {
			t.Errorf("perm %v: selected HotelID = %q, want %q", p, got.HotelID, wantID)
		}
	}
}

// TestHotelIDRank covers the usability ranking of HotelIDs.
func TestHotelIDRank(t *testing.T) {
	cases := map[string]int{
		"/g/11abcd":   2, // genuine Google place ID
		"ChIJabc123":  2, // genuine Google place ID
		"chijLower":   2, // case-insensitive ChIJ prefix
		"flatio:9876": 1, // non-empty OTA id
		"12345":       1, // non-empty numeric id
		"":            0, // no id
		"   ":         0, // whitespace-only id
	}
	for id, want := range cases {
		if got := hotelIDRank(id); got != want {
			t.Errorf("hotelIDRank(%q) = %d, want %d", id, got, want)
		}
	}
}
