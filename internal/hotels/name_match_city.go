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
// Baden". A name word counts as a town when its address names it as a town
// (addressTowns), or when it is an alias-table city trailing the name (a leading one,
// as in "Vienna House Andel's Prague", is a brand); words of the query name or
// city never count. With no city there is nothing to conflict with.
//
// ponytail: there is no gazetteer, so an address-less row whose town is not in
// the alias table passes unless distance (beyondCityRadius) settles it; a
// geocoded town list would close that gap.
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
// when there is no city to conflict with.
func townConflict(name, city string) func(models.HotelResult) bool {
	cityWords := canonicalCityWords(hotelNameWords(city))
	if len(cityWords) == 0 {
		return nil
	}
	allowed := make(map[string]bool)
	for _, w := range append(canonicalCityWords(hotelNameWords(name)), cityWords...) {
		allowed[w] = true
	}
	return func(h models.HotelResult) bool { return carriesOtherTown(h, allowed) }
}

// beyondCityRadius returns a test for a candidate pinned more than
// nameMatchMaxKm from city, or nil when distance cannot judge: the city is
// not an alias-table city (a suffix may name a region or island, "…, Crete")
// or it is not in the geocode cache. Distance catches an address-less row
// such as Google's "Hotel Sacher Baden", but it only chooses among competing
// matches (preferWithinRadius) and judges stripped-name retries
// (withinRadius); it never vetoes the sole full-name match, which may be an
// airport hotel 28 km out.
func beyondCityRadius(city string) func(models.HotelResult) bool {
	cityWords := canonicalCityWords(hotelNameWords(city))
	if len(cityWords) != 1 {
		return nil
	}
	if _, known := cityAliasCanon[cityWords[0]]; !known {
		return nil
	}
	lat, lon, located := cachedLocation(city)
	if !located {
		return nil
	}
	return func(h models.HotelResult) bool {
		return (h.Lat != 0 || h.Lon != 0) && Haversine(lat, lon, h.Lat, h.Lon) > nameMatchMaxKm
	}
}

// preferWithinRadius keeps the full-name matches inside the city radius when
// there are any. Outside it, only a match whose name adds nothing to the
// query name survives (an airport hotel 28 km out); "Hotel Sacher Baden" for
// "Hotel Sacher" in Vienna does not.
func preferWithinRadius(matches []models.HotelResult, name, city string) []models.HotelResult {
	beyond := beyondCityRadius(city)
	if beyond == nil {
		return matches
	}
	var within, exact []models.HotelResult
	for _, h := range matches {
		switch {
		case !beyond(h):
			within = append(within, h)
		case !addsWords(h.Name, name+" "+city):
			exact = append(exact, h)
		}
	}
	if len(within) > 0 {
		return within
	}
	return exact
}

// addsWords reports whether name carries a meaningful word query lacks.
func addsWords(name, query string) bool {
	known := make(map[string]bool)
	for _, w := range canonicalCityWords(meaningfulHotelNameWords(query)) {
		known[w] = true
	}
	for _, w := range canonicalCityWords(meaningfulHotelNameWords(name)) {
		if !known[w] {
			return true
		}
	}
	return false
}

// withinRadius keeps only the candidates inside the city radius, or all of
// them when distance cannot judge.
func withinRadius(hotels []models.HotelResult, city string) []models.HotelResult {
	beyond := beyondCityRadius(city)
	if beyond == nil {
		return hotels
	}
	var out []models.HotelResult
	for _, h := range hotels {
		if !beyond(h) {
			out = append(out, h)
		}
	}
	return out
}

// nameMatchMaxKm is how far from the query city's centre a name match may be.
// ponytail: one radius for every city; a per-city radius if metro areas differ.
const nameMatchMaxKm = 25.0

// cachedLocation reads the geocode cache only, under the city's own name,
// its English form (the key the search geocodes, "Wien" -> "Vienna") or its
// local form, ignoring case: the cache keeps each query as typed. The
// matcher must not add a geocoding request, so a city nobody resolved yet
// has no distance check.
//
// ponytail: a linear scan of the per-process cache; key it case-folded if
// the cache ever grows large.
func cachedLocation(query string) (lat, lon float64, ok bool) {
	forms := []string{strings.TrimSpace(query), englishHotelCity(query), normalizeHotelCity(query)}
	geoCache.RLock()
	defer geoCache.RUnlock()
	for key, e := range geoCache.entries {
		for _, form := range forms {
			if strings.EqualFold(key, form) {
				return e.lat, e.lon, true
			}
		}
	}
	return 0, 0, false
}

func carriesOtherTown(h models.HotelResult, allowed map[string]bool) bool {
	towns := addressTowns(h.Address)
	words := canonicalCityWords(hotelNameWords(h.Name))
	lastAllowed := -1
	for i, w := range words {
		if allowed[w] {
			lastAllowed = i
		}
	}
	for i, w := range words {
		if allowed[w] || len([]rune(w)) < 3 {
			continue
		}
		_, isCity := cityAliasCanon[w]
		if towns[w] || (isCity && i > lastAllowed) {
			return true
		}
	}
	return false
}

// addressTowns returns an address's town words: alias-table cities anywhere
// in it, the place after a postcode ("2500 Baden", "1100 Wien"), and a lone
// word forming the last non-country part ("…, Baden, Austria"). Street and
// district words ("Am Hauptbahnhof 1", "Mariahilf, …") are not towns.
func addressTowns(address string) map[string]bool {
	towns := make(map[string]bool)
	parts := strings.Split(address, ",")
	lastPlace := -1
	for i, part := range parts {
		if strings.TrimSpace(part) != "" && !models.IsCountryName(part) {
			lastPlace = i
		}
	}
	for i, part := range parts {
		words := canonicalCityWords(hotelNameWords(part))
		// A lone word in the last non-country part is the town ("…, Baden,
		// Austria"); earlier it is a street or district ("Mariahilf, …").
		if i == lastPlace && len(words) == 1 && !models.IsPostcode(words[0]) {
			towns[words[0]] = true
		}
		afterPostcode := false
		for _, w := range words {
			if _, isCity := cityAliasCanon[w]; isCity || afterPostcode {
				towns[w] = true
			}
			if models.IsPostcode(w) {
				afterPostcode = true
			}
		}
	}
	return towns
}
