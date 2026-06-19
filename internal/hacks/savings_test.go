package hacks

import (
	"context"
	"testing"
)

// stubDetect returns a fixed hack set, ignoring the route — a deterministic
// offline seam so BestSaving's selection/honesty logic is provable without any
// network fan-out.
func stubDetect(hacks []Hack) DetectFunc {
	return func(context.Context, DetectorInput) []Hack { return hacks }
}

func TestBestSaving_SurfacesCheapestRealSaving(t *testing.T) {
	in := DetectorInput{Origin: "HEL", Destination: "AMS", Date: "2026-09-01", NaivePrice: 300, Currency: "EUR"}
	detect := stubDetect([]Hack{
		{Type: "positioning", Title: "Positioning", Savings: 40, Currency: "EUR", Risks: []string{"r1"}},
		{Type: "hidden_city", Title: "Hidden city", Savings: 90, Currency: "EUR", Risks: []string{"contract risk"}, Steps: []string{"s1"}},
		{Type: "split", Title: "Split", Savings: 20, Currency: "EUR"},
	})

	got := BestSaving(context.Background(), in, detect)
	if got == nil {
		t.Fatal("expected a HackSaving, got nil")
	}
	if got.Type != "hidden_city" {
		t.Errorf("Type = %q, want hidden_city (largest real saving)", got.Type)
	}
	if got.Savings != 90 {
		t.Errorf("Savings = %v, want 90", got.Savings)
	}
	if got.NaivePrice != 300 {
		t.Errorf("NaivePrice = %v, want 300", got.NaivePrice)
	}
	if got.Price != 210 {
		t.Errorf("Price = %v, want 210 (300-90)", got.Price)
	}
	if got.SavingsPct != 30 {
		t.Errorf("SavingsPct = %v, want 30.0", got.SavingsPct)
	}
	// Risk caveats must be preserved verbatim (honesty requirement).
	if len(got.Risks) != 1 || got.Risks[0] != "contract risk" {
		t.Errorf("Risks = %v, want [contract risk]", got.Risks)
	}
}

func TestBestSaving_NoSavingWhenNoneReal(t *testing.T) {
	in := DetectorInput{Origin: "HEL", Destination: "AMS", Date: "2026-09-01", NaivePrice: 300}
	// Zero / negative savings are advisory tips, not cheaper options.
	detect := stubDetect([]Hack{
		{Type: "advance_purchase", Title: "Book earlier", Savings: 0},
		{Type: "weird", Savings: -10},
	})
	if got := BestSaving(context.Background(), in, detect); got != nil {
		t.Fatalf("expected nil (no real saving), got %+v", got)
	}
}

func TestBestSaving_RejectsFabricatedFullFareSaving(t *testing.T) {
	in := DetectorInput{Origin: "HEL", Destination: "AMS", Date: "2026-09-01", NaivePrice: 300}
	// A saving >= the whole naive fare cannot be real; it must be dropped, not
	// surfaced as a free/negative price.
	detect := stubDetect([]Hack{
		{Type: "bogus", Title: "Too good", Savings: 300},
		{Type: "bogus2", Title: "Impossible", Savings: 500},
	})
	if got := BestSaving(context.Background(), in, detect); got != nil {
		t.Fatalf("expected nil (fabricated saving rejected), got %+v", got)
	}
}

func TestBestSaving_RequiresPositiveNaivePrice(t *testing.T) {
	in := DetectorInput{Origin: "HEL", Destination: "AMS", Date: "2026-09-01", NaivePrice: 0}
	detect := stubDetect([]Hack{{Type: "x", Savings: 50}})
	if got := BestSaving(context.Background(), in, detect); got != nil {
		t.Fatalf("expected nil when no naive baseline, got %+v", got)
	}
}

func TestBestSaving_DefaultsCurrencyToInput(t *testing.T) {
	in := DetectorInput{Origin: "HEL", Destination: "AMS", Date: "2026-09-01", NaivePrice: 200, Currency: "GBP"}
	detect := stubDetect([]Hack{{Type: "split", Savings: 50}}) // no currency on hack
	got := BestSaving(context.Background(), in, detect)
	if got == nil || got.Currency != "GBP" {
		t.Fatalf("Currency = %v, want GBP from input", got)
	}
}
