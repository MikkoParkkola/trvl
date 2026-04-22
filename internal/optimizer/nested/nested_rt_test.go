package nested

import (
	"context"
	"testing"
)

// legKey uniquely identifies a leg by its outbound and return dates.
type legKey struct{ date, returnDate string }

// makeMock returns a SearchLegFunc that looks up prices by (date, returnDate).
// Unmatched legs return 50 EUR as a neutral default.
func makeMock(prices map[legKey]float64) SearchLegFunc {
	return func(_ context.Context, leg Leg) (float64, string, error) {
		if p, ok := prices[legKey{leg.Date, leg.ReturnDate}]; ok {
			return p, "EUR", nil
		}
		return 50.0, "EUR", nil
	}
}

// TestSolvePlans_SeparateBeatsOverlap verifies separate-rts wins when cheaper.
func TestSolvePlans_SeparateBeatsOverlap(t *testing.T) {
	visits := []Visit{
		{Start: "2026-05-01", End: "2026-05-05"},
		{Start: "2026-05-10", End: "2026-05-15"},
	}
	prices := map[legKey]float64{
		// separate-rts: 100 + 100 = 200
		{"2026-05-01", "2026-05-05"}: 100,
		{"2026-05-10", "2026-05-15"}: 100,
		// overlap-outer-inner: 300 + 300 = 600
		{"2026-05-01", "2026-05-15"}: 300,
		{"2026-05-10", "2026-05-05"}: 300,
	}
	plans, err := SolvePlans(context.Background(), "AMS", "PRG", visits, "2026-05-15", makeMock(prices))
	if err != nil {
		t.Fatalf("SolvePlans error: %v", err)
	}
	if len(plans) == 0 {
		t.Fatal("no plans returned")
	}
	if plans[0].Name != "separate-rts" {
		t.Errorf("expected cheapest = separate-rts, got %q (%.0f EUR)", plans[0].Name, plans[0].TotalPrice)
	}
}

// TestSolvePlans_OverlapBeatsSeparate verifies overlap-outer-inner wins when cheaper.
func TestSolvePlans_OverlapBeatsSeparate(t *testing.T) {
	visits := []Visit{
		{Start: "2026-05-01", End: "2026-05-05"},
		{Start: "2026-05-10", End: "2026-05-15"},
	}
	prices := map[legKey]float64{
		// separate-rts: 300 + 300 = 600
		{"2026-05-01", "2026-05-05"}: 300,
		{"2026-05-10", "2026-05-15"}: 300,
		// overlap-outer-inner: 80 + 80 = 160
		{"2026-05-01", "2026-05-15"}: 80,
		{"2026-05-10", "2026-05-05"}: 80,
	}
	plans, err := SolvePlans(context.Background(), "AMS", "PRG", visits, "2026-05-15", makeMock(prices))
	if err != nil {
		t.Fatalf("SolvePlans error: %v", err)
	}
	if len(plans) == 0 {
		t.Fatal("no plans returned")
	}
	if plans[0].Name != "overlap-outer-inner" {
		t.Errorf("expected cheapest = overlap-outer-inner, got %q (%.0f EUR)", plans[0].Name, plans[0].TotalPrice)
	}
}

// TestSolvePlans_AllOneWay verifies the all-one-way plan is generated.
func TestSolvePlans_AllOneWay(t *testing.T) {
	visits := []Visit{
		{Start: "2026-06-01", End: "2026-06-05"},
	}
	plans, err := SolvePlans(context.Background(), "HEL", "PRG", visits, "", makeMock(nil))
	if err != nil {
		t.Fatalf("SolvePlans error: %v", err)
	}
	found := false
	for _, p := range plans {
		if p.Name == "all-one-way" {
			found = true
			// single visit → 2 OW legs at 50 EUR each = 100 EUR
			if p.TotalPrice != 100 {
				t.Errorf("all-one-way total = %.0f, want 100", p.TotalPrice)
			}
			if len(p.Legs) != 2 {
				t.Errorf("all-one-way legs = %d, want 2", len(p.Legs))
			}
		}
	}
	if !found {
		t.Error("all-one-way plan not returned")
	}
}

// TestSolvePlans_N3_Overlap checks N=3 visits produce a 3-leg overlap plan.
func TestSolvePlans_N3_Overlap(t *testing.T) {
	visits := []Visit{
		{Start: "2026-07-01", End: "2026-07-05"},
		{Start: "2026-07-10", End: "2026-07-15"},
		{Start: "2026-07-20", End: "2026-07-25"},
	}
	plans, err := SolvePlans(context.Background(), "AMS", "PRG", visits, "2026-07-25", makeMock(nil))
	if err != nil {
		t.Fatalf("SolvePlans error: %v", err)
	}
	found := false
	for _, p := range plans {
		if p.Name == "overlap-outer-inner" {
			found = true
			if len(p.Legs) != 3 {
				t.Errorf("N=3 overlap: expected 3 legs, got %d", len(p.Legs))
			}
			// Outer leg must span full window.
			if p.Legs[0].Date != "2026-07-01" || p.Legs[0].ReturnDate != "2026-07-25" {
				t.Errorf("outer leg mismatch: got %+v", p.Legs[0])
			}
		}
	}
	if !found {
		t.Error("overlap-outer-inner not returned for N=3 visits")
	}
}

// TestSolvePlans_EmptyVisits returns error.
func TestSolvePlans_EmptyVisits(t *testing.T) {
	_, err := SolvePlans(context.Background(), "AMS", "PRG", nil, "", makeMock(nil))
	if err == nil {
		t.Error("expected error for empty visits, got nil")
	}
}

// TestSolvePlans_Sorted verifies plans are sorted cheapest first.
func TestSolvePlans_Sorted(t *testing.T) {
	visits := []Visit{
		{Start: "2026-08-01", End: "2026-08-05"},
		{Start: "2026-08-10", End: "2026-08-15"},
	}
	plans, err := SolvePlans(context.Background(), "AMS", "PRG", visits, "2026-08-15", makeMock(nil))
	if err != nil {
		t.Fatalf("SolvePlans error: %v", err)
	}
	for i := 1; i < len(plans); i++ {
		if plans[i].TotalPrice < plans[i-1].TotalPrice {
			t.Errorf("plans not sorted: [%d]=%.2f < [%d]=%.2f", i, plans[i].TotalPrice, i-1, plans[i-1].TotalPrice)
		}
	}
}

// TestSolvePlans_ReturnHomeOverride verifies returnHome overrides last visit End.
func TestSolvePlans_ReturnHomeOverride(t *testing.T) {
	visits := []Visit{
		{Start: "2026-09-01", End: "2026-09-05"},
	}
	customReturn := "2026-09-10" // different from End
	plans, err := SolvePlans(context.Background(), "AMS", "PRG", visits, customReturn, makeMock(nil))
	if err != nil {
		t.Fatalf("SolvePlans error: %v", err)
	}
	for _, p := range plans {
		if p.Name == "separate-rts" {
			if p.Legs[0].ReturnDate != customReturn {
				t.Errorf("separate-rts should use returnHome=%q, got %q", customReturn, p.Legs[0].ReturnDate)
			}
		}
	}
}
