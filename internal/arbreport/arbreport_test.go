package arbreport

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/cabinarb"
	"github.com/MikkoParkkola/trvl/internal/hotelarb"
)

// fakeEngine builds an EngineFunc returning fixed opportunities / skip reason.
func fakeEngine(opps []Opportunity, skip string) EngineFunc {
	return func(_ context.Context, _ Params) ([]Opportunity, string) {
		return opps, skip
	}
}

func baseParams() Params {
	return Params{
		Origin:      "HEL",
		Destination: "LHR",
		DepartDate:  "2026-08-01",
		Travelers:   2,
		Currency:    "EUR",
	}
}

func TestAggregateCombinesAndRanksBySaving(t *testing.T) {
	eng := Engines{
		Currency: fakeEngine([]Opportunity{
			{Type: "currency_arbitrage", Description: "book in HUF", EstimatedSaving: 40, Currency: "EUR", Confidence: ConfidenceMedium},
		}, ""),
		Cabin: fakeEngine([]Opportunity{
			{Type: "cabin_upgrade", Description: "eco→prem", EstimatedSaving: 120, Currency: "EUR", Confidence: ConfidenceHigh},
		}, ""),
		Hotel: fakeEngine([]Opportunity{
			{Type: "hotel_rebook", Description: "rebook lower", EstimatedSaving: 75, Currency: "EUR", Confidence: ConfidenceHigh},
		}, ""),
	}

	got := AggregateWithEngines(context.Background(), baseParams(), eng)

	if got.Count != 3 {
		t.Fatalf("expected 3 opportunities, got %d", got.Count)
	}
	if len(got.Skipped) != 0 {
		t.Fatalf("expected no skipped engines, got %v", got.Skipped)
	}
	// Ranked by saving desc: cabin(120) > hotel(75) > currency(40).
	wantOrder := []float64{120, 75, 40}
	for i, w := range wantOrder {
		if got.Opportunities[i].EstimatedSaving != w {
			t.Errorf("rank %d: want saving %.0f, got %.0f (%+v)", i, w, got.Opportunities[i].EstimatedSaving, got.Opportunities[i])
		}
	}
	// Engine label is backfilled from the seam name when blank.
	if got.Opportunities[0].Engine != "cabin" {
		t.Errorf("expected top opportunity engine 'cabin', got %q", got.Opportunities[0].Engine)
	}
}

func TestAggregateSkipsUnavailableEnginesGracefully(t *testing.T) {
	eng := Engines{
		Currency: fakeEngine(nil, "N/A: departure date required for currency arbitrage"),
		Cabin: fakeEngine([]Opportunity{
			{Type: "cabin_upgrade", EstimatedSaving: 50},
		}, ""),
		Hotel: fakeEngine(nil, "N/A: no active hotel holds or points offers supplied"),
	}

	got := AggregateWithEngines(context.Background(), baseParams(), eng)

	if got.Count != 1 {
		t.Fatalf("expected 1 opportunity, got %d", got.Count)
	}
	if len(got.Skipped) != 2 {
		t.Fatalf("expected 2 skipped engines, got %d: %v", len(got.Skipped), got.Skipped)
	}
	skipByEngine := map[string]string{}
	for _, s := range got.Skipped {
		skipByEngine[s.Engine] = s.Reason
	}
	if skipByEngine["currency"] == "" || skipByEngine["hotel"] == "" {
		t.Errorf("expected currency and hotel to be skipped with reasons, got %v", got.Skipped)
	}
}

func TestAggregateNoFabricationOnEmpty(t *testing.T) {
	// Every engine skips: report must be empty, with reasons, never invented.
	eng := Engines{
		Currency: fakeEngine(nil, "N/A: no currency arbitrage on this route"),
		Cabin:    fakeEngine(nil, "N/A: no cabin fare ladder supplied for this trip"),
		Hotel:    fakeEngine(nil, "N/A: no active hotel holds or points offers supplied"),
	}

	got := AggregateWithEngines(context.Background(), baseParams(), eng)

	if got.Count != 0 {
		t.Fatalf("expected 0 opportunities, got %d: %+v", got.Count, got.Opportunities)
	}
	if got.Opportunities == nil {
		t.Error("Opportunities must be a non-nil empty slice, not nil")
	}
	if len(got.Skipped) != 3 {
		t.Errorf("expected all 3 engines skipped, got %d: %v", len(got.Skipped), got.Skipped)
	}
}

