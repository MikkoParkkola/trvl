package travelctx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// fixedClock is a deterministic Clock for tests.
type fixedClock struct{ t time.Time }

func (f fixedClock) Now() time.Time { return f.t }

// stubGeo is a deterministic GeoResolver for tests; never touches the network.
type stubGeo struct {
	loc Location
	err error
}

func (s stubGeo) Resolve(context.Context) (Location, error) { return s.loc, s.err }

func helsinki() *time.Location {
	loc, err := time.LoadLocation("Europe/Helsinki")
	if err != nil {
		return time.UTC
	}
	return loc
}

func TestResolve_ExplicitOriginWins(t *testing.T) {
	prefs := &preferences.Preferences{HomeAirports: []string{"AMS"}}
	clk := fixedClock{t: time.Date(2026, 5, 31, 19, 0, 0, 0, helsinki())}
	// Even with prefs set and geo allowed, an explicit origin must win and
	// must NOT trigger any detection (geo stub would error if consulted).
	got := Resolve(context.Background(), prefs, Options{
		ExplicitOrigin: "hel",
		AllowGeoIP:     true,
		Clock:          clk,
		Geo:            stubGeo{err: errors.New("geo must not be called")},
	})
	if got.Origin.Airport != "HEL" {
		t.Fatalf("airport = %q, want HEL", got.Origin.Airport)
	}
	if got.Origin.Source != SourceExplicit {
		t.Fatalf("source = %q, want explicit", got.Origin.Source)
	}
}

func TestResolve_PrefsBeatsGeo(t *testing.T) {
	prefs := &preferences.Preferences{
		HomeAirports: []string{"HEL"},
		HomeCities:   []string{"Helsinki"},
	}
	clk := fixedClock{t: time.Now()}
	got := Resolve(context.Background(), prefs, Options{
		AllowGeoIP: true,
		Clock:      clk,
		Geo:        stubGeo{loc: Location{Airport: "AMS", City: "Amsterdam"}},
	})
	if got.Origin.Airport != "HEL" {
		t.Fatalf("airport = %q, want HEL (prefs beats geo)", got.Origin.Airport)
	}
	if got.Origin.Source != SourcePrefs {
		t.Fatalf("source = %q, want preferences", got.Origin.Source)
	}
	if got.Origin.City != "Helsinki" {
		t.Fatalf("city = %q, want Helsinki", got.Origin.City)
	}
}

func TestResolve_GeoFallbackWhenNoPrefs(t *testing.T) {
	clk := fixedClock{t: time.Now()}
	got := Resolve(context.Background(), &preferences.Preferences{}, Options{
		AllowGeoIP: true,
		Clock:      clk,
		Geo:        stubGeo{loc: Location{City: "Barcelona", Country: "ES"}},
	})
	if got.Origin.Airport != "BCN" {
		t.Fatalf("airport = %q, want BCN (city->airport)", got.Origin.Airport)
	}
	if got.Origin.Source != SourceGeoIP {
		t.Fatalf("source = %q, want geoip", got.Origin.Source)
	}
}

func TestResolve_GeoCountryFallback(t *testing.T) {
	clk := fixedClock{t: time.Now()}
	// City unknown to the airport table, but country maps to a primary hub.
	got := Resolve(context.Background(), nil, Options{
		AllowGeoIP: true,
		Clock:      clk,
		Geo:        stubGeo{loc: Location{City: "Espoo", Country: "FI"}},
	})
	if got.Origin.Airport != "HEL" {
		t.Fatalf("airport = %q, want HEL (country->hub)", got.Origin.Airport)
	}
}

func TestResolve_GeoDisabledStopsAtPrefs(t *testing.T) {
	clk := fixedClock{t: time.Now()}
	// AllowGeoIP=false: with no prefs, origin must be unresolved and geo
	// must never be consulted (stub errors if it is).
	got := Resolve(context.Background(), nil, Options{
		AllowGeoIP: false,
		Clock:      clk,
		Geo:        stubGeo{err: errors.New("geo must not be called when disabled")},
	})
	if got.Origin.HasAirport() {
		t.Fatalf("airport = %q, want empty (geo disabled, no prefs)", got.Origin.Airport)
	}
}

func TestResolve_GeoErrorDegradesGracefully(t *testing.T) {
	clk := fixedClock{t: time.Now()}
	got := Resolve(context.Background(), nil, Options{
		AllowGeoIP: true,
		Clock:      clk,
		Geo:        stubGeo{err: errors.New("network down")},
	})
	if got.Origin.HasAirport() {
		t.Fatalf("airport = %q, want empty on geo error", got.Origin.Airport)
	}
	// Time must still be populated even when location fails entirely.
	if got.Now.IsZero() {
		t.Fatal("Now is zero; time must be available regardless of geo")
	}
}

