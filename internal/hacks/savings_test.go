package hacks

import (
	"context"
	"math"
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

func TestBestSaving_BlankCurrencyTrustedOnlyForEUR(t *testing.T) {
	// A detector that leaves Currency blank is assumed to be mid-migration and
	// still emitting EUR. That assumption is honest only when the request is also
	// EUR: the saving surfaces, labelled EUR.
	in := DetectorInput{Origin: "HEL", Destination: "AMS", Date: "2026-09-01", NaivePrice: 200, Currency: "EUR"}
	detect := stubDetect([]Hack{{Type: "split", Savings: 50}}) // no currency on hack
	got := BestSaving(context.Background(), in, detect)
	if got == nil || got.Currency != "EUR" {
		t.Fatalf("Currency = %v, want EUR (blank hack trusted as EUR under migration)", got)
	}
}

func TestBestSaving_DropsBlankCurrencyForNonEURRequest(t *testing.T) {
	// Same blank-currency hack, but the caller asked for GBP. Trusting the blank
	// value would relabel an assumed-EUR constant as GBP — a silent lie. The
	// honest outcome is to drop it and surface nothing.
	in := DetectorInput{Origin: "LHR", Destination: "AMS", Date: "2026-09-01", NaivePrice: 200, Currency: "GBP"}
	detect := stubDetect([]Hack{{Type: "split", Savings: 50}}) // no currency on hack
	if got := BestSaving(context.Background(), in, detect); got != nil {
		t.Fatalf("expected nil (blank currency untrustworthy for non-EUR request), got %+v", got)
	}
}

func TestBestSaving_MatchesCurrencyCaseAndWhitespace(t *testing.T) {
	// Currency comparison must normalise case and surrounding whitespace so a
	// detector emitting " eur " is not spuriously treated as foreign.
	in := DetectorInput{Origin: "HEL", Destination: "AMS", Date: "2026-09-01", NaivePrice: 300, Currency: "eur"}
	detect := stubDetect([]Hack{{Type: "split", Title: "Messy", Savings: 60, Currency: " eur "}})
	got := BestSaving(context.Background(), in, detect)
	if got == nil {
		t.Fatal("expected the normalised-currency hack to win, got nil")
	}
	if got.Currency != "EUR" || got.Savings != 60 || got.Price != 240 {
		t.Errorf("got Currency/Savings/Price = %v/%v/%v, want EUR/60/240", got.Currency, got.Savings, got.Price)
	}
}

func TestBestSaving_RejectsNonFiniteValues(t *testing.T) {
	// NaN/Inf must never reach the JSON headline (encoding/json cannot marshal
	// them). A non-finite baseline yields nil; a non-finite saving is skipped so a
	// finite candidate can still win.
	nan, inf := math.NaN(), math.Inf(1)

	if got := BestSaving(context.Background(),
		DetectorInput{Origin: "HEL", Destination: "AMS", Date: "2026-09-01", NaivePrice: nan, Currency: "EUR"},
		stubDetect([]Hack{{Type: "x", Savings: 50, Currency: "EUR"}})); got != nil {
		t.Fatalf("NaN baseline: expected nil, got %+v", got)
	}
	if got := BestSaving(context.Background(),
		DetectorInput{Origin: "HEL", Destination: "AMS", Date: "2026-09-01", NaivePrice: inf, Currency: "EUR"},
		stubDetect([]Hack{{Type: "x", Savings: 50, Currency: "EUR"}})); got != nil {
		t.Fatalf("Inf baseline: expected nil, got %+v", got)
	}

	in := DetectorInput{Origin: "HEL", Destination: "AMS", Date: "2026-09-01", NaivePrice: 300, Currency: "EUR"}
	detect := stubDetect([]Hack{
		{Type: "poison", Title: "NaN saving", Savings: nan, Currency: "EUR"},
		{Type: "poison2", Title: "Inf saving", Savings: inf, Currency: "EUR"},
		{Type: "split", Title: "Real", Savings: 40, Currency: "EUR"},
	})
	got := BestSaving(context.Background(), in, detect)
	if got == nil || got.Type != "split" || got.Savings != 40 {
		t.Fatalf("expected the finite split hack (40) to win over NaN/Inf, got %+v", got)
	}
}

func TestBestSaving_DropsSavingThatRoundsPriceToZero(t *testing.T) {
	// A saving that leaves a rounded resulting price of zero-or-below is not an
	// honest lower price (100 - 99.6 -> ~0). It must be skipped so a genuinely
	// cheaper hack can win instead of surfacing a free/zero fare.
	in := DetectorInput{Origin: "HEL", Destination: "AMS", Date: "2026-09-01", NaivePrice: 100, Currency: "EUR"}
	detect := stubDetect([]Hack{
		{Type: "rounds_to_zero", Title: "99.6 off 100", Savings: 99.6, Currency: "EUR"},
		{Type: "split", Title: "Honest", Savings: 30, Currency: "EUR"},
	})
	got := BestSaving(context.Background(), in, detect)
	if got == nil {
		t.Fatal("expected the honest 30-saving hack, got nil")
	}
	if got.Type != "split" || got.Savings != 30 {
		t.Errorf("Type/Savings = %q/%v, want split/30 (rounds-to-zero hack must be dropped)", got.Type, got.Savings)
	}
	if got.Price <= 0 {
		t.Errorf("Price = %v, want a positive fare", got.Price)
	}
}

func TestBestSaving_DropsForeignOnlyToNil(t *testing.T) {
	// When every candidate is in a foreign currency, there is nothing that can be
	// honestly compared to the baseline, so the result is nil (not a mislabelled
	// pick).
	in := DetectorInput{Origin: "ARN", Destination: "AMS", Date: "2026-09-01", NaivePrice: 300, Currency: "EUR"}
	detect := stubDetect([]Hack{
		{Type: "positioning", Title: "SEK only", Savings: 250, Currency: "SEK"},
		{Type: "split", Title: "USD only", Savings: 80, Currency: "USD"},
	})
	if got := BestSaving(context.Background(), in, detect); got != nil {
		t.Fatalf("expected nil (all foreign-currency), got %+v", got)
	}
}

func TestBestSaving_DropsForeignCurrencyHack(t *testing.T) {
	// The naive baseline is EUR. A detector reporting its saving in a different
	// currency (SEK) must not be compared against the EUR baseline nor mixed into
	// the headline. Even though its raw Savings (250) is numerically larger than
	// the EUR candidate's (40), currencies are not interchangeable numbers: the
	// SEK hack is dropped and only the EUR candidate can win. Under the old code
	// the SEK hack won on its raw number and yielded a nonsense Price (300-250).
	in := DetectorInput{Origin: "ARN", Destination: "AMS", Date: "2026-09-01", NaivePrice: 300, Currency: "EUR"}
	detect := stubDetect([]Hack{
		{Type: "positioning", Title: "SEK hack", Savings: 250, Currency: "SEK"},
		{Type: "split", Title: "EUR hack", Savings: 40, Currency: "EUR"},
	})
	got := BestSaving(context.Background(), in, detect)
	if got == nil {
		t.Fatal("expected the EUR candidate, got nil")
	}
	if got.Type != "split" {
		t.Errorf("Type = %q, want split (foreign-currency SEK hack must be dropped, not chosen for its larger raw number)", got.Type)
	}
	if got.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR (requested)", got.Currency)
	}
	if got.Savings != 40 || got.Price != 260 {
		t.Errorf("Savings/Price = %v/%v, want 40/260", got.Savings, got.Price)
	}
}
