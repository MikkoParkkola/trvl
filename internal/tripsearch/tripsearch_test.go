package tripsearch

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// mkPrefs is a test helper returning an in-memory preferences snapshot so we
// never touch disk.
func mkPrefs() *preferences.Preferences {
	return &preferences.Preferences{
		HomeAirports: []string{"AMS"},
		NearbyAirports: map[string][]string{
			"AMS": {"EIN"},
			"HEL": {"TKU", "TMP"},
		},
		EarlyConnectionFloor: "10:00",
		LoungeCards:          []string{"PP"},
	}
}

func TestExpandOrigins_HomeAppliesFan(t *testing.T) {
	prefs := mkPrefs()
	out, err := ExpandOrigins("home", prefs)
	if err != nil {
		t.Fatalf("ExpandOrigins: %v", err)
	}
	got := map[string]bool{}
	for _, o := range out {
		got[o] = true
	}
	if !got["AMS"] || !got["EIN"] {
		t.Errorf("expected AMS + EIN, got %v", out)
	}
}

func TestExpandOrigins_ExplicitStillFans(t *testing.T) {
	prefs := mkPrefs()
	out, err := ExpandOrigins("HEL", prefs)
	if err != nil {
		t.Fatalf("ExpandOrigins: %v", err)
	}
	got := map[string]bool{}
	for _, o := range out {
		got[o] = true
	}
	if !got["HEL"] || !got["TKU"] || !got["TMP"] {
		t.Errorf("explicit HEL should fan to TKU/TMP, got %v", out)
	}
}

func TestAddRailFlyOrigins_AMSAddsBelgium(t *testing.T) {
	out := AddRailFlyOrigins([]string{"AMS"})
	want := map[string]bool{"AMS": true, "ZYR": true, "ANR": true, "BRU": true}
	for k := range want {
		found := false
		for _, o := range out {
			if o == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing %s from rail-fly expansion: %v", k, out)
		}
	}
}

func TestAddRailFlyOrigins_NoAMSNoChange(t *testing.T) {
	in := []string{"HEL", "TKU"}
	out := AddRailFlyOrigins(in)
	if len(out) != len(in) {
		t.Errorf("non-AMS should be unchanged: %v", out)
	}
}

func TestApplyFilters_LoungeRequiredDropsUncovered(t *testing.T) {
	prefs := mkPrefs()
	flts := []models.FlightResult{
		{
			Price: 100,
			Legs: []models.FlightLeg{
				{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "XYZ"}, DepartureTime: "2026-06-01T18:00"},
				{DepartureAirport: models.AirportInfo{Code: "XYZ"}, ArrivalAirport: models.AirportInfo{Code: "PRG"}, DepartureTime: "2026-06-01T21:00", LayoverMinutes: 180},
			},
		},
	}
	req := Request{LoungeRequired: true}
	out, log := ApplyFilters(flts, req, prefs)
	if log.LoungeAccess.Dropped != 1 {
		t.Errorf("expected 1 drop by lounge filter, got %+v", log.LoungeAccess)
	}
	if len(out) != 0 {
		t.Errorf("expected all flights dropped, got %d", len(out))
	}
}

func TestApplyFilters_NoEarlyConnectionDropsEarly(t *testing.T) {
	prefs := mkPrefs()
	flts := []models.FlightResult{
		// Overnight 10h at AMS, next leg 08:00 — too early.
		{
			Price: 100,
			Legs: []models.FlightLeg{
				{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "AMS"}, DepartureTime: "2026-06-01T22:00"},
				{DepartureAirport: models.AirportInfo{Code: "AMS"}, ArrivalAirport: models.AirportInfo{Code: "PRG"}, DepartureTime: "2026-06-02T08:00", LayoverMinutes: 600},
			},
		},
	}
	req := Request{NoEarlyConnection: true}
	out, log := ApplyFilters(flts, req, prefs)
	if log.NoEarlyConnection.Dropped != 1 {
		t.Errorf("expected 1 drop, got %+v", log.NoEarlyConnection)
	}
	if len(out) != 0 {
		t.Errorf("early-morning connection should be dropped")
	}
}

