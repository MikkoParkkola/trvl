package hotels

import (
	"os"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// These tests pin the SHARED post-search hotel policy that both the CLI
// (cmd/trvl/hotels.go) and the MCP surface (mcp/tools_hotels.go, and by reuse
// mcp/tools_accommodations.go) now route through ApplySharedHotelPolicy.
// Because both surfaces call this one function, their behaviour is identical by
// construction — the CLI-vs-MCP drift behind trvl #452 (the flights MaxPrice
// truncation) and the CLI-only adults-only exclusion can no longer happen
// without failing these tests.

func polHotels(adultsOnly ...bool) *models.HotelSearchResult {
	h := make([]models.HotelResult, 0, len(adultsOnly))
	for i, ao := range adultsOnly {
		h = append(h, models.HotelResult{Name: "H", HotelID: string(rune('a' + i)), AdultsOnly: ao})
	}
	return &models.HotelSearchResult{Success: true, Hotels: h, Count: len(h)}
}

func TestApplySharedHotelPolicy_ExcludesAdultsOnlyWhenChildrenPresent(t *testing.T) {
	r := polHotels(false, true, false, true)
	hidden := ApplySharedHotelPolicy(r, true)
	if hidden != 2 {
		t.Fatalf("expected 2 adults-only hidden, got %d", hidden)
	}
	if len(r.Hotels) != 2 || r.Count != 2 {
		t.Fatalf("expected 2 family-friendly hotels kept (count consistent), got %d (count=%d)", len(r.Hotels), r.Count)
	}
	for _, h := range r.Hotels {
		if h.AdultsOnly {
			t.Fatalf("adults-only property survived the exclusion")
		}
	}
}

func TestApplySharedHotelPolicy_NoExclusionWhenNoChildren(t *testing.T) {
	// Parity requirement: with no children in the party, adults-only properties
	// are perfectly bookable and must NOT be hidden on either surface.
	r := polHotels(false, true, true)
	hidden := ApplySharedHotelPolicy(r, false)
	if hidden != 0 || len(r.Hotels) != 3 || r.Count != 3 {
		t.Fatalf("no children -> keep all 3, hide none; got hidden=%d kept=%d count=%d", hidden, len(r.Hotels), r.Count)
	}
}

func TestApplySharedHotelPolicy_CountConsistentAndNilSafe(t *testing.T) {
	if got := ApplySharedHotelPolicy(nil, true); got != 0 { // must not panic on nil
		t.Fatalf("nil result should hide 0, got %d", got)
	}
	r := polHotels(true, false)
	ApplySharedHotelPolicy(r, true)
	if r.Count != len(r.Hotels) {
		t.Fatalf("Count (%d) must stay equal to len(Hotels) (%d)", r.Count, len(r.Hotels))
	}
}

// TestHotelSearchPolicyParity is the anti-regression guard for CLI↔MCP
// post-search policy drift on the hotel surfaces — the hotels analogue of
// TestFlightSearchPolicyParity. Two halves:
//
//  1. Behaviour: given identical inputs the single shared policy produces one
//     deterministic hotel set. Because BOTH surfaces route their post-search
//     adults-only exclusion through ApplySharedHotelPolicy, testing it once
//     tests both.
//
//  2. Source guard (non-tautological): it reads the actual CLI and MCP source
//     and asserts each delegates to ApplySharedHotelPolicy, neither
//     re-implements the exclusion inline (no bare excludeAdultsOnly call), and
//     the MCP surface never re-seeds the hard price ceiling from the profile
//     hint (opts.MaxPrice = hints.MaxPrice) — the exact #452/#453 truncation
//     bug, whose hotel twin this PR removed.
func TestHotelSearchPolicyParity(t *testing.T) {
	t.Run("shared policy is deterministic for identical inputs", func(t *testing.T) {
		cases := []struct {
			name       string
			adultsOnly []bool
			children   bool
			wantKept   int
		}{
			{"children hide adults-only", []bool{false, true, false}, true, 2},
			{"no children keep all", []bool{false, true, true}, false, 3},
			{"all family friendly", []bool{false, false}, true, 2},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := polHotels(tc.adultsOnly...)
				ApplySharedHotelPolicy(got, tc.children)
				if len(got.Hotels) != tc.wantKept || got.Count != tc.wantKept {
					t.Fatalf("got %d kept (count=%d), want %d", len(got.Hotels), got.Count, tc.wantKept)
				}
			})
		}
	})

	t.Run("both surfaces delegate; neither re-implements the exclusion", func(t *testing.T) {
		surfaces := map[string]string{
			"CLI": "../../cmd/trvl/hotels.go",
			"MCP": "../../mcp/tools_hotels.go",
		}
		for name, path := range surfaces {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s: read %s: %v", name, path, err)
			}
			src := string(b)
			if !strings.Contains(src, "ApplySharedHotelPolicy(") {
				t.Errorf("%s (%s) no longer calls ApplySharedHotelPolicy — the shared adults-only exclusion must be exercised by both surfaces", name, path)
			}
			if strings.Contains(src, "excludeAdultsOnly(") {
				t.Errorf("%s (%s) calls excludeAdultsOnly directly — the exclusion must live only in ApplySharedHotelPolicy, or the two surfaces will drift again (#452)", name, path)
			}
		}
		// Direct guard against reintroducing the #452/#453 pre-search bug on the
		// hotel MCP surface: seeding the hard opts.MaxPrice ceiling from the
		// profile hint, which silently truncated the provider merge.
		mcp, err := os.ReadFile(surfaces["MCP"])
		if err != nil {
			t.Fatalf("read MCP source: %v", err)
		}
		if strings.Contains(string(mcp), "opts.MaxPrice = hints.MaxPrice") {
			t.Error("MCP reintroduced the truncation bug: opts.MaxPrice must NOT be seeded from the profile hint (it is a hard post-fetch ceiling that silently truncates the provider merge). A genuine user preference BudgetPerNightMax still applies; a profile hint must not.")
		}
	})
}
