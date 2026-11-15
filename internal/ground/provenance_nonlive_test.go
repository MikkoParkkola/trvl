package ground

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// assertNeverLiveThenClassified guards a producer that is NOT on PR A's live
// list: it must not mark anything live itself, and after the merge's
// fail-closed pass every route is estimated (priced) or unavailable (Price 0).
func assertNeverLiveThenClassified(t *testing.T, provider string, routes []models.GroundRoute) {
	t.Helper()
	if len(routes) == 0 {
		t.Fatalf("%s: want routes, got none", provider)
	}
	for i, r := range routes {
		if r.PriceProvenance == models.PriceLive {
			t.Errorf("%s route %d marked live by the producer", provider, i)
		}
	}
	classifyGroundProvenance(routes)
	for i, r := range routes {
		want := models.PriceEstimated
		if r.Price == 0 {
			want = models.PriceUnavailable
		}
		if r.PriceProvenance != want {
			t.Errorf("%s route %d (price %v) provenance after the merge pass = %q, want %q", provider, i, r.Price, r.PriceProvenance, want)
		}
	}
}

func TestNonLiveProducer_StenaLineTable(t *testing.T) {
	orig := stenalineLimiter
	stenalineLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { stenalineLimiter = orig })
	routes, err := SearchStenaLine(context.Background(), "Gothenburg", "Kiel", "2026-07-15", "EUR")
	if err != nil {
		t.Fatalf("SearchStenaLine: %v", err)
	}
	assertNeverLiveThenClassified(t, "stenaline", routes)
}

func TestNonLiveProducer_VikingLineTable(t *testing.T) {
	routes, err := SearchVikingLine(context.Background(), "Helsinki", "Tallinn", "2026-07-15", "EUR")
	if err != nil {
		t.Fatalf("SearchVikingLine: %v", err)
	}
	assertNeverLiveThenClassified(t, "vikingline", routes)
}

// TestNonLiveProducer_Rome2RioRangeLows: Rome2Rio prices are indicative
// range lows, never a fare for one departure.
func TestNonLiveProducer_Rome2RioRangeLows(t *testing.T) {
	body := loadR2RFixture(t, "rome2rio_london_paris.html")
	routes, _, err := parseRome2Rio(body, "London", "Paris")
	if err != nil {
		t.Fatalf("parseRome2Rio: %v", err)
	}
	assertNeverLiveThenClassified(t, "rome2rio", routes)
}

// TestNonLiveProducer_SNCFCalendarDayMinimum: the SNCF calendar answers one
// minimum per day, not a fare for a departure.
func TestNonLiveProducer_SNCFCalendarDayMinimum(t *testing.T) {
	from := SNCFStation{Code: "FRPLY", Name: "Paris Gare de Lyon", City: "Paris"}
	to := SNCFStation{Code: "FRLYS", Name: "Lyon Part-Dieu", City: "Lyon"}
	routes, err := parseSNCFResponse(strings.NewReader(`[{"date":"2026-07-15","price":4500}]`), from, to, "2026-07-15", "EUR")
	if err != nil {
		t.Fatalf("parseSNCFResponse: %v", err)
	}
	assertNeverLiveThenClassified(t, "sncf calendar", routes)
}

// TestNonLiveProducer_EurostarDayCheapest: cheapestFaresSearch returns one
// cheapest fare per date; stamping it on every train that day does not make it
// a per-departure fare. Pending the lead's call on Eurostar's live listing.
func TestNonLiveProducer_EurostarDayCheapest(t *testing.T) {
	var resp eurostarGQLResponse
	if err := json.Unmarshal([]byte(`{"data":{"cheapestFaresSearch":[{"cheapestFares":[{"date":"2026-07-15","price":39}]}]}}`), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	from, to := eurostarStations["london"], eurostarStations["paris"]
	timetable := []eurostarTimetableEntry{
		{DepartureTime: "2026-07-15T07:01:00", ArrivalTime: "2026-07-15T10:20:00"},
		{DepartureTime: "2026-07-15T09:01:00", ArrivalTime: "2026-07-15T12:20:00"},
	}
	for _, tc := range []struct {
		name string
		tt   []eurostarTimetableEntry
	}{{"with timetable", timetable}, {"no timetable", nil}} {
		routes, err := buildEurostarRoutes(resp, from, to, "EUR", false, tc.tt)
		if err != nil {
			t.Fatalf("%s: buildEurostarRoutes: %v", tc.name, err)
		}
		assertNeverLiveThenClassified(t, "eurostar "+tc.name, routes)
	}
}

// TestNonLiveProducer_TrainlineBrowserText: the opt-in browser text scrape is
// not upgraded to live in PR A.
func TestNonLiveProducer_TrainlineBrowserText(t *testing.T) {
	routes := parseTrainlineBrowserText("06:31 09:47 £ 79.90\n08:01 11:18 £ 39.00", "London", "Paris", "2026-04-10", "EUR")
	assertNeverLiveThenClassified(t, "trainline browser text", routes)
}
