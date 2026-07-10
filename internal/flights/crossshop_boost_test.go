package flights

import (
	"context"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// crossshop_boost_test.go — additional coverage for departsWithinWindow (boundary), decompose edge cases,
// and enrichCrossShop failure/partial paths using the existing fakePricer seam (no network).

func TestDepartsWithinWindow_Boundaries(t *testing.T) {
	target := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	window := 3 * time.Hour

	// exact within
	c := gflight("HEL", "IST", "2026-07-01T08:00", 100, "EUR")
	if !departsWithinWindow(c, target, window) {
		t.Error("exact depart should be within")
	}
	// +3h boundary (inclusive)
	c = gflight("HEL", "IST", "2026-07-01T11:00", 100, "EUR")
	if !departsWithinWindow(c, target, window) {
		t.Error("+3h boundary should be within (<=)")
	}
	// +3h1m out
	c = gflight("HEL", "IST", "2026-07-01T11:01", 100, "EUR")
	if departsWithinWindow(c, target, window) {
		t.Error("+3h1m should be out")
	}
	// -3h boundary
	c = gflight("HEL", "IST", "2026-07-01T05:00", 100, "EUR")
	if !departsWithinWindow(c, target, window) {
		t.Error("-3h boundary ok")
	}
	// bad time parse -> false
	c = models.FlightResult{Legs: []models.FlightLeg{{DepartureTime: "bad"}}}
	if departsWithinWindow(c, target, window) {
		t.Error("unparseable depart must fail closed")
	}
	// zero time
	c = models.FlightResult{}
	if departsWithinWindow(c, target, window) {
		t.Error("zero depart false")
	}
}

func TestDecomposeSegments_Edges(t *testing.T) {
	// missing codes -> still produces segment (fail closed later)
	f := models.FlightResult{
		SelfConnect: true,
		Legs: []models.FlightLeg{
			{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: ""}, DepartureTime: "2026-07-01T08:00"},
			{DepartureAirport: models.AirportInfo{Code: ""}, ArrivalAirport: models.AirportInfo{Code: "BCN"}, DepartureTime: "2026-07-01T12:00"},
		},
	}
	segs := decomposeSegments(f)
	if len(segs) != 2 {
		t.Fatalf("want 2 even with missing, got %d", len(segs))
	}

	// bad time still records Date? no, but HasTime false, segment kept for count
	f2 := models.FlightResult{
		SelfConnect: true,
		Legs: []models.FlightLeg{
			{DepartureAirport: models.AirportInfo{Code: "A"}, ArrivalAirport: models.AirportInfo{Code: "B"}, DepartureTime: "badtime"},
			{DepartureAirport: models.AirportInfo{Code: "B"}, ArrivalAirport: models.AirportInfo{Code: "C"}, DepartureTime: "2026-07-01T10:00"},
		},
	}
	segs2 := decomposeSegments(f2)
	if len(segs2) != 2 || segs2[0].HasTime {
		t.Errorf("bad time seg should have HasTime=false but segment present: %+v", segs2[0])
	}
}

func TestEnrichCrossShop_PartialsAndBoundaries(t *testing.T) {
	f := helIstBcnKiwi(300)

	// window boundary exact +3h should match (use gflight with time at boundary)
	pricer := fakePricer(map[string][]models.FlightResult{
		"HEL-IST": {gflight("HEL", "IST", "2026-07-01T11:00", 95, "EUR")},  // exactly +3h from 08:00
		"IST-BCN": {gflight("IST", "BCN", "2026-07-01T16:00", 105, "EUR")}, // +3h from 13:00
	})
	alts, st := enrichCrossShop(context.Background(), []models.FlightResult{f}, pricer, crossShopDefaultWindow, SearchOptions{})
	if len(alts) != 1 || st.Status != models.StatusCheckedHit {
		t.Errorf("boundary window should price: alts=%d status=%s", len(alts), st.Status)
	}

	// non self connect skipped
	plain := models.FlightResult{Provider: "google_flights", Legs: []models.FlightLeg{{}, {}}}
	_, st = enrichCrossShop(context.Background(), []models.FlightResult{plain}, pricer, 0, SearchOptions{})
	if st.Status != models.StatusSkipped {
		t.Errorf("no eligible selfconnect -> skipped, got %s", st.Status)
	}

	// boundary test exercising departsWithinWindow via enrich: within-window segment produces alt (priceable);
	// 1m off with tight window is unpriceable -> no alt + StatusFailed (real status for partial case).
	// Note: window<=0 is normalized to default inside enrichCrossShop, so use positive durations for tight windows.
	pricerWithin := fakePricer(map[string][]models.FlightResult{
		"HEL-IST": {gflight("HEL", "IST", "2026-07-01T08:00", 90, "EUR")}, // exact match, within 1m window
		"IST-BCN": {gflight("IST", "BCN", "2026-07-01T13:00", 100, "EUR")},
	})
	alts, st = enrichCrossShop(context.Background(), []models.FlightResult{f}, pricerWithin, 1*time.Minute, SearchOptions{})
	if len(alts) != 1 || st.Status != models.StatusCheckedHit {
		t.Errorf("within-window should produce alt + checked_hit: alts=%d status=%s", len(alts), st.Status)
	}

	pricerOff := fakePricer(map[string][]models.FlightResult{
		"HEL-IST": {gflight("HEL", "IST", "2026-07-01T08:01", 90, "EUR")}, // 1m off -> out for 30s window
		"IST-BCN": {gflight("IST", "BCN", "2026-07-01T13:00", 100, "EUR")},
	})
	alts, st = enrichCrossShop(context.Background(), []models.FlightResult{f}, pricerOff, 30*time.Second, SearchOptions{})
	if len(alts) != 0 || st.Status != models.StatusFailed {
		t.Errorf("off-by-1m with tight window should yield 0 alts + failed: alts=%d status=%s", len(alts), st.Status)
	}
}