func TestAggregateEmptyOppsTreatedAsSkip(t *testing.T) {
	// An engine returning empty opps with no skip reason is recorded as a skip,
	// not a silent disappearance.
	eng := Engines{
		Currency: fakeEngine([]Opportunity{}, ""),
		Cabin:    fakeEngine(nil, ""),
		Hotel:    fakeEngine([]Opportunity{{Type: "hotel_points", EstimatedSaving: 10}}, ""),
	}

	got := AggregateWithEngines(context.Background(), baseParams(), eng)

	if got.Count != 1 {
		t.Fatalf("expected 1 opportunity, got %d", got.Count)
	}
	if len(got.Skipped) != 2 {
		t.Errorf("expected 2 skipped (currency+cabin), got %v", got.Skipped)
	}
}

func TestAggregateNilEngineRecordedAsSkip(t *testing.T) {
	eng := Engines{} // all nil
	got := AggregateWithEngines(context.Background(), baseParams(), eng)
	if got.Count != 0 {
		t.Fatalf("expected 0 opportunities, got %d", got.Count)
	}
	if len(got.Skipped) != 3 {
		t.Errorf("expected 3 skipped for nil engines, got %v", got.Skipped)
	}
}

func TestReportCarriesTripContext(t *testing.T) {
	p := baseParams()
	p.ReturnDate = "2026-08-10"
	p.Currency = "gbp"
	got := AggregateWithEngines(context.Background(), p, Engines{})
	if got.Origin != "HEL" || got.Destination != "LHR" {
		t.Errorf("trip endpoints not carried: %+v", got)
	}
	if got.ReturnDate != "2026-08-10" {
		t.Errorf("return date not carried: %q", got.ReturnDate)
	}
	if got.Currency != "GBP" {
		t.Errorf("currency not normalised to upper: %q", got.Currency)
	}
}

// --- default adapter tests (pure engines, fully offline) ---

func TestDefaultCabinEngineRealEngine(t *testing.T) {
	p := baseParams()
	p.CabinFares = []cabinarb.CabinFare{
		{Cabin: cabinarb.CabinEconomy, Price: 500, Currency: "EUR"},
		{Cabin: cabinarb.CabinPremiumEconomy, Price: 540, Currency: "EUR"}, // 8% upsell, within threshold
	}
	opps, skip := defaultCabinEngine(context.Background(), p)
	if skip != "" {
		t.Fatalf("expected cabin opportunity, got skip %q", skip)
	}
	if len(opps) != 1 {
		t.Fatalf("expected 1 cabin opp, got %d", len(opps))
	}
	if opps[0].Type != "cabin_upgrade" || opps[0].Confidence != ConfidenceHigh {
		t.Errorf("unexpected cabin opp: %+v", opps[0])
	}
	// Upsell (target priced above baseline) => no cash saving reported.
	if opps[0].EstimatedSaving != 0 {
		t.Errorf("upsell should report zero saving, got %.2f", opps[0].EstimatedSaving)
	}
}

func TestDefaultCabinEngineStrictUpgradeIsSaving(t *testing.T) {
	p := baseParams()
	p.CabinFares = []cabinarb.CabinFare{
		{Cabin: cabinarb.CabinEconomy, Price: 500},
		{Cabin: cabinarb.CabinPremiumEconomy, Price: 460}, // cheaper than economy
	}
	opps, skip := defaultCabinEngine(context.Background(), p)
	if skip != "" {
		t.Fatalf("expected cabin opportunity, got skip %q", skip)
	}
	if opps[0].EstimatedSaving != 40 {
		t.Errorf("strict upgrade should save 40, got %.2f", opps[0].EstimatedSaving)
	}
}

func TestDefaultCabinEngineSkipsWithoutFares(t *testing.T) {
	_, skip := defaultCabinEngine(context.Background(), baseParams())
	if skip == "" {
		t.Error("expected skip reason when no cabin fares supplied")
	}
}

