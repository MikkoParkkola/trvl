package flights

import (
	"context"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// fakePricer returns a synthetic segmentPricer backed by a route->results map,
// keyed by "ORIGIN-DEST". A missing key yields zero candidates (unpriceable).
func fakePricer(by map[string][]models.FlightResult) segmentPricer {
	return func(_ context.Context, origin, dest, _ string, _ SearchOptions) ([]models.FlightResult, error) {
		return by[origin+"-"+dest], nil
	}
}

// gflight builds a synthetic Google Flights single-segment candidate departing
// at the given "2006-01-02T15:04" local time.
func gflight(origin, dest, depart string, price float64, currency string) models.FlightResult {
	return models.FlightResult{
		Price:    price,
		Currency: currency,
		Provider: "google_flights",
		Legs: []models.FlightLeg{{
			DepartureAirport: models.AirportInfo{Code: origin},
			ArrivalAirport:   models.AirportInfo{Code: dest},
			DepartureTime:    depart,
		}},
	}
}

// helInBcnItinerary builds a Kiwi-shaped self-connect HEL->IST->BCN itinerary
// directly from the mapped-itinerary path so the test exercises the real
// layover->segment decomposition (XSHOP.1).
func helIstBcnKiwi(bundledPrice float64) models.FlightResult {
	itin := kiwiItinerary{
		FlyFrom:   "HEL",
		FlyTo:     "BCN",
		CityFrom:  "Helsinki",
		CityTo:    "Barcelona",
		Departure: kiwiDateTime{Local: "2026-07-01T08:00:00", UTC: "2026-07-01T05:00:00Z"},
		Arrival:   kiwiDateTime{Local: "2026-07-01T18:00:00", UTC: "2026-07-01T16:00:00Z"},
		Price:     bundledPrice,
		Currency:  "EUR",
		Layovers: []kiwiLayover{{
			At:        "IST",
			City:      "Istanbul",
			CityCode:  "IST",
			Arrival:   kiwiDateTime{Local: "2026-07-01T11:00:00", UTC: "2026-07-01T08:00:00Z"},
			Departure: kiwiDateTime{Local: "2026-07-01T13:00:00", UTC: "2026-07-01T10:00:00Z"},
		}},
	}
	return mapKiwiItinerary(itin, "EUR")
}

// TestDecomposeSegments_FromLayovers proves segment decomposition derives the
// ordered route + per-segment date/time from flyFrom + layovers + flyTo, with
// NO reliance on carrier data (MIK-4956 XSHOP.1, test (a)).
func TestDecomposeSegments_FromLayovers(t *testing.T) {
	f := helIstBcnKiwi(300)
	segs := decomposeSegments(f)

	if len(segs) != 2 {
		t.Fatalf("want 2 segments (HEL->IST, IST->BCN), got %d", len(segs))
	}
	if segs[0].Origin != "HEL" || segs[0].Dest != "IST" {
		t.Errorf("segment 0 = %s->%s, want HEL->IST", segs[0].Origin, segs[0].Dest)
	}
	if segs[1].Origin != "IST" || segs[1].Dest != "BCN" {
		t.Errorf("segment 1 = %s->%s, want IST->BCN", segs[1].Origin, segs[1].Dest)
	}
	if segs[0].Date != "2026-07-01" || segs[1].Date != "2026-07-01" {
		t.Errorf("segment dates = %q, %q, want both 2026-07-01", segs[0].Date, segs[1].Date)
	}
	// Segment 0 departs at the itinerary departure (08:00); segment 1 departs
	// at the layover departure (13:00) — proving times come from the layover.
	if !segs[0].HasTime || segs[0].DepartAt.Hour() != 8 {
		t.Errorf("segment 0 depart hour = %d, want 8", segs[0].DepartAt.Hour())
	}
	if !segs[1].HasTime || segs[1].DepartAt.Hour() != 13 {
		t.Errorf("segment 1 depart hour = %d, want 13 (from layover departure)", segs[1].DepartAt.Hour())
	}
}

// TestDecomposeSegments_SingleSegmentSkipped proves a non-stop itinerary is not
// eligible for cross-shop decomposition (nothing to re-price separately).
func TestDecomposeSegments_SingleSegmentSkipped(t *testing.T) {
	direct := models.FlightResult{
		Currency: "EUR",
		Legs: []models.FlightLeg{{
			DepartureAirport: models.AirportInfo{Code: "HEL"},
			ArrivalAirport:   models.AirportInfo{Code: "BCN"},
			DepartureTime:    "2026-07-01T08:00",
		}},
	}
	if segs := decomposeSegments(direct); segs != nil {
		t.Fatalf("single-segment itinerary should not decompose, got %d segments", len(segs))
	}
}

// TestEnrichCrossShop_BookDirectWhenAllPricedAndCheaper proves the book-direct
// alternative is assembled with per-segment PriceSources, a SUMMED price, and a
// positive Savings vs the Kiwi-bundled fare when it is cheaper
// (MIK-4956 XSHOP.4 happy path + XSHOP.5 comparison, test (b)).
func TestEnrichCrossShop_BookDirectWhenAllPricedAndCheaper(t *testing.T) {
	bundled := 300.0
	f := helIstBcnKiwi(bundled)

	pricer := fakePricer(map[string][]models.FlightResult{
		"HEL-IST": {gflight("HEL", "IST", "2026-07-01T08:30", 90, "EUR")},  // within window of 08:00
		"IST-BCN": {gflight("IST", "BCN", "2026-07-01T13:15", 110, "EUR")}, // within window of 13:00
	})

	alts, status := enrichCrossShop(context.Background(), []models.FlightResult{f}, pricer, crossShopDefaultWindow, SearchOptions{})

	if status.Status != models.StatusCheckedHit {
		t.Fatalf("status = %q, want checked_hit", status.Status)
	}
	if len(alts) != 1 {
		t.Fatalf("want 1 book-direct alternative, got %d", len(alts))
	}
	alt := alts[0]
	if !alt.BookDirect {
		t.Error("alternative not flagged BookDirect")
	}
	if want := 90.0 + 110.0; alt.Price != want {
		t.Errorf("summed price = %.2f, want %.2f", alt.Price, want)
	}
	if len(alt.SegmentSources) != 2 {
		t.Fatalf("want 2 segment sources, got %d", len(alt.SegmentSources))
	}
	if alt.SegmentSources[0].Price != 90 || alt.SegmentSources[1].Price != 110 {
		t.Errorf("segment prices = %.0f, %.0f; want 90, 110", alt.SegmentSources[0].Price, alt.SegmentSources[1].Price)
	}
	// XSHOP.5: comparison surfaced — Kiwi 300 vs book-direct 200 => savings 100.
	if want := bundled - 200.0; alt.Savings != want {
		t.Errorf("savings = %.2f, want %.2f", alt.Savings, want)
	}
	if alt.CheapestSource != crossShopProviderID {
		t.Errorf("cheapest source = %q, want %q", alt.CheapestSource, crossShopProviderID)
	}
}

// TestEnrichCrossShop_SelfConnectWarning proves the missed-connection risk
// warning is carried on every book-direct alternative (test (d)).
func TestEnrichCrossShop_SelfConnectWarning(t *testing.T) {
	f := helIstBcnKiwi(300)
	pricer := fakePricer(map[string][]models.FlightResult{
		"HEL-IST": {gflight("HEL", "IST", "2026-07-01T08:30", 90, "EUR")},
		"IST-BCN": {gflight("IST", "BCN", "2026-07-01T13:15", 110, "EUR")},
	})

	alts, _ := enrichCrossShop(context.Background(), []models.FlightResult{f}, pricer, crossShopDefaultWindow, SearchOptions{})
	if len(alts) != 1 {
		t.Fatalf("want 1 alternative, got %d", len(alts))
	}
	if !alts[0].SelfConnect {
		t.Error("book-direct alternative must be flagged SelfConnect")
	}
	found := false
	for _, w := range alts[0].Warnings {
		if w == crossShopSelfConnectWarning {
			found = true
		}
	}
	if !found {
		t.Errorf("self-connect warning missing; warnings = %v", alts[0].Warnings)
	}
}

// TestEnrichCrossShop_NoFabricationWhenSegmentUnpriceable proves the
// no-fabrication guard: when a segment cannot be priced, NO book-direct total is
// emitted and the status is non-definitive so completeness goes partial
// (MIK-4956 XSHOP.4, test (c)).
func TestEnrichCrossShop_NoFabricationWhenSegmentUnpriceable(t *testing.T) {
	f := helIstBcnKiwi(300)
	// Only the first segment prices; IST-BCN is missing from the map.
	pricer := fakePricer(map[string][]models.FlightResult{
		"HEL-IST": {gflight("HEL", "IST", "2026-07-01T08:30", 90, "EUR")},
	})

	alts, status := enrichCrossShop(context.Background(), []models.FlightResult{f}, pricer, crossShopDefaultWindow, SearchOptions{})

	if len(alts) != 0 {
		t.Fatalf("no book-direct should be emitted when a segment is unpriceable, got %d", len(alts))
	}
	if status.Status != models.StatusFailed {
		t.Fatalf("status = %q, want failed (drives partial completeness)", status.Status)
	}

	// Drive the completeness envelope as the search would: Google + Kiwi
	// definitive, the cross-shop enricher failed => overall partial.
	statuses := []models.ProviderStatus{
		{ID: "google_flights", Name: "Google Flights", Status: models.StatusCheckedHit, Results: 5},
		{ID: "kiwi", Name: "Kiwi", Status: models.StatusCheckedHit, Results: 3},
		status,
	}
	c := models.ComputeCompleteness(statuses)
	if c.State != models.CompletenessPartial {
		t.Errorf("completeness = %q, want partial", c.State)
	}
	if c.MayClaimExhaustive() {
		t.Error("partial search must not claim exhaustive")
	}
}

// TestEnrichCrossShop_OffWindowSegmentUnpriceable proves a candidate outside the
// departure-time window is NOT substituted (it is a different flight than the
// routed segment), so the segment is unpriceable and no total is fabricated.
func TestEnrichCrossShop_OffWindowSegmentUnpriceable(t *testing.T) {
	f := helIstBcnKiwi(300)
	pricer := fakePricer(map[string][]models.FlightResult{
		"HEL-IST": {gflight("HEL", "IST", "2026-07-01T08:30", 90, "EUR")},
		// IST-BCN candidate departs 22:00 — far outside the 13:00 +/- 3h window.
		"IST-BCN": {gflight("IST", "BCN", "2026-07-01T22:00", 110, "EUR")},
	})

	alts, status := enrichCrossShop(context.Background(), []models.FlightResult{f}, pricer, crossShopDefaultWindow, SearchOptions{})
	if len(alts) != 0 {
		t.Fatalf("off-window candidate must not be matched, got %d alternatives", len(alts))
	}
	if status.Status != models.StatusFailed {
		t.Errorf("status = %q, want failed", status.Status)
	}
}

// TestEnrichCrossShop_CrossCurrencyUnpriceable proves a segment priced in a
// different currency is not summed (a cross-currency total would be fabricated).
func TestEnrichCrossShop_CrossCurrencyUnpriceable(t *testing.T) {
	f := helIstBcnKiwi(300)
	pricer := fakePricer(map[string][]models.FlightResult{
		"HEL-IST": {gflight("HEL", "IST", "2026-07-01T08:30", 90, "EUR")},
		"IST-BCN": {gflight("IST", "BCN", "2026-07-01T13:15", 110, "USD")}, // wrong currency
	})

	alts, status := enrichCrossShop(context.Background(), []models.FlightResult{f}, pricer, crossShopDefaultWindow, SearchOptions{})
	if len(alts) != 0 {
		t.Fatalf("cross-currency segment must not be summed, got %d alternatives", len(alts))
	}
	if status.Status != models.StatusFailed {
		t.Errorf("status = %q, want failed", status.Status)
	}
}

// TestEnrichCrossShop_NoEligibleItinerariesSkipped proves that when no itinerary
// is multi-stop, the enricher reports skipped (does not touch completeness).
func TestEnrichCrossShop_NoEligibleItinerariesSkipped(t *testing.T) {
	direct := models.FlightResult{
		Currency: "EUR",
		Legs: []models.FlightLeg{{
			DepartureAirport: models.AirportInfo{Code: "HEL"},
			ArrivalAirport:   models.AirportInfo{Code: "BCN"},
			DepartureTime:    "2026-07-01T08:00",
		}},
	}
	alts, status := enrichCrossShop(context.Background(), []models.FlightResult{direct}, fakePricer(nil), crossShopDefaultWindow, SearchOptions{})
	if len(alts) != 0 {
		t.Fatalf("want no alternatives, got %d", len(alts))
	}
	if status.Status != models.StatusSkipped {
		t.Errorf("status = %q, want skipped", status.Status)
	}
	// Skipped must not degrade completeness.
	c := models.ComputeCompleteness([]models.ProviderStatus{
		{ID: "google_flights", Status: models.StatusCheckedHit, Results: 1},
		status,
	})
	if c.State != models.CompletenessComplete {
		t.Errorf("completeness = %q, want complete (skipped does not gate)", c.State)
	}
}

// TestEnrichCrossShop_MoreExpensiveStillSurfaced proves XSHOP.5 surfaces the
// comparison even when book-direct is dearer (negative savings, Kiwi cheapest);
// the alternative is not suppressed.
func TestEnrichCrossShop_MoreExpensiveStillSurfaced(t *testing.T) {
	bundled := 150.0
	f := helIstBcnKiwi(bundled)
	pricer := fakePricer(map[string][]models.FlightResult{
		"HEL-IST": {gflight("HEL", "IST", "2026-07-01T08:30", 120, "EUR")},
		"IST-BCN": {gflight("IST", "BCN", "2026-07-01T13:15", 130, "EUR")},
	})

	alts, _ := enrichCrossShop(context.Background(), []models.FlightResult{f}, pricer, crossShopDefaultWindow, SearchOptions{})
	if len(alts) != 1 {
		t.Fatalf("want 1 alternative even when dearer, got %d", len(alts))
	}
	if alts[0].Price != 250 {
		t.Errorf("summed price = %.2f, want 250", alts[0].Price)
	}
	if want := bundled - 250.0; alts[0].Savings != want {
		t.Errorf("savings = %.2f, want %.2f (negative)", alts[0].Savings, want)
	}
	if alts[0].CheapestSource != "kiwi" {
		t.Errorf("cheapest source = %q, want kiwi (the dearer book-direct must not claim cheapest)", alts[0].CheapestSource)
	}
}

// TestEnrichCrossShop_SingleTicketConnectionSkipped proves a multi-leg
// single-ticket through-fare (SelfConnect=false, e.g. a Google/Ryanair
// connection with through-fare protection) is NOT cross-shopped — only Kiwi's
// self-connect itineraries are the target. Otherwise we would discard through-
// fare protection for no reason.
func TestEnrichCrossShop_SingleTicketConnectionSkipped(t *testing.T) {
	through := models.FlightResult{
		Currency:    "EUR",
		Provider:    "google_flights",
		SelfConnect: false, // single ticket — protected connection
		Legs: []models.FlightLeg{
			{
				DepartureAirport: models.AirportInfo{Code: "HEL"},
				ArrivalAirport:   models.AirportInfo{Code: "IST"},
				DepartureTime:    "2026-07-01T08:00",
			},
			{
				DepartureAirport: models.AirportInfo{Code: "IST"},
				ArrivalAirport:   models.AirportInfo{Code: "BCN"},
				DepartureTime:    "2026-07-01T13:00",
			},
		},
	}
	pricer := fakePricer(map[string][]models.FlightResult{
		"HEL-IST": {gflight("HEL", "IST", "2026-07-01T08:30", 90, "EUR")},
		"IST-BCN": {gflight("IST", "BCN", "2026-07-01T13:15", 110, "EUR")},
	})

	alts, status := enrichCrossShop(context.Background(), []models.FlightResult{through}, pricer, crossShopDefaultWindow, SearchOptions{})
	if len(alts) != 0 {
		t.Fatalf("single-ticket through-fare must not be cross-shopped, got %d alternatives", len(alts))
	}
	if status.Status != models.StatusSkipped {
		t.Errorf("status = %q, want skipped (no eligible self-connect itinerary)", status.Status)
	}
}

// TestCrossShopEnabled_DefaultOff proves the enricher is gated off by default so
// it never alters existing booking flows without explicit opt-in.
func TestCrossShopEnabled_DefaultOff(t *testing.T) {
	t.Setenv("TRVL_CROSSSHOP_ENRICH", "")
	if crossShopEnabled() {
		t.Error("cross-shop must be off by default")
	}
	t.Setenv("TRVL_CROSSSHOP_ENRICH", "1")
	if !crossShopEnabled() {
		t.Error("cross-shop must enable with TRVL_CROSSSHOP_ENRICH=1")
	}
}

// TestPriceSegment_RejectsZeroPrice proves a zero-price candidate is not treated
// as a valid re-price (it would corrupt the sum).
func TestPriceSegment_RejectsZeroPrice(t *testing.T) {
	seg := flightSegment{Origin: "HEL", Dest: "IST", Date: "2026-07-01", DepartAt: mustTime("2026-07-01T08:00"), HasTime: true}
	pricer := fakePricer(map[string][]models.FlightResult{
		"HEL-IST": {gflight("HEL", "IST", "2026-07-01T08:10", 0, "EUR")},
	})
	if _, ok := priceSegment(context.Background(), pricer, seg, "EUR", crossShopDefaultWindow, SearchOptions{}); ok {
		t.Error("zero-price candidate must not price a segment")
	}
}

func mustTime(s string) time.Time {
	t, err := time.Parse(flightTimeLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}
