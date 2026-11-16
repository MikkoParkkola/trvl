package hotels

import (
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// cityAliasCanon maps every spelling in hotelCityAliases, English and local,
// to one lowercase form, so "vienna" and "wien" compare as the same word.
var cityAliasCanon = func() map[string]string {
	m := make(map[string]string, 2*len(hotelCityAliases))
	for english, local := range hotelCityAliases {
		canon := strings.ToLower(local)
		m[english] = canon
		m[canon] = canon
	}
	return m
}()

// canonicalCityWords rewrites alias-table city words to their shared form and
// leaves every other word alone. It works in place and returns words.
func canonicalCityWords(words []string) []string {
	for i, w := range words {
		if canon, ok := cityAliasCanon[w]; ok {
			words[i] = canon
		}
	}
	return words
}

// sameCityWord reports whether two words name the same city, alias-aware.
func sameCityWord(a, b string) bool {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	return canonicalCityWords([]string{a})[0] == canonicalCityWords([]string{b})[0]
}

// retryCity is the city a trailing-word retry is about: the location when one
// was given, otherwise the trailing word the retry strips.
func retryCity(name, location string) string {
	if strings.TrimSpace(location) != "" {
		return location
	}
	words := strings.Fields(name)
	if len(words) == 0 {
		return ""
	}
	return words[len(words)-1]
}

// withoutOtherTowns drops candidates whose name carries a town that is not the
// query's city, so "Hotel Sacher" for Vienna never matches "Hotel Sacher
// Baden". A name word counts as a town when it is an alias-table city or also
// appears in the candidate's own address; words of the query name or city
// never count. With no city there is nothing to conflict with.
//
// ponytail: there is no gazetteer, so an address-less row whose town is not in
// the alias table passes; a geocoded town list would close that gap.
func withoutOtherTowns(hotels []models.HotelResult, name, city string) []models.HotelResult {
	conflicts := townConflict(name, city)
	if conflicts == nil {
		return hotels
	}
	var out []models.HotelResult
	for _, h := range hotels {
		if !conflicts(h) {
			out = append(out, h)
		}
	}
	return out
}

// townConflict returns the withoutOtherTowns test for one candidate, or nil
// when there is no city to conflict with. When the city is already in the
// geocode cache, a candidate pinned more than nameMatchMaxKm from it conflicts
// too: that is what catches an address-less row such as Google's.
func townConflict(name, city string) func(models.HotelResult) bool {
	cityWords := canonicalCityWords(hotelNameWords(city))
	if len(cityWords) == 0 {
		return nil
	}
	allowed := make(map[string]bool)
	for _, w := range append(canonicalCityWords(hotelNameWords(name)), cityWords...) {
		allowed[w] = true
	}
	cityLat, cityLon, located := cachedLocation(city)
	return func(h models.HotelResult) bool {
		if located && (h.Lat != 0 || h.Lon != 0) && Haversine(cityLat, cityLon, h.Lat, h.Lon) > nameMatchMaxKm {
			return true
		}
		return carriesOtherTown(h, allowed)
	}
}

// nameMatchMaxKm is how far from the query city's centre a name match may be.
// ponytail: one radius for every city; a per-city radius if metro areas differ.
const nameMatchMaxKm = 25.0

// cachedLocation reads the geocode cache only, under the city's own name or
// the alias SearchHotels geocodes it as ("Vienna" is cached as "Wien"). The
// matcher must not add a geocoding request, so a city nobody resolved yet has
// no distance check.
func cachedLocation(query string) (lat, lon float64, ok bool) {
	geoCache.RLock()
	defer geoCache.RUnlock()
	for _, q := range []string{strings.TrimSpace(query), normalizeHotelCity(query)} {
		if e, found := geoCache.entries[q]; found {
			return e.lat, e.lon, true
		}
	}
	return 0, 0, false
}

func carriesOtherTown(h models.HotelResult, allowed map[string]bool) bool {
	address := make(map[string]bool)
	for _, w := range canonicalCityWords(hotelNameWords(h.Address)) {
		address[w] = true
	}
	for _, w := range canonicalCityWords(hotelNameWords(h.Name)) {
		if allowed[w] || len([]rune(w)) < 3 {
			continue
		}
		if _, isCity := cityAliasCanon[w]; isCity || address[w] {
			return true
		}
	}
	return false
}