func TestDefaultHotelEngineRebook(t *testing.T) {
	p := baseParams()
	p.HotelRebooks = []HotelRebook{{
		Hold: hotelarb.Hold{
			ID:            "h1",
			HotelName:     "Grand",
			OriginalPrice: 300,
			Currency:      "EUR",
			Refundable:    true,
		},
		Quote: hotelarb.PriceQuote{Price: 240, Currency: "EUR"},
		Opts:  hotelarb.RebookOptions{MinSavings: 10},
	}}
	opps, skip := defaultHotelEngine(context.Background(), p)
	if skip != "" {
		t.Fatalf("expected hotel opportunity, got skip %q", skip)
	}
	if len(opps) != 1 || opps[0].Type != "hotel_rebook" {
		t.Fatalf("expected 1 hotel_rebook opp, got %+v", opps)
	}
	if opps[0].EstimatedSaving != 60 {
		t.Errorf("expected 60 saving, got %.2f", opps[0].EstimatedSaving)
	}
}

func TestDefaultHotelEngineSkipsWithoutInputs(t *testing.T) {
	_, skip := defaultHotelEngine(context.Background(), baseParams())
	if skip == "" {
		t.Error("expected skip reason when no hotel inputs supplied")
	}
}

func TestDefaultHotelEngineNoProfitableRebook(t *testing.T) {
	p := baseParams()
	p.HotelRebooks = []HotelRebook{{
		Hold:  hotelarb.Hold{ID: "h1", HotelName: "Grand", OriginalPrice: 200, Currency: "EUR", Refundable: true},
		Quote: hotelarb.PriceQuote{Price: 250, Currency: "EUR"}, // higher, no saving
	}}
	_, skip := defaultHotelEngine(context.Background(), p)
	if skip == "" {
		t.Error("expected skip when no profitable rebook")
	}
}

func TestDefaultCurrencyEngineSkipsWithoutDate(t *testing.T) {
	// Offline: missing departure date must short-circuit before any network call.
	p := baseParams()
	p.DepartDate = ""
	_, skip := defaultCurrencyEngine(context.Background(), p)
	if skip == "" {
		t.Error("expected skip reason when departure date missing")
	}
}

func TestDefaultCurrencyEngineSkipsWithoutEndpoints(t *testing.T) {
	p := baseParams()
	p.Origin = ""
	_, skip := defaultCurrencyEngine(context.Background(), p)
	if skip == "" {
		t.Error("expected skip reason when origin missing")
	}
}

func TestDefaultEnginesWired(t *testing.T) {
	e := DefaultEngines()
	if e.Currency == nil || e.Cabin == nil || e.Hotel == nil {
		t.Fatal("DefaultEngines must wire all three engines")
	}
	// Drive through the public Aggregate with inputs that keep every engine
	// fully offline: cabin gets fares, hotel gets a hold, currency skips on
	// the missing-date guard before touching the network.
	p := Params{
		Origin:      "HEL",
		Destination: "LHR",
		// DepartDate intentionally empty -> currency skips offline.
		CabinFares: []cabinarb.CabinFare{
			{Cabin: cabinarb.CabinEconomy, Price: 500},
			{Cabin: cabinarb.CabinPremiumEconomy, Price: 460},
		},
		HotelRebooks: []HotelRebook{{
			Hold:  hotelarb.Hold{ID: "h1", HotelName: "Grand", OriginalPrice: 300, Currency: "EUR", Refundable: true},
			Quote: hotelarb.PriceQuote{Price: 240, Currency: "EUR"},
		}},
	}
	got := AggregateWithEngines(context.Background(), p, DefaultEngines())
	if got.Count != 2 {
		t.Fatalf("expected 2 opportunities (cabin+hotel), got %d: %+v", got.Count, got.Opportunities)
	}
	// hotel saving 60 ranks above cabin saving 40.
	if got.Opportunities[0].Engine != "hotel" {
		t.Errorf("expected hotel first, got %q", got.Opportunities[0].Engine)
	}
}