func TestResolve_TimeAlwaysPresent(t *testing.T) {
	want := time.Date(2026, 5, 31, 19, 48, 0, 0, helsinki())
	got := Resolve(context.Background(), nil, Options{Clock: fixedClock{t: want}})
	if !got.Now.Equal(want) {
		t.Fatalf("Now = %v, want %v", got.Now, want)
	}
	if got.Timezone != "Europe/Helsinki" {
		t.Fatalf("Timezone = %q, want Europe/Helsinki", got.Timezone)
	}
	if got.TimeSource != SourceClock {
		t.Fatalf("TimeSource = %q, want clock", got.TimeSource)
	}
}

func TestLeadTimeDays(t *testing.T) {
	now := time.Date(2026, 5, 31, 19, 0, 0, 0, helsinki())
	c := Context{Now: now}
	cases := []struct {
		name string
		dep  time.Time
		want int
	}{
		{"same day", time.Date(2026, 5, 31, 6, 0, 0, 0, helsinki()), 0},
		{"tomorrow regardless of clock", time.Date(2026, 6, 1, 1, 0, 0, 0, helsinki()), 1},
		{"two weeks", time.Date(2026, 6, 14, 12, 0, 0, 0, helsinki()), 14},
		{"past", time.Date(2026, 5, 28, 12, 0, 0, 0, helsinki()), -3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.LeadTimeDays(tc.dep); got != tc.want {
				t.Fatalf("LeadTimeDays = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestClassifyWindow(t *testing.T) {
	cases := []struct {
		lead int
		want BookingWindow
	}{
		{-1, WindowPast},
		{0, WindowLastMin},
		{3, WindowLastMin},
		{4, WindowShort},
		{13, WindowShort},
		{14, WindowSweetSpot},
		{60, WindowSweetSpot},
		{61, WindowEarly},
		{120, WindowEarly},
		{121, WindowVeryEarly},
	}
	for _, tc := range cases {
		if got := ClassifyWindow(tc.lead); got != tc.want {
			t.Errorf("ClassifyWindow(%d) = %q, want %q", tc.lead, got, tc.want)
		}
	}
}

func TestAdvisory_SweetSpotIsSilent(t *testing.T) {
	if a := WindowSweetSpot.Advisory(); a != "" {
		t.Fatalf("sweet-spot advisory = %q, want empty (no nudge)", a)
	}
	if a := WindowLastMin.Advisory(); a == "" {
		t.Fatal("last-min advisory should be non-empty")
	}
}

func TestAirportForLocation(t *testing.T) {
	cases := []struct {
		city, country, want string
	}{
		{"Amsterdam", "NL", "AMS"},
		{"Prague", "CZ", "PRG"},
		{"", "FI", "HEL"},        // country-only fallback
		{"Nowhereville", "", ""}, // nothing resolvable
		{"Nowhereville", "ZZ", ""},
	}
	for _, tc := range cases {
		if got := airportForLocation(tc.city, tc.country); got != tc.want {
			t.Errorf("airportForLocation(%q,%q) = %q, want %q", tc.city, tc.country, got, tc.want)
		}
	}
}

func TestParseLatLon(t *testing.T) {
	lat, lon := parseLatLon("60.1695,24.9354")
	if lat < 60.16 || lat > 60.17 {
		t.Errorf("lat = %v, want ~60.1695", lat)
	}
	if lon < 24.93 || lon > 24.94 {
		t.Errorf("lon = %v, want ~24.9354", lon)
	}
	// Negative + malformed.
	lat2, lon2 := parseLatLon("-33.8688,151.2093")
	if lat2 > -33.0 {
		t.Errorf("lat2 = %v, want negative ~-33.87", lat2)
	}
	if lon2 < 151.0 {
		t.Errorf("lon2 = %v, want ~151.21", lon2)
	}
	if l1, l2 := parseLatLon("garbage"); l1 != 0 || l2 != 0 {
		t.Errorf("garbage parse = (%v,%v), want (0,0)", l1, l2)
	}
}

func TestTimezoneName_PrefersIANAOverLocal(t *testing.T) {
	// A named zone is returned verbatim.
	hel := helsinki()
	if hel.String() != "UTC" { // skip if zoneinfo unavailable
		got := timezoneName(time.Date(2026, 5, 31, 12, 0, 0, 0, hel))
		if got != "Europe/Helsinki" {
			t.Fatalf("named zone = %q, want Europe/Helsinki", got)
		}
	}

	// time.Local stringifies as "Local"; timezoneName must do better,
	// falling back to TZ env or the zone abbreviation rather than "Local".
	t.Setenv("TZ", "Europe/Helsinki")
	got := timezoneName(time.Date(2026, 5, 31, 12, 0, 0, 0, time.Local))
	if got == "Local" || got == "" {
		t.Fatalf("local-zone fallback = %q, want a meaningful zone name", got)
	}
}

func TestGeoDisabledByEnv(t *testing.T) {
	t.Setenv("TRVL_NO_GEO", "1")
	if !geoDisabledByEnv() {
		t.Fatal("TRVL_NO_GEO=1 should disable geo")
	}
	t.Setenv("TRVL_NO_GEO", "")
	t.Setenv("CI", "true")
	if !geoDisabledByEnv() {
		t.Fatal("CI=true should disable geo")
	}
}
