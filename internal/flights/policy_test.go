package flights

import (
	"os"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// These tests pin the SHARED budget/time/bag policy that both the CLI
// (cmd/trvl/flights.go) and the MCP surface (mcp/tools_flights.go) now route
// through ApplySharedFlightPolicy. Because both surfaces call this one
// function, their behavior is identical by construction — the class of silent
// CLI-vs-MCP drift behind trvl #452 (and the earlier PreferredAlliance /
// CabinClass parity gaps) can no longer happen without failing these tests.

func polResult(prices ...float64) *models.FlightSearchResult {
	fl := make([]models.FlightResult, 0, len(prices))
	for _, p := range prices {
		fl = append(fl, models.FlightResult{
			Price: p,
			Legs:  []models.FlightLeg{{DepartureTime: "2026-08-10T10:00"}},
		})
	}
	return &models.FlightSearchResult{Success: true, Flights: fl, Count: len(fl)}
}

func TestApplySharedFlightPolicy_BudgetAppliedWhenNotSkipped(t *testing.T) {
	r := polResult(100, 500, 900)
	prefs := &preferences.Preferences{BudgetFlightMax: 600}
	ApplySharedFlightPolicy(r, prefs, false)
	if len(r.Flights) != 2 || r.Count != 2 {
		t.Fatalf("budget 600 should keep 2 flights (100,500), got %d (count=%d)", len(r.Flights), r.Count)
	}
}

func TestApplySharedFlightPolicy_BudgetSkippedWhenExplicitPrice(t *testing.T) {
	// Parity with the CLI --max-price behavior: an explicit price ceiling means
	// the caller already constrained on price, so the preference budget must NOT
	// be re-applied. This is the exact divergence the refactor removed — the MCP
	// surface previously applied the pref budget unconditionally while the CLI
	// skipped it when the flag was set.
	r := polResult(100, 500, 900)
	prefs := &preferences.Preferences{BudgetFlightMax: 600}
	ApplySharedFlightPolicy(r, prefs, true) // skipBudgetPref
	if len(r.Flights) != 3 || r.Count != 3 {
		t.Fatalf("explicit price should skip the pref budget -> keep all 3, got %d (count=%d)", len(r.Flights), r.Count)
	}
}

func TestApplySharedFlightPolicy_CountConsistentAndNilSafe(t *testing.T) {
	ApplySharedFlightPolicy(nil, nil, false) // must not panic on nil
	ApplySharedFlightPolicy(&models.FlightSearchResult{Success: false}, &preferences.Preferences{}, false)

	r := polResult(100, 200)
	prefs := &preferences.Preferences{BudgetFlightMax: 150}
	ApplySharedFlightPolicy(r, prefs, false)
	if r.Count != len(r.Flights) {
		t.Fatalf("Count (%d) must stay equal to len(Flights) (%d)", r.Count, len(r.Flights))
	}
}

// TestFlightSearchPolicyParity is the anti-regression guard for CLI↔MCP
// post-search policy drift — the bug class behind #452 (MaxPrice), the earlier
// PreferredAlliance regression, and the pending CabinClass divergence.
//
// It has two halves:
//
//  1. Behaviour: given identical inputs (same synthetic FlightSearchResult, same
//     Preferences, no surface-specific extras) the single shared policy produces
//     one deterministic flight set. Because BOTH surfaces now route their
//     post-search preference filtering through ApplySharedFlightPolicy, this IS
//     the flight set both produce — testing it once tests both.
//
//  2. Source guard (non-tautological): it reads the actual CLI and MCP source
//     files and asserts each delegates to ApplySharedFlightPolicy and neither
//     re-implements the shared budget/time/bag chain inline, nor reintroduces
//     the #452 pre-search MaxPrice seeding. If someone reintroduced the
//     "MaxPrice only in MCP" bug today — whether by re-adding the pre-search
//     `opts.MaxPrice = hints.MaxPrice` line, or by inlining a second
//     FilterFlightsByBudget call in only one surface — this test fails, because
//     the offending surface would then either seed opts.MaxPrice from the
//     profile again or call a shared filter directly instead of through the one
//     policy function.
func TestFlightSearchPolicyParity(t *testing.T) {
	t.Run("shared policy is deterministic for identical inputs", func(t *testing.T) {
		cases := []struct {
			name        string
			prices      []float64
			prefs       *preferences.Preferences
			explicitCap bool
			wantPrices  []float64
		}{
			{
				name:       "budget drops over-cap flights",
				prices:     []float64{100, 500, 900},
				prefs:      &preferences.Preferences{BudgetFlightMax: 500},
				wantPrices: []float64{100, 500},
			},
			{
				name:        "explicit price cap skips the profile budget",
				prices:      []float64{100, 500, 900},
				prefs:       &preferences.Preferences{BudgetFlightMax: 500},
				explicitCap: true,
				wantPrices:  []float64{100, 500, 900},
			},
			{
				name:       "no budget keeps everything",
				prices:     []float64{100, 900},
				prefs:      &preferences.Preferences{},
				wantPrices: []float64{100, 900},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// Both surfaces call ApplySharedFlightPolicy with these exact
				// arguments; running it once represents both post-processings.
				got := polResult(tc.prices...)
				ApplySharedFlightPolicy(got, tc.prefs, tc.explicitCap)
				if len(got.Flights) != len(tc.wantPrices) {
					t.Fatalf("got %d flights, want %d", len(got.Flights), len(tc.wantPrices))
				}
				for i, p := range tc.wantPrices {
					if got.Flights[i].Price != p {
						t.Fatalf("flight %d price = %.0f, want %.0f", i, got.Flights[i].Price, p)
					}
				}
				if got.Count != len(got.Flights) {
					t.Fatalf("Count %d != len(Flights) %d", got.Count, len(got.Flights))
				}
			})
		}
	})

	t.Run("both surfaces delegate; neither re-implements the chain", func(t *testing.T) {
		surfaces := map[string]string{
			"CLI": "../../cmd/trvl/flights.go",
			"MCP": "../../mcp/tools_flights.go",
		}
		// The three functions that make up the shared post-search chain. After
		// the refactor NEITHER surface may call them directly — only
		// ApplySharedFlightPolicy (in this package) may.
		sharedChain := []string{
			"FilterFlightsByBudget(",
			"FilterFlightsByTimePreference(",
			"AdjustBagAllowance(",
		}
		for name, path := range surfaces {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s: read %s: %v", name, path, err)
			}
			src := string(b)
			if !strings.Contains(src, "ApplySharedFlightPolicy(") {
				t.Errorf("%s (%s) no longer calls ApplySharedFlightPolicy — the shared post-search policy must be exercised by both surfaces", name, path)
			}
			for _, fn := range sharedChain {
				if strings.Contains(src, fn) {
					t.Errorf("%s (%s) calls %s directly — the shared budget/time/bag chain must live only in ApplySharedFlightPolicy, or the two surfaces will drift again (#452)", name, path, fn)
				}
			}
		}
		// Direct guard against reintroducing the exact #452 pre-search bug:
		// seeding the hard opts.MaxPrice ceiling from the profile hint, which
		// only ever happened on the MCP surface and silently truncated merges.
		mcp, err := os.ReadFile(surfaces["MCP"])
		if err != nil {
			t.Fatalf("read MCP source: %v", err)
		}
		if strings.Contains(string(mcp), "opts.MaxPrice = hints.MaxPrice") {
			t.Error("MCP reintroduced the #452 pre-search bug: opts.MaxPrice must NOT be seeded from the profile hint (it is a hard post-fetch ceiling that silently truncates the provider merge)")
		}
	})
}

