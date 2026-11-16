package hotels

import (
	"context"
	"errors"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// sacherPair is the H1 evidence in miniature: Google names the Vienna hotel in
// English, and a same-brand hotel in another town carries that town in its
// name and address.
func sacherPair() []models.HotelResult {
	return []models.HotelResult{
		{Name: "Hotel Sacher Baden", HotelID: "/g/sacher_baden", Address: "Helenenstraße 55, 2500 Baden, Austria"},
		{Name: "Hotel Sacher Vienna", HotelID: "/g/sacher_vienna"},
	}
}

// TestFindBestNameMatch_CityWordsCompareThroughAliases: "Wien" in the query
// and "Vienna" in Google's name are the same city, and the Baden hotel is not
// the Vienna one, with the city in the name or after a comma.
func TestFindBestNameMatch_CityWordsCompareThroughAliases(t *testing.T) {
	for _, q := range []string{"Hotel Sacher Wien", "Hotel Sacher, Wien", "Hotel Sacher, Vienna"} {
		got := findBestNameMatch(sacherPair(), q)
		if got == nil || got.Name != "Hotel Sacher Vienna" {
			t.Errorf("findBestNameMatch(%q) = %v, want Hotel Sacher Vienna", q, got)
		}
	}
}

// TestFilterByNameMatch_CityWordsCompareThroughAliases is the plural
// matcher's half of the same rule.
func TestFilterByNameMatch_CityWordsCompareThroughAliases(t *testing.T) {
	got := filterByNameMatch(sacherPair(), "Hotel Sacher Wien")
	if names := hotelNames(got); len(names) != 1 || names[0] != "Hotel Sacher Vienna" {
		t.Fatalf("filterByNameMatch(Hotel Sacher Wien) = %v, want only Hotel Sacher Vienna", names)
	}
}

// bookingSacherBaden is the other-town hotel. Its real address names Vienna
// too ("Baden bei Wien"), so the trailing-word retry finds the query's city
// word in its address and keeps it eligible.
func bookingSacherBaden() models.HotelResult {
	return models.HotelResult{
		Name: "Hotel Sacher Baden", Address: "Helenenstraße 55, 2500 Baden bei Wien", HotelID: "4711",
		Price: 200, Currency: "USD", Lat: 60.1699, Lon: 24.9384,
	}
}

// TestSearchHotelsByName_StrippedNameRefusesOtherTown: "Hotel Sacher Wien"
// in Vienna may retry as "Hotel Sacher", but a candidate whose name carries a
// different town is not the requested property.
func TestSearchHotelsByName_StrippedNameRefusesOtherTown(t *testing.T) {
	serveGooglePage(t, hotelWithPrice{"Hotel Imperial", 300})
	withBookingResult(t, bookingSacherBaden())
	seedGeo(t, "Hotel Sacher Wien, Vienna")

	got, _, err := SearchHotelsByName(context.Background(), "Hotel Sacher Wien", "Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if len(got) != 0 {
		t.Fatalf("matches = %v, want none: Baden is not Vienna", hotelNames(got))
	}
	if !errors.Is(err, ErrNoNameMatch) {
		t.Fatalf("err = %v, want ErrNoNameMatch", err)
	}
}

// TestSearchHotelByName_StrippedNameRefusesOtherTown is the single-result
// lookup's half: the comma suffix is the city the guard compares against.
func TestSearchHotelByName_StrippedNameRefusesOtherTown(t *testing.T) {
	serveGooglePage(t, hotelWithPrice{"Hotel Imperial", 300})
	withBookingResult(t, bookingSacherBaden())
	// SearchHotels geocodes "Vienna" under its alias "Wien".
	for _, q := range []string{"Wien", "Hotel Sacher Wien Vienna", "Hotel Sacher Wien, Vienna"} {
		seedGeo(t, q)
	}

	got, err := SearchHotelByName(context.Background(), "Hotel Sacher Wien, Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if err == nil {
		t.Fatalf("matched %q, want no match: Baden is not Vienna", got.Name)
	}
	if !errors.Is(err, ErrNoNameMatch) {
		t.Fatalf("err = %v, want ErrNoNameMatch", err)
	}
}

// seedGeoAt pins the geocode cache entry for query to the given point.
func seedGeoAt(t *testing.T, query string, lat, lon float64) {
	t.Helper()
	geoCache.Lock()
	geoCache.entries[query] = geoEntry{lat: lat, lon: lon}
	geoCache.Unlock()
	t.Cleanup(func() {
		geoCache.Lock()
		delete(geoCache.entries, query)
		geoCache.Unlock()
	})
}

// TestNameMatch_RefusesCandidateBeyondCityRadius: a Google row has no
// address, so only its distance from the resolved city shows that "Hotel
// Sacher Baden", a Baden-area pin about 26 km out, is not the Vienna hotel.
// The city counts as resolved under its own name or under the alias
// SearchHotels geocodes it as ("Wien").
func TestNameMatch_RefusesCandidateBeyondCityRadius(t *testing.T) {
	const cityLat, cityLon = 48.2083537, 16.3725042
	candidates := []models.HotelResult{
		{Name: "Hotel Sacher Baden", HotelID: "/g/sacher_baden", Lat: 48.0, Lon: 16.22},
		{Name: "Hotel Sacher Vienna", HotelID: "/g/sacher_vienna", Lat: 48.2040009, Lon: 16.3697963},
	}
	if d := Haversine(cityLat, cityLon, 48.0, 16.22); d <= 25 {
		t.Fatalf("precondition: Baden pin is %.1f km from the centre, want beyond 25", d)
	}
	for _, seeded := range []string{"Vienna", "Wien"} {
		t.Run(seeded, func(t *testing.T) {
			seedGeoAt(t, seeded, cityLat, cityLon)
			if got := findBestNameMatch(candidates, "Hotel Sacher, Vienna"); got == nil || got.Name != "Hotel Sacher Vienna" {
				t.Errorf("findBestNameMatch = %v, want Hotel Sacher Vienna", got)
			}
			kept := preferWithinRadius(filterByNameMatch(withoutOtherTowns(candidates, "Hotel Sacher", "Vienna"), "Hotel Sacher"), "Vienna")
			if names := hotelNames(kept); len(names) != 1 || names[0] != "Hotel Sacher Vienna" {
				t.Errorf("plural match = %v, want only Hotel Sacher Vienna", names)
			}
		})
	}
}

// TestFindBestNameMatch_RegionSuffixKeepsDistantExactMatch: a comma suffix
// can name a region or island, not a city. An exact name match 80 km from
// the region's geocoded point is still the property.
func TestFindBestNameMatch_RegionSuffixKeepsDistantExactMatch(t *testing.T) {
	seedGeoAt(t, "Crete", 35.2401, 24.8093)
	candidates := []models.HotelResult{{Name: "Blue Palace Elounda", HotelID: "/g/blue_palace", Lat: 35.2650, Lon: 25.7380}}
	if d := Haversine(35.2401, 24.8093, 35.2650, 25.7380); d < 60 {
		t.Fatalf("precondition: candidate is %.0f km out, want a distant one", d)
	}
	if got := findBestNameMatch(candidates, "Blue Palace Elounda, Crete"); got == nil {
		t.Fatal("exact name match dropped for being far from the region's centre")
	}
}

// TestNameMatch_OnlyTrailingCityWordConflicts: a city word leading a brand
// ("Vienna House …") is not the hotel's town; a trailing one ("… Prague") is.
// Prague queries keep the Vienna House hotel; Vienna queries refuse it.
func TestNameMatch_OnlyTrailingCityWordConflicts(t *testing.T) {
	andels := []models.HotelResult{{Name: "Vienna House Andel's Prague", HotelID: "/g/andels"}}
	if got := findBestNameMatch(andels, "Andel's, Prague"); got == nil {
		t.Error(`findBestNameMatch("Andel's, Prague") = nil, want Vienna House Andel's Prague`)
	}
	if kept := withoutOtherTowns(andels, "Andel's", "Prague"); len(kept) != 1 {
		t.Errorf("Prague query dropped %v, want it kept", hotelNames(andels))
	}
	if got := findBestNameMatch(andels, "Andel's, Vienna"); got != nil {
		t.Errorf(`findBestNameMatch("Andel's, Vienna") = %q, want nil: its town is Prague`, got.Name)
	}
}

// TestSearchHotelByName_ReSearchKeepsTheMatchRules: the Booking match has no
// Google ID, so the lookup re-searches Google. A Google-ID row from that
// re-search must pass the same name and town rules as the first match;
// "Hotel Sacher Baden" is only Jaccard-similar to "Hotel Sacher Wien".
func TestSearchHotelByName_ReSearchKeepsTheMatchRules(t *testing.T) {
	serveGooglePage(t, hotelWithPrice{"Hotel Sacher Baden", 200})
	withBookingResult(t, models.HotelResult{
		Name: "Hotel Sacher Wien", Address: "Philharmoniker-Straße 4, 1010 Wien", HotelID: "4711",
		Price: 480, Currency: "USD", Lat: 60.1699, Lon: 24.9384,
	})
	seedGeo(t, "Hotel Sacher Wien")
	seedGeo(t, "Hotel Sacher Wien, Hotel Sacher Wien")

	got, err := SearchHotelByName(context.Background(), "Hotel Sacher Wien", lookupCheckIn, lookupCheckOut, "USD")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.Name != "Hotel Sacher Wien" {
		t.Fatalf("matched %q (%s), want the Booking row Hotel Sacher Wien", got.Name, got.HotelID)
	}
	if hotelIDRank("0x476d079e5136ca9f:0xfdc2e58a51a25b46") != 2 {
		t.Error("a hex feature ID must rank as a Google ID")
	}
}

// TestNameMatch_RadiusNeverVetoesTheSoleFullNameMatch: an airport hotel
// sits about 28 km from its city. When it is the only hotel matching the
// full name, distance has nothing to choose between and must not drop it.
func TestNameMatch_RadiusNeverVetoesTheSoleFullNameMatch(t *testing.T) {
	const cityLat, cityLon, hotelLat = 60.1699, 24.9384, 60.4199
	hilton := models.HotelResult{
		Name: "Hilton Munich Airport", HotelID: "/g/hilton_muc", Price: 200, Currency: "USD",
		Lat: hotelLat, Lon: cityLon, Address: "Terminalstraße Mitte 20, 85356 Munich",
	}
	if d := Haversine(cityLat, cityLon, hotelLat, cityLon); d <= 25 {
		t.Fatalf("precondition: hotel is %.1f km out, want beyond 25", d)
	}
	seedGeoAt(t, "Munich", cityLat, cityLon)
	seedGeoAt(t, "München", cityLat, cityLon)

	if got := findBestNameMatch([]models.HotelResult{hilton}, "Hilton Munich Airport, Munich"); got == nil {
		t.Error("findBestNameMatch dropped the sole full-name match for its distance")
	}

	freshRateManager(t)
	serveGooglePage(t, hotelWithPrice{"Hotel Imperial", 300})
	withBookingResult(t, hilton)
	seedGeoAt(t, "Hilton Munich Airport", cityLat, cityLon) // the name already holds the city
	got, _, err := SearchHotelsByName(context.Background(), "Hilton Munich Airport", "Munich", lookupCheckIn, lookupCheckOut, "USD")
	if err != nil || len(got) != 1 {
		t.Fatalf("SearchHotelsByName = %v, %v; want the sole full-name match", hotelNames(got), err)
	}
}