func TestSearch_FullPipelineWithFakeSearch(t *testing.T) {
	prefs := mkPrefs()

	// Fake search returns two bundles at different prices; cheaper should
	// come first after ranking.
	fakeSearch := func(ctx context.Context, origins, dests []string, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		return &models.FlightSearchResult{
			Success:  true,
			TripType: "round_trip",
			Flights: []models.FlightResult{
				{Price: 200, Currency: "EUR", Legs: []models.FlightLeg{
					{DepartureAirport: models.AirportInfo{Code: "AMS"}, ArrivalAirport: models.AirportInfo{Code: "PRG"}, DepartureTime: "2026-04-23T14:00"},
				}},
				{Price: 150, Currency: "EUR", Legs: []models.FlightLeg{
					{DepartureAirport: models.AirportInfo{Code: "BRU"}, ArrivalAirport: models.AirportInfo{Code: "PRG"}, DepartureTime: "2026-04-23T16:30"},
				}},
			},
			Count: 2,
		}, nil
	}

	progressSeen := []string{}
	progress := func(stage string, _, _ int) {
		progressSeen = append(progressSeen, stage)
	}

	req := Request{
		Origin:              "home",
		Destination:         "PRG",
		Date:                "2026-04-23",
		ReturnDate:          "2026-06-03",
		TopN:                2,
		PreferencesOverride: prefs,
	}
	res, err := Search(context.Background(), req, fakeSearch, progress)
	if err != nil {
		t.Fatalf("Hunt: %v", err)
	}
	if res.Count != 2 {
		t.Errorf("expected 2 flights, got %d", res.Count)
	}
	if res.Flights[0].Price != 150 {
		t.Errorf("cheapest should rank first, got %.0f", res.Flights[0].Price)
	}
	// Origins should include rail-fly expansion since AMS is in home prefs.
	haveBRU := false
	for _, o := range res.Origins {
		if o == "BRU" {
			haveBRU = true
		}
	}
	if !haveBRU {
		t.Errorf("rail-fly expansion missing from Origins: %v", res.Origins)
	}
	if len(progressSeen) < 3 {
		t.Errorf("expected progress callbacks, saw %v", progressSeen)
	}
}

func TestExpandOrigins_AffinityIncludedAtThreshold(t *testing.T) {
	prefs := mkPrefs()
	prefs.AirportAffinity = map[string]int{"CDG": 3, "LIS": 2}

	out, err := ExpandOrigins("home", prefs)
	if err != nil {
		t.Fatalf("ExpandOrigins: %v", err)
	}
	got := map[string]bool{}
	for _, o := range out {
		got[o] = true
	}
	if !got["CDG"] {
		t.Errorf("CDG (affinity=3) should be included in home fan-out, got %v", out)
	}
	if got["LIS"] {
		t.Errorf("LIS (affinity=2) should NOT be included (below threshold), got %v", out)
	}
}

func TestExpandOrigins_AffinityNotAppliedToExplicitOrigin(t *testing.T) {
	prefs := mkPrefs()
	prefs.AirportAffinity = map[string]int{"CDG": 10}

	// Explicit "HEL" — affinity fan-out must NOT apply.
	out, err := ExpandOrigins("HEL", prefs)
	if err != nil {
		t.Fatalf("ExpandOrigins: %v", err)
	}
	got := map[string]bool{}
	for _, o := range out {
		got[o] = true
	}
	if got["CDG"] {
		t.Errorf("CDG should not appear when origin is explicit 'HEL', got %v", out)
	}
}

func TestRouteSummary(t *testing.T) {
	f := models.FlightResult{Legs: []models.FlightLeg{
		{DepartureAirport: models.AirportInfo{Code: "BRU"}, ArrivalAirport: models.AirportInfo{Code: "AMS"}},
		{DepartureAirport: models.AirportInfo{Code: "AMS"}, ArrivalAirport: models.AirportInfo{Code: "PRG"}},
	}}
	if got := RouteSummary(f); got != "BRU→AMS→PRG" {
		t.Errorf("RouteSummary: %q", got)
	}
}

func TestAnnotations_RailFlyTagged(t *testing.T) {
	f := models.FlightResult{Legs: []models.FlightLeg{
		{DepartureAirport: models.AirportInfo{Code: "BRU"}, ArrivalAirport: models.AirportInfo{Code: "PRG"}},
	}}
	if got := Annotations(f, []string{"AMS", "BRU"}); got != "[rail+fly]" {
		t.Errorf("expected [rail+fly], got %q", got)
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{{"12h", 720}, {"90m", 90}, {"", 0}, {"garbage", 0}}
	for _, c := range cases {
		if got := ParseDuration(c.in); got != c.want {
			t.Errorf("ParseDuration(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
