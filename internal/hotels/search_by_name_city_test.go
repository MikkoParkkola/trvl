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
	for _, q := range []string{"Vienna", "Hotel Sacher Wien Vienna", "Hotel Sacher Wien, Vienna"} {
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
func TestNameMatch_RefusesCandidateBeyondCityRadius(t *testing.T) {
	const cityLat, cityLon = 48.2083537, 16.3725042
	seedGeoAt(t, "Vienna", cityLat, cityLon)
	candidates := []models.HotelResult{
		{Name: "Hotel Sacher Baden", HotelID: "/g/sacher_baden", Lat: 48.0, Lon: 16.22},
		{Name: "Hotel Sacher Vienna", HotelID: "/g/sacher_vienna", Lat: 48.2040009, Lon: 16.3697963},
	}
	if d := Haversine(cityLat, cityLon, 48.0, 16.22); d <= 25 {
		t.Fatalf("precondition: Baden pin is %.1f km from the centre, want beyond 25", d)
	}

	if got := findBestNameMatch(candidates, "Hotel Sacher, Vienna"); got == nil || got.Name != "Hotel Sacher Vienna" {
		t.Errorf("findBestNameMatch = %v, want Hotel Sacher Vienna", got)
	}
	kept := filterByNameMatch(withoutOtherTowns(candidates, "Hotel Sacher", "Vienna"), "Hotel Sacher")
	if names := hotelNames(kept); len(names) != 1 || names[0] != "Hotel Sacher Vienna" {
		t.Errorf("plural match = %v, want only Hotel Sacher Vienna", names)
	}
}
