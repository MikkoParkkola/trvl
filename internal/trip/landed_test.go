package trip

import (
	"strings"
	"testing"
)

// TestTransferCost covers PLANCOMP.1's transfer table: a curated city returns
// per-person-times-party round-trip cost; an uncatalogued one degrades to a
// typed not-found status (zero, false) rather than a fabricated figure; and
// matching tolerates IATA-style free text and trailing country names.
func TestTransferCost(t *testing.T) {
	cases := []struct {
		name      string
		dest      string
		guests    int
		wantKnown bool
		wantTotal float64 // EUR; only checked when wantKnown
	}{
		{"barcelona two guests", "Barcelona", 2, true, 11.40 * 2},
		{"case insensitive", "barcelona", 1, true, 11.40},
		{"with country suffix", "Barcelona, Spain", 1, true, 11.40},
		{"uncatalogued city", "Atlantis", 2, false, 0},
		{"zero guests", "Barcelona", 0, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, known := transferCost(tc.dest, tc.guests)
			if known != tc.wantKnown {
				t.Fatalf("known = %v, want %v", known, tc.wantKnown)
			}
			if known && got != tc.wantTotal {
				t.Errorf("total = %.2f, want %.2f", got, tc.wantTotal)
			}
			if !known && got != 0 {
				t.Errorf("not-found total = %.2f, want 0", got)
			}
		})
	}
}

// TestCityTax covers PLANCOMP.1's city-tax table including the night cap: tax is
// per-person, per-night, and stops accruing after cityTaxNightCap nights.
func TestCityTax(t *testing.T) {
	cases := []struct {
		name      string
		dest      string
		guests    int
		nights    int
		wantKnown bool
		wantTotal float64
	}{
		{"barcelona 3 nights 2 guests", "Barcelona", 2, 3, true, 4.00 * 2 * 3},
		{"night cap applies", "Barcelona", 1, 10, true, 4.00 * cityTaxNightCap},
		{"exactly at cap", "Barcelona", 1, cityTaxNightCap, true, 4.00 * cityTaxNightCap},
		{"uncatalogued city", "Atlantis", 2, 3, false, 0},
		{"zero nights", "Barcelona", 2, 0, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, known := cityTax(tc.dest, tc.guests, tc.nights)
			if known != tc.wantKnown {
				t.Fatalf("known = %v, want %v", known, tc.wantKnown)
			}
			if known && got != tc.wantTotal {
				t.Errorf("total = %.2f, want %.2f", got, tc.wantTotal)
			}
		})
	}
}

// TestBudgetVerdict covers PLANCOMP.2: an unset budget never reports over;
// fitting under budget reports not-over; exceeding budget yields the explicit
// "no package fits" message carrying the cheapest total and the overage.
func TestBudgetVerdict(t *testing.T) {
	t.Run("no budget set", func(t *testing.T) {
		over, overage, msg := budgetVerdict(2500, 0, "EUR")
		if over || overage != 0 || msg != "" {
			t.Errorf("unset budget reported over: %v %.2f %q", over, overage, msg)
		}
	})
	t.Run("fits under budget", func(t *testing.T) {
		over, overage, msg := budgetVerdict(1800, 2000, "EUR")
		if over || overage != 0 || msg != "" {
			t.Errorf("under-budget reported over: %v %.2f %q", over, overage, msg)
		}
	})
	t.Run("exactly at budget fits", func(t *testing.T) {
		over, _, _ := budgetVerdict(2000, 2000, "EUR")
		if over {
			t.Error("total equal to budget should fit, not over")
		}
	})
	t.Run("over budget emits message", func(t *testing.T) {
		over, overage, msg := budgetVerdict(2300, 2000, "EUR")
		if !over {
			t.Fatal("expected over budget")
		}
		if overage != 300 {
			t.Errorf("overage = %.2f, want 300", overage)
		}
		if !strings.Contains(msg, "no package fits") {
			t.Errorf("message missing headline: %q", msg)
		}
		if !strings.Contains(msg, "EUR2300") {
			t.Errorf("message missing cheapest total: %q", msg)
		}
		if !strings.Contains(msg, "EUR300") {
			t.Errorf("message missing overage: %q", msg)
		}
	})
}
