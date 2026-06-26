package travelgraph_test

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/travelgraph"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// Profile gap (profile-aware nudges): g.prefs now gates and ranks nudges.
//
// These tests exercise the below-threshold path because it fires
// deterministically (no fareintel dependency), letting us assert the exact
// gating and ordering the profile imposes.

// belowWatch returns a watch already below its target, so it deterministically
// fires a KindBelowThreshold nudge.
func belowWatch(id, origin, dest string) watch.Watch {
	return watch.Watch{
		ID:          id,
		Origin:      origin,
		Destination: dest,
		BelowPrice:  200.0,
		LastPrice:   150.0,
		Currency:    "EUR",
	}
}

// firstSource returns the first source ID of the first nudge, or "".
func firstSource(nudges []travelgraph.Nudge) string {
	if len(nudges) == 0 || len(nudges[0].Sources) == 0 {
		return ""
	}
	return nudges[0].Sources[0]
}

// sourcesInOrder flattens the first source of each nudge, preserving order.
func sourcesInOrder(nudges []travelgraph.Nudge) []string {
	out := make([]string, 0, len(nudges))
	for _, n := range nudges {
		if len(n.Sources) > 0 {
			out = append(out, n.Sources[0])
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestProfile_ExcludedDestination_GatesNudge proves a route whose destination
// is on ExcludedDestinations emits no nudge, while a sibling route still does.
func TestProfile_ExcludedDestination_GatesNudge(t *testing.T) {
	cases := []struct {
		name     string
		excluded []string
		wantIDs  []string // expected source IDs, in order
	}{
		{
			name:     "no exclusions: both fire (key order AMS-BCN < AMS-WAW)",
			excluded: nil,
			wantIDs:  []string{"w-bcn", "w-waw"},
		},
		{
			name:     "exclude WAW: only BCN fires",
			excluded: []string{"WAW"},
			wantIDs:  []string{"w-bcn"},
		},
		{
			name:     "exclude is case-insensitive",
			excluded: []string{"waw"},
			wantIDs:  []string{"w-bcn"},
		},
		{
			name:     "exclude both: zero nudges",
			excluded: []string{"WAW", "BCN"},
			wantIDs:  []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prefs := preferences.Default()
			prefs.ExcludedDestinations = tc.excluded
			ws := []watch.Watch{
				belowWatch("w-bcn", "AMS", "BCN"),
				belowWatch("w-waw", "AMS", "WAW"),
			}

			g := travelgraph.Build(ws, nil, prefs, nil)
			got := sourcesInOrder(travelgraph.Nudges(g))

			if !equalStrings(got, tc.wantIDs) {
				t.Errorf("sources = %v, want %v", got, tc.wantIDs)
			}
		})
	}
}

// TestProfile_HomeAirportRoutesRankFirst proves a home-origin route is ordered
// ahead of a non-home route, reversing plain key order.
func TestProfile_HomeAirportRoutesRankFirst(t *testing.T) {
	// Plain key order would put the alphabetically-first origin first. With HEL
	// as home, the HEL-origin nudge must jump to the front instead.
	ws := []watch.Watch{
		belowWatch("w-far", "ZZZ", "ROM"),  // non-home origin, alphabetically last
		belowWatch("w-home", "HEL", "ROM"), // home origin
	}

	prefs := preferences.Default()
	prefs.HomeAirports = []string{"HEL"}

	g := travelgraph.Build(ws, nil, prefs, nil)
	got := sourcesInOrder(travelgraph.Nudges(g))

	want := []string{"w-home", "w-far"}
	if !equalStrings(got, want) {
		t.Errorf("home-origin route did not rank first: got %v, want %v", got, want)
	}
}

// TestProfile_NearbyAirportCountsAsHome proves a configured nearby airport is
// treated as a home origin for ranking.
func TestProfile_NearbyAirportCountsAsHome(t *testing.T) {
	ws := []watch.Watch{
		belowWatch("w-far", "ZZZ", "ROM"),    // non-home
		belowWatch("w-nearby", "ARN", "ROM"), // ARN configured as nearby of HEL
	}

	prefs := preferences.Default()
	prefs.HomeAirports = []string{"HEL"}
	prefs.NearbyAirports = map[string][]string{"HEL": {"ARN"}}

	g := travelgraph.Build(ws, nil, prefs, nil)
	if got := firstSource(travelgraph.Nudges(g)); got != "w-nearby" {
		t.Errorf("nearby-airport route did not rank first: got %q, want w-nearby", got)
	}
}

// TestProfile_AffinityBreaksTieAmongNonHomeRoutes proves destination affinity
// reorders routes that share the same home status.
func TestProfile_AffinityBreaksTieAmongNonHomeRoutes(t *testing.T) {
	// Two non-home routes; key order is AMS-LOW < AMS-TOP. Affinity should lift
	// the high-affinity destination above the low-affinity one.
	ws := []watch.Watch{
		belowWatch("w-low", "AMS", "LOW"),
		belowWatch("w-top", "AMS", "TOP"),
	}

	prefs := preferences.Default()
	prefs.AirportAffinity = map[string]float64{"TOP": 0.9, "LOW": 0.1}

	g := travelgraph.Build(ws, nil, prefs, nil)
	if got := firstSource(travelgraph.Nudges(g)); got != "w-top" {
		t.Errorf("high-affinity route did not rank first: got %q, want w-top", got)
	}
}

// TestProfile_HomeBonusDominatesAffinity proves a home-origin route outranks a
// non-home route even when the non-home destination has higher affinity.
func TestProfile_HomeBonusDominatesAffinity(t *testing.T) {
	ws := []watch.Watch{
		belowWatch("w-affinity", "ZZZ", "TOP"), // non-home, high affinity
		belowWatch("w-home", "HEL", "MEH"),     // home, no affinity
	}

	prefs := preferences.Default()
	prefs.HomeAirports = []string{"HEL"}
	prefs.AirportAffinity = map[string]float64{"TOP": 1.0}

	g := travelgraph.Build(ws, nil, prefs, nil)
	if got := firstSource(travelgraph.Nudges(g)); got != "w-home" {
		t.Errorf("home bonus did not dominate affinity: got %q, want w-home", got)
	}
}

// TestProfile_NoProfile_StableKeyOrder proves the no-profile baseline still
// surfaces every grounded nudge and is now deterministically ordered by route
// key (the pre-fix bare map iteration was order-undefined). nil prefs must not
// panic.
func TestProfile_NoProfile_StableKeyOrder(t *testing.T) {
	ws := []watch.Watch{
		belowWatch("w-c", "AMS", "CCC"),
		belowWatch("w-a", "AMS", "AAA"),
		belowWatch("w-b", "AMS", "BBB"),
	}

	g := travelgraph.Build(ws, nil, nil, nil)
	got := sourcesInOrder(travelgraph.Nudges(g))

	// Route keys order: AMS-AAA < AMS-BBB < AMS-CCC.
	want := []string{"w-a", "w-b", "w-c"}
	if !equalStrings(got, want) {
		t.Errorf("no-profile order not stable by key: got %v, want %v", got, want)
	}
}
